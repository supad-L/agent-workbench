package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-workbench/internal/netcheck"
	"agent-workbench/internal/sandbox"
)

func setup(t *testing.T, mode string) (*Service, Request) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helpers := filepath.Join(root, "helpers")
	if err = os.Mkdir(helpers, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(helpers, "awb-network-probe-linux-arm64"), []byte("synthetic helper fixture"), 0555); err != nil {
		t.Fatal(err)
	}
	request := Request{Provider: "codex", Runtime: sandbox.Podman, Image: "fixture", Binary: "/usr/local/bin/codex", Scratch: root, Mode: "system", Target: "http://127.0.0.1:10000"}
	lookup := func(name string) (string, error) {
		if mode == "missing" {
			return "", errors.New("missing")
		}
		if name != "podman" && name != "docker" {
			t.Fatal(name)
		}
		return "/fixture/" + name, nil
	}
	run := func(ctx context.Context, binary string, args []string, input string) ([]byte, error) {
		switch args[0] {
		case "version":
			if mode == "engine" {
				return nil, errors.New("engine error")
			}
			return []byte("6.1.4"), nil
		case "image":
			if mode == "image" {
				return nil, errors.New("missing image")
			}
			if mode == "architecture" {
				return []byte("sha256:" + strings.Repeat("a", 64) + "|windows|arm64"), nil
			}
			return []byte("sha256:" + strings.Repeat("a", 64) + "|linux|arm64"), nil
		case "run":
			joined := strings.Join(args, " ")
			for _, required := range []string{"--pull=never", "--read-only", "--cap-drop=ALL", "--user=1000:1000", "HOME=/tmp/awb-home"} {
				if !strings.Contains(joined, required) {
					t.Fatal("unsafe diagnostic", required)
				}
			}
			if strings.Contains(joined, "secret-password") || strings.Contains(joined, "--privileged") || strings.Contains(joined, "--network=host") {
				t.Fatal("unsafe args")
			}
			if args[len(args)-1] == "--version" {
				if !strings.Contains(joined, "--network=none") {
					t.Fatal("version has network")
				}
				if mode == "cli" {
					return nil, errors.New("no CLI")
				}
				if strings.Contains(joined, "--entrypoint /usr/local/bin/claude ") {
					return []byte("2.1.0 (Claude Code)"), nil
				}
				return []byte("codex-cli 0.163.0"), nil
			}
			if !strings.Contains(joined, "--network=bridge") {
				t.Fatal("missing diagnostic network")
			}
			var in netcheck.Input
			if json.Unmarshal([]byte(input), &in) != nil {
				t.Fatal("missing stdin policy")
			}
			if mode == "network" {
				return []byte(`{"code":"route_failed"}`), nil
			}
			if mode == "malformed" {
				return []byte("raw diagnostics must not escape"), nil
			}
			return []byte(`{"reachable":true,"ip":"203.0.113.7","code":"reachable"}`), nil
		default:
			t.Fatal(args)
			return nil, errors.New("unexpected command")
		}
	}
	cleanup := func(ctx context.Context, identity sandbox.ExecutionIdentity) error {
		if identity.Runtime != sandbox.Podman && identity.Runtime != sandbox.Docker {
			t.Fatal(identity)
		}
		if mode == "cleanup" {
			return errors.New("cleanup unconfirmed")
		}
		return nil
	}
	s, err := newService(context.Background(), filepath.Join(root, "journal"), helpers, lookup, run, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, request
}

func TestDiagnosticOutcomes(t *testing.T) {
	for _, mode := range []string{"pass", "missing", "engine", "image", "architecture", "cli", "network", "malformed", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			s, r := setup(t, mode)
			report, err := s.Inspect(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if report.ExecutionReady || report.EnvironmentReady != (mode == "pass") {
				t.Fatal(report)
			}
			if mode == "cleanup" {
				again, err := s.Inspect(context.Background(), r)
				if err != nil || len(again.Checks) != 1 || again.Checks[0].Name != "cleanup" {
					t.Fatal(again, err)
				}
			}
		})
	}
}

