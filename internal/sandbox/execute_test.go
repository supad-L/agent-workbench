package sandbox

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-workbench/internal/workspace"
)

func executionFixture(t *testing.T) Execution {
	t.Helper()
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Execution{Runtime: Podman, Spec: Spec{ImageID: "sha256:" + strings.Repeat("a", 64), SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}, Command: []string{"/bin/sh", "-c", "cat"}}, Input: "private prompt", Timeout: time.Second, MaxOutputBytes: 100, MaxLineBytes: 50}
}

func TestExecutionLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "exit", "cancel", "timeout", "handler", "line_limit", "total_limit", "cleanup", "leftover", "list_failure"} {
		t.Run(mode, func(t *testing.T) {
			request := executionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var received []string
			var launched, removed bool
			runner := func(runCtx context.Context, binary string, args []string, input string, out io.Writer) error {
				if binary != "/selected/podman" {
					t.Fatal("unexpected engine")
				}
				switch args[0] {
				case "run":
					launched = true
					if input != "private prompt" || !reflect.DeepEqual(args[len(args)-3:], []string{request.Spec.ImageID, "-c", "cat"}) {
						t.Fatal("input or entrypoint not preserved")
					}
					text := strings.Join(args, " ")
					for _, required := range []string{"--network=none", "--pull=never", "--read-only", "--entrypoint /bin/sh", "HOME=/tmp/awb-home", "CODEX_HOME=/tmp/awb-home/codex", "--interactive"} {
						if !strings.Contains(text, required) {
							t.Fatal("missing boundary", required)
						}
					}
					switch mode {
					case "exit":
						return errors.New("secret raw stderr")
					case "cancel":
						cancel()
						<-runCtx.Done()
						return runCtx.Err()
					case "timeout":
						<-runCtx.Done()
						return runCtx.Err()
					case "line_limit":
						_, err := io.WriteString(out, strings.Repeat("x", 51))
						return err
					case "total_limit":
						_, err := io.WriteString(out, strings.Repeat("x", 101))
						return err
					default:
						for _, chunk := range []string{"one\ntw", "o\nlast"} {
							if _, err := io.WriteString(out, chunk); err != nil {
								return err
							}
						}
						return nil
					}
				case "container":
					if runCtx.Err() != nil {
						t.Fatal("cleanup inherited cancelled context")
					}
					if mode == "list_failure" {
						return errors.New("no engine")
					}
					if !removed || mode == "leftover" {
						_, err := io.WriteString(out, "owned-container")
						return err
					}
					return nil
				case "rm":
					if len(args) != 4 || args[2] != "--time=0" {
						t.Fatal("Podman cleanup must not wait for the default stop timeout", args)
					}
					if mode == "cleanup" {
						return errors.New("rm failed")
					}
					removed = true
					return nil
				default:
					t.Fatal("unexpected operation", args)
					return nil
				}
			}
			if mode == "timeout" {
				request.Timeout = time.Millisecond
			}
			result, err := execute(ctx, request, func(line []byte) error {
				if mode == "handler" {
					return errors.New("handler stopped")
				}
				received = append(received, string(line))
				return nil
			}, func(name string) (string, error) {
				if name != "podman" {
					t.Fatal(name)
				}
				return "/selected/podman", nil
			}, runner)
			if !launched {
				t.Fatal("not launched")
			}
			if mode == "success" {
				if err != nil || result.Status != "completed" || !result.CleanupVerified || !reflect.DeepEqual(received, []string{"one", "two", "last"}) {
					t.Fatalf("%+v %v %v", result, err, received)
				}
			} else if err == nil || result.Status == "completed" {
				t.Fatalf("unexpected success: %+v", result)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("raw diagnostics leaked")
			}
			uncertain := mode == "cleanup" || mode == "leftover" || mode == "list_failure"
			if uncertain != (result.Status == "uncertain") || uncertain == result.CleanupVerified {
				t.Fatalf("cleanup: %+v", result)
			}
		})
	}
}

