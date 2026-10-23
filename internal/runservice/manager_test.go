package runservice

import (
	"agent-workbench/contracts"
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/internal/store"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type modelFunc func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error)

func (f modelFunc) Turn(ctx context.Context, r runtime.Request, emit func(runtime.Event) error) (runtime.Result, error) {
	return f(ctx, r, emit)
}

func fixture(t *testing.T, factory Factory) (*store.Store, *Manager, store.Principal, catalog.Role, catalog.Connection) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	p := store.Principal{ID: "human", Role: "human"}
	if _, err = s.CreateProject(ctx, p, "project", store.Project{ID: "project", Name: "Private project name", Manifest: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	c := catalog.Connection{ID: "connection", Label: "Primary", Revision: 1, AccountID: "account", MaxConcurrent: 1, Enabled: true, Config: apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: "https://gateway.invalid", AddressKind: "base_url"}, Model: "primary", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, MaxOutputTokens: 64}, Models: []catalog.Model{{ID: "primary", Tools: "supported", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}}
	if err = s.PutConnection(ctx, p, c); err != nil {
		t.Fatal(err)
	}
	r := catalog.Templates()[0]
	r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "primary"}
	r.Tools = []string{"get_status", "run_command", "read_project", "read_plan", "list_roles"}
	r.Enabled = true
	r, err = s.SaveRole(ctx, p, r)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(ctx, s, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return s, m, p, r, c
}

func input(r catalog.Role, c catalog.Connection, key string) store.RunInput {
	return store.RunInput{RoleID: r.ID, RoleRevision: r.Revision, Input: "trial input", Scope: "conversation_only", Key: key, ConnectionRevisions: map[string]int{c.ID: c.Revision}}
}
func awaitRun(t *testing.T, s *store.Store, p store.Principal, id string) store.Run {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.Run(context.Background(), p, "project", id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return store.Run{}
}
func factoryFor(model runtime.Model) Factory {
	return func(c apiconfig.Config, _ func(string) string) (runtime.Model, func(), apiconfig.Summary, error) {
		summary, err := c.Validate()
		return model, func() {}, summary, err
	}
}

func TestManagerToolLoopScopedReadOnlyAndUsage(t *testing.T) {
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, r runtime.Request, emit func(runtime.Event) error) (runtime.Result, error) {
		if len(r.Tools) != 1 || r.Tools[0].Name != "get_status" {
			t.Error("trial exposed project or development tools", r.Tools)
		}
		if calls.Add(1) == 1 {
			return runtime.Result{Calls: []runtime.ToolCall{{ID: "status", Name: "get_status", Arguments: json.RawMessage(`{}`)}}, Usage: runtime.Usage{Known: true, InputTokens: 20, OutputTokens: 5}}, nil
		}
		if !strings.Contains(r.Messages[len(r.Messages)-1].Text, "read_only_trial") {
			t.Error("broker result missing")
		}
		if err := emit(runtime.Event{Type: "text_delta", Text: "done"}); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Text: "done", Usage: runtime.Usage{Known: true, InputTokens: 30, OutputTokens: 6}}, nil
	})
	s, m, p, r, c := fixture(t, factoryFor(model))
	run, err := m.Start(context.Background(), p, "project", input(r, c, "run"), nil)
	if err != nil {
		t.Fatal(err)
	}
	final := awaitRun(t, s, p, run.ID)
	if final.Status != "completed" || final.State.Text != "done" || calls.Load() != 2 {
		t.Fatal(final, calls.Load())
	}
	stats, _, err := s.Usage(context.Background(), p)
	if err != nil || stats[0].Cost != 61 || stats[0].Held != 0 {
		t.Fatal(stats, err)
	}
	retry, err := m.Start(context.Background(), p, "project", input(r, c, "run"), nil)
	if err != nil || retry.ID != run.ID || calls.Load() != 2 {
		t.Fatal("retry repeated model", err)
	}
}

func TestCancellationRevocationAndShutdownReleaseClients(t *testing.T) {
	for _, kind := range []string{"cancel", "connection", "role", "shutdown"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan struct{})
			model := modelFunc(func(ctx context.Context, _ runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
				close(started)
				<-ctx.Done()
				return runtime.Result{}, ctx.Err()
			})
			s, m, p, r, c := fixture(t, factoryFor(model))
			run, err := m.Start(context.Background(), p, "project", input(r, c, "run"), nil)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("model not started")
			}
			if _, err = m.Start(context.Background(), p, "project", input(r, c, "busy"), nil); err == nil {
				t.Fatal("account overbooked")
			}
			switch kind {
			case "cancel":
				if err = m.Cancel(context.Background(), p, "project", run.ID); err != nil {
					t.Fatal(err)
				}
			case "connection":
				m.RevokeConnection(c.ID)
			case "role":
				m.RevokeRole(r.ID)
			case "shutdown":
				m.Close()
			}
			final := awaitRun(t, s, p, run.ID)
			expected := "cancelled"
			if kind == "shutdown" {
				expected = "interrupted"
			}
			if final.Status != expected {
				t.Fatal(final.Status)
			}
			stats, _, err := s.Usage(context.Background(), p)
			if err != nil || stats[0].Held == 0 || stats[0].UnknownRequests != 1 {
				t.Fatal("uncertain canceled request became free", stats, err)
			}
		})
	}
}

