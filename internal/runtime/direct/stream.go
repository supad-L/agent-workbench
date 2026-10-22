package direct

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
)

var errStreamDone = errors.New("provider terminal event")

type partialCall struct {
	id, name  string
	arguments strings.Builder
	initial   json.RawMessage
}
type decoder struct {
	protocol    connection.Protocol
	result      modelruntime.Result
	calls       map[int]*partialCall
	finish      string
	done        bool
	inputKnown  bool
	outputKnown bool
	emit        func(modelruntime.Event) error
}

func decode(protocol connection.Protocol, reader io.Reader, emit func(modelruntime.Event) error) (modelruntime.Result, error) {
	d := decoder{protocol: protocol, calls: map[int]*partialCall{}, emit: emit}
	err := scanSSE(reader, d.event)
	if err != nil && !errors.Is(err, errStreamDone) {
		return modelruntime.Result{}, err
	}
	if !d.done {
		return modelruntime.Result{}, errors.New("incomplete provider stream: terminal event missing")
	}
	indices := make([]int, 0, len(d.calls))
	for index := range d.calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := d.calls[index]
		arguments := json.RawMessage(call.arguments.String())
		if len(arguments) == 0 {
			arguments = call.initial
		}
		d.result.Calls = append(d.result.Calls, modelruntime.ToolCall{ID: call.id, Name: call.name, Arguments: arguments})
	}
	if len(d.result.Calls) > 32 {
		return modelruntime.Result{}, errors.New("too many tool calls")
	}
	if protocol == connection.Chat && (len(d.result.Calls) > 0) != (d.finish == "tool_calls") {
		return modelruntime.Result{}, errors.New("chat terminal reason does not match tool calls")
	}
	if protocol == connection.Messages && (len(d.result.Calls) > 0) != (d.finish == "tool_use") {
		return modelruntime.Result{}, errors.New("message terminal reason does not match tool calls")
	}
	return d.result, nil
}

func (d *decoder) usage(input, output *int64) error {
	if input != nil {
		if *input < 0 {
			return errors.New("negative provider token usage")
		}
		d.result.Usage.InputTokens = *input
		d.inputKnown = true
	}
	if output != nil {
		if *output < 0 {
			return errors.New("negative provider token usage")
		}
		d.result.Usage.OutputTokens = *output
		d.outputKnown = true
	}
	d.result.Usage.Known = d.inputKnown && d.outputKnown
	return nil
}

func scanSSE(reader io.Reader, receive func(string, string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var event string
	var data []string
	size := 0
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := receive(event, strings.Join(data, "\n"))
		event, data, size = "", nil, 0
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			size += len(value)
			if size > 1<<20 {
				return errors.New("provider event exceeds size limit")
			}
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("provider stream read failed")
	}
	return flush()
}

func (d *decoder) call(index int) *partialCall {
	if d.calls[index] == nil {
		d.calls[index] = &partialCall{}
	}
	return d.calls[index]
}

func (d *decoder) text(text string) error {
	d.result.Text += text
	if len(d.result.Text) > 8<<20 {
		return errors.New("model text exceeds size limit")
	}
	if d.emit != nil && text != "" {
		return d.emit(modelruntime.Event{Type: "text_delta", Text: text})
	}
	return nil
}

func (d *decoder) event(event, data string) error {
	if data == "[DONE]" {
		if d.protocol != connection.Chat {
			return nil
		}
		if d.finish != "stop" && d.finish != "tool_calls" {
			return errors.New("chat stream has no successful finish reason")
		}
		d.done = true
		return errStreamDone
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return errors.New("malformed provider stream JSON")
	}
	if _, exists := raw["error"]; exists {
		return errors.New("provider reported stream error")
	}
	switch d.protocol {
	case connection.Chat:
		return d.chat(raw)
	case connection.Messages:
		return d.messages(raw)
	case connection.Responses:
		return d.responses(raw)
	default:
		return fmt.Errorf("unsupported stream protocol: %s", d.protocol)
	}
}

func (d *decoder) chat(raw map[string]json.RawMessage) error {
	var chunk struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &chunk); err != nil {
		return errors.New("invalid chat event")
	}
	if chunk.Usage != nil {
		if err := d.usage(chunk.Usage.Input, chunk.Usage.Output); err != nil {
			return err
		}
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return errors.New("multiple chat choices are not supported")
		}
		if err := d.text(choice.Delta.Content); err != nil {
			return err
		}
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Index < 0 || tc.Index >= 32 {
				return errors.New("invalid tool index")
			}
			call := d.call(tc.Index)
			call.id += tc.ID
			call.name += tc.Function.Name
			call.arguments.WriteString(tc.Function.Arguments)
			if call.arguments.Len() > 1<<20 {
				return errors.New("tool arguments exceed size limit")
			}
		}
		if choice.FinishReason != nil {
			d.finish = *choice.FinishReason
		}
	}
	return nil
}

