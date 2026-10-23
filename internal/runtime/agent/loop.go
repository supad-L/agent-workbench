// Package agent executes bounded model/tool loops independently from task acceptance.
package agent

import (
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/direct"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MaxInputBytes bounds every serialized turn, including tool schemas and history.
const MaxInputBytes = 65536

// Binding is one capability supplied by the trusted broker, never by the model.
type Binding struct {
	Definition runtime.Tool
	Execute    func(context.Context, string, json.RawMessage) (string, error)
}

// Attempt identifies one possibly billable request, including failed/uncertain retries.
type Attempt struct {
	Sequence        int `json:"sequence"`
	Turn            int `json:"turn"`
	InputBytes      int `json:"input_bytes"`
	MaxOutputTokens int `json:"max_output_tokens"`
	OutputBytes     int `json:"output_bytes"`
}

// ToolResult journals a completed call so replay cannot execute it twice.
type ToolResult struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Text      string          `json:"text"`
}

// State is resumable conversation state, not task completion or a credential container.
type State struct {
	Messages    []runtime.Message     `json:"messages"`
	NextTurn    int                   `json:"next_turn"`
	Attempts    int                   `json:"attempts"`
	Completed   bool                  `json:"completed"`
	Text        string                `json:"text"`
	ToolResults map[string]ToolResult `json:"tool_results"`
	Pending     []runtime.ToolCall    `json:"pending,omitempty"`
}

// Progress is normalized telemetry. Tool arguments and credentials are not progress events.
type Progress struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	Turn int    `json:"turn"`
}

// Options supplies execution limits and persistence hooks. Hooks must be idempotent.
type Options struct {
	Model           runtime.Model
	ModelID         string
	System          string
	Tools           []Binding
	MaxTurns        int
	Retries         int
	MaxOutputTokens int
	Checkpoint      func(context.Context, State) error
	BeforeAttempt   func(context.Context, Attempt) error
	AfterAttempt    func(context.Context, Attempt, *runtime.Usage) error
	Verify          func(context.Context) error
	Emit            func(Progress) error
}

type noNetwork struct{}

func (noNetwork) Load(string) (any, error) {
	return nil, errors.New("工具 Schema 禁止加载外部资源")
}

