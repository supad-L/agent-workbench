// Package sandbox compiles isolated, task-owned container invocations.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agent-workbench/internal/workspace"
)

// Mount describes a private snapshot or explicitly writable task directory.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// Spec contains only platform-approved paths and an immutable local image ID.
type Spec struct {
	Name       string
	ImageID    string
	SourceRoot string
	Writable   map[string]string
	Scope      workspace.Scope
	Command    []string
}

// Arguments constructs a no-network probe, never an unrestricted host execution.
func Arguments(spec Spec) ([]string, error) {
	if err := spec.Scope.Validate(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(spec.ImageID, "sha256:") || len(spec.ImageID) != 71 {
		return nil, errors.New("local immutable image ID required")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(spec.ImageID, "sha256:")); err != nil {
		return nil, errors.New("invalid immutable image digest")
	}
	if !strings.HasPrefix(spec.Name, "awb-probe-") || strings.ContainsAny(spec.Name, "/\\ ,") {
		return nil, errors.New("invalid probe identity")
	}
	if len(spec.Command) == 0 {
		return nil, errors.New("container command required")
	}
	if err := realDirectory(spec.SourceRoot); err != nil {
		return nil, err
	}
	args := []string{"run", "--rm", "--pull=never", "--name", spec.Name, "--network=none", "--read-only", "--user=1000:1000", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=256m", "--cpus=1", "--pids-limit=64", "--tmpfs", "/tmp:rw,nosuid,nodev,size=32m,mode=1777", "--workdir", "/workspace", "--mount", "type=bind,source=" + spec.SourceRoot + ",target=/workspace,readonly"}
	if len(spec.Writable) != len(spec.Scope.WriteDirs) {
		return nil, errors.New("writable mounts must exactly match scope")
	}
	var writableSources []string
	for _, relative := range spec.Scope.WriteDirs {
		source, ok := spec.Writable[relative]
		if !ok {
			return nil, errors.New("missing writable task mount")
		}
		if err := realDirectory(source); err != nil {
			return nil, err
		}
		if pathsOverlap(spec.SourceRoot, source) {
			return nil, errors.New("writable mount aliases the read-only source")
		}
		for _, existing := range writableSources {
			if pathsOverlap(existing, source) {
				return nil, errors.New("writable mounts must have disjoint private sources")
			}
		}
		writableSources = append(writableSources, source)
		args = append(args, "--mount", "type=bind,source="+source+",target=/workspace/"+strings.TrimSuffix(relative, "/"))
	}
	args = append(args, spec.ImageID)
	args = append(args, spec.Command...)
	return args, nil
}

func pathsOverlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, string(filepath.Separator))+string(filepath.Separator)) || strings.HasPrefix(b, strings.TrimRight(a, string(filepath.Separator))+string(filepath.Separator))
}

func realDirectory(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, ",\n\r") {
		return errors.New("mount requires an absolute canonical path")
	}
	real, err := filepath.EvalSymlinks(name)
	if err != nil {
		return err
	}
	if real != name {
		return errors.New("mount path must not contain symlinks")
	}
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("mount is not a directory")
	}
	return nil
}

// Report distinguishes unavailable infrastructure from an actual isolation pass.
type Report struct {
	Status   string      `json:"status"`
	Evidence string      `json:"evidence"`
	Detail   string      `json:"detail"`
	ImageID  string      `json:"image_id,omitempty"`
	Runtime  RuntimeKind `json:"runtime,omitempty"`
}

type dockerDriver interface {
	command(context.Context, ...string) ([]byte, error)
	evidence() string
}

type nativeRuntime struct {
	executable string
	kind       RuntimeKind
}

func (d nativeRuntime) command(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, d.executable, args...).Output()
}
func (d nativeRuntime) evidence() string { return "real_" + string(d.kind) }

// Probe tests real file-write boundaries with a locally present image, without pulling.
func Probe(ctx context.Context, image string) (report Report, returnErr error) {
	return ProbeRuntime(ctx, ProbeOptions{Runtime: Docker, Image: image})
}

func probeWithDriver(ctx context.Context, image string, driver dockerDriver) (report Report, returnErr error) {
	return probeWithDriverAt(ctx, image, driver, "")
}