func TestProxySecretAndProtectedProvider(t *testing.T) {
	s, r := setup(t, "pass")
	r.Mode = "proxy"
	r.Proxy = "http://fixture:secret-password@127.0.0.1:9999"
	report, err := s.Inspect(context.Background(), r)
	raw, _ := json.Marshal(report)
	if err != nil || !report.EnvironmentReady || strings.Contains(string(raw), "secret-password") {
		t.Fatal(report, err)
	}
	r.Provider = "claude"
	r.Binary = "/usr/local/bin/claude"
	report, err = s.Inspect(context.Background(), r)
	if err != nil || report.EnvironmentReady || report.IP != "" || report.Checks[len(report.Checks)-1].Status != "blocked" {
		t.Fatal(report, err)
	}
	r.Provider = "codex"
	r.Target = ""
	r.Binary = "/usr/local/bin/codex"
	report, err = s.Inspect(context.Background(), r)
	if err != nil || report.Checks[len(report.Checks)-1].Status != "skipped" {
		t.Fatal(report, err)
	}
	for _, change := range []func(*Request){func(r *Request) { r.Runtime = "auto" }, func(r *Request) { r.Provider = "custom" }, func(r *Request) { r.Binary = "/bin/../sh" }, func(r *Request) { r.Image = "--privileged" }, func(r *Request) { r.Scratch = "/missing" }, func(r *Request) { r.Mode = "system" }, func(r *Request) { r.Proxy = strings.Repeat("x", 4097) }} {
		invalid := r
		change(&invalid)
		if _, err = s.Inspect(context.Background(), invalid); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestDockerDiagnosticPolicy(t *testing.T) {
	s, r := setup(t, "pass")
	r.Runtime = sandbox.Docker
	report, err := s.Inspect(context.Background(), r)
	if err != nil || !report.EnvironmentReady || report.Runtime != sandbox.Docker || report.ExecutionReady {
		t.Fatal(report, err)
	}
}

func TestCleanupJournalRecovery(t *testing.T) {
	s, r := setup(t, "cleanup")
	if _, err := s.Inspect(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	var cleaned int
	reopened, err := newService(context.Background(), s.root, s.helpers, s.lookup, s.run, func(ctx context.Context, id sandbox.ExecutionIdentity) error { cleaned++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.pending || cleaned != 1 {
		t.Fatal("owned journal not reconciled")
	}
	entries, err := os.ReadDir(s.root)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

func TestBusyCancellationAndMissingHelper(t *testing.T) {
	s, r := setup(t, "pass")
	if err := os.Remove(filepath.Join(s.helpers, "awb-network-probe-linux-arm64")); err != nil {
		t.Fatal(err)
	}
	report, err := s.Inspect(context.Background(), r)
	if err != nil || report.EnvironmentReady || report.Checks[len(report.Checks)-1].Status != "blocked" {
		t.Fatal(report, err)
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	s.run = func(ctx context.Context, binary string, args []string, input string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() {
		defer close(finished)
		_, err := s.Inspect(context.Background(), r)
		if err != nil {
			t.Error(err)
		}
	}()
	<-started
	if _, err = s.Inspect(context.Background(), r); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	s.Close()
	<-finished
	if _, err = s.Inspect(context.Background(), r); err == nil {
		t.Fatal("closed service accepted probe")
	}
}

func TestInvalidJournalsAndOutputLimit(t *testing.T) {
	for _, contents := range []string{"invalid", strings.Repeat("x", 5000), `{"runtime":"shell","fixture":"/unowned"}`} {
		s, _ := setup(t, "pass")
		if err := os.WriteFile(filepath.Join(s.root, strings.Repeat("a", 26)+".json"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		reopened, err := newService(context.Background(), s.root, s.helpers, s.lookup, s.run, s.cleanup)
		if err != nil {
			t.Fatal(err)
		}
		if !reopened.pending {
			t.Fatal("unowned journal accepted")
		}
		reopened.Close()
	}
	var output limitedOutput
	if _, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", 70000))); err == nil || len(output.Bytes()) > 65536 {
		t.Fatal("output was not bounded")
	}
}
