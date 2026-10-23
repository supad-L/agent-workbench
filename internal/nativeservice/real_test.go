package nativeservice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/codex"
	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/store"
	"agent-workbench/internal/workspace"
)

const realFixtureScript = `#!/bin/sh
set -eu
read -r prompt
id=native-persistent-fixture
home="$CODEX_HOME/sessions"
mkdir -p "$home"
if [ "$1" = --sandbox ]; then
 [ "$4" = resume ]
 [ "$9" = "$id" ]
 [ -f "$home/rollout-$id.jsonl" ]
fi
count=0
if [ -f "$CODEX_HOME/count" ]; then count="$(cat "$CODEX_HOME/count")"; fi
count=$((count+1))
printf '%s' "$count" > "$CODEX_HOME/count"
printf '{"type":"session_meta","payload":{"id":"%s"}}\n' "$id" > "$home/rollout-$id.jsonl"
printf '{"type":"thread.started","thread_id":"%s"}\n' "$id"
printf '{"type":"turn.started"}\n'
printf '{"type":"item.completed","item":{"id":"text-1","type":"agent_message","text":"persisted native fixture draft"}}\n'
if [ "$prompt" = crash ]; then sleep 60; fi
printf '{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":6}}\n'
`

func TestRealNativeSessionRestart(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	if kind == "" {
		t.Skip("explicit runtime/image/shared scratch required")
	}
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if image == "" || scratch == "" {
		t.Fatal("explicit local image and shared scratch required")
	}
	root, err := os.MkdirTemp(scratch, "awb-native-restart-")
	if err != nil {
		t.Fatal(err)
	}
	keep := os.Getenv("AWB_TEST_NATIVE_KEEP_ROOT")
	if keep != "" {
		if !filepath.IsAbs(keep) || filepath.Clean(keep) != keep {
			t.Fatal("keep root must be canonical absolute")
		}
		if err = os.Remove(root); err != nil {
			t.Fatal(err)
		}
		if err = os.Mkdir(keep, 0700); err != nil {
			t.Fatal("keep root must be new", err)
		}
		root, err = filepath.EvalSymlinks(keep)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		t.Cleanup(func() {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
	}
	source := filepath.Join(root, "source")
	if err = os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "fixture-codex"), []byte(realFixtureScript), 0755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "state")
	human := store.Principal{ID: "fixture-human", Role: "human"}
	project := "native-real-fixture"
	open := func() (*store.Store, *Manager) {
		t.Helper()
		s, err := store.Open(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		m, err := New(context.Background(), s, filepath.Join(data, "native-runtime"))
		if err != nil {
			t.Fatal(err)
		}
		return s, m
	}
	s, m := open()
	defer func() {
		m.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err = s.CreateProject(context.Background(), human, "project", store.Project{ID: project, Name: "真实容器内合成会话", Snapshot: source, Manifest: []byte("[]")}); err != nil {
		t.Fatal(err)
	}
	binding := store.NativeBinding{Runtime: sandbox.RuntimeKind(kind), Binary: "/workspace/fixture-codex", Model: "fixture-model", Spec: sandbox.Spec{ImageID: image, SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}}}
	session, err := m.Create(context.Background(), human, project, binding, "create")
	if err != nil {
		t.Fatal(err)
	}
	session, err = m.Run(context.Background(), human, project, session.ID, "start", "initial\n", session.Revision, false, 10*time.Second)
	if err != nil || session.Status != "completed" || session.Checkpoint.Text != "persisted native fixture draft" {
		t.Fatal(session, err)
	}
	m.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, m = open()
	view, err := m.Get(context.Background(), human, project, session.ID)
	if err != nil || !view.CanResume {
		t.Fatal(view, err)
	}
	revision := view.Session.Revision
	session, err = m.Run(context.Background(), human, project, session.ID, "follow-up", "new explicit input\n", revision, true, 10*time.Second)
	if err != nil || session.Attempt != 2 || session.Status != "completed" {
		t.Fatal(session, err)
	}
	if _, err = m.Run(context.Background(), human, project, session.ID, "follow-up", "new explicit input\n", revision, true, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	count, err := os.ReadFile(filepath.Join(session.RunDirectory, "codex", "count"))
	if err != nil || string(count) != "2" {
		t.Fatal("idempotent action repeated", string(count), err)
	}
	attempts, err := s.NativeAttempts(context.Background(), human, project, session.ID)
	if err != nil || len(attempts) != 2 || !attempts[0].Result.CleanupVerified {
		t.Fatal(attempts, err)
	}

	// A separate platform process is killed after a durable native checkpoint.
	crash, err := m.Create(context.Background(), human, project, binding, "crash-create")
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	signalFile := filepath.Join(root, "checkpoint-ready")
	child := exec.Command(executable, "-test.run=^TestNativeCrashHelper$", "-test.timeout=25s")
	for _, name := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TEMP", "TMP", "LANG"} {
		if value, ok := os.LookupEnv(name); ok {
			child.Env = append(child.Env, name+"="+value)
		}
	}
	child.Env = append(child.Env, "AWB_NATIVE_CRASH_HELPER=1", "AWB_NATIVE_CRASH_DATA="+data, "AWB_NATIVE_CRASH_SESSION="+crash.ID, "AWB_NATIVE_CRASH_SIGNAL="+signalFile)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err = os.Stat(signalFile); err == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	killErr := child.Process.Kill()
	waitErr := child.Wait()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		t.Fatal(killErr)
	}
	// Reconciliation runs even on a failed readiness check, before fixture cleanup.
	s, m = open()
	if !ready || waitErr == nil {
		t.Fatal("child did not reach the checkpoint before forced shutdown")
	}
	restored, err := m.Get(context.Background(), human, project, crash.ID)
	if err != nil || restored.Session.Status != "uncertain" || restored.CanResume || !restored.Session.CleanupVerified || restored.Session.Checkpoint.Text != "persisted native fixture draft" {
		t.Fatal("crash reconciliation", restored, err)
	}
	count, err = os.ReadFile(filepath.Join(restored.Session.RunDirectory, "codex", "count"))
	if err != nil || string(count) != "1" {
		t.Fatal("startup reran unknown invocation", string(count), err)
	}
}

