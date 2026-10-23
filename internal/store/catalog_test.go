package store

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func catalogConnection() catalog.Connection {
	return catalog.Connection{ID: "conn-1", Label: "合成连接", Revision: 1, AccountID: "synthetic-account", MaxConcurrent: 1, Enabled: true, Config: apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: "https://gateway.invalid", AddressKind: "base_url"}, Model: "model-1", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}}, Models: []catalog.Model{{ID: "model-1", Tools: "supported"}}}
}

func TestCatalogRoleBindingsAuthorityCASAndRestart(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	s, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	human := Principal{ID: "user", Role: "human"}
	agent := Principal{ID: "worker", Role: "agent", ProjectID: "project-1"}
	c := catalogConnection()
	if err = s.PutConnection(ctx, human, c); err != nil {
		t.Fatal(err)
	}
	if err = s.PutConnection(ctx, human, c); err != nil {
		t.Fatal("idempotent projection rejected", err)
	}
	c.Label = "same revision changed"
	if err = s.PutConnection(ctx, human, c); !errors.Is(err, ErrConflict) {
		t.Fatal("stale projection accepted", err)
	}
	role := catalog.Templates()[1]
	role.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "model-1"}
	role.Enabled = true
	if _, err = s.SaveRole(ctx, agent, role); !errors.Is(err, ErrForbidden) {
		t.Fatal("agent gained role mutation authority", err)
	}
	role, err = s.SaveRole(ctx, human, role)
	if err != nil {
		t.Fatal(err)
	}
	var successful atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := s.SaveRole(ctx, human, role); err == nil {
				successful.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successful.Load() != 1 {
		t.Fatal("stale role edit race", successful.Load())
	}
	if err = s.DeleteConnection(ctx, human, c.ID, 1); err == nil {
		t.Fatal("referenced connection deleted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := s.Roles(ctx, human)
	if err != nil || len(roles) != 1 || roles[0].Revision != 2 {
		t.Fatal("roles lost on restart", roles, err)
	}
	connections, err := s.Connections(ctx, human)
	if err != nil || len(connections) != 1 {
		t.Fatal("connection metadata lost", err)
	}
	if err = s.DeleteRole(ctx, human, role.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete accepted", err)
	}
	if err = s.DeleteRole(ctx, human, role.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConnection(ctx, human, c.ID, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRoleRejectsMissingModelTextOnlyConnectionAndUnconsentedFallback(t *testing.T) {
	s, human, _ := setup(t)
	ctx := context.Background()
	c := catalogConnection()
	c.Models[0].Tools = "unsupported"
	if err := s.PutConnection(ctx, human, c); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*catalog.Role)
	}{
		{"missing model", func(r *catalog.Role) { r.Primary.ModelID = "missing" }},
		{"text only with tools", func(r *catalog.Role) {}},
		{"unconsented fallback", func(r *catalog.Role) {
			r.Tools = []string{}
			r.Fallback.Enabled = true
			r.Fallback.Candidates = []catalog.Choice{{ConnectionID: c.ID, ModelID: "model-1"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := catalog.Templates()[0]
			r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "model-1"}
			r.Enabled = true
			test.change(&r)
			if _, err := s.SaveRole(ctx, human, r); err == nil {
				t.Fatal("invalid role accepted")
			}
		})
	}
	r := catalog.Templates()[0]
	r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "model-1"}
	r.Tools = []string{}
	r.Enabled = true
	if _, err := s.SaveRole(ctx, human, r); err != nil {
		t.Fatal("text only discussion role rejected", err)
	}
}

func TestM1DatabaseUpgradesWithoutLosingProjects(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(directory, "workbench.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO projects VALUES('old-project','旧项目','','',?,0,'fixture')", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	var version int
	if err = s.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	if _, err = s.Project(context.Background(), Principal{ID: "user", Role: "human"}, "old-project"); err != nil {
		t.Fatal("M1 data lost", err)
	}
}