func TestExecutionRejectsBeforeLaunch(t *testing.T) {
	for _, mode := range []string{"runtime", "timeout", "output", "line", "input", "read_scope", "spec", "missing_engine", "cancelled", "callback"} {
		t.Run(mode, func(t *testing.T) {
			request := executionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callback := func([]byte) error { return nil }
			switch mode {
			case "runtime":
				request.Runtime = ""
			case "timeout":
				request.Timeout = 0
			case "output":
				request.MaxOutputBytes = 0
			case "line":
				request.MaxLineBytes = 101
			case "input":
				request.Input = strings.Repeat("x", (1<<20)+1)
			case "read_scope":
				request.Spec.Scope.ReadPaths = []string{"src/"}
			case "spec":
				request.Spec.ImageID = "latest"
			case "cancelled":
				cancel()
			case "callback":
				callback = nil
			}
			result, err := execute(ctx, request, callback, func(string) (string, error) { return "", errors.New("not installed") }, func(context.Context, string, []string, string, io.Writer) error {
				t.Fatal("launched invalid request")
				return nil
			})
			if err == nil || result.Status != "blocked" || result.CleanupVerified {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestLineWriterBoundsAndCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lines []string
	writer := &lineWriter{maxTotal: 100, maxLine: 10, onLine: func(line []byte) error { lines = append(lines, string(line)); return nil }, cancel: cancel}
	for _, chunk := range []string{"\n \n你", "好\nend"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.finish(); err != nil || !reflect.DeepEqual(lines, []string{"你好", "end"}) {
		t.Fatalf("%v %v", lines, err)
	}
	writer.onLine = func([]byte) error { return errors.New("stop") }
	if _, err := writer.Write([]byte("fail\n")); err == nil {
		t.Fatal("callback error ignored")
	}
	if ctx.Err() == nil {
		t.Fatal("process not cancelled")
	}
	if _, err := writer.Write([]byte("later")); err == nil {
		t.Fatal("accepted after failure")
	}
}

// TestRealOfflineExecution is opt-in and never reads credentials or pulls images.
func TestRealOfflineExecution(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	if kind == "" {
		t.Skip("set explicit AWB_TEST_SANDBOX_RUNTIME, IMAGE and SCRATCH for real offline isolation")
	}
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if image == "" || scratch == "" {
		t.Fatal("explicit immutable image ID and shared scratch required")
	}
	root, err := os.MkdirTemp(scratch, "awb-execution-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	source, writable := filepath.Join(root, "source"), filepath.Join(root, "writable")
	for _, dir := range []string{filepath.Join(source, "src"), filepath.Join(source, "protected"), writable} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(writable, 0777); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(source, "protected", "original.txt")
	if err := os.WriteFile(protected, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/workspace/protected/original.txt", filepath.Join(writable, "escape")); err != nil {
		t.Fatal(err)
	}
	request := Execution{Runtime: RuntimeKind(kind), Spec: Spec{ImageID: image, SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}, WriteDirs: []string{"src/"}}, Writable: map[string]string{"src/": writable}, Command: []string{"/bin/sh", "-c", `set -eu
read -r prompt
[ "$prompt" = 'fixture input' ]
mkdir -p "$HOME" "$CODEX_HOME" "$XDG_CACHE_HOME"
printf cache > "$XDG_CACHE_HOME/test"
printf allowed > /workspace/src/allowed.txt
if printf bad > /workspace/protected/original.txt 2>/dev/null; then exit 11; fi
if printf bad > /workspace/src/escape 2>/dev/null; then exit 12; fi
if touch /etc/awb-execution 2>/dev/null; then exit 13; fi
printf 'first\n'
printf 'last'
`}}, Input: "fixture input\n", Timeout: 15 * time.Second, MaxOutputBytes: 100, MaxLineBytes: 50}
	var lines []string
	result, err := Execute(context.Background(), request, func(line []byte) error { lines = append(lines, string(line)); return nil })
	if err != nil || result.Status != "completed" || !result.CleanupVerified || !reflect.DeepEqual(lines, []string{"first", "last"}) {
		t.Fatalf("real execution: %+v %v %v", result, err, lines)
	}
	original, err := os.ReadFile(protected)
	if err != nil || string(original) != "original" {
		t.Fatalf("protected source changed: %v", err)
	}
	allowed, err := os.ReadFile(filepath.Join(writable, "allowed.txt"))
	if err != nil || string(allowed) != "allowed" {
		t.Fatalf("write missing: %v", err)
	}
	for _, mode := range []string{"cancel", "timeout", "output"} {
		t.Run(mode, func(t *testing.T) {
			req := request
			req.Spec.Command = []string{"/bin/sh", "-c", "printf 'ready\\n'; sleep 30"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			handler := func([]byte) error {
				if mode == "cancel" {
					cancel()
				}
				return nil
			}
			switch mode {
			case "timeout":
				req.Timeout = 500 * time.Millisecond
			case "output":
				req.Spec.Command = []string{"/bin/sh", "-c", "while :; do printf '0123456789\\n'; done"}
			}
			result, err := Execute(ctx, req, handler)
			if err == nil || result.Status != "failed" || !result.CleanupVerified {
				t.Fatalf("real %s: %+v %v", mode, result, err)
			}
		})
	}
}

func TestDockerExecutionCleanup(t *testing.T) {
	request := executionFixture(t)
	request.Runtime = Docker
	removed := false
	runner := func(_ context.Context, binary string, args []string, _ string, out io.Writer) error {
		if binary != "/selected/docker" {
			t.Fatal("engine fallback")
		}
		switch args[0] {
		case "run":
			return nil
		case "container":
			if !removed {
				_, err := io.WriteString(out, "owned")
				return err
			}
			return nil
		case "rm":
			if len(args) != 3 || args[1] != "-f" {
				t.Fatal("non-Docker removal arguments", args)
			}
			removed = true
			return nil
		default:
			t.Fatal(args)
			return nil
		}
	}
	result, err := execute(context.Background(), request, func([]byte) error { return nil }, func(string) (string, error) { return "/selected/docker", nil }, runner)
	if err != nil || !removed || !result.CleanupVerified {
		t.Fatalf("%+v %v", result, err)
	}
}
