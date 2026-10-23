package nativeservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-workbench/internal/accountcoord"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/store"
	"agent-workbench/internal/workspace"
)

type fixtureExecutor struct {
	execution sandbox.Execution
	mode      string
	calls     *atomic.Int32
	ready     chan struct{}
}

func (f fixtureExecutor) Capabilities() runtime.NativeCapabilities {
	return runtime.NativeCapabilities{Resume: true}
}
func (f fixtureExecutor) Run(ctx context.Context, request runtime.NativeRequest, _ func(runtime.NativeEvent) error) (runtime.NativeResult, error) {
	f.calls.Add(1)
	if err := f.execution.BeforeLaunch(ctx, sandbox.ExecutionIdentity{Runtime: f.execution.Runtime, Name: "awb-probe-" + strings.Repeat("a", 26)}); err != nil {
		return runtime.NativeResult{Status: "blocked"}, err
	}
	id := "native-1"
	if request.ResumeSessionID != "" && request.ResumeSessionID != id {
		return runtime.NativeResult{Status: "uncertain"}, errors.New("wrong resume")
	}
	directory := filepath.Join(f.execution.StateDirectory, "sessions")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return runtime.NativeResult{}, err
	}
	if err := os.WriteFile(filepath.Join(directory, "rollout-"+id+".jsonl"), []byte("opaque synthetic native context\n"), 0644); err != nil {
		return runtime.NativeResult{}, err
	}
	checkpoint := runtime.NativeCheckpoint{SessionID: id, Text: "saved draft"}
	if err := request.Checkpoint(ctx, checkpoint); err != nil {
		return runtime.NativeResult{Status: "uncertain", SessionID: id, CleanupVerified: true}, err
	}
	if f.mode == "wait" {
		close(f.ready)
		<-ctx.Done()
	}
	if f.mode == "interrupted" || f.mode == "wait" {
		return runtime.NativeResult{Status: "interrupted", SessionID: id, Text: checkpoint.Text, CleanupVerified: true}, context.Canceled
	}
	checkpoint.ProviderCompleted = true
	checkpoint.Usage = runtime.Usage{Known: true, InputTokens: 12, OutputTokens: 6}
	if err := request.Checkpoint(ctx, checkpoint); err != nil {
		return runtime.NativeResult{Status: "uncertain", SessionID: id, CleanupVerified: true}, err
	}
	status := "completed"
	if f.mode == "uncertain" {
		status = "uncertain"
	}
	return runtime.NativeResult{Status: status, SessionID: id, Text: checkpoint.Text, Usage: checkpoint.Usage, CleanupVerified: f.mode != "unclean"}, nil
}

