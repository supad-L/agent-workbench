package agent

import (
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/direct"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type modelFunc func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error)

func (f modelFunc) Turn(ctx context.Context, r runtime.Request, emit func(runtime.Event) error) (runtime.Result, error) {
	return f(ctx, r, emit)
}

func TestLoopToolContinuationAndCheckpointReplay(t *testing.T) {
	calls, executed := 0, 0
	var checkpoint State
	model := modelFunc(func(_ context.Context, r runtime.Request, emit func(runtime.Event) error) (runtime.Result, error) {
		calls++
		if calls == 1 {
			return runtime.Result{Calls: []runtime.ToolCall{{ID: "call-1", Name: "add", Arguments: json.RawMessage(`{"a":20,"b":22}`)}}, ProviderItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"reasoning-fixture"}`)}}, nil
		}
		if r.Messages[len(r.Messages)-1].Text != "42" || len(r.Messages[1].ProviderItems) != 1 {
			t.Fatal("tool result or provider context lost")
		}
		if err := emit(runtime.Event{Type: "text_delta", Text: "42"}); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Text: "42"}, nil
	})
	o := Options{Model: model, ModelID: "fixture", MaxTurns: 3, MaxOutputTokens: 64, Tools: []Binding{{Definition: runtime.Tool{Name: "add", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":false}`)}, Execute: func(context.Context, string, json.RawMessage) (string, error) { executed++; return "42", nil }}}, Checkpoint: func(_ context.Context, s State) error {
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &checkpoint)
	}}
	state, err := Run(context.Background(), o, State{Messages: []runtime.Message{{Role: "user", Text: "20+22"}}})
	if err != nil || !state.Completed || state.Text != "42" || executed != 1 || state.Attempts != 2 {
		t.Fatal(state, err)
	}
	if _, err = Run(context.Background(), o, checkpoint); err != nil || executed != 1 || calls != 2 {
		t.Fatal("completed checkpoint repeated work", err)
	}
}

func TestLoopRejectsWholeInvalidBatchBeforeAnyExecution(t *testing.T) {
	for _, test := range []struct{ name, args, tool string }{{"schema violation", `{"number":"wrong"}`, "safe"}, {"unauthorized tool", `{}`, "shell"}, {"external schema", `{}`, "safe"}} {
		t.Run(test.name, func(t *testing.T) {
			executed := false
			schema := json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"}},"required":["number"],"additionalProperties":false}`)
			if test.name == "external schema" {
				schema = json.RawMessage(`{"$ref":"https://untrusted.invalid/schema"}`)
			}
			o := Options{Model: modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
				return runtime.Result{Calls: []runtime.ToolCall{{ID: "valid", Name: "safe", Arguments: json.RawMessage(`{"number":1}`)}, {ID: "invalid", Name: test.tool, Arguments: json.RawMessage(test.args)}}}, nil
			}), ModelID: "fixture", MaxTurns: 2, MaxOutputTokens: 64, Tools: []Binding{{Definition: runtime.Tool{Name: "safe", Parameters: schema}, Execute: func(context.Context, string, json.RawMessage) (string, error) { executed = true; return "ok", nil }}}}
			if _, err := Run(context.Background(), o, State{Messages: []runtime.Message{{Role: "user", Text: "fixture"}}}); err == nil || executed {
				t.Fatal("unsafe batch executed", err)
			}
		})
	}
}

func TestRetryAndCancellationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
		want    int
	}{{"rate retry", &direct.Failure{Status: 429}, 2}, {"authentication no retry", &direct.Failure{Status: 401}, 1}, {"truncated no retry", errors.New("truncated"), 1}, {"long Retry-After", &direct.Failure{Status: 429, RetryAfter: 6000000000}, 1}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			unknown := 0
			o := Options{Model: modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
				calls++
				return runtime.Result{}, test.failure
			}), ModelID: "fixture", MaxTurns: 2, Retries: 1, MaxOutputTokens: 64, AfterAttempt: func(_ context.Context, _ Attempt, u *runtime.Usage) error {
				if u == nil {
					unknown++
				}
				return nil
			}}
			if _, err := Run(context.Background(), o, State{Messages: []runtime.Message{{Role: "user", Text: "fixture"}}}); err == nil || calls != test.want || unknown != calls {
				t.Fatal(calls, unknown, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := Options{Model: modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
		t.Fatal("canceled run called model")
		return runtime.Result{}, nil
	}), ModelID: "fixture", MaxTurns: 1, MaxOutputTokens: 64}
	if _, err := Run(ctx, o, State{Messages: []runtime.Message{{Role: "user", Text: "fixture"}}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o, State{Messages: []runtime.Message{{Role: "user", Text: strings.Repeat("x", MaxInputBytes)}}}); err == nil {
		t.Fatal("oversized context sent")
	}
}

func TestResumeCompletesPendingBatchWithoutCallingModelAgainFirst(t *testing.T) {
	ctx := context.Background()
	executed := map[string]int{}
	var saved State
	calls := 0
	crash := true
	o := Options{Model: modelFunc(func(_ context.Context, r runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
		calls++
		if calls == 1 {
			return runtime.Result{Calls: []runtime.ToolCall{{ID: "one", Name: "read", Arguments: json.RawMessage(`{}`)}, {ID: "two", Name: "read", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		if len(r.Messages) != 4 {
			t.Fatal("pending results missing", len(r.Messages))
		}
		return runtime.Result{Text: "done"}, nil
	}), ModelID: "fixture", MaxTurns: 3, MaxOutputTokens: 64, Tools: []Binding{{Definition: runtime.Tool{Name: "read", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`)}, Execute: func(_ context.Context, id string, _ json.RawMessage) (string, error) { executed[id]++; return id, nil }}}}
	o.Checkpoint = func(_ context.Context, state State) error {
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return err
		}
		if crash && len(state.Pending) == 1 {
			crash = false
			return errors.New("synthetic interruption")
		}
		return nil
	}
	if _, err := Run(ctx, o, State{Messages: []runtime.Message{{Role: "user", Text: "read twice"}}}); err == nil {
		t.Fatal("fixture did not interrupt")
	}
	final, err := Run(ctx, o, saved)
	if err != nil || !final.Completed || executed["one"] != 1 || executed["two"] != 1 || calls != 2 {
		t.Fatal(executed, calls, err)
	}
}
