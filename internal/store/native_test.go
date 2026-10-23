package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
)

func nativeFixture(t *testing.T) (*Store, Principal, Project, NativeSession) {
	t.Helper()
	s, p, project := setup(t)
	session, err := s.CreateNativeSession(context.Background(), p, project.ID, NativeCreate{Root: t.TempDir(), Binding: NativeBinding{Model: "fixture", Runtime: sandbox.Podman}, SourceDigest: "source-digest", Key: "create-native"})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, project, session
}
func startNative(t *testing.T, s *Store, p Principal, project Project, session NativeSession) (NativeSession, NativeAttempt) {
	t.Helper()
	session, attempt, claimed, err := s.ClaimNativeAttempt(context.Background(), p, project.ID, session.ID, "start", "NEVER_PERSIST_PROMPT", session.Revision, false)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	if err = s.MarkNativeLaunch(context.Background(), session.ID, attempt.Number, sandbox.ExecutionIdentity{Runtime: sandbox.Podman, Name: "awb-probe-" + strings.Repeat("a", 26)}); err != nil {
		t.Fatal(err)
	}
	return session, attempt
}

func TestNativeJournalIdempotencyFencingAndHistory(t *testing.T) {
	ctx := context.Background()
	s, p, project, session := nativeFixture(t)
	initial := session
	session, attempt := startNative(t, s, p, project, session)
	checkpoint := runtime.NativeCheckpoint{SessionID: "native-1", Text: "draft"}
	if err := s.CheckpointNative(ctx, session.ID, attempt.Number, 1, checkpoint); err != nil {
		t.Fatal(err)
	}
	saved, err := s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointNative(ctx, session.ID, attempt.Number, 1, checkpoint); err != nil {
		t.Fatal(err)
	}
	same, err := s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil || same.Revision != saved.Revision {
		t.Fatal("replayed checkpoint mutated state", err)
	}
	changed := checkpoint
	changed.Text = "different"
	if err = s.CheckpointNative(ctx, session.ID, attempt.Number, 1, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting checkpoint accepted", err)
	}
	if err = s.CheckpointNative(ctx, session.ID, attempt.Number, 3, checkpoint); !errors.Is(err, ErrConflict) {
		t.Fatal("sequence skipped", err)
	}
	if err = s.FinishNative(ctx, session.ID, attempt.Number, runtime.NativeResult{Status: "completed", SessionID: "native-1", Text: "draft", CleanupVerified: true}, "state-digest"); err != nil {
		t.Fatal(err)
	}
	saved, err = s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil || saved.Recovery != "resumable" {
		t.Fatal(saved, err)
	}
	_, _, claimed, err := s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "start", "NEVER_PERSIST_PROMPT", initial.Revision, false)
	if err != nil || claimed {
		t.Fatal("action replay launched", err)
	}
	if _, _, _, err = s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "start", "changed prompt", saved.Revision, false); !errors.Is(err, ErrConflict) {
		t.Fatal("request key reused", err)
	}
	_, second, claimed, err := s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "resume", "explicit follow-up", saved.Revision, true)
	if err != nil || !claimed || second.Number != 2 {
		t.Fatal(second, err)
	}
	if err = s.CheckpointNative(ctx, session.ID, attempt.Number, 2, checkpoint); !errors.Is(err, ErrConflict) {
		t.Fatal("old attempt wrote new generation", err)
	}
	attempts, err := s.NativeAttempts(ctx, p, project.ID, session.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Checkpoint.Text != "draft" || attempts[0].Status != "completed" {
		t.Fatal(attempts, err)
	}
	var raw string
	if err = s.db.QueryRow("SELECT data FROM native_attempts WHERE session_id=? AND number=1", session.ID).Scan(&raw); err != nil || strings.Contains(raw, "NEVER_PERSIST_PROMPT") {
		t.Fatal("raw prompt persisted", err)
	}
}

func TestNativeUnknownNeverBecomesResumable(t *testing.T) {
	for _, mode := range []string{"uncertain", "cleanup_missing", "prepared_restart", "launched_restart"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, p, project, session := nativeFixture(t)
			session, attempt, _, err := s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "action", "prompt", session.Revision, false)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "prepared_restart" {
				if err = s.MarkNativeLaunch(ctx, session.ID, attempt.Number, sandbox.ExecutionIdentity{Runtime: sandbox.Podman, Name: "owned"}); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "restart") {
				err = s.RecoverNative(ctx, session.ID, attempt.Number, true)
			} else {
				result := runtime.NativeResult{Status: "uncertain", SessionID: "native-1", CleanupVerified: true}
				if mode == "cleanup_missing" {
					result.Status = "completed"
					result.CleanupVerified = false
				}
				err = s.FinishNative(ctx, session.ID, attempt.Number, result, "state-digest")
			}
			if err != nil {
				t.Fatal(err)
			}
			session, err = s.NativeSession(ctx, p, project.ID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "prepared_restart" {
				if session.Status != "interrupted" || session.Recovery != "not_started" {
					t.Fatal(session)
				}
			} else {
				if session.Status != "uncertain" || session.Recovery != "uncertain" {
					t.Fatal("unknown result became resumable", session)
				}
				if _, _, _, err = s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "next", "follow-up", session.Revision, true); !errors.Is(err, ErrConflict) {
					t.Fatal("unknown resumed", err)
				}
			}
		})
	}
}