func bindAccount(t *testing.T, f fixture) store.NativeSession {
	t.Helper()
	c := accountcoord.New()
	op, err := c.Begin(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Finish(op, true, false, true); err != nil {
		t.Fatal(err)
	}
	f.manager.accounts = c
	binding := f.session.Binding
	ref := c.View().Reference
	binding.Account = &ref
	session, err := f.manager.Create(context.Background(), f.human, f.project, binding, "account-bound")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAccountLogoutCancelsAndWaitsForDurableNativeSettlement(t *testing.T) {
	f := setupNative(t, "wait")
	session := bindAccount(t, f)
	finished := make(chan error, 1)
	go func() {
		_, err := f.manager.Run(context.Background(), f.human, f.project, session.ID, "account-start", "input", session.Revision, false, time.Minute)
		finished <- err
	}()
	<-f.ready
	if f.manager.accounts.View().Active != 1 {
		t.Fatal("missing slot")
	}
	op, err := f.manager.accounts.Begin(context.Background(), "logout")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.store.NativeSession(context.Background(), f.human, f.project, session.ID)
	if err != nil || stored.Status != "interrupted" || !stored.CleanupVerified || stored.Checkpoint.Text != "saved draft" {
		t.Fatal(stored, err)
	}
	if err = <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = f.manager.accounts.Finish(op, false, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.manager.Run(context.Background(), f.human, f.project, stored.ID, "old-grant", "resume", stored.Revision, true, time.Second); !errors.Is(err, accountcoord.ErrUnavailable) {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatal("revoked execution started again")
	}
}

func TestAccountCleanupDebtSurvivesManagerRestart(t *testing.T) {
	f := setupNative(t, "unclean")
	session := bindAccount(t, f)
	_, err := f.manager.Run(context.Background(), f.human, f.project, session.ID, "account-start", "input", session.Revision, false, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if f.manager.accounts.View().State != "blocked" {
		t.Fatal("unclean run released account")
	}
	f.manager.Close()
	c := accountcoord.New()
	m, err := newManager(context.Background(), f.store, filepath.Join(f.root, "runtime"), f.manager.factory, f.manager.cleanup, c)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if c.View().State != "blocked" {
		t.Fatal("restart forgot cleanup debt")
	}
	if _, err = c.Begin(context.Background(), "login"); !errors.Is(err, accountcoord.ErrUnavailable) {
		t.Fatal(err)
	}
}

type fixture struct {
	manager      *Manager
	store        *store.Store
	human        store.Principal
	project      string
	session      store.NativeSession
	root, dbPath string
	calls        *atomic.Int32
	ready        chan struct{}
}

func setupNative(t *testing.T, mode string) fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err = os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "project.txt"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p := store.Principal{ID: "human", Role: "human"}
	project := "native-project"
	if _, err = s.CreateProject(context.Background(), p, "project", store.Project{ID: project, Name: "Native fixture", Snapshot: source, Manifest: []byte("[]")}); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	ready := make(chan struct{})
	build := func(_ store.NativeBinding, execution sandbox.Execution) runtime.NativeExecutor {
		return fixtureExecutor{execution: execution, mode: mode, calls: calls, ready: ready}
	}
	m, err := newManager(context.Background(), s, filepath.Join(root, "runtime"), build, func(context.Context, sandbox.ExecutionIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	session, err := m.Create(context.Background(), p, project, store.NativeBinding{Runtime: sandbox.Podman, Model: "fixture", Spec: sandbox.Spec{ImageID: "sha256:" + strings.Repeat("a", 64), SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}}}, "create")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture{m, s, p, project, session, root, dbPath, calls, ready}
}

func TestPersistentSessionContinueAndReplay(t *testing.T) {
	f := setupNative(t, "complete")
	ctx := context.Background()
	first, err := f.manager.Run(ctx, f.human, f.project, f.session.ID, "start", "initial", f.session.Revision, false, time.Second)
	if err != nil || first.Status != "completed" {
		t.Fatal(first, err)
	}
	view, err := f.manager.Get(ctx, f.human, f.project, first.ID)
	if err != nil || !view.CanResume || view.LiveReady {
		t.Fatal(view, err)
	}
	resumed, err := f.manager.Run(ctx, f.human, f.project, first.ID, "resume", "new explicit input", first.Revision, true, time.Second)
	if err != nil || resumed.Attempt != 2 {
		t.Fatal(resumed, err)
	}
	if _, err = f.manager.Run(ctx, f.human, f.project, first.ID, "resume", "new explicit input", first.Revision, true, time.Second); err != nil || f.calls.Load() != 2 {
		t.Fatal("resume replay executed", f.calls.Load(), err)
	}
	attempts, err := f.store.NativeAttempts(ctx, f.human, f.project, first.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Checkpoint.Text != "saved draft" {
		t.Fatal(attempts, err)
	}
	f.manager.Close()
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	m, err := newManager(ctx, reopened, filepath.Join(f.root, "runtime"), f.manager.factory, func(context.Context, sandbox.ExecutionIdentity) error {
		t.Fatal("finished session cleanup repeated")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	restored, err := m.Get(ctx, f.human, f.project, first.ID)
	if err != nil || !restored.CanResume || restored.Session.Attempt != 2 || f.calls.Load() != 2 {
		t.Fatal(restored, err)
	}
}

func TestInterruptedAndUnknownOutcomes(t *testing.T) {
	for _, mode := range []string{"interrupted", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			f := setupNative(t, mode)
			session, err := f.manager.Run(context.Background(), f.human, f.project, f.session.ID, "start", "input", f.session.Revision, false, time.Second)
			if mode == "interrupted" && err == nil {
				t.Fatal("interruption hidden")
			}
			view, err := f.manager.Get(context.Background(), f.human, f.project, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.CanResume != (mode == "interrupted") {
				t.Fatal(view)
			}
			if mode == "uncertain" {
				if _, err = f.manager.Run(context.Background(), f.human, f.project, session.ID, "next", "continue", session.Revision, true, time.Second); err == nil || f.calls.Load() != 1 {
					t.Fatal("unknown outcome retried", err)
				}
			}
		})
	}
}

func TestContinuationRejectsChangedOrMissingFiles(t *testing.T) {
	for _, mode := range []string{"source", "state", "missing", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := setupNative(t, "complete")
			session, err := f.manager.Run(context.Background(), f.human, f.project, f.session.ID, "start", "input", f.session.Revision, false, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(session.RunDirectory, "codex")
			switch mode {
			case "source":
				err = os.WriteFile(filepath.Join(session.Binding.Spec.SourceRoot, "project.txt"), []byte("changed"), 0644)
			case "state":
				err = os.WriteFile(filepath.Join(home, "sessions", "rollout-native-1.jsonl"), []byte("changed"), 0644)
			case "missing":
				err = os.RemoveAll(home)
			case "symlink":
				if err = os.RemoveAll(home); err == nil {
					err = os.Symlink(session.Binding.Spec.SourceRoot, home)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			view, err := f.manager.Get(context.Background(), f.human, f.project, session.ID)
			if err != nil || view.CanResume {
				t.Fatal(view, err)
			}
			if _, err = f.manager.Run(context.Background(), f.human, f.project, session.ID, "resume", "new input", session.Revision, true, time.Second); err == nil || f.calls.Load() != 1 {
				t.Fatal("changed files resumed", err)
			}
		})
	}
}

func TestRecoveryOnlyReconcilesNeverExecutes(t *testing.T) {
	for _, mode := range []string{"prepared", "launched", "cleanup_failure", "provider_completed"} {
		t.Run(mode, func(t *testing.T) {
			f := setupNative(t, "complete")
			ctx := context.Background()
			session, attempt, _, err := f.store.ClaimNativeAttempt(ctx, f.human, f.project, f.session.ID, "start", "input", f.session.Revision, false)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "prepared" {
				if err = f.store.MarkNativeLaunch(ctx, session.ID, attempt.Number, sandbox.ExecutionIdentity{Runtime: sandbox.Podman, Name: "awb-probe-" + strings.Repeat("a", 26)}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "provider_completed" {
				if err = f.store.CheckpointNative(ctx, session.ID, attempt.Number, 1, runtime.NativeCheckpoint{SessionID: "native-1", ProviderCompleted: true}); err != nil {
					t.Fatal(err)
				}
			}
			f.manager.Close()
			if err = f.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(ctx, f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := reopened.Close(); err != nil {
					t.Error(err)
				}
			}()
			cleanups := 0
			manager, err := newManager(ctx, reopened, filepath.Join(f.root, "runtime"), f.manager.factory, func(context.Context, sandbox.ExecutionIdentity) error {
				cleanups++
				if mode == "cleanup_failure" {
					return errors.New("engine unavailable")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			view, err := manager.Get(ctx, f.human, f.project, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != 0 || view.CanResume {
				t.Fatal("recovery executed", view)
			}
			if mode == "prepared" {
				if cleanups != 0 || view.Session.Status != "interrupted" || view.Session.Recovery != "not_started" {
					t.Fatal(view)
				}
			} else if view.Session.Status != "uncertain" || cleanups != 1 || view.Session.CleanupVerified == (mode == "cleanup_failure") {
				t.Fatal(view)
			}
		})
	}
}

func TestCloseCancelsAndSettlesCurrentAttempt(t *testing.T) {
	f := setupNative(t, "wait")
	done := make(chan error, 1)
	go func() {
		_, err := f.manager.Run(context.Background(), f.human, f.project, f.session.ID, "start", "input", f.session.Revision, false, time.Second)
		done <- err
	}()
	select {
	case <-f.ready:
	case <-time.After(time.Second):
		t.Fatal("checkpoint not saved")
	}
	f.manager.Close()
	if err := <-done; err == nil {
		t.Fatal("close did not cancel")
	}
	session, err := f.store.NativeSession(context.Background(), f.human, f.project, f.session.ID)
	if err != nil || session.Status != "interrupted" || !session.CleanupVerified {
		t.Fatal(session, err)
	}
}
