// Package runtime defines provider-independent model turns and tool results.
package runtime

import (
	"context"
	"encoding/json"
)

// Tool describes a callable platform capability, not permission to execute it.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is a fully assembled call. Partial stream arguments are never executable.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Message contains normalized text, calls, results, or preserved provider output.
type Message struct {
	Role          string            `json:"role"`
	Text          string            `json:"text,omitempty"`
	Calls         []ToolCall        `json:"calls,omitempty"`
	CallID        string            `json:"call_id,omitempty"`
	ProviderItems []json.RawMessage `json:"provider_items,omitempty"`
}

// Request is one bounded model turn.
type Request struct {
	Model           string    `json:"model"`
	System          string    `json:"system,omitempty"`
	Messages        []Message `json:"messages"`
	Tools           []Tool    `json:"tools,omitempty"`
	MaxOutputTokens int       `json:"max_output_tokens"`
}

// Usage distinguishes unknown token counts from zero.
type Usage struct {
	Known        bool  `json:"known"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Result is returned only after a provider-confirmed successful turn.
type Result struct {
	Text          string            `json:"text"`
	Calls         []ToolCall        `json:"calls,omitempty"`
	Usage         Usage             `json:"usage"`
	ProviderItems []json.RawMessage `json:"provider_items,omitempty"`
}

// Event contains a normalized, non-authoritative progress update.
type Event struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Model completes a turn; task acceptance belongs to the orchestration layer.
type Model interface {
	Turn(context.Context, Request, func(Event) error) (Result, error)
}
