package preflight

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-workbench/internal/sandbox"
)

type journal struct {
	Runtime sandbox.RuntimeKind `json:"runtime"`
	Fixture string              `json:"fixture"`
}

func (s *Service) container(ctx context.Context, binary string, r Request, image, entry string, command []string, input, helper string) (output []byte, returnErr error, clean bool) {
	nonce := strings.ToLower(rand.Text())
	fixture := filepath.Join(r.Scratch, "awb-preflight-"+nonce)
	if err := os.Mkdir(fixture, 0700); err != nil {
		return nil, errors.New("cannot prepare diagnostic fixture"), true
	}
	record := journal{Runtime: r.Runtime, Fixture: fixture}
	recordPath := filepath.Join(s.root, nonce+".json")
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err, true
	}
	// A synced write-ahead identity precedes every possible container creation.
	file, err := os.OpenFile(recordPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.Remove(fixture)
		return nil, errors.New("cannot record diagnostic ownership"), true
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		s.markPending()
		return nil, errors.New("diagnostic ownership write unconfirmed"), false
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.cleanup(cleanupCtx, sandbox.ExecutionIdentity{Runtime: r.Runtime, Name: "awb-probe-" + nonce}) != nil {
			s.markPending()
			clean = false
			return
		}
		if os.RemoveAll(fixture) != nil || os.Remove(recordPath) != nil {
			s.markPending()
			clean = false
			return
		}
		clean = true
	}()
	args := []string{"run", "--rm", "--no-healthcheck", "--interactive", "--pull=never", "--name", "awb-probe-" + nonce, "--network=none", "--read-only", "--user=1000:1000", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=256m", "--cpus=1", "--pids-limit=64", "--tmpfs", "/tmp:rw,nosuid,nodev,size=32m,mode=1777", "--workdir", "/tmp", "--env", "HOME=/tmp/awb-home", "--env", "CODEX_HOME=/tmp/awb-home/codex", "--env", "CLAUDE_CONFIG_DIR=/tmp/awb-home/claude"}
	// Disable client/image proxy injection. The trusted helper uses stdin policy,
	// not ambient proxy variables; version checks retain network=none.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "OPENAI_API_KEY", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "NODE_OPTIONS", "LD_PRELOAD"} {
		args = append(args, "--env", name+"=")
	}
	if r.Runtime == sandbox.Podman {
		args = append(args, "--http-proxy=false", "--image-volume=ignore")
	}
	if helper != "" {
		if err = copyHelper(helper, filepath.Join(fixture, "probe")); err != nil {
			return nil, errors.New("cannot prepare builtin probe"), false
		}
		for i, arg := range args {
			if arg == "--network=none" {
				args[i] = "--network=bridge"
			}
		}
		args = append(args, "--mount", "type=bind,source="+filepath.Join(fixture, "probe")+",target=/awb-network-probe,readonly")
	}
	args = append(args, "--entrypoint", entry, image)
	args = append(args, command...)
	output, returnErr = s.command(ctx, binary, args, input, 10*time.Second)
	return output, returnErr, false
}

func copyHelper(source, target string) (returnErr error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, in.Close()) }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0555)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, out.Close()) }()
	_, err = io.Copy(out, io.LimitReader(in, 32<<20))
	return err
}

func (s *Service) markPending() { s.mu.Lock(); s.pending = true; s.mu.Unlock() }

func (s *Service) recover(ctx context.Context) error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, entry := range entries {
		nonce := strings.TrimSuffix(entry.Name(), ".json")
		if !noncePattern.MatchString(nonce) || entry.Name() != nonce+".json" || !entry.Type().IsRegular() {
			return errors.New("unknown diagnostic journal")
		}
		file, err := os.Open(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if err != nil || closeErr != nil || len(raw) > 4096 {
			return errors.New("invalid diagnostic journal")
		}
		var record journal
		if json.Unmarshal(raw, &record) != nil || (record.Runtime != sandbox.Docker && record.Runtime != sandbox.Podman) || !filepath.IsAbs(record.Fixture) || filepath.Base(record.Fixture) != "awb-preflight-"+nonce {
			return errors.New("invalid diagnostic ownership")
		}
		if err = s.cleanup(ctx, sandbox.ExecutionIdentity{Runtime: record.Runtime, Name: "awb-probe-" + nonce}); err != nil {
			return errors.New("diagnostic container cleanup unconfirmed")
		}
		if _, err = os.Lstat(record.Fixture); err == nil {
			if !directory(record.Fixture) {
				return errors.New("diagnostic fixture ownership changed")
			}
			if err = os.RemoveAll(record.Fixture); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = os.Remove(filepath.Join(s.root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