func TestFallbackRequiresConsentAndDropsProjectTools(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(string(rune(status)), func(t *testing.T) {
			var primary, backup atomic.Int32
			factory := func(c apiconfig.Config, _ func(string) string) (runtime.Model, func(), apiconfig.Summary, error) {
				summary, err := c.Validate()
				return modelFunc(func(_ context.Context, r runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
					if c.Model == "primary" {
						primary.Add(1)
						return runtime.Result{}, &direct.Failure{Status: status}
					}
					backup.Add(1)
					if len(r.Tools) != 1 || r.Tools[0].Name != "get_status" || len(r.Messages) != 1 || r.Messages[0].Text != "trial input" {
						t.Error("fallback leaked disallowed context", r)
					}
					return runtime.Result{Text: "backup", Usage: runtime.Usage{Known: true, InputTokens: 5, OutputTokens: 2}}, nil
				}), func() {}, summary, err
			}
			s, m, p, r, c := fixture(t, factory)
			alternate := c
			alternate.ID = "backup"
			alternate.AccountID = "backup-account"
			alternate.Config.Model = "backup"
			alternate.Models = []catalog.Model{{ID: "backup", Tools: "supported", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}
			if err := s.PutConnection(context.Background(), p, alternate); err != nil {
				t.Fatal(err)
			}
			r.Fallback = catalog.Fallback{Enabled: true, Candidates: []catalog.Choice{{ConnectionID: alternate.ID, ModelID: "backup"}}, DataScope: "conversation_only", Currency: "CNY", MaxExtraCost: 1000000}
			r, err := s.SaveRole(context.Background(), p, r)
			if err != nil {
				t.Fatal(err)
			}
			in := input(r, c, "fallback")
			in.Scope = "project_summary"
			in.ConnectionRevisions[alternate.ID] = 1
			run, err := m.Start(context.Background(), p, "project", in, nil)
			if err != nil {
				t.Fatal(err)
			}
			final := awaitRun(t, s, p, run.ID)
			if status == 401 {
				if primary.Load() != 1 || backup.Load() != 0 || final.Status != "failed" {
					t.Fatal("auth failure switched account", primary.Load(), backup.Load(), final.Status)
				}
			} else if primary.Load() != 2 || backup.Load() != 1 || final.Status != "completed" || final.Scope != "conversation_only" {
				t.Fatal(primary.Load(), backup.Load(), final.Status, final.Scope)
			}
		})
	}
}

func TestExplicitResumeKeepsAttemptCounterAndUncertainUsage(t *testing.T) {
	var requests atomic.Int32
	started := make(chan struct{})
	model := modelFunc(func(ctx context.Context, _ runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
		if requests.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return runtime.Result{}, ctx.Err()
		}
		return runtime.Result{Text: "resumed", Usage: runtime.Usage{Known: true, InputTokens: 2, OutputTokens: 1}}, nil
	})
	s, m, p, r, c := fixture(t, factoryFor(model))
	run, err := m.Start(context.Background(), p, "project", input(r, c, "resume"), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}
	if err = m.Cancel(context.Background(), p, "project", run.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := awaitRun(t, s, p, run.ID)
	if cancelled.State.Attempts != 1 {
		t.Fatal("request counter missing")
	}
	// Close waits for ownership cleanup; a fresh manager is also the restart case.
	m.Close()
	reopened, err := New(context.Background(), s, factoryFor(model))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.Resume(context.Background(), p, "project", run.ID, nil); err != nil {
		t.Fatal(err)
	}
	final := awaitRun(t, s, p, run.ID)
	if final.Status != "completed" || final.State.Attempts != 2 || requests.Load() != 2 {
		t.Fatal(final, requests.Load())
	}
	stats, _, err := s.Usage(context.Background(), p)
	if err != nil || stats[0].UnknownRequests != 1 || stats[0].Held == 0 || stats[0].Cost != 3 {
		t.Fatal(stats, err)
	}
	if _, err = reopened.Resume(context.Background(), store.Principal{ID: "worker", Role: "agent", ProjectID: "project"}, "project", run.ID, nil); err == nil {
		t.Fatal("agent gained paid resume control")
	}
}