type signalCheckpoint struct {
	executor runtime.NativeExecutor
	file     string
}

func (s signalCheckpoint) Capabilities() runtime.NativeCapabilities { return s.executor.Capabilities() }
func (s signalCheckpoint) Run(ctx context.Context, request runtime.NativeRequest, emit func(runtime.NativeEvent) error) (runtime.NativeResult, error) {
	checkpoint := request.Checkpoint
	request.Checkpoint = func(ctx context.Context, value runtime.NativeCheckpoint) error {
		if err := checkpoint(ctx, value); err != nil {
			return err
		}
		if value.Text != "" {
			return os.WriteFile(s.file, []byte("checkpoint durable"), 0600)
		}
		return nil
	}
	return s.executor.Run(ctx, request, emit)
}

func TestNativeCrashHelper(t *testing.T) {
	if os.Getenv("AWB_NATIVE_CRASH_HELPER") != "1" {
		t.Skip("private subprocess helper")
	}
	data, id, file := os.Getenv("AWB_NATIVE_CRASH_DATA"), os.Getenv("AWB_NATIVE_CRASH_SESSION"), os.Getenv("AWB_NATIVE_CRASH_SIGNAL")
	if data == "" || id == "" || file == "" {
		t.Fatal("missing helper inputs")
	}
	s, err := store.Open(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newManager(context.Background(), s, filepath.Join(data, "native-runtime"), func(binding store.NativeBinding, execution sandbox.Execution) runtime.NativeExecutor {
		return signalCheckpoint{executor: codex.Executor{Base: execution, Binary: binding.Binary}, file: file}
	}, sandbox.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		m.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	human := store.Principal{ID: "fixture-human", Role: "human"}
	view, err := m.Get(context.Background(), human, "native-real-fixture", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Run(context.Background(), human, "native-real-fixture", id, "crash-run", "crash\n", view.Session.Revision, false, 15*time.Second); err != nil && !strings.Contains(err.Error(), "interrupted") {
		t.Fatal(err)
	}
}
