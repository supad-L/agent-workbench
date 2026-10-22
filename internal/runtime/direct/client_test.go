package direct_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-workbench/internal/connection"
	modelruntime "agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/prototypes/protocol"
)

func wireTurn(t *testing.T, p connection.Protocol, wire string) (modelruntime.Result, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, wire); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: p, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, AuthKind: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return client.Turn(context.Background(), input(), nil)
}

func TestMissingUsageIsNotKnownZero(t *testing.T) {
	usage := regexp.MustCompile(`"usage":\{[^}]*\}`)
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			wire := usage.ReplaceAllString(protocol.Fixture(p, true), `"usage":{}`)
			result, err := wireTurn(t, p, wire)
			if err != nil {
				t.Fatal(err)
			}
			if result.Usage.Known {
				t.Fatal("missing token fields were marked as known zero")
			}
			if result.Text != "工具结果：42" {
				t.Fatal("text lost")
			}
		})
	}
}

func TestToolCallsRequireMatchingTerminalReason(t *testing.T) {
	for _, test := range []struct {
		p        connection.Protocol
		from, to string
	}{{connection.Chat, `"finish_reason":"tool_calls"`, `"finish_reason":"stop"`}, {connection.Messages, `"stop_reason":"tool_use"`, `"stop_reason":"end_turn"`}} {
		t.Run(string(test.p), func(t *testing.T) {
			wire := strings.ReplaceAll(protocol.Fixture(test.p, false), test.from, test.to)
			if result, err := wireTurn(t, test.p, wire); err == nil || len(result.Calls) != 0 {
				t.Fatal("tool calls accepted without tool-call terminal reason")
			}
		})
	}
}

func TestResponsesFinalOutputRemainsAuthoritative(t *testing.T) {
	wire := protocol.Fixture(connection.Responses, true)
	parts := strings.Split(wire, "\n\n")
	var final string
	for _, part := range parts {
		if strings.Contains(part, `"type":"response.completed"`) {
			final = part + "\n\n"
		}
	}
	result, err := wireTurn(t, connection.Responses, final)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "工具结果：42" {
		t.Fatal("completed response text was lost when delta events were absent")
	}
}

func TestMultilineSSEAndInterleavedTools(t *testing.T) {
	wire := ": keepalive\r\nevent: delta\r\ndata: {\"choices\":\r\ndata: [{\"index\":0,\"delta\":{\"content\":\"工具结果：42\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n"
	result, err := wireTurn(t, connection.Chat, wire)
	if err != nil || result.Text != "工具结果：42" {
		t.Fatalf("multiline framing failed: %+v %v", result, err)
	}
	tools := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"second\",\"function\":{\"name\":\"probe\",\"arguments\":\"{\\\"value\\\":2}\"}},{\"index\":0,\"id\":\"first\",\"function\":{\"name\":\"probe\",\"arguments\":\"{\\\"value\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	result, err = wireTurn(t, connection.Chat, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 2 || result.Calls[0].ID != "first" || result.Calls[1].ID != "second" {
		t.Fatal("interleaved tool identities were mixed")
	}
}

func TestNegativeUsageCannotCreditBudget(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Chat, connection.Messages, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			wire := protocol.Fixture(p, true)
			wire = strings.ReplaceAll(wire, `"prompt_tokens":10`, `"prompt_tokens":-1`)
			wire = strings.ReplaceAll(wire, `"input_tokens":10`, `"input_tokens":-1`)
			if _, err := wireTurn(t, p, wire); err == nil {
				t.Fatal("negative token count accepted")
			}
		})
	}
}

func input() modelruntime.Request {
	return modelruntime.Request{Model: "synthetic", Messages: []modelruntime.Message{{Role: "user", Text: "test"}}, MaxOutputTokens: 128, Tools: []modelruntime.Tool{{Name: "probe", Parameters: json.RawMessage(`{"type":"object"}`)}}}
}