func (d *decoder) messages(raw map[string]json.RawMessage) error {
	typ := stringField(raw, "type")
	index := intField(raw, "index")
	switch typ {
	case "message_start":
		var message struct {
			Usage struct {
				Input  *int64 `json:"input_tokens"`
				Output *int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw["message"], &message); err != nil {
			return errors.New("invalid message start")
		}
		if err := d.usage(message.Usage.Input, message.Usage.Output); err != nil {
			return err
		}
	case "content_block_start":
		var block struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			Text  string          `json:"text"`
		}
		if err := json.Unmarshal(raw["content_block"], &block); err != nil {
			return errors.New("invalid content block")
		}
		if block.Type == "tool_use" {
			if index < 0 || index >= 64 || len(d.calls) >= 32 {
				return errors.New("invalid tool index")
			}
			call := d.call(index)
			call.id, call.name, call.initial = block.ID, block.Name, block.Input
		}
		if block.Type == "text" {
			return d.text(block.Text)
		}
	case "content_block_delta":
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(raw["delta"], &delta); err != nil {
			return errors.New("invalid content delta")
		}
		if delta.Type == "text_delta" {
			return d.text(delta.Text)
		}
		if delta.Type == "input_json_delta" {
			call := d.calls[index]
			if call == nil {
				return errors.New("tool delta without tool start")
			}
			call.arguments.WriteString(delta.PartialJSON)
			if call.arguments.Len() > 1<<20 {
				return errors.New("tool arguments exceed size limit")
			}
		}
	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		if err := json.Unmarshal(raw["delta"], &delta); err != nil {
			return errors.New("invalid message delta")
		}
		d.finish = delta.StopReason
		var usage struct {
			Output *int64 `json:"output_tokens"`
		}
		if len(raw["usage"]) != 0 {
			if err := json.Unmarshal(raw["usage"], &usage); err != nil {
				return errors.New("invalid message usage")
			}
			if usage.Output != nil {
				if err := d.usage(nil, usage.Output); err != nil {
					return err
				}
			}
		}
	case "message_stop":
		if d.finish != "end_turn" && d.finish != "tool_use" && d.finish != "stop_sequence" {
			return errors.New("message did not finish successfully")
		}
		d.done = true
		return errStreamDone
	case "error":
		return errors.New("provider reported stream error")
	}
	return nil
}

func (d *decoder) responses(raw map[string]json.RawMessage) error {
	switch stringField(raw, "type") {
	case "response.output_text.delta":
		return d.text(stringField(raw, "delta"))
	case "response.completed":
		var response struct {
			Status string            `json:"status"`
			Output []json.RawMessage `json:"output"`
			Usage  *struct {
				Input  *int64 `json:"input_tokens"`
				Output *int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw["response"], &response); err != nil {
			return errors.New("invalid completed response")
		}
		if response.Status != "completed" {
			return errors.New("response not completed")
		}
		d.result.ProviderItems = response.Output
		var finalText strings.Builder
		for _, item := range response.Output {
			var call struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(item, &call); err != nil {
				return errors.New("invalid response output")
			}
			if call.Type == "function_call" {
				d.result.Calls = append(d.result.Calls, modelruntime.ToolCall{ID: call.CallID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
			}
			if call.Type == "message" {
				for _, part := range call.Content {
					if part.Type == "output_text" {
						finalText.WriteString(part.Text)
					}
				}
			}
		}
		if finalText.Len() > 8<<20 {
			return errors.New("model text exceeds size limit")
		}
		if finalText.Len() != 0 {
			d.result.Text = finalText.String()
		}
		if response.Usage != nil {
			if err := d.usage(response.Usage.Input, response.Usage.Output); err != nil {
				return err
			}
		}
		d.done = true
		return errStreamDone
	case "response.failed", "response.incomplete", "error":
		return errors.New("response failed or incomplete")
	}
	return nil
}

func stringField(raw map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(raw[key], &value) != nil {
		return ""
	}
	return value
}
func intField(raw map[string]json.RawMessage, key string) int {
	var value int
	if json.Unmarshal(raw[key], &value) != nil {
		return -1
	}
	return value
}
