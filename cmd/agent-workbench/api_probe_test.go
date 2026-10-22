package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agent-workbench/internal/connection"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/prototypes/protocol"
)

func TestExplicitAPIProbeUsesOnlyConfiguredCredentialAndEndpoint(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if p == connection.Messages {
					if r.Header.Get("X-Api-Key") != "synthetic-key" {
						t.Error("wrong explicit credential")
					}
				} else if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("wrong explicit credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprint(w, protocol.Fixture(p, calls.Add(1) == 2)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("AWB_TEST_KEY", "synthetic-key")
			config := map[string]any{"endpoint": connection.Endpoint{Protocol: p, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, "model": "synthetic-model", "auth_kind": "", "key_env": "AWB_TEST_KEY", "egress": map[string]string{"mode": "system", "dns_mode": "system"}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "probe.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run(context.Background(), []string{"api-probe", "--config", file}, &out); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 || !json.Valid(out.Bytes()) || strings.Contains(out.String(), "synthetic-key") || strings.Contains(out.String(), "工具结果") {
				t.Fatal("probe failed or leaked raw response")
			}
		})
	}
}

func TestDeepSeekProbeUsesExplicitNonThinkingChatDialect(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		thinking, ok := body["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" || body["max_tokens"] != float64(256) || body["max_completion_tokens"] != nil {
			t.Error("DeepSeek bounded non-thinking request options missing")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-deepseek-key" {
			t.Error("wrong explicit credential")
		}
		n := calls.Add(1)
		if n == 2 {
			messages := body["messages"].([]any)
			last := messages[len(messages)-1].(map[string]any)
			if last["role"] != "tool" || last["tool_call_id"] == nil || last["content"] != "42" {
				t.Error("tool result was not continued")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, protocol.Fixture(connection.Chat, n == 2)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("AWB_TEST_KEY", "synthetic-deepseek-key")
	config := apiProbeConfig{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: server.URL, AddressKind: "base_url", AllowLocalHTTP: true}, Model: "synthetic-model", ChatDialect: "deepseek", AuthKind: "bearer", KeyEnv: "AWB_TEST_KEY"}
	config.Egress.Mode, config.Egress.DNSMode = "system", "system"
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "probe.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := apiProbe(context.Background(), file, &out); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || strings.Contains(out.String(), "synthetic-deepseek-key") {
		t.Fatal("DeepSeek fixture failed or leaked credentials")
	}
	for _, config := range []direct.Config{
		{Endpoint: connection.Endpoint{Protocol: connection.Messages, Address: "https://example.invalid", AddressKind: "base_url"}, AuthKind: "none", ChatDialect: "deepseek"},
		{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: "https://example.invalid", AddressKind: "base_url"}, AuthKind: "none", ChatDialect: "automatic"},
	} {
		if _, err := direct.New(config); err == nil {
			t.Fatal("unsupported dialect/protocol accepted")
		}
	}
}

func TestAPIProbeRejectsAmbientOrUnverifiedCredentialsAndRoutes(t *testing.T) {
	for _, config := range []string{
		`{"endpoint":{"protocol":"messages","address":"https://example.invalid","address_kind":"base_url"},"model":"test","key_env":"HOME","egress":{"mode":"system","dns_mode":"system"}}`,
		`{"endpoint":{"protocol":"messages","address":"https://example.invalid","address_kind":"base_url"},"model":"test","key_env":"AWB_MISSING_KEY","egress":{"mode":"system","dns_mode":"system"}}`,
		`{"endpoint":{"protocol":"messages","address":"https://example.invalid","address_kind":"base_url"},"model":"test","key_env":"AWB_TEST_KEY","egress":{"mode":"enforced_proxy","dns_mode":"proxy"}}`,
		`{"unexpected_key":"synthetic-secret"}`,
		`{} {}`,
	} {
		file := filepath.Join(t.TempDir(), "probe.json")
		if err := os.WriteFile(file, []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := apiProbe(context.Background(), file, &out); err == nil {
			t.Fatal("unconfigured or unsafe probe accepted")
		}
	}
	if err := apiProbe(context.Background(), "", &bytes.Buffer{}); err == nil {
		t.Fatal("implicit config allowed")
	}
}
