package codex

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"agent-workbench/internal/runtime"
)

type stream struct {
	emit                       func(runtime.NativeEvent) error
	sessionID                  string
	started, completed, failed bool
	text                       strings.Builder
	usage                      runtime.Usage
	items                      map[string]itemState
}
type itemState struct {
	kind, status string
	textHash     [32]byte
	finished     bool
}
type nativeItem struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Status string `json:"status"`
}
type nativeUsage struct {
	Input  *int64 `json:"input_tokens"`
	Output *int64 `json:"output_tokens"`
	Cached *int64 `json:"cached_input_tokens"`
}

func newStream(emit func(runtime.NativeEvent) error) *stream {
	return &stream{emit: emit, items: map[string]itemState{}}
}
func invalidStream() error { return errors.New("invalid or unsupported native event sequence") }

func (s *stream) line(line []byte) error {
	if !utf8.Valid(line) {
		return invalidStream()
	}
	var event struct {
		Type     string       `json:"type"`
		ThreadID string       `json:"thread_id"`
		Item     *nativeItem  `json:"item"`
		Usage    *nativeUsage `json:"usage"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type == "" || s.completed || s.failed {
		return invalidStream()
	}
	switch event.Type {
	case "thread.started":
		if s.sessionID != "" || s.started || !identifier(event.ThreadID, 128) {
			return invalidStream()
		}
		s.sessionID = event.ThreadID
	case "turn.started":
		if s.sessionID == "" || s.started {
			return invalidStream()
		}
		s.started = true
	case "item.started", "item.updated", "item.completed":
		if !s.started || event.Item == nil {
			return invalidStream()
		}
		return s.item(event.Type, *event.Item)
	case "turn.completed":
		if !s.started {
			return invalidStream()
		}
		for _, item := range s.items {
			if toolKind(item.kind) != "" && !item.finished {
				return invalidStream()
			}
		}
		if event.Usage != nil {
			u := event.Usage
			for _, n := range []*int64{u.Input, u.Output, u.Cached} {
				if n != nil && *n < 0 {
					return invalidStream()
				}
			}
			if u.Input != nil && u.Cached != nil && *u.Cached > *u.Input {
				return invalidStream()
			}
			if u.Input != nil && u.Output != nil {
				s.usage = runtime.Usage{Known: true, InputTokens: *u.Input, OutputTokens: *u.Output}
			}
		}
		s.completed = true
	case "turn.failed", "error":
		s.failed = true
		return errors.New("native turn failed; raw diagnostics withheld")
	default:
		// A changed event protocol must be reviewed before it can yield success.
		return invalidStream()
	}
	return nil
}

func toolKind(kind string) string {
	switch kind {
	case "command_execution":
		return "command"
	case "file_change":
		return "file_change"
	case "mcp_tool_call":
		return "mcp"
	case "web_search":
		return "web_search"
	default:
		return ""
	}
}
func (s *stream) item(eventType string, item nativeItem) error {
	if !identifier(item.ID, 64) || len(s.items) >= 4096 {
		return invalidStream()
	}
	switch item.Type {
	case "agent_message", "reasoning", "plan", "command_execution", "file_change", "mcp_tool_call", "web_search":
	default:
		return invalidStream()
	}
	old, exists := s.items[item.ID]
	if exists && old.kind != item.Type {
		return invalidStream()
	}
	finished := eventType == "item.completed"
	if old.finished {
		if !finished || old.textHash != sha256.Sum256([]byte(item.Text)) || old.status != item.Status {
			return invalidStream()
		}
		return nil
	}
	kind := toolKind(item.Type)
	if kind != "" && !exists {
		if err := s.emit(runtime.NativeEvent{Type: "tool_started", ToolCallID: item.ID, Tool: kind}); err != nil {
			return err
		}
	}
	if finished && kind != "" {
		if item.Status != "completed" && item.Status != "failed" {
			return invalidStream()
		}
		progress := runtime.NativeEvent{Type: "tool_finished", ToolCallID: item.ID, Tool: kind}
		if item.Status == "failed" {
			progress.ErrorCode = "native_tool_failed"
		}
		if err := s.emit(progress); err != nil {
			return err
		}
	}
	if finished && item.Type == "agent_message" && item.Text != "" {
		addition := item.Text
		if s.text.Len() > 0 {
			addition = "\n" + addition
		}
		s.text.WriteString(addition)
		for len(addition) > 0 {
			size := min(len(addition), 32768)
			for size < len(addition) && !utf8.RuneStart(addition[size]) {
				size--
			}
			if err := s.emit(runtime.NativeEvent{Type: "text_delta", Text: addition[:size]}); err != nil {
				return err
			}
			addition = addition[size:]
		}
	}
	// Reasoning, command text/output and tool arguments are deliberately discarded.
	s.items[item.ID] = itemState{kind: item.Type, textHash: sha256.Sum256([]byte(item.Text)), status: item.Status, finished: finished}
	return nil
}
