package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/workspace"
)

const started = `{"type":"thread.started","thread_id":"session-1"}
{"type":"turn.started"}`
const answer = `{"type":"item.completed","item":{"id":"answer-1","type":"agent_message","text":"完成"}}`
const completed = `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":4,"output_tokens":6}}`
const successStream = started + "\n" + `{"type":"item.started","item":{"id":"command-1","type":"command_execution","command":"private diagnostic fixture","status":"in_progress"}}
{"type":"item.completed","item":{"id":"command-1","type":"command_execution","aggregated_output":"private diagnostic fixture","status":"completed"}}
{"type":"item.completed","item":{"id":"reason-1","type":"reasoning","text":"private diagnostic fixture"}}` + "\n" + answer + "\n" + completed

func requestFixture() runtime.NativeRequest {
	return runtime.NativeRequest{Model: "provider/model:variant", Prompt: "approved context", Timeout: time.Second, MaxOutputBytes: 1 << 20}
}
func runFixture(t *testing.T, data string, execution sandbox.ExecutionResult, processErr error) (runtime.NativeResult, []runtime.NativeEvent, error) {
	t.Helper()
	var events []runtime.NativeEvent
	result, err := (Executor{}).run(context.Background(), requestFixture(), func(event runtime.NativeEvent) error { events = append(events, event); return nil }, func(_ context.Context, invocation sandbox.Execution, line func([]byte) error) (sandbox.ExecutionResult, error) {
		if invocation.Input != "approved context" || invocation.Timeout != time.Second || invocation.MaxLineBytes != 256<<10 {
			t.Fatal("invocation limits/context lost")
		}
		expected := []string{"/usr/local/bin/codex", "exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "workspace-write", "--model", "provider/model:variant", "-"}
		if !reflect.DeepEqual(invocation.Spec.Command, expected) {
			t.Fatal(invocation.Spec.Command)
		}
		for _, value := range strings.Split(data, "\n") {
			if value == "" {
				continue
			}
			if err := line([]byte(value)); err != nil {
				return execution, err
			}
		}
		return execution, processErr
	})
	return result, events, err
}
func cleanExecution() sandbox.ExecutionResult {
	return sandbox.ExecutionResult{Status: "completed", CleanupVerified: true}
}

func TestSuccessfulNativeRun(t *testing.T) {
	result, events, err := runFixture(t, successStream, cleanExecution(), nil)
	if err != nil || result.Status != "completed" || result.SessionID != "session-1" || result.Text != "完成" || !result.Usage.Known || result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 6 {
		t.Fatalf("%+v %v", result, err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	if !reflect.DeepEqual(types, []string{"tool_started", "tool_finished", "text_delta", "turn_finished"}) {
		t.Fatal(events)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "private diagnostic fixture") {
		t.Fatal("raw native payload leaked")
	}
	capabilities := (Executor{}).Capabilities()
	if !capabilities.SessionID || !capabilities.Cancel || !capabilities.ContextRebuild || capabilities.Resume || capabilities.LiveReady || capabilities.TokenLimit {
		t.Fatal(capabilities)
	}
}

func TestNativeRunFailureStates(t *testing.T) {
	for _, test := range []struct {
		name, data, status, code string
		execution                sandbox.ExecutionResult
		err                      error
	}{
		{"truncated", started + "\n" + answer, "uncertain", "native_result_unconfirmed", cleanExecution(), nil},
		{"empty", "", "uncertain", "native_result_unconfirmed", cleanExecution(), nil},
		{"failed", started + "\n" + `{"type":"turn.failed","error":{"message":"private diagnostic fixture"}}`, "failed", "native_turn_failed", cleanExecution(), nil},
		{"auth_error", `{"type":"error","message":"private diagnostic fixture"}`, "failed", "native_turn_failed", cleanExecution(), nil},
		{"nonzero", successStream, "uncertain", "native_result_unconfirmed", cleanExecution(), errors.New("private diagnostic fixture")},
		{"cancel", started, "interrupted", "native_interrupted", cleanExecution(), context.Canceled},
		{"timeout", started, "interrupted", "native_interrupted", cleanExecution(), context.DeadlineExceeded},
		{"cleanup", successStream, "uncertain", "native_cleanup_unconfirmed", sandbox.ExecutionResult{Status: "uncertain"}, nil},
		{"blocked", "", "blocked", "native_not_ready", sandbox.ExecutionResult{Status: "blocked"}, errors.New("private diagnostic fixture")},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, events, err := runFixture(t, test.data, test.execution, test.err)
			if err == nil || result.Status != test.status || result.ErrorCode != test.code || strings.Contains(err.Error(), "private diagnostic fixture") {
				t.Fatalf("%+v %v", result, err)
			}
			for _, event := range events {
				if event.Type == "turn_finished" {
					t.Fatal("premature completion")
				}
			}
		})
	}
}

func TestNativeUsageAndRepeatedItems(t *testing.T) {
	for _, test := range []struct {
		name, usage string
		known       bool
	}{
		{"missing", "", false}, {"null", `,"usage":null`, false}, {"partial", `,"usage":{"input_tokens":1}`, false}, {"zero", `,"usage":{"input_tokens":0,"output_tokens":0}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, events, err := runFixture(t, started+"\n"+answer+"\n"+answer+"\n"+`{"type":"turn.completed"`+test.usage+`}`, cleanExecution(), nil)
			if err != nil || result.Usage.Known != test.known || result.Text != "完成" || len(events) != 2 {
				t.Fatalf("%+v %v %v", result, events, err)
			}
		})
	}
}

func TestRejectInvalidNativeStreams(t *testing.T) {
	for _, data := range []string{
		`not-json private diagnostic fixture`, `null`, `{}`, `{"type":"item.completed"}`, `{"type":"turn.started"}`, `{"type":"thread.started","thread_id":"bad/id"}`,
		started + "\n" + `{"type":"turn.started"}`, started + "\n" + `{"type":"thread.started","thread_id":"second"}`,
		started + "\n" + `{"type":"item.completed","item":{"id":"a","type":"new_tool"}}`,
		started + "\n" + `{"type":"item.started","item":{"id":"a","type":"command_execution","status":"in_progress"}}` + "\n" + completed,
		started + "\n" + `{"type":"item.completed","item":{"id":"a","type":"command_execution","status":"in_progress"}}`,
		started + "\n" + answer + "\n" + strings.ReplaceAll(answer, "完成", "changed"),
		started + "\n" + answer + "\n" + `{"type":"item.completed","item":{"id":"answer-1","type":"plan"}}`,
		started + "\n" + `{"type":"item.completed","item":{"id":"bad/id","type":"agent_message"}}`,
		started + "\n" + `{"type":"turn.completed","usage":{"input_tokens":-1,"output_tokens":0}}`,
		started + "\n" + `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":0,"cached_input_tokens":2}}`,
		started + "\n" + `{"type":"turn.completed","usage":{"input_tokens":9223372036854775808}}`,
		successStream + "\n" + `{"type":"turn.completed"}`, started + "\n" + `{"type":"future.event"}`, "\xff",
	} {
		t.Run(data[:min(len(data), 45)], func(t *testing.T) {
			result, events, err := runFixture(t, data, cleanExecution(), nil)
			if err == nil || result.Status == "completed" {
				t.Fatalf("accepted %s", data)
			}
			for _, event := range events {
				if event.Type == "turn_finished" {
					t.Fatal("premature completion")
				}
			}
		})
	}
}

func TestObserverFailureAndRequestValidation(t *testing.T) {
	for _, mode := range []string{"resume", "model", "prompt", "binary", "observer", "completed_observer"} {
		t.Run(mode, func(t *testing.T) {
			request := requestFixture()
			executor := Executor{}
			emit := func(runtime.NativeEvent) error { return nil }
			switch mode {
			case "resume":
				request.ResumeSessionID = "old"
			case "model":
				request.Model = "-bad"
			case "prompt":
				request.Prompt = ""
			case "binary":
				executor.Binary = "../host/codex"
			case "observer":
				emit = nil
			case "completed_observer":
				emit = func(runtime.NativeEvent) error { return errors.New("private diagnostic fixture") }
			}
			result, err := executor.run(context.Background(), request, emit, func(_ context.Context, _ sandbox.Execution, line func([]byte) error) (sandbox.ExecutionResult, error) {
				if mode != "completed_observer" {
					t.Fatal("invalid request launched")
				}
				for _, event := range strings.Split(started+"\n"+completed, "\n") {
					if err := line([]byte(event)); err != nil {
						return cleanExecution(), err
					}
				}
				return cleanExecution(), nil
			})
			if err == nil || result.Status == "completed" || strings.Contains(err.Error(), "private diagnostic fixture") {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestToolsAndDeduplication(t *testing.T) {
	for _, kind := range []string{"command_execution", "file_change", "mcp_tool_call", "web_search"} {
		t.Run(kind, func(t *testing.T) {
			item := `{"type":"item.completed","item":{"id":"tool-1","type":"` + kind + `","status":"failed"}}`
			data := started + "\n" + `{"type":"item.updated","item":{"id":"tool-1","type":"` + kind + `","status":"in_progress"}}` + "\n" + item + "\n" + item + "\n" + completed
			result, events, err := runFixture(t, data, cleanExecution(), nil)
			if err != nil || result.Status != "completed" || len(events) != 3 || events[1].ErrorCode != "native_tool_failed" {
				t.Fatalf("%+v %v %v", result, events, err)
			}
		})
	}
}

// TestRealOfflineCodexFixture combines the adapter with a real container process.
// The script emits synthetic CLI events; it does not run Codex or a model.
func TestRealOfflineCodexFixture(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	if kind == "" {
		t.Skip("explicit offline container fixture opt-in required")
	}
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if image == "" || scratch == "" {
		t.Fatal("explicit immutable image and scratch required")
	}
	root, err := os.MkdirTemp(scratch, "awb-codex-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nset -eu\n[ \"$1\" = exec ]\n[ \"$9\" = - ]\nread -r prompt\n[ \"$prompt\" = 'approved context' ]\nprintf 'private diagnostic fixture' >&2\ncat <<'JSONL'\n" + successStream + "\nJSONL\n"
	if err := os.WriteFile(filepath.Join(source, "fixture-codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Binary: "/workspace/fixture-codex", Base: sandbox.Execution{Runtime: sandbox.RuntimeKind(kind), Spec: sandbox.Spec{ImageID: image, SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}}}}
	request := requestFixture()
	request.Timeout = 10 * time.Second
	request.Prompt += "\n"
	var events []runtime.NativeEvent
	result, err := executor.Run(context.Background(), request, func(event runtime.NativeEvent) error { events = append(events, event); return nil })
	if err != nil || result.Status != "completed" || !result.CleanupVerified || result.Text != "完成" || !result.Usage.Known {
		t.Fatalf("real container synthetic CLI: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "private diagnostic fixture") {
		t.Fatal("diagnostics leaked")
	}
}

func TestLargeUnicodeTextEventsStayBounded(t *testing.T) {
	text := strings.Repeat("你", 24000)
	item, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"id": "large", "type": "agent_message", "text": text}})
	result, events, err := runFixture(t, started+"\n"+string(item)+"\n"+answer+"\n"+completed, cleanExecution(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, event := range events {
		if event.Type == "text_delta" {
			if len(event.Text) > 32768 || !utf8.ValidString(event.Text) {
				t.Fatal("invalid text event boundary")
			}
			joined.WriteString(event.Text)
		}
	}
	if joined.String() != text+"\n完成" || joined.String() != result.Text {
		t.Fatal("text events and final text disagree")
	}
}

func TestObserverErrorsStopBeforeCompletion(t *testing.T) {
	for _, target := range []string{"tool_started", "tool_finished", "text_delta"} {
		t.Run(target, func(t *testing.T) {
			var completedEvent bool
			result, err := (Executor{}).run(context.Background(), requestFixture(), func(event runtime.NativeEvent) error {
				if event.Type == "turn_finished" {
					completedEvent = true
				}
				if event.Type == target {
					return errors.New("private diagnostic fixture")
				}
				return nil
			}, func(_ context.Context, _ sandbox.Execution, line func([]byte) error) (sandbox.ExecutionResult, error) {
				for _, value := range strings.Split(successStream, "\n") {
					if err := line([]byte(value)); err != nil {
						return cleanExecution(), err
					}
				}
				return cleanExecution(), nil
			})
			if err == nil || result.Status != "uncertain" || completedEvent || strings.Contains(err.Error(), "private diagnostic fixture") {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestPersistentInvocationAndCheckpoint(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resume"}[resume], func(t *testing.T) {
			request := requestFixture()
			if resume {
				request.ResumeSessionID = "session-1"
			}
			var checkpoints []runtime.NativeCheckpoint
			request.Checkpoint = func(_ context.Context, checkpoint runtime.NativeCheckpoint) error {
				checkpoints = append(checkpoints, checkpoint)
				return nil
			}
			executor := Executor{Base: sandbox.Execution{StateDirectory: "/private/session-state"}}
			result, err := executor.run(context.Background(), request, func(runtime.NativeEvent) error { return nil }, func(_ context.Context, invocation sandbox.Execution, line func([]byte) error) (sandbox.ExecutionResult, error) {
				command := invocation.Spec.Command
				joined := strings.Join(command, " ")
				if strings.Contains(joined, "--ephemeral") || strings.Contains(joined, "--last") || invocation.StateDirectory != "/private/session-state" {
					t.Fatal(command)
				}
				if resume && !reflect.DeepEqual(command, []string{"/usr/local/bin/codex", "--sandbox", "workspace-write", "exec", "resume", "--json", "--skip-git-repo-check", "--model", request.Model, "session-1", "-"}) {
					t.Fatal(command)
				}
				for _, event := range strings.Split(successStream, "\n") {
					if err := line([]byte(event)); err != nil {
						return cleanExecution(), err
					}
				}
				return cleanExecution(), nil
			})
			if err != nil || result.Status != "completed" || !executor.Capabilities().Resume {
				t.Fatalf("%+v %v", result, err)
			}
			if len(checkpoints) < 3 || checkpoints[0].SessionID != "session-1" || checkpoints[0].ProviderCompleted || !checkpoints[len(checkpoints)-1].ProviderCompleted || checkpoints[len(checkpoints)-1].Text != "完成" {
				t.Fatal(checkpoints)
			}
		})
	}
}

func TestResumeRejectsDifferentNativeSessionAndLostCheckpoint(t *testing.T) {
	for _, mode := range []string{"wrong_session", "checkpoint_failure"} {
		t.Run(mode, func(t *testing.T) {
			request := requestFixture()
			request.ResumeSessionID = "session-1"
			if mode == "checkpoint_failure" {
				request.Checkpoint = func(context.Context, runtime.NativeCheckpoint) error { return errors.New("journal unavailable") }
			}
			data := successStream
			if mode == "wrong_session" {
				data = strings.ReplaceAll(data, "session-1", "other-session")
			}
			result, err := (Executor{Base: sandbox.Execution{StateDirectory: "/private/session-state"}}).run(context.Background(), request, func(runtime.NativeEvent) error { return nil }, func(_ context.Context, _ sandbox.Execution, line func([]byte) error) (sandbox.ExecutionResult, error) {
				for _, event := range strings.Split(data, "\n") {
					if err := line([]byte(event)); err != nil {
						return cleanExecution(), err
					}
				}
				return cleanExecution(), nil
			})
			if err == nil || result.Status != "uncertain" {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

// This is a synthetic native protocol in a real container, never a model call.
func TestRealOfflinePersistentCodexFixture(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	if kind == "" {
		t.Skip("explicit offline container fixture opt-in required")
	}
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if image == "" || scratch == "" {
		t.Fatal("explicit image and shared scratch required")
	}
	root, err := os.MkdirTemp(scratch, "awb-persistent-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	source, state := filepath.Join(root, "source"), filepath.Join(root, "native-state")
	for _, dir := range []string{source, state} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(state, 0777); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
[ "$CODEX_HOME" = /awb-native-state ]
[ "$HOME" = /tmp/awb-home ]
read -r prompt
if [ "$1" = exec ]; then
 [ "$prompt" = initial ]
 mkdir -p "$CODEX_HOME/sessions"
 printf saved > "$CODEX_HOME/sessions/session-1"
else
 [ "$1" = --sandbox ]
 [ "$4" = resume ]
 [ "$9" = session-1 ]
 [ "$prompt" = follow-up ]
 [ "$(cat "$CODEX_HOME/sessions/session-1")" = saved ]
 printf resumed > "$CODEX_HOME/sessions/session-1"
fi
cat <<'JSONL'
` + successStream + "\nJSONL\n"
	// argv positions: --sandbox workspace-write exec resume --json --skip-git-repo-check --model MODEL SESSION -.
	if err := os.WriteFile(filepath.Join(source, "fixture-codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Binary: "/workspace/fixture-codex", Base: sandbox.Execution{Runtime: sandbox.RuntimeKind(kind), StateDirectory: state, Spec: sandbox.Spec{ImageID: image, SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}}}}
	request := requestFixture()
	request.Timeout = 10 * time.Second
	request.Prompt = "initial\n"
	var identity sandbox.ExecutionIdentity
	executor.Base.BeforeLaunch = func(_ context.Context, id sandbox.ExecutionIdentity) error { identity = id; return nil }
	result, err := executor.Run(context.Background(), request, func(runtime.NativeEvent) error { return nil })
	if err != nil || result.Status != "completed" || identity.Name == "" {
		t.Fatalf("first: %+v %v", result, err)
	}
	// A new adapter instance only receives the saved state path and explicit session ID.
	resumed := Executor{Binary: executor.Binary, Base: executor.Base}
	request.Prompt = "follow-up\n"
	request.ResumeSessionID = result.SessionID
	result, err = resumed.Run(context.Background(), request, func(runtime.NativeEvent) error { return nil })
	if err != nil || result.Status != "completed" || !result.CleanupVerified {
		t.Fatalf("resume: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(state, "sessions", "session-1"))
	if err != nil || string(data) != "resumed" {
		t.Fatal("state did not survive", err)
	}
	if err := sandbox.Cleanup(context.Background(), identity); err != nil {
		t.Fatal("idempotent cleanup", err)
	}
}
