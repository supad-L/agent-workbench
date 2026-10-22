package direct

import (
	"encoding/json"
	"errors"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
)

func buildRequest(protocol connection.Protocol, r modelruntime.Request) (map[string]any, error) {
	body := map[string]any{"model": r.Model, "stream": true}
	var messages []any
	var tools []any
	switch protocol {
	case connection.Messages:
		body["max_tokens"] = r.MaxOutputTokens
		if r.System != "" {
			body["system"] = r.System
		}
		for _, message := range r.Messages {
			role := message.Role
			var blocks []any
			if role == "tool" {
				role = "user"
				blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": message.CallID, "content": message.Text})
			} else {
				if message.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": message.Text})
				}
				for _, call := range message.Calls {
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": call.Arguments})
				}
			}
			messages = append(messages, map[string]any{"role": role, "content": blocks})
		}
		for _, tool := range r.Tools {
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Parameters})
		}
		body["messages"] = messages
	case connection.Chat:
		body["max_completion_tokens"] = r.MaxOutputTokens
		body["stream_options"] = map[string]any{"include_usage": true}
		if r.System != "" {
			messages = append(messages, map[string]any{"role": "system", "content": r.System})
		}
		for _, message := range r.Messages {
			m := map[string]any{"role": message.Role, "content": message.Text}
			if message.Role == "tool" {
				m["tool_call_id"] = message.CallID
			}
			if len(message.Calls) != 0 {
				var calls []any
				for _, call := range message.Calls {
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
				}
				m["tool_calls"] = calls
			}
			messages = append(messages, m)
		}
		for _, tool := range r.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}})
		}
		body["messages"] = messages
	case connection.Responses:
		body["max_output_tokens"] = r.MaxOutputTokens
		body["store"] = false
		if r.System != "" {
			body["instructions"] = r.System
		}
		for _, message := range r.Messages {
			if len(message.ProviderItems) != 0 {
				for _, item := range message.ProviderItems {
					messages = append(messages, json.RawMessage(item))
				}
				continue
			}
			if message.Role == "tool" {
				messages = append(messages, map[string]any{"type": "function_call_output", "call_id": message.CallID, "output": message.Text})
				continue
			}
			if message.Text != "" {
				messages = append(messages, map[string]any{"role": message.Role, "content": message.Text})
			}
			for _, call := range message.Calls {
				messages = append(messages, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
			}
		}
		for _, tool := range r.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
		}
		body["input"] = messages
	default:
		return nil, errors.New("unsupported protocol")
	}
	if len(tools) != 0 {
		body["tools"] = tools
	}
	return body, nil
}
