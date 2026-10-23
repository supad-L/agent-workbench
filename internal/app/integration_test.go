package app

import (
	"agent-workbench/contracts"
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/store"
	"agent-workbench/prototypes/protocol"
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
)

func local(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	a, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	s := httptest.NewServer(a.Handler())
	t.Cleanup(s.Close)
	return a, s
}

func TestAuthorizationRequiresBearerScheme(t *testing.T) {
	a, _ := local(t)
	for _, header := range []string{a.HumanToken, "Basic " + a.HumanToken, ""} {
		request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		request.Header.Set("Authorization", header)
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("malformed authorization accepted: %d", response.Code)
		}
	}
}
func TestDesktopM1ProjectDocumentDraftEditingAndErrorRoutes(t *testing.T) {
	a, s := local(t)
	token := a.HumanToken
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "untracked.txt"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	status, raw := call(t, s, token, "POST", "/api/projects", map[string]string{"name": "Imported", "source": source, "idempotency_key": "create"})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var p store.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	root := "/api/projects/" + p.ID
	status, retry := call(t, s, token, "POST", "/api/projects", map[string]string{"name": "Imported", "source": source, "idempotency_key": "create"})
	if status != 200 || !bytes.Equal(raw, retry) {
		t.Fatal("project retry changed snapshot")
	}
	status, raw = call(t, s, token, "GET", "/api/projects", nil)
	if status != 200 || !strings.Contains(string(raw), p.ID) {
		t.Fatal("project list missing imported project")
	}
	document := filepath.Join(t.TempDir(), "requirements.md")
	if err := os.WriteFile(document, []byte("first document version"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"doc-1", "doc-2"} {
		status, raw = call(t, s, token, "POST", root+"/documents", map[string]string{"path": document, "idempotency_key": key})
		if status != 200 {
			t.Fatal(status, string(raw))
		}
	}
	rawPlan, err := contracts.Files.ReadFile("examples/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	status, raw = call(t, s, token, "POST", root+"/plans", map[string]any{"content": json.RawMessage(rawPlan), "idempotency_key": "draft", "base_version": 0, "reason": ""})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var draft store.Plan
	if err = json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	status, raw = call(t, s, token, "PUT", root+"/plans/1", map[string]any{"content": json.RawMessage(rawPlan), "expected_hash": draft.Hash, "expected_revision": draft.Revision, "idempotency_key": "edit"})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	if err = json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 2 {
		t.Fatal("revision lost")
	}
	status, raw = call(t, s, token, "GET", root, nil)
	if status != 200 {
		t.Fatal(status)
	}
	var state struct {
		Documents []store.Document `json:"documents"`
		Plans     []store.Plan     `json:"plans"`
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Documents) != 2 || state.Documents[1].Version != 2 || len(state.Plans) != 1 {
		t.Fatal("project view lost documents or drafts")
	}
	status, raw = call(t, s, token, "POST", root+"/permission-demo", map[string]any{})
	if status != 200 || !strings.Contains(string(raw), `"agent_can_confirm":false`) {
		t.Fatal("permission demo did not exercise the guard")
	}
	status, _ = call(t, s, token, "GET", "/api/plan-template", nil)
	if status != 200 {
		t.Fatal(status)
	}
	for _, test := range []struct {
		method, path string
		body         any
		status       int
	}{{"GET", "/api/projects/missing", nil, 404}, {"POST", "/api/projects", map[string]string{"name": "", "idempotency_key": "invalid"}, 400}, {"POST", root + "/messages", map[string]string{"text": "x", "idempotency_key": "x", "role": "human"}, 400}, {"POST", root + "/plans", map[string]any{"content": map[string]string{}, "idempotency_key": "bad"}, 400}, {"PUT", root + "/plans/not-a-version", map[string]string{}, 400}, {"POST", root + "/plans/invalid/confirm", map[string]string{}, 400}, {"POST", root + "/documents", map[string]string{"path": "/not/a/file", "idempotency_key": "bad-doc"}, 400}, {"GET", root + "/events?cursor=-1", nil, 400}, {"POST", root + "/messages", map[string]string{"text": "", "idempotency_key": "empty"}, 400}} {
		status, raw = call(t, s, token, test.method, test.path, test.body)
		if status != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, status, raw)
		}
	}
	req := httptest.NewRequest("POST", "http://localhost/api/projects", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
}

func TestCustomCompanyAPIsUseSelectedProtocolAndSecretHeaders(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			a, s := local(t)
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-company-key" || r.Header.Get("X-Company-Route") != "synthetic-route" {
					t.Error("configured authentication/header lost")
				}
				if !strings.HasPrefix(r.URL.Path, "/company/v1/") {
					t.Error("company URL prefix lost")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprint(w, protocol.Fixture(p, calls.Add(1) == 2)); err != nil {
					t.Error(err)
				}
			}))
			defer gateway.Close()
			config := apiconfig.Config{Endpoint: connection.Endpoint{Protocol: p, Address: gateway.URL + "/company", AddressKind: "base_url", AllowLocalHTTP: true}, Model: "company-model", AuthKind: "bearer", KeyEnv: "AWB_COMPANY_KEY", HeaderEnvs: map[string]string{"X-Company-Route": "AWB_ROUTE"}, Egress: egress.Policy{Mode: "system", DNSMode: "system"}, TimeoutSeconds: 5, MaxOutputTokens: 300}
			status, raw := call(t, s, a.HumanToken, "POST", "/api/connections/check", config)
			if status != 200 || calls.Load() != 0 {
				t.Fatal("format validation called a model", status, string(raw))
			}
			status, raw = call(t, s, a.HumanToken, "POST", "/api/connections/test", map[string]any{"config": config, "values": map[string]string{"AWB_COMPANY_KEY": "synthetic-company-key", "AWB_ROUTE": "synthetic-route"}})
			if status != 200 || calls.Load() != 2 || strings.Contains(string(raw), "synthetic-company-key") {
				t.Fatal("custom tool probe failed or leaked credentials", status, string(raw))
			}
			status, raw = call(t, s, a.HumanToken, "POST", "/api/proxies/validate", map[string]string{"url": "http://user:synthetic-proxy-pass@127.0.0.1:1080"})
			if status != 200 || strings.Contains(string(raw), "synthetic-proxy-pass") || strings.Contains(string(raw), "user") {
				t.Fatal("proxy validator leaked credentials", status, string(raw))
			}
			status, raw = call(t, s, a.HumanToken, "POST", "/api/proxies/validate", map[string]string{"url": "http://synthetic-user:synthetic-pass@proxy.example:not-a-port"})
			if status != 400 || !strings.Contains(string(raw), "代理") || strings.Contains(string(raw), "synthetic-pass") || strings.Contains(string(raw), "synthetic-user") {
				t.Fatal("invalid proxy accepted or error was not localized and redacted")
			}
		})
	}
}