// Run verifies complete tool calls, checkpoints every boundary, and never runs a shell itself.
func Run(ctx context.Context, o Options, state State) (State, error) {
	if o.Model == nil || o.ModelID == "" || o.MaxTurns < 1 || o.MaxTurns > 16 || o.Retries < 0 || o.Retries > 2 || o.MaxOutputTokens < 1 || o.MaxOutputTokens > 4096 || len(state.Messages) == 0 || state.NextTurn < 0 || state.NextTurn > o.MaxTurns || state.Attempts < 0 || state.Attempts > o.MaxTurns*(o.Retries+1) {
		return state, errors.New("运行参数或恢复状态无效")
	}
	if state.ToolResults == nil {
		state.ToolResults = map[string]ToolResult{}
	}
	if state.Completed {
		return state, nil
	}
	bindings := map[string]Binding{}
	schemas := map[string]*jsonschema.Schema{}
	definitions := []runtime.Tool{}
	for _, binding := range o.Tools {
		if binding.Execute == nil || binding.Definition.Name == "" || bindings[binding.Definition.Name].Execute != nil || len(binding.Definition.Parameters) > 8192 {
			return state, errors.New("工具定义无效或重复")
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(noNetwork{})
		compiler.AssertFormat()
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(binding.Definition.Parameters))
		if err != nil {
			return state, errors.New("工具参数 Schema 无效")
		}
		if err = compiler.AddResource("https://tools.agent-workbench.invalid/parameters", doc); err != nil {
			return state, errors.New("工具参数 Schema 无效")
		}
		schema, err := compiler.Compile("https://tools.agent-workbench.invalid/parameters")
		if err != nil {
			return state, errors.New("工具参数 Schema 无效或含外部引用")
		}
		bindings[binding.Definition.Name] = binding
		schemas[binding.Definition.Name] = schema
		definitions = append(definitions, binding.Definition)
	}
	checkpoint := func() error {
		if o.Checkpoint != nil {
			return o.Checkpoint(ctx, state)
		}
		return nil
	}
	emit := func(p Progress) error {
		if o.Emit != nil {
			return o.Emit(p)
		}
		return nil
	}
	validateCalls := func(calls []runtime.ToolCall) error {
		seen := map[string]bool{}
		total := 0
		for _, call := range calls {
			binding, ok := bindings[call.Name]
			total += len(call.Arguments)
			if !ok || binding.Execute == nil || call.ID == "" || seen[call.ID] || total > 32768 {
				return errors.New("模型返回未授权、重复或过大的工具调用")
			}
			seen[call.ID] = true
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(call.Arguments))
			if err != nil || schemas[call.Name].Validate(doc) != nil {
				return errors.New("工具调用参数不符合 Schema")
			}
			if previous, exists := state.ToolResults[call.ID]; exists && (previous.Name != call.Name || !bytes.Equal(previous.Arguments, call.Arguments)) {
				return errors.New("工具调用编号被用于不同内容")
			}
		}
		return nil
	}
	processPending := func() error {
		if err := validateCalls(state.Pending); err != nil {
			return err
		}
		for len(state.Pending) > 0 {
			call := state.Pending[0]
			if err := ctx.Err(); err != nil {
				return err
			}
			if o.Verify != nil {
				if err := o.Verify(ctx); err != nil {
					return err
				}
			}
			previous, exists := state.ToolResults[call.ID]
			if !exists {
				if err := emit(Progress{Type: "tool_started", Tool: call.Name, Turn: state.NextTurn}); err != nil {
					return err
				}
				text, err := bindings[call.Name].Execute(ctx, call.ID, call.Arguments)
				if err != nil {
					return fmt.Errorf("工具 %s 执行失败", call.Name)
				}
				if len(text) > 32768 {
					return errors.New("工具结果超过 32 KiB 上限")
				}
				previous = ToolResult{Name: call.Name, Arguments: call.Arguments, Text: text}
				state.ToolResults[call.ID] = previous
			}
			state.Messages = append(state.Messages, runtime.Message{Role: "tool", CallID: call.ID, Text: previous.Text})
			state.Pending = state.Pending[1:]
			if err := checkpoint(); err != nil {
				return err
			}
			if err := emit(Progress{Type: "tool_finished", Tool: call.Name, Turn: state.NextTurn}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := processPending(); err != nil {
		return state, err
	}
	for state.NextTurn < o.MaxTurns {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		if o.Verify != nil {
			if err := o.Verify(ctx); err != nil {
				return state, err
			}
		}
		request := runtime.Request{Model: o.ModelID, System: o.System, Messages: state.Messages, Tools: definitions, MaxOutputTokens: o.MaxOutputTokens}
		raw, err := json.Marshal(request)
		if err != nil {
			return state, err
		}
		if len(raw) > MaxInputBytes {
			return state, errors.New("运行上下文超过 64 KiB 上限，请缩小输入或新建会话")
		}
		var result runtime.Result
		for retry := 0; retry <= o.Retries; retry++ {
			if state.Attempts >= o.MaxTurns*(o.Retries+1) {
				return state, errors.New("已达到累计请求次数上限")
			}
			state.Attempts++
			attempt := Attempt{Sequence: state.Attempts, Turn: state.NextTurn, InputBytes: len(raw), MaxOutputTokens: o.MaxOutputTokens}
			if err = checkpoint(); err != nil {
				return state, err
			}
			if o.BeforeAttempt != nil {
				if err = o.BeforeAttempt(ctx, attempt); err != nil {
					return state, err
				}
			}
			deltaBytes := 0
			result, err = o.Model.Turn(ctx, request, func(event runtime.Event) error {
				deltaBytes += len(event.Text)
				if deltaBytes > 65536 {
					return errors.New("模型输出超过显示上限")
				}
				return emit(Progress{Type: event.Type, Text: event.Text, Turn: state.NextTurn})
			})
			if o.AfterAttempt != nil {
				var usage *runtime.Usage
				if err == nil {
					usage = &result.Usage
					attempt.OutputBytes = len(result.Text)
					for _, call := range result.Calls {
						attempt.OutputBytes += len(call.Arguments)
					}
				}
				if usageErr := o.AfterAttempt(ctx, attempt, usage); usageErr != nil {
					return state, usageErr
				}
			}
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return state, ctx.Err()
			}
			wait, canRetry := retryDelay(err, retry)
			if !canRetry || retry == o.Retries {
				return state, err
			}
			if err = emit(Progress{Type: "retry_wait", Turn: state.NextTurn}); err != nil {
				return state, err
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return state, ctx.Err()
			case <-timer.C:
			}
		}
		if len(result.Text) > 65536 || len(result.Calls) > 32 {
			return state, errors.New("模型结果超过运行限制")
		}
		// Validate the complete batch before journaling or executing any capability.
		if err = validateCalls(result.Calls); err != nil {
			return state, err
		}
		state.Messages = append(state.Messages, runtime.Message{Role: "assistant", Text: result.Text, Calls: result.Calls, ProviderItems: result.ProviderItems})
		state.Pending = result.Calls
		state.NextTurn++
		if err = checkpoint(); err != nil {
			return state, err
		}
		if err = processPending(); err != nil {
			return state, err
		}
		if len(result.Calls) == 0 {
			state.Completed = true
			state.Text = result.Text
		}
		if err = checkpoint(); err != nil {
			return state, err
		}
		if state.Completed {
			if err = emit(Progress{Type: "turn_finished", Turn: state.NextTurn}); err != nil {
				return state, err
			}
			return state, nil
		}
	}
	return state, errors.New("已达到工具循环上限，运行已停止")
}

func retryDelay(err error, retry int) (time.Duration, bool) {
	var failure *direct.Failure
	if !errors.As(err, &failure) {
		return 0, false
	}
	if failure.Kind != "transport" && failure.Status != 429 && failure.Status < 500 {
		return 0, false
	}
	wait := time.Duration(150*(1<<retry)) * time.Millisecond
	if failure.RetryAfter > wait {
		wait = failure.RetryAfter
	}
	if wait > 5*time.Second {
		return 0, false
	}
	return wait, true
}
