package runtime

import (
	"context"
	"time"
)

// NativeCapabilities states implemented behavior, independently from live evidence.
// A session ID alone never implies that a native session can be resumed.
type NativeCapabilities struct {
	SessionID      bool `json:"session_id"`
	Resume         bool `json:"resume"`
	ContextRebuild bool `json:"context_rebuild"`
	Cancel         bool `json:"cancel"`
	TokenLimit     bool `json:"token_limit"`
	LiveReady      bool `json:"live_ready"`
}

// NativeRequest is one explicitly bounded attempt, with platform-assembled context.
// Retrying or rebuilding context requires a new attempt; adapters never retry.
type NativeRequest struct {
	Model           string
	Prompt          string
	ResumeSessionID string
	Timeout         time.Duration
	MaxOutputBytes  int
	// Checkpoint must durably record a normalized snapshot or fail to stop execution.
	Checkpoint func(context.Context, NativeCheckpoint) error
}

// NativeCheckpoint is progress, not proof of process exit or platform acceptance.
type NativeCheckpoint struct {
	SessionID         string `json:"session_id,omitempty"`
	Text              string `json:"text,omitempty"`
	Usage             Usage  `json:"usage"`
	ProviderCompleted bool   `json:"provider_completed"`
}

// NativeEvent is normalized progress, not a tool authorization or verified result.
// It contains no raw command, arguments, reasoning or native diagnostics.
// The orchestration layer supplies project/attempt identity and persisted sequence.
type NativeEvent struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Tool       string `json:"tool,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// NativeResult preserves draft output on interruption; only completed is a
// successful native turn. No status constitutes platform task acceptance.
type NativeResult struct {
	Status          string `json:"status"`
	SessionID       string `json:"session_id,omitempty"`
	Text            string `json:"text,omitempty"`
	Usage           Usage  `json:"usage"`
	CleanupVerified bool   `json:"cleanup_verified"`
	ErrorCode       string `json:"error_code,omitempty"`
}

// NativeExecutor executes a programming tool inside a platform-owned sandbox.
// Cancellation uses context; recovery capabilities are explicit, never inferred.
type NativeExecutor interface {
	Capabilities() NativeCapabilities
	Run(context.Context, NativeRequest, func(NativeEvent) error) (NativeResult, error)
}