func TestInterruptedAndInvalidStreamsNeverReturnExecutableCalls(t *testing.T) {
	tests := []struct {
		name string
		p    connection.Protocol
		wire string
	}{
		{"chat missing terminator", connection.Chat, strings.ReplaceAll(protocol.Fixture(connection.Chat, false), "data: [DONE]\n\n", "")},
		{"message missing terminal", connection.Messages, strings.ReplaceAll(protocol.Fixture(connection.Messages, false), "data: {\"type\":\"message_stop\"}\n\n", "")},
		{"responses missing terminal", connection.Responses, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\"}\n\n"},
		{"chat truncated by limit", connection.Chat, strings.ReplaceAll(protocol.Fixture(connection.Chat, false), "tool_calls\"}]", "length\"}]")},
		{"message truncated by limit", connection.Messages, strings.ReplaceAll(protocol.Fixture(connection.Messages, false), "\"stop_reason\":\"tool_use\"", "\"stop_reason\":\"max_tokens\"")},
		{"responses incomplete", connection.Responses, "data: {\"type\":\"response.incomplete\"}\n\n"},
		{"malformed JSON", connection.Chat, "data: {broken}\n\n"},
		{"unauthorized tool", connection.Chat, strings.ReplaceAll(protocol.Fixture(connection.Chat, false), "\"name\":\"probe\"", "\"name\":\"write_host_file\"")},
		{"invalid assembled arguments", connection.Messages, strings.ReplaceAll(protocol.Fixture(connection.Messages, false), "42}", "42")},
		{"provider error", connection.Messages, "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"private details\"}}\n\n"},
		{"oversized event", connection.Chat, "data: " + strings.Repeat("x", (1<<20)+1) + "\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprint(w, test.wire); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: test.p, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, AuthKind: "none"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Turn(context.Background(), input(), nil)
			if err == nil {
				t.Fatalf("invalid stream accepted: %+v", result)
			}
			if len(result.Calls) != 0 {
				t.Fatal("failed stream returned executable calls")
			}
			if strings.Contains(err.Error(), "private details") {
				t.Fatal("provider response body leaked")
			}
		})
	}
}

func TestCredentialHeadersAndSafeFailures(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if p == connection.Messages {
					if r.Header.Get("X-Api-Key") != "synthetic-secret" || r.Header.Get("Anthropic-Version") == "" {
						t.Error("missing Messages authentication")
					}
				} else if r.Header.Get("Authorization") != "Bearer synthetic-secret" {
					t.Error("missing bearer authentication")
				}
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(429)
				if _, err := fmt.Fprint(w, `{"error":"synthetic-secret https://private-company/path"}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: p, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, Key: "synthetic-secret"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Turn(context.Background(), input(), nil)
			var failure *direct.Failure
			if !errors.As(err, &failure) || failure.Status != 429 || failure.RetryAfter != 7*time.Second {
				t.Fatalf("wrong failure: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "private-company") {
				t.Fatal("upstream body leaked")
			}
		})
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: connection.Messages, Address: redirect.URL, AddressKind: "base_url", AllowLocalHTTP: true}, Key: "synthetic-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Turn(context.Background(), input(), nil)
	if err == nil || calls.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestCancelClosesInFlightStream(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"starting\"},\"finish_reason\":null}]}\n\n"); err != nil {
			t.Error(err)
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := direct.New(direct.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, AuthKind: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.Turn(ctx, input(), func(modelruntime.Event) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("provider connection remained open")
	}
}

func TestConfigurationRejectsAuthOverride(t *testing.T) {
	tests := []direct.Config{
		{AuthKind: "unknown"},
		{AuthKind: "bearer"},
		{AuthKind: "none", Headers: map[string]string{"X-Test": "bad\nvalue"}},
		{AuthKind: "none", Headers: map[string]string{"Authorization": "unexpected"}},
	}
	for _, config := range tests {
		config.Endpoint = connection.Endpoint{Protocol: connection.Chat, Address: "https://example.invalid", AddressKind: "base_url"}
		if _, err := direct.New(config); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
