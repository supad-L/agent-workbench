package app

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/store"
	"agent-workbench/prototypes/protocol"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionProbeUsesBudgetLedgerAndDoesNotCreateVisibleProject(t *testing.T) {
	a, s := local(t)
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, protocol.Fixture(connection.Chat, requests.Add(1) == 2)); err != nil {
			t.Error(err)
		}
	}))
	defer gateway.Close()
	config := apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: gateway.URL, AddressKind: "base_url", AllowLocalHTTP: true}, Model: "fixture", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, TimeoutSeconds: 5, MaxOutputTokens: 64}
	c := catalog.Connection{ID: "probe", Label: "预算探针", Revision: 1, Config: config, AccountID: "probe-account", MaxConcurrent: 1, Enabled: true, Models: []catalog.Model{{ID: "fixture", Tools: "unknown", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}}
	status, raw := call(t, s, a.HumanToken, "PUT", "/api/budgets/CNY", store.Budget{Currency: "CNY", Enabled: true, Limit: 100})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	body := map[string]any{"config": config, "connection": c, "values": map[string]string{}, "idempotency_key": "blocked"}
	status, raw = call(t, s, a.HumanToken, "POST", "/api/connections/test", body)
	if status != 400 || requests.Load() != 0 || !strings.Contains(string(raw), "预算") {
		t.Fatal("connection probe bypassed budget", status, string(raw), requests.Load())
	}
	status, raw = call(t, s, a.HumanToken, "PUT", "/api/budgets/CNY", store.Budget{Currency: "CNY", Revision: 1, Enabled: true, Limit: 1000000})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	body["idempotency_key"] = "allowed"
	status, raw = call(t, s, a.HumanToken, "POST", "/api/connections/test", body)
	if status != 200 || requests.Load() != 2 {
		t.Fatal(status, string(raw), requests.Load())
	}
	status, replay := call(t, s, a.HumanToken, "POST", "/api/connections/test", body)
	if status != 200 || requests.Load() != 2 || !strings.Contains(string(replay), "tool_verified") {
		t.Fatal("replayed probe repeated billing or lost its report", status)
	}
	status, raw = call(t, s, a.HumanToken, "GET", "/api/projects", nil)
	if status != 200 || string(raw) != "[]\n" {
		t.Fatal("diagnostic namespace appeared as a user project", string(raw))
	}
	stats, _, err := a.Store.Usage(t.Context(), store.Principal{ID: "human", Role: "human"})
	if err != nil || stats[0].Cost != 30 || stats[0].Held != 0 {
		t.Fatal(stats, err)
	}
	var report map[string]any
	_, raw = call(t, s, a.HumanToken, "POST", "/api/connections/test", body)
	if err = json.Unmarshal(raw, &report); err != nil || report["usage_known"] != true {
		t.Fatal(string(raw), err)
	}
}

func TestProbeRejectsTextOnlySuccessAndAccountsItsUsage(t *testing.T) {
	a, s := local(t)
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, protocol.Fixture(connection.Chat, true)); err != nil {
			t.Error(err)
		}
	}))
	defer gateway.Close()
	c := probeFixtureConnection(gateway.URL)
	status, raw := call(t, s, a.HumanToken, "POST", "/api/connections/test", map[string]any{"config": c.Config, "connection": c, "values": map[string]string{}, "idempotency_key": "text-only"})
	if status != 400 || !strings.Contains(string(raw), "两轮工具") || requests.Load() != 1 {
		t.Fatal("text-only response passed a tool probe", status, string(raw))
	}
	stats, _, err := a.Store.Usage(t.Context(), store.Principal{ID: "user", Role: "human"})
	if err != nil || stats[0].Cost != 15 {
		t.Fatal("failed verification lost actual usage", stats, err)
	}
}

func probeFixtureConnection(url string) catalog.Connection {
	config := apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: url, AddressKind: "base_url", AllowLocalHTTP: true}, Model: "fixture", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, TimeoutSeconds: 5, MaxOutputTokens: 64}
	return catalog.Connection{ID: "probe", Label: "探针", Revision: 1, Config: config, AccountID: "probe-account", MaxConcurrent: 1, Enabled: true, Models: []catalog.Model{{ID: "fixture", Tools: "unknown", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}}
}

func TestDisconnectedProbeCancelsItsRequestAndRetainsUncertainReservation(t *testing.T) {
	a, s := local(t)
	started := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			t.Error("request context was not canceled")
		}
	}))
	defer gateway.Close()
	c := probeFixtureConnection(gateway.URL)
	body, err := json.Marshal(map[string]any{"config": c.Config, "connection": c, "values": map[string]string{}, "idempotency_key": "disconnect"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/api/connections/test", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.HumanToken)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := s.Client().Do(req)
		if err == nil {
			if err = response.Body.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe not started")
	}
	cancel()
	<-done
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := a.Store.Runs(t.Context(), store.Principal{ID: "user", Role: "human"}, store.ProbeProject)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && runs[0].Status != "running" {
			stats, _, err := a.Store.Usage(t.Context(), store.Principal{ID: "user", Role: "human"})
			if err != nil || stats[0].Held == 0 || stats[0].UnknownRequests != 1 {
				t.Fatal(stats, err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("disconnected probe continued running")
}