func TestNativeScopeAndConcurrentClaims(t *testing.T) {
	ctx := context.Background()
	s, p, project, session := nativeFixture(t)
	agent := Principal{ID: "agent", Role: "agent", ProjectID: project.ID}
	if _, err := s.NativeSessions(ctx, agent, project.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.NativeSession(ctx, p, "other-project", session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.NativeAttempts(ctx, agent, project.ID, session.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, _, claimed, err := s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, key, "prompt", session.Revision, false)
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
			wins <- claimed
		}(key)
	}
	wg.Wait()
	close(wins)
	count := 0
	for won := range wins {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Fatal("not a single winner", count)
	}
}

func TestUnavailableResumePreservesPriorNativeContext(t *testing.T) {
	ctx := context.Background()
	s, p, project, session := nativeFixture(t)
	session, attempt := startNative(t, s, p, project, session)
	if err := s.FinishNative(ctx, session.ID, attempt.Number, runtime.NativeResult{Status: "completed", SessionID: "native-1", CleanupVerified: true}, "original-state"); err != nil {
		t.Fatal(err)
	}
	session, err := s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, next, _, err := s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "resume", "explicit input", session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishNative(ctx, session.ID, next.Number, runtime.NativeResult{Status: "blocked", ErrorCode: "native_not_ready"}, ""); err != nil {
		t.Fatal(err)
	}
	session, err = s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil || session.StateDigest != "original-state" || session.Recovery != "resumable" || session.NativeID != "native-1" {
		t.Fatal(session, err)
	}
}

func TestNativeContinuationIsBoundToConfirmedPlan(t *testing.T) {
	ctx := context.Background()
	s, p, project, unconfirmed := nativeFixture(t)
	raw, err := s.CreatePlan(ctx, p, project.ID, "draft-v1", "", 0, content(t))
	v1 := planResult(t, raw, err)
	if _, err = s.ConfirmPlan(ctx, p, project.ID, "confirm-v1", v1.Version, v1.Revision, v1.Hash); err != nil {
		t.Fatal(err)
	}
	if s.NativeBasisCurrent(ctx, unconfirmed) {
		t.Fatal("old unconfirmed basis carried forward")
	}
	session, err := s.CreateNativeSession(ctx, p, project.ID, NativeCreate{Root: t.TempDir(), Binding: unconfirmed.Binding, SourceDigest: "source", Key: "confirmed-native"})
	if err != nil {
		t.Fatal(err)
	}
	if session.PlanVersion != v1.Version || session.PlanHash != v1.Hash || !s.NativeBasisCurrent(ctx, session) {
		t.Fatal("confirmed basis not captured", session)
	}
	session, attempt := startNative(t, s, p, project, session)
	if err = s.FinishNative(ctx, session.ID, attempt.Number, runtime.NativeResult{Status: "completed", SessionID: "native-1", CleanupVerified: true}, "state"); err != nil {
		t.Fatal(err)
	}
	raw, err = s.CreatePlan(ctx, p, project.ID, "draft-v2", "explicit change", 1, content(t))
	v2 := planResult(t, raw, err)
	if _, err = s.ConfirmPlan(ctx, p, project.ID, "confirm-v2", v2.Version, v2.Revision, v2.Hash); err != nil {
		t.Fatal(err)
	}
	session, err = s.NativeSession(ctx, p, project.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.NativeBasisCurrent(ctx, session) {
		t.Fatal("changed plan still appeared current")
	}
	if _, _, _, err = s.ClaimNativeAttempt(ctx, p, project.ID, session.ID, "new", "follow-up", session.Revision, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale basis resumed", err)
	}
}

func TestSchemaFourUpgradePreservesAPIRunAndCatalog(t *testing.T) {
	ctx := context.Background()
	s, p, project, role, connection := runFixture(t)
	run, err := s.CreateRun(ctx, p, project.ID, runInput(role, connection, "before-native-upgrade"))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(s.lock.Name())
	if _, err = s.db.ExecContext(ctx, "DROP TABLE native_attempts; DROP TABLE native_sessions; DELETE FROM schema_migrations WHERE version=5"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := upgraded.Close(); err != nil {
			t.Error(err)
		}
	}()
	preserved, err := upgraded.Run(ctx, p, project.ID, run.ID)
	if err != nil || preserved.Input != run.Input || preserved.Role.ID != role.ID {
		t.Fatal("API run lost on upgrade", preserved, err)
	}
	roles, err := upgraded.Roles(ctx, p)
	if err != nil || len(roles) != 1 {
		t.Fatal("roles lost", err)
	}
	sessions, err := upgraded.NativeSessions(ctx, p, project.ID)
	if err != nil || len(sessions) != 0 {
		t.Fatal("native migration failed", sessions, err)
	}
	var version int
	if err = upgraded.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 5 {
		t.Fatal(version, err)
	}
}
