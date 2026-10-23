// Package codex adapts bounded Codex exec JSONL inside an offline container.
// Authentication and network egress are not enabled here; private session state is optional.
package codex

import (
	"context"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
)

// Executor holds only platform-approved isolated paths and an immutable image.
// Binary is a container path (default /usr/local/bin/codex), never a host binary.
// Base.Command, Base.Input and limits are replaced for every attempt.
type Executor struct {
	Base   sandbox.Execution
	Binary string
}

var _ runtime.NativeExecutor = Executor{}

// Capabilities reports the implemented offline adapter, not real model evidence.
func (e Executor) Capabilities() runtime.NativeCapabilities {
	return runtime.NativeCapabilities{SessionID: true, Resume: e.Base.StateDirectory != "", ContextRebuild: true, Cancel: true}
}

// Run starts or explicitly resumes one invocation; it never retries implicitly.
// Account and online execution remain blocked by sandbox.Execute's fixed policy.
func (e Executor) Run(ctx context.Context, request runtime.NativeRequest, emit func(runtime.NativeEvent) error) (runtime.NativeResult, error) {
	return e.run(ctx, request, emit, sandbox.Execute)
}

type executeFunc func(context.Context, sandbox.Execution, func([]byte) error) (sandbox.ExecutionResult, error)

func (e Executor) run(ctx context.Context, request runtime.NativeRequest, emit func(runtime.NativeEvent) error, execute executeFunc) (runtime.NativeResult, error) {
	result := runtime.NativeResult{Status: "blocked"}
	if request.ResumeSessionID != "" && (e.Base.StateDirectory == "" || !identifier(request.ResumeSessionID, 128) || strings.HasPrefix(request.ResumeSessionID, "-")) {
		return result, errors.New("native resume unavailable; create a new attempt with rebuilt context")
	}
	if !validModel(request.Model) || strings.TrimSpace(request.Prompt) == "" || !utf8.ValidString(request.Prompt) || len(request.Prompt) > 1<<20 || emit == nil {
		return result, errors.New("explicit model, bounded UTF-8 context and event handler required")
	}
	binary := e.Binary
	if binary == "" {
		binary = "/usr/local/bin/codex"
	}
	if !path.IsAbs(binary) || path.Clean(binary) != binary || strings.IndexFunc(binary, unicode.IsControl) >= 0 {
		return result, errors.New("canonical container binary path required")
	}
	invocation := e.Base
	invocation.Input = request.Prompt
	invocation.Timeout = request.Timeout
	invocation.MaxOutputBytes = request.MaxOutputBytes
	invocation.MaxLineBytes = min(request.MaxOutputBytes, 256<<10)
	invocation.Spec.Command = []string{binary, "exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "workspace-write", "--model", request.Model, "-"}
	if e.Base.StateDirectory != "" {
		invocation.Spec.Command = []string{binary, "exec", "--json", "--skip-git-repo-check", "--sandbox", "workspace-write", "--model", request.Model, "-"}
		if request.ResumeSessionID != "" {
			// resume does not accept exec's local --sandbox flag; use the global policy.
			invocation.Spec.Command = []string{binary, "--sandbox", "workspace-write", "exec", "resume", "--json", "--skip-git-repo-check", "--model", request.Model, request.ResumeSessionID, "-"}
		}
	}
	parser := newStream(emit)
	parser.expectedSessionID = request.ResumeSessionID
	line := func(raw []byte) error {
		if err := parser.line(raw); err != nil {
			return err
		}
		if request.Checkpoint != nil {
			return request.Checkpoint(ctx, runtime.NativeCheckpoint{SessionID: parser.sessionID, Text: parser.text.String(), Usage: parser.usage, ProviderCompleted: parser.completed})
		}
		return nil
	}
	execution, err := execute(ctx, invocation, line)
	result = runtime.NativeResult{Status: "uncertain", SessionID: parser.sessionID, Text: parser.text.String(), Usage: parser.usage, CleanupVerified: execution.CleanupVerified}
	if execution.Status == "blocked" {
		result.Status = "blocked"
		result.ErrorCode = "native_not_ready"
		return result, errors.New("native isolated execution not ready")
	}
	if !execution.CleanupVerified {
		result.ErrorCode = "native_cleanup_unconfirmed"
		return result, errors.New("native cleanup unconfirmed; retain execution ownership")
	}
	if err != nil || !parser.completed {
		result.ErrorCode = "native_result_unconfirmed"
		switch {
		case parser.failed:
			result.Status = "failed"
			result.ErrorCode = "native_turn_failed"
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			result.Status = "interrupted"
			result.ErrorCode = "native_interrupted"
		}
		return result, errors.New(result.ErrorCode)
	}
	result.Status = "completed"
	if err := emit(runtime.NativeEvent{Type: "turn_finished"}); err != nil {
		result.Status = "uncertain"
		result.ErrorCode = "native_observer_failed"
		return result, errors.New("native completion observer failed")
	}
	return result, nil
}

func identifier(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func validModel(value string) bool {
	return utf8.ValidString(value) && value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.HasPrefix(value, "-") && strings.IndexFunc(value, unicode.IsControl) < 0
}