func TestProjectScopeReadsOnlyCapturedConfirmedPlan(t *testing.T) {
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, r runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
		if len(r.Tools) != 4 {
			t.Error("wrong trial capabilities", r.Tools)
		}
		if calls.Add(1) == 1 {
			toolCalls := []runtime.ToolCall{}
			for _, tool := range r.Tools {
				toolCalls = append(toolCalls, runtime.ToolCall{ID: tool.Name, Name: tool.Name, Arguments: json.RawMessage(`{}`)})
			}
			return runtime.Result{Calls: toolCalls}, nil
		}
		raw, err := json.Marshal(r.Messages)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "Private project name") || !strings.Contains(string(raw), "自动任务") || strings.Contains(string(raw), "run_command") {
			t.Error("project/plan snapshots or role summaries wrong")
		}
		return runtime.Result{Text: "scoped"}, nil
	})
	s, m, p, r, c := fixture(t, factoryFor(model))
	raw, err := contracts.Files.ReadFile("examples/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.CreatePlan(context.Background(), p, "project", "draft", "", 0, raw)
	if err != nil {
		t.Fatal(err)
	}
	var plan store.Plan
	if err = json.Unmarshal(draft, &plan); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmPlan(context.Background(), p, "project", "confirm", plan.Version, plan.Revision, plan.Hash); err != nil {
		t.Fatal(err)
	}
	in := input(r, c, "scope")
	in.Scope = "project_summary"
	run, err := m.Start(context.Background(), p, "project", in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if final := awaitRun(t, s, p, run.ID); final.Status != "completed" {
		t.Fatal(final.Error)
	}
}

func TestFallbackCannotSpendBeyondItsOwnCap(t *testing.T) {
	var backup atomic.Int32
	factory := func(c apiconfig.Config, _ func(string) string) (runtime.Model, func(), apiconfig.Summary, error) {
		summary, err := c.Validate()
		return modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
			if c.Model == "primary" {
				return runtime.Result{}, &direct.Failure{Status: 503}
			}
			backup.Add(1)
			return runtime.Result{Text: "must not run"}, nil
		}), func() {}, summary, err
	}
	s, m, p, r, c := fixture(t, factory)
	alternate := c
	alternate.ID = "backup-cap"
	alternate.AccountID = "other-account"
	alternate.Config.Model = "backup"
	alternate.Models = []catalog.Model{{ID: "backup", Tools: "supported", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}
	if err := s.PutConnection(context.Background(), p, alternate); err != nil {
		t.Fatal(err)
	}
	r.Fallback = catalog.Fallback{Enabled: true, Candidates: []catalog.Choice{{ConnectionID: alternate.ID, ModelID: "backup"}}, DataScope: "conversation_only", Currency: "CNY", MaxExtraCost: 1}
	r, err := s.SaveRole(context.Background(), p, r)
	if err != nil {
		t.Fatal(err)
	}
	in := input(r, c, "capped")
	in.ConnectionRevisions[alternate.ID] = 1
	run, err := m.Start(context.Background(), p, "project", in, nil)
	if err != nil {
		t.Fatal(err)
	}
	final := awaitRun(t, s, p, run.ID)
	if final.Status != "failed" || backup.Load() != 0 || !strings.Contains(final.Error, "额外费用上限") {
		t.Fatal(final.Status, final.Error, backup.Load())
	}
}

func TestCredentialSetupFailureDoesNotSendOrPersistAKey(t *testing.T) {
	factory := func(c apiconfig.Config, lookup func(string) string) (runtime.Model, func(), apiconfig.Summary, error) {
		if lookup("AWB_MISSING") == "" {
			return nil, nil, apiconfig.Summary{}, errors.New("请配置 API Key")
		}
		t.Fatal("unexpected credential source")
		return nil, nil, apiconfig.Summary{}, nil
	}
	s, m, p, r, c := fixture(t, factory)
	if _, err := m.Start(context.Background(), p, "project", input(r, c, "missing-key"), nil); err == nil {
		t.Fatal("missing credential accepted")
	}
	runs, err := s.Runs(context.Background(), p, "project")
	if err != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].State.Attempts != 0 {
		t.Fatal(runs, err)
	}
	stats, _, err := s.Usage(context.Background(), p)
	if err != nil || stats[0].Held != 0 || stats[0].KnownInput != 0 {
		t.Fatal("setup failure reserved or billed", stats, err)
	}
	m.Close()
	if _, err = m.Start(context.Background(), p, "project", input(r, c, "closed"), nil); err == nil {
		t.Fatal("closed manager admitted a run")
	}
}

func TestRestartNeverAutoRunsAndResumeRejectsNewConfiguration(t *testing.T) {
	var requests atomic.Int32
	model := modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
		requests.Add(1)
		return runtime.Result{}, nil
	})
	s, m, p, r, c := fixture(t, factoryFor(model))
	run, err := s.CreateRun(context.Background(), p, "project", input(r, c, "crashed"))
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	restarted, err := New(context.Background(), s, factoryFor(model))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	interrupted, err := s.Run(context.Background(), p, "project", run.ID)
	if err != nil || interrupted.Status != "interrupted" || requests.Load() != 0 {
		t.Fatal(interrupted, requests.Load(), err)
	}
	c.Revision++
	if err = s.PutConnection(context.Background(), p, c); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Resume(context.Background(), p, "project", run.ID, nil); err == nil || requests.Load() != 0 {
		t.Fatal("resumed with changed credentials without a new run", err)
	}
}