func probeWithDriverAt(ctx context.Context, image string, driver dockerDriver, scratchDir string) (report Report, returnErr error) {
	blocked := func(detail string) (Report, error) {
		return Report{Status: "blocked", Evidence: "not_run", Detail: detail}, errors.New(detail)
	}
	if image == "" || strings.HasPrefix(image, "-") {
		return blocked("explicit locally installed image required")
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := driver.command(inspectCtx, "image", "inspect", "--format", "{{.Id}}", image)
	cancel()
	if err != nil {
		return blocked("selected container engine or requested local image is unavailable; no image was pulled")
	}
	imageID := strings.TrimSpace(string(output))
	// Podman reports an unprefixed image ID; both engines execute the same digest.
	if len(imageID) == 64 {
		imageID = "sha256:" + imageID
	}
	root, err := os.MkdirTemp(scratchDir, "awb-sandbox-")
	if err != nil {
		return blocked("cannot create private probe directory")
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			report.Status = "failed"
			report.Detail = "private fixture cleanup failed"
			returnErr = errors.Join(returnErr, errors.New("private fixture cleanup failed"))
		}
	}()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return blocked("cannot resolve probe directory")
	}
	root = resolvedRoot
	source := filepath.Join(root, "source")
	writable := filepath.Join(root, "task-src")
	for _, directory := range []string{filepath.Join(source, "frontend/src"), filepath.Join(source, "backend"), writable} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return blocked("cannot prepare probe fixture")
		}
	}
	// The private test fixture allows UID 1000 writes without altering host ownership.
	if err := os.Chmod(writable, 0777); err != nil {
		return blocked("cannot prepare fixture permissions")
	}
	if err := os.WriteFile(filepath.Join(source, "backend", "protected.txt"), []byte("unchanged"), 0644); err != nil {
		return blocked("cannot prepare protected fixture")
	}
	if err := os.Symlink("/workspace/backend/protected.txt", filepath.Join(writable, "escape-link")); err != nil {
		return blocked("cannot prepare symlink fixture")
	}
	name := "awb-probe-" + strings.ToLower(rand.Text())
	script := `set -eu
printf allowed > /workspace/frontend/src/allowed.txt
if printf bad > /workspace/backend/protected.txt 2>/dev/null; then exit 11; fi
if printf bad > /workspace/frontend/src/escape-link 2>/dev/null; then exit 12; fi
if touch /etc/awb-escape 2>/dev/null; then exit 13; fi
printf temporary > /tmp/awb-cache
printf 'scope-probe-passed\n'
`
	args, err := Arguments(Spec{Name: name, ImageID: imageID, SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}, WriteDirs: []string{"frontend/src/"}}, Writable: map[string]string{"frontend/src/": writable}, Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		return blocked("probe policy compilation failed")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		remaining, err := driver.command(cleanupCtx, "container", "ls", "-aq", "--filter", "name=^/"+name+"$")
		if err == nil && len(strings.TrimSpace(string(remaining))) != 0 {
			_, err = driver.command(cleanupCtx, "rm", "-f", name)
		}
		if err != nil {
			report.Status = "failed"
			report.Detail = "probe container cleanup could not be verified"
			returnErr = errors.Join(returnErr, errors.New("probe container cleanup could not be verified"))
		}
	}() // Handles a killed docker client; never removes unrelated containers.
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	output, err = driver.command(runCtx, args...)
	cancel()
	if err != nil {
		return Report{Status: "failed", Evidence: driver.evidence(), Detail: "container write-boundary probe failed", ImageID: imageID}, errors.New("container write-boundary probe failed")
	}
	protected, readErr := os.ReadFile(filepath.Join(source, "backend", "protected.txt"))
	allowed, allowedErr := os.ReadFile(filepath.Join(writable, "allowed.txt"))
	if readErr != nil || allowedErr != nil || string(protected) != "unchanged" || string(allowed) != "allowed" || strings.TrimSpace(string(output)) != "scope-probe-passed" {
		return Report{Status: "failed", Evidence: driver.evidence(), Detail: "probe output or protected source differs", ImageID: imageID}, errors.New("probe postconditions failed")
	}
	return Report{Status: "passed", Evidence: driver.evidence(), Detail: "allowed directory writable; protected source, symlink escape and root writes denied; private tmp works", ImageID: imageID}, nil
}
