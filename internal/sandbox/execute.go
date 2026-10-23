package sandbox

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Execution is a bounded offline invocation over a platform-prepared snapshot.
// The caller owns the snapshot and private writable directories for its lifetime.
// It must never supply an original project, account directory or platform data.
// Partial read scopes require an exported snapshot and are not supported here yet.
type Execution struct {
	Runtime        RuntimeKind
	Spec           Spec
	Input          string
	Timeout        time.Duration
	MaxOutputBytes int
	MaxLineBytes   int
}

// ExecutionResult records process supervision, not task acceptance or model access.
type ExecutionResult struct {
	Runtime         RuntimeKind `json:"runtime"`
	Status          string      `json:"status"`
	CleanupVerified bool        `json:"cleanup_verified"`
}

// Execute runs only in the selected Docker/Podman engine, with no network or
// account mounts. onLine is synchronous and must return promptly; its error stops
// the container. Stderr and engine errors are never returned as raw diagnostics.
func Execute(ctx context.Context, request Execution, onLine func([]byte) error) (ExecutionResult, error) {
	return execute(ctx, request, onLine, exec.LookPath, runProcess)
}

type processRunner func(context.Context, string, []string, string, io.Writer) error

func execute(ctx context.Context, request Execution, onLine func([]byte) error, lookup func(string) (string, error), run processRunner) (result ExecutionResult, returnErr error) {
	result = ExecutionResult{Runtime: request.Runtime, Status: "blocked"}
	if request.Runtime != Docker && request.Runtime != Podman {
		return result, errors.New("explicit docker or podman runtime required")
	}
	if request.Timeout <= 0 || request.Timeout > 20*time.Minute || request.MaxOutputBytes <= 0 || request.MaxOutputBytes > 16<<20 || request.MaxLineBytes <= 0 || request.MaxLineBytes > request.MaxOutputBytes || len(request.Input) > 1<<20 || onLine == nil {
		return result, errors.New("bounded timeout, input, output and line handler required")
	}
	if len(request.Spec.Scope.ReadPaths) != 1 || request.Spec.Scope.ReadPaths[0] != "." {
		return result, errors.New("executor requires an explicitly approved complete snapshot read scope")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	request.Spec.Name = "awb-probe-" + strings.ToLower(rand.Text())
	args, err := Arguments(request.Spec)
	if err != nil {
		return result, err
	}
	// Override the image entrypoint so the approved argv is the actual process.
	imageIndex := len(args) - len(request.Spec.Command) - 1
	args = append(args[:imageIndex:imageIndex], append([]string{"--interactive", "--env", "HOME=/tmp/awb-home", "--env", "CODEX_HOME=/tmp/awb-home/codex", "--env", "XDG_CACHE_HOME=/tmp/awb-home/cache", "--env", "TMPDIR=/tmp", "--entrypoint", request.Spec.Command[0], request.Spec.ImageID}, request.Spec.Command[1:]...)...)
	binary, err := lookup(string(request.Runtime))
	if err != nil {
		return result, errors.New("selected container engine unavailable; fallback disabled")
	}
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	result.Status = "failed"
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var remaining strings.Builder
		cleanupErr := run(cleanupCtx, binary, []string{"container", "ls", "-aq", "--filter", "name=^/" + request.Spec.Name + "$"}, "", &remaining)
		if cleanupErr == nil && strings.TrimSpace(remaining.String()) != "" {
			removeArgs := []string{"rm", "-f"}
			// Podman defaults to a 10s graceful stop even for rm -f; cancellation
			// must kill immediately before the independent cleanup deadline.
			if request.Runtime == Podman {
				removeArgs = append(removeArgs, "--time=0")
			}
			removeArgs = append(removeArgs, request.Spec.Name)
			cleanupErr = run(cleanupCtx, binary, removeArgs, "", io.Discard)
			if cleanupErr == nil {
				remaining.Reset()
				cleanupErr = run(cleanupCtx, binary, []string{"container", "ls", "-aq", "--filter", "name=^/" + request.Spec.Name + "$"}, "", &remaining)
			}
		}
		if cleanupErr != nil || strings.TrimSpace(remaining.String()) != "" {
			result.Status = "uncertain"
			returnErr = errors.Join(returnErr, errors.New("container cleanup could not be verified; do not release execution ownership"))
			return
		}
		result.CleanupVerified = true
	}()
	lines := &lineWriter{maxTotal: request.MaxOutputBytes, maxLine: request.MaxLineBytes, onLine: onLine, cancel: cancel}
	err = run(runCtx, binary, args, request.Input, lines)
	if err == nil {
		err = lines.finish()
	}
	if lines.err != nil {
		return result, lines.err
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if err != nil {
		if runCtx.Err() != nil {
			return result, runCtx.Err()
		}
		return result, errors.New("isolated process failed; native diagnostics withheld")
	}
	result.Status = "completed"
	return result, nil
}

func runProcess(ctx context.Context, binary string, args []string, input string, stdout io.Writer) error {
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = strings.NewReader(input)
	command.Stdout = stdout
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	return command.Run()
}

type lineWriter struct {
	maxTotal, maxLine, total int
	pending                  []byte
	onLine                   func([]byte) error
	cancel                   context.CancelFunc
	err                      error
}

func (w *lineWriter) fail(err error) (int, error) { w.err = err; w.cancel(); return 0, err }
func (w *lineWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(data) > w.maxTotal-w.total {
		return w.fail(errors.New("native output exceeds total limit"))
	}
	w.total += len(data)
	for _, b := range data {
		if b == '\n' {
			if err := w.emit(); err != nil {
				return w.fail(err)
			}
		} else {
			if len(w.pending) >= w.maxLine {
				return w.fail(errors.New("native output exceeds line limit"))
			}
			w.pending = append(w.pending, b)
		}
	}
	return len(data), nil
}
func (w *lineWriter) emit() error {
	line := append([]byte(nil), w.pending...)
	w.pending = w.pending[:0]
	if len(strings.TrimSpace(string(line))) == 0 {
		return nil
	}
	if err := w.onLine(line); err != nil {
		return fmt.Errorf("native event handler: %w", err)
	}
	return nil
}
func (w *lineWriter) finish() error {
	if len(w.pending) == 0 {
		return nil
	}
	if err := w.emit(); err != nil {
		_, _ = w.fail(err)
		return err
	}
	return nil
}
