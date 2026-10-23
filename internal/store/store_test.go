package store

import (
	"agent-workbench/contracts"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestFutureSchemaIsRejectedWithoutMigration(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(directory, "workbench.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY); INSERT INTO schema_migrations VALUES(999)"); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(context.Background(), directory); err == nil {
		if closeErr := opened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("future schema accepted")
	}
	var changed int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version != 999").Scan(&changed); err != nil || changed != 0 {
		t.Fatal("unsupported schema history was modified", changed, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='projects'").Scan(&changed); err != nil || changed != 0 {
		t.Fatal("tables created in unsupported database", changed, err)
	}
}

func setup(t *testing.T) (*Store, Principal, Project) {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	p := Principal{ID: "human-1", Role: "human"}
	v := Project{ID: "project-1", Name: "Fixture", Manifest: json.RawMessage(`[]`)}
	if _, err := s.CreateProject(context.Background(), p, "create-1", v); err != nil {
		t.Fatal(err)
	}
	return s, p, v
}
func content(t *testing.T) json.RawMessage {
	t.Helper()
	v, err := contracts.Files.ReadFile("examples/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func planResult(t *testing.T, raw []byte, err error) Plan {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestM1ImmutableVersionAndAuthorityWorkflow(t *testing.T) {
	s, human, project := setup(t)
	ctx := context.Background()
	agent := Principal{ID: "worker-1", Role: "agent", ProjectID: project.ID}
	raw, err := s.CreatePlan(ctx, agent, project.ID, "draft-1", "", 0, content(t))
	v1 := planResult(t, raw, err)
	if _, err = s.ConfirmPlan(ctx, agent, project.ID, "forged-confirm", 1, 1, v1.Hash); !errors.Is(err, ErrForbidden) {
		t.Fatal("agent acquired confirmation authority", err)
	}
	if _, err = s.ConfirmPlan(ctx, human, project.ID, "stale-confirm", 1, 1, "sha256:wrong"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale hash accepted", err)
	}
	raw, err = s.ConfirmPlan(ctx, human, project.ID, "confirm-1", 1, 1, v1.Hash)
	confirmed := planResult(t, raw, err)
	if confirmed.Status != "confirmed" {
		t.Fatal("confirmation missing")
	}
	replay, err := s.ConfirmPlan(ctx, human, project.ID, "confirm-1", 1, 1, v1.Hash)
	if err != nil || string(replay) != string(raw) {
		t.Fatal("confirmation retry was not idempotent", err)
	}
	if _, err = s.UpdatePlan(ctx, agent, project.ID, "mutate-v1", 1, 1, v1.Hash, content(t)); !errors.Is(err, ErrConflict) {
		t.Fatal("confirmed plan mutated", err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE plan_versions SET content='{}' WHERE project_id=? AND version=1", project.ID); err == nil {
		t.Fatal("database trigger allowed confirmed plan mutation")
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM plan_versions WHERE project_id=? AND version=1", project.ID); err == nil {
		t.Fatal("database trigger allowed confirmed plan deletion")
	}
	raw, err = s.CreatePlan(ctx, agent, project.ID, "draft-2", "新增已讨论需求，待用户确认", 1, content(t))
	v2 := planResult(t, raw, err)
	if v2.Version != 2 || v2.Status != "draft" {
		t.Fatal("V2 draft missing")
	}
	current, err := s.Project(ctx, human, project.ID)
	if err != nil || current.ActiveVersion != 1 {
		t.Fatal("unconfirmed V2 replaced active V1")
	}
	plans, err := s.Plans(ctx, human, project.ID)
	if err != nil || plans[0].Hash != v1.Hash {
		t.Fatal("V1 hash changed")
	}
	denied := Principal{ID: "worker-2", Role: "agent", ProjectID: "other-project"}
	if _, err = s.Project(ctx, denied, project.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("project scope bypass")
	}
}

func TestTransactionsIdempotencyDraftCASAndForeignKeys(t *testing.T) {
	s, human, p := setup(t)
	ctx := context.Background()
	if err := s.CheckHealth(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.AppendMessage(ctx, human, p.ID, "same-message", "hello"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	messages, err := s.Messages(ctx, human, p.ID)
	if err != nil || len(messages) != 1 {
		t.Fatal("retry duplicated message")
	}
	if _, err = s.AppendMessage(ctx, human, p.ID, "same-message", "different"); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency collision accepted")
	}
	before, err := s.Events(ctx, human, p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendMessage(ctx, human, "missing-project", "invalid", "no FK parent"); err == nil {
		t.Fatal("foreign key disabled")
	}
	after, err := s.Events(ctx, human, p.ID, 0)
	if err != nil || len(before) != len(after) {
		t.Fatal("failed transaction emitted an event")
	}
	raw, err := s.CreatePlan(ctx, human, p.ID, "draft", "", 0, content(t))
	draft := planResult(t, raw, err)
	var object map[string]any
	if err = json.Unmarshal(draft.Content, &object); err != nil {
		t.Fatal(err)
	}
	object["title"] = "Updated draft"
	changed, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = s.UpdatePlan(ctx, human, p.ID, "update", draft.Version, draft.Revision, draft.Hash, changed)
	updated := planResult(t, raw, err)
	if updated.Revision != 2 || updated.Hash == draft.Hash {
		t.Fatal("draft revision did not advance")
	}
	if _, err = s.UpdatePlan(ctx, human, p.ID, "stale-update", draft.Version, draft.Revision, draft.Hash, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("stale draft update accepted")
	}
}

func TestRestartAndSingleInstanceLock(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data ? local")
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(ctx, dir); err == nil {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("second instance acquired lock")
	}
	p := Principal{ID: "human", Role: "human"}
	project := Project{ID: "persisted", Name: "Persisted", Manifest: json.RawMessage(`[]`)}
	if _, err = s.CreateProject(ctx, p, "create", project); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err = s.Project(ctx, p, project.ID); err != nil {
		t.Fatal("project lost on restart", err)
	}
	events, err := s.Events(ctx, p, project.ID, 0)
	if err != nil || len(events) != 1 {
		t.Fatal("event replay lost", err)
	}
}

func TestCompetingDraftCannotReplaceChangedBaseline(t *testing.T) {
	s, human, p := setup(t)
	ctx := context.Background()
	raw, err := s.CreatePlan(ctx, human, p.ID, "v1", "", 0, content(t))
	v1 := planResult(t, raw, err)
	if _, err = s.ConfirmPlan(ctx, human, p.ID, "confirm-v1", v1.Version, v1.Revision, v1.Hash); err != nil {
		t.Fatal(err)
	}
	raw, err = s.CreatePlan(ctx, human, p.ID, "v2", "需求变更 A", 1, content(t))
	v2 := planResult(t, raw, err)
	raw, err = s.CreatePlan(ctx, human, p.ID, "v3", "需求变更 B", 1, content(t))
	v3 := planResult(t, raw, err)
	if _, err = s.ConfirmPlan(ctx, human, p.ID, "confirm-v2", v2.Version, v2.Revision, v2.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmPlan(ctx, human, p.ID, "stale-v3", v3.Version, v3.Revision, v3.Hash); !errors.Is(err, ErrConflict) {
		t.Fatal("draft based on V1 replaced already adopted V2", err)
	}
	current, err := s.Project(ctx, human, p.ID)
	if err != nil || current.ActiveVersion != 2 {
		t.Fatal("current plan changed after stale confirmation", err)
	}
}
