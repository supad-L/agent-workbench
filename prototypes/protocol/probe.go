// Package protocol runs synthetic, loopback-only model/tool protocol probes.
package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/direct"
)

// Report describes a synthetic protocol check, never real provider support.
type Report struct {
	Protocol     connection.Protocol `json:"protocol"`
	Evidence     string              `json:"evidence"`
	Requests     int                 `json:"requests"`
	ToolCallID   string              `json:"tool_call_id,omitempty"`
	ToolVerified bool                `json:"tool_verified"`
	Text         string              `json:"text"`
	UsageKnown   bool                `json:"usage_known"`
}

// Run proves two-round tool calls against an in-process synthetic HTTP service.
func Run(ctx context.Context, p connection.Protocol) (Report, error) {
	var mu sync.Mutex
	requests := 0
	var handlerError error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			handlerError = err
			w.WriteHeader(400)
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1/") {
			handlerError = fmt.Errorf("unexpected method/path")
			w.WriteHeader(400)
			return
		}
		second := false
		switch p {
		case connection.Chat:
			var messages []struct {
				Role    string `json:"role"`
				CallID  string `json:"tool_call_id"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(body["messages"], &messages); err != nil {
				handlerError = err
				w.WriteHeader(400)
				return
			}
			if len(messages) > 0 {
				last := messages[len(messages)-1]
				second = last.Role == "tool" && last.CallID == "call-probe-1" && last.Content == "42"
			}
		case connection.Messages:
			var messages []struct {
				Content []struct {
					Type    string `json:"type"`
					ID      string `json:"tool_use_id"`
					Content string `json:"content"`
				} `json:"content"`
			}
			if err := json.Unmarshal(body["messages"], &messages); err != nil {
				handlerError = err
				w.WriteHeader(400)
				return
			}
			if len(messages) > 0 {
				for _, block := range messages[len(messages)-1].Content {
					second = second || block.Type == "tool_result" && block.ID == "call-probe-1" && block.Content == "42"
				}
			}
		case connection.Responses:
			var items []struct {
				Type   string `json:"type"`
				ID     string `json:"call_id"`
				Output string `json:"output"`
			}
			if err := json.Unmarshal(body["input"], &items); err != nil {
				handlerError = err
				w.WriteHeader(400)
				return
			}
			if len(items) > 0 {
				last := items[len(items)-1]
				second = last.Type == "function_call_output" && last.ID == "call-probe-1" && last.Output == "42"
			}
		}
		if requests == 2 && !second {
			handlerError = fmt.Errorf("tool result not encoded for %s", p)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, Fixture(p, second)); err != nil {
			handlerError = err
		}
	}))
	defer server.Close()
	client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: p, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, AuthKind: "none"})
	if err != nil {
		return Report{}, err
	}
	request := modelruntime.Request{Model: "synthetic-model", MaxOutputTokens: 128, Messages: []modelruntime.Message{{Role: "user", Text: "Use probe to return 42"}}, Tools: []modelruntime.Tool{{Name: "probe", Description: "Synthetic no-side-effect test tool", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)}}}
	first, err := client.Turn(ctx, request, nil)
	if err != nil {
		return Report{}, err
	}
	if len(first.Calls) != 1 || first.Calls[0].Name != "probe" || string(first.Calls[0].Arguments) != `{"value":42}` {
		return Report{}, fmt.Errorf("unexpected assembled tool call")
	}
	request.Messages = append(request.Messages, modelruntime.Message{Role: "assistant", Text: first.Text, Calls: first.Calls, ProviderItems: first.ProviderItems}, modelruntime.Message{Role: "tool", CallID: first.Calls[0].ID, Text: "42"})
	second, err := client.Turn(ctx, request, nil)
	if err != nil {
		return Report{}, err
	}
	mu.Lock()
	defer mu.Unlock()
	if handlerError != nil {
		return Report{}, handlerError
	}
	if requests != 2 || second.Text != "工具结果：42" || len(second.Calls) != 0 {
		return Report{}, fmt.Errorf("tool round trip not completed")
	}
	return Report{Protocol: p, Evidence: "synthetic_loopback_only", Requests: requests, ToolCallID: first.Calls[0].ID, ToolVerified: true, Text: second.Text, UsageKnown: second.Usage.Known}, nil
}

// Fixture returns documented wire shapes with intentionally fragmented arguments.
func Fixture(p connection.Protocol, second bool) string {
	var events []string
	switch p {
	case connection.Chat:
		if second {
			events = []string{`{"choices":[{"index":0,"delta":{"content":"工具结果：42"},"finish_reason":null}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`}
		} else {
			events = []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-probe-1","function":{"name":"probe","arguments":"{\"value\":"}}]},"finish_reason":null}]}`, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"42}"}}]},"finish_reason":"tool_calls"}]}`}
		}
		events = append(events, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, `[DONE]`)
	case connection.Messages:
		events = append(events, `{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}`)
		if second {
			events = append(events, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"工具结果：42"}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		} else {
			events = append(events, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-probe-1","name":"probe","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"value\":"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"42}"}}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`)
		}
		events = append(events, `{"type":"message_stop"}`)
	case connection.Responses:
		if second {
			events = []string{`{"type":"response.output_text.delta","delta":"工具结果：42"}`, `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"工具结果：42"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}`}
		} else {
			events = []string{`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc-probe-1","call_id":"call-probe-1","name":"probe","arguments":""}}`, `{"type":"response.function_call_arguments.delta","item_id":"fc-probe-1","delta":"{\"value\":"}`, `{"type":"response.function_call_arguments.delta","item_id":"fc-probe-1","delta":"42}"}`, `{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"fc-probe-1","call_id":"call-probe-1","name":"probe","arguments":"{\"value\":42}"}],"usage":{"input_tokens":10,"output_tokens":5}}}`}
		}
	}
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}
