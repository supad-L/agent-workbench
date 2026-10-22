package protocol

import (
	"context"
	"encoding/json"
	"errors"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
)

// Configured runs a no-project-data, no-side-effect probe against an explicit model connection.
// Its caller must obtain credentials and routing policy only from explicit user configuration.
func Configured(ctx context.Context, client modelruntime.Model, model string, p connection.Protocol) (Report, error) {
	return ConfiguredWithLimit(ctx, client, model, p, 256)
}

// ConfiguredWithLimit uses an explicit bounded output limit for a live tool probe.
func ConfiguredWithLimit(ctx context.Context, client modelruntime.Model, model string, p connection.Protocol, limit int) (Report, error) {
	if limit < 1 || limit > 4096 {
		return Report{}, errors.New("invalid probe output limit")
	}
	request := modelruntime.Request{Model: model, System: "This is a connectivity test. Call the probe tool with value 42, then briefly confirm the tool result. Do not request other tools.", MaxOutputTokens: limit, Messages: []modelruntime.Message{{Role: "user", Text: "Call probe with value 42."}}, Tools: []modelruntime.Tool{{Name: "probe", Description: "Returns the provided integer for a connectivity test; has no external side effects.", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)}}}
	first, err := client.Turn(ctx, request, nil)
	if err != nil {
		return Report{}, err
	}
	if len(first.Calls) != 1 || first.Calls[0].Name != "probe" {
		return Report{}, errors.New("connection did not exercise the requested tool")
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(first.Calls[0].Arguments, &arguments); err != nil || len(arguments) != 1 {
		return Report{}, errors.New("probe tool arguments were not valid")
	}
	var value int
	if err := json.Unmarshal(arguments["value"], &value); err != nil || value != 42 {
		return Report{}, errors.New("probe tool arguments did not match the test")
	}
	request.Messages = append(request.Messages, modelruntime.Message{Role: "assistant", Text: first.Text, Calls: first.Calls, ProviderItems: first.ProviderItems}, modelruntime.Message{Role: "tool", CallID: first.Calls[0].ID, Text: "42"})
	second, err := client.Turn(ctx, request, nil)
	if err != nil {
		return Report{}, err
	}
	if len(second.Calls) != 0 || second.Text == "" {
		return Report{}, errors.New("connection did not complete the tool-result round")
	}
	// Raw model text is deliberately excluded from this user-visible probe report.
	return Report{Protocol: p, Evidence: "explicitly_configured_endpoint", Requests: 2, ToolVerified: true, UsageKnown: first.Usage.Known && second.Usage.Known}, nil
}
