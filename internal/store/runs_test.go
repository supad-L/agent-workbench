package store

import (
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/agent"
	"context"
	"errors"
	"testing"
)

func runFixture(t *testing.T) (*Store, Principal, Project, catalog.Role, catalog.Connection) {
	t.Helper()
	s, p, project := setup(t)
	c := catalogConnection()
	c.Models[0].PriceKnown = true
	c.Models[0].Currency = "CNY"
	c.Models[0].InputPrice = 1000000
	c.Models[0].OutputPrice = 1000000
	if err := s.PutConnection(context.Background(), p, c); err != nil {
		t.Fatal(err)
	}
	r := catalog.Templates()[0]
	r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: c.Models[0].ID}
	r.Tools = []string{"get_status"}
	r.Enabled = true
	r, err := s.SaveRole(context.Background(), p, r)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, project, r, c
}

func runInput(role catalog.Role, c catalog.Connection, key string) RunInput {
	return RunInput{RoleID: role.ID, RoleRevision: role.Revision, Input: "合成试运行", Scope: "conversation_only", Key: key, ConnectionRevisions: map[string]int{c.ID: c.Revision}}
}

func TestRunsReserveSettleIdempotencyAndAccountSlot(t *testing.T) {
	s, human, project, role, c := runFixture(t)
	ctx := context.Background()
	input := runInput(role, c, "run-1")
	run, err := s.CreateRun(ctx, human, project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.CreateRun(ctx, human, project.ID, input)
	if err != nil || repeated.ID != run.ID {
		t.Fatal("run retry duplicated", err)
	}
	if _, err = s.CreateRun(ctx, human, project.ID, runInput(role, c, "run-2")); err == nil {
		t.Fatal("account concurrency bypassed")
	}
	if _, err = s.SaveBudget(ctx, human, Budget{Currency: "CNY", Limit: 100000, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	attempt := agent.Attempt{Sequence: 1, InputBytes: 600, MaxOutputTokens: 64, OutputBytes: 90}
	if err = s.BeginAttempt(ctx, run.ID, attempt); err != nil {
		t.Fatal(err)
	}
	actual := &runtime.Usage{Known: true, InputTokens: 100, OutputTokens: 10}
	if err = s.SettleAttempt(ctx, run.ID, attempt, actual); err != nil {
		t.Fatal(err)
	}
	if err = s.SettleAttempt(ctx, run.ID, attempt, actual); err != nil {
		t.Fatal("usage retry not idempotent", err)
	}
	if err = s.SettleAttempt(ctx, run.ID, attempt, &runtime.Usage{Known: true, InputTokens: 101}); !errors.Is(err, ErrConflict) {
		t.Fatal("usage conflict accepted", err)
	}
	attempt.Sequence = 2
	if err = s.BeginAttempt(ctx, run.ID, attempt); err != nil {
		t.Fatal(err)
	}
	if err = s.SettleAttempt(ctx, run.ID, attempt, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Sequence = 3
	if err = s.BeginAttempt(ctx, run.ID, attempt); err == nil {
		t.Fatal("unknown request reservation was released")
	}
	usage, budgets, err := s.Usage(ctx, human)
	if err != nil || len(budgets) != 1 {
		t.Fatal(err)
	}
	if usage[0].Cost != 110 || usage[0].Held != 65600 || usage[0].UnknownRequests != 1 {
		t.Fatal("unknown usage became free or was double settled", usage)
	}
	if err = s.FinishRun(ctx, run.ID, "cancelled", "fixture", run.State); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(ctx, human, project.ID, runInput(role, c, "run-2")); err != nil {
		t.Fatal("finished account slot retained", err)
	}
}

func TestUsageEstimatesAndRestartRecoveryNeverRestartPaidCalls(t *testing.T) {
	s, human, project, role, c := runFixture(t)
	ctx := context.Background()
	run, err := s.CreateRun(ctx, human, project.ID, runInput(role, c, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	attempt := agent.Attempt{Sequence: 1, InputBytes: 300, OutputBytes: 90, MaxOutputTokens: 64}
	if err = s.BeginAttempt(ctx, run.ID, attempt); err != nil {
		t.Fatal(err)
	}
	if err = s.SettleAttempt(ctx, run.ID, attempt, &runtime.Usage{Known: false}); err != nil {
		t.Fatal(err)
	}
	stats, _, err := s.Usage(ctx, human)
	if err != nil || stats[0].EstimatedInput != 100 || stats[0].EstimatedOutput != 30 || stats[0].KnownInput != 0 || stats[0].Cost != 0 || stats[0].Held == 0 {
		t.Fatal(stats, err)
	}
	state := run.State
	state.Attempts = 1
	if err = s.CheckpointRun(ctx, run.ID, state); err != nil {
		t.Fatal(err)
	}
	if err = s.PreviewRun(ctx, run.ID, "未完成的流式文本", agent.Progress{Type: "text_delta"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Run(ctx, human, project.ID, run.ID)
	if err != nil || recovered.Status != "interrupted" || recovered.State.Attempts != 1 {
		t.Fatal(recovered, err)
	}
	resumed, err := s.ResumeRun(ctx, human, project.ID, run.ID)
	if err != nil || resumed.State.Attempts != 1 || resumed.Status != "running" {
		t.Fatal(resumed, err)
	}
	if err = s.CheckRunConfiguration(ctx, resumed); err != nil {
		t.Fatal(err)
	}
	c.Revision++
	c.Label = "修改连接"
	if err = s.PutConnection(ctx, human, c); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckRunConfiguration(ctx, resumed); !errors.Is(err, ErrConflict) {
		t.Fatal("stale credential snapshot accepted", err)
	}
	if _, err = s.Run(ctx, Principal{ID: "other", Role: "agent", ProjectID: "different-project"}, project.ID, run.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross project run read", err)
	}
}

func TestUnknownPricingCannotBypassEnabledBudget(t *testing.T) {
	s, human, project, role, c := runFixture(t)
	ctx := context.Background()
	c.Revision++
	c.Models[0].PriceKnown = false
	if err := s.PutConnection(ctx, human, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveBudget(ctx, human, Budget{Currency: "USD", Enabled: true, Limit: 100000}); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, human, project.ID, runInput(role, c, "unpriced"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginAttempt(ctx, run.ID, agent.Attempt{Sequence: 1, InputBytes: 300, MaxOutputTokens: 64}); err == nil {
		t.Fatal("unknown price bypassed monetary guard")
	}
}

func TestConnectionAliasesShareMinimumAccountConcurrency(t *testing.T) {
	s, p, project, r, c := runFixture(t)
	ctx := context.Background()
	alias := c
	alias.ID = "alias"
	alias.MaxConcurrent = 32
	if err := s.PutConnection(ctx, p, alias); err != nil {
		t.Fatal(err)
	}
	other := r
	other.ID = "alias-role"
	other.Revision = 0
	other.Primary.ConnectionID = alias.ID
	other, err := s.SaveRole(ctx, p, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(ctx, p, project.ID, runInput(r, c, "original")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(ctx, p, project.ID, runInput(other, alias, "alias")); err == nil {
		t.Fatal("alias bypassed shared account slot")
	}
}

func TestBudgetRevisionDisableAndUnknownCostsStaySeparate(t *testing.T) {
	s, p, project, r, c := runFixture(t)
	ctx := context.Background()
	b, err := s.SaveBudget(ctx, p, Budget{Currency: "CNY", Enabled: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveBudget(ctx, p, Budget{Currency: "CNY", Enabled: true, Limit: 10000}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale budget update accepted", err)
	}
	b.Enabled = false
	if _, err = s.SaveBudget(ctx, p, b); err != nil {
		t.Fatal(err)
	}
	c.Revision++
	c.Models[0].PriceKnown = false
	if err = s.PutConnection(ctx, p, c); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, p, project.ID, runInput(r, c, "unknown"))
	if err != nil {
		t.Fatal(err)
	}
	attempt := agent.Attempt{Sequence: 1, InputBytes: 300, OutputBytes: 30, MaxOutputTokens: 64}
	if err = s.BeginAttempt(ctx, run.ID, attempt); err != nil {
		t.Fatal(err)
	}
	if err = s.SettleAttempt(ctx, run.ID, attempt, &runtime.Usage{Known: true, InputTokens: 30, OutputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	stats, _, err := s.Usage(ctx, p)
	if err != nil || stats[0].KnownInput != 30 || stats[0].UnpricedRequests != 1 || stats[0].Cost != 0 {
		t.Fatal("unpriced usage classification wrong", stats, err)
	}
}
