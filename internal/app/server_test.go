package app

import (
	"agent-workbench/contracts"
	"agent-workbench/internal/store"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, server *httptest.Server, token, method, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}
func TestHumanAgentAPIAuthorityAndEventReplay(t *testing.T) {
	ctx := context.Background()
	backend, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Store.Close(); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(backend.Handler())
	defer server.Close()
	token := backend.HumanToken
	status, raw := call(t, server, token, "POST", "/api/projects", map[string]any{"name": "API fixture", "source": "", "idempotency_key": "create-1"})
	if status != 200 {
		t.Fatalf("create: %d %s", status, raw)
	}
	var p store.Project
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	status, raw = call(t, server, token, "POST", "/api/projects/"+p.ID+"/agent-token", nil)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var creds map[string]string
	if err = json.Unmarshal(raw, &creds); err != nil {
		t.Fatal(err)
	}
	agent := creds["token"]
	plan, err := contracts.Files.ReadFile("examples/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	status, raw = call(t, server, agent, "POST", "/api/projects/"+p.ID+"/plans", map[string]any{"content": json.RawMessage(plan), "base_version": 0, "reason": "", "idempotency_key": "draft"})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var v store.Plan
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	confirm := map[string]any{"expected_hash": v.Hash, "expected_revision": v.Revision, "idempotency_key": "confirm"}
	status, _ = call(t, server, agent, "POST", "/api/projects/"+p.ID+"/plans/1/confirm", confirm)
	if status != 403 {
		t.Fatal("agent confirmed plan through API", status)
	}
	status, _ = call(t, server, token, "POST", "/api/projects/"+p.ID+"/plans/1/confirm", confirm)
	if status != 200 {
		t.Fatal("human could not confirm", status)
	}
	status, _ = call(t, server, "missing-token", "GET", "/api/projects", nil)
	if status != 401 {
		t.Fatal("unauthenticated API accepted")
	}
	status, _ = call(t, server, agent, "POST", "/api/projects", map[string]string{"name": "forged", "idempotency_key": "x"})
	if status != 403 {
		t.Fatal("agent imported a project")
	}
	status, _ = call(t, server, token, "POST", "/api/projects/"+p.ID+"/messages", map[string]string{"text": "saved", "idempotency_key": "message"})
	if status != 200 {
		t.Fatal(status)
	}
	events, err := backend.Store.Events(ctx, store.Principal{ID: "desktop-human", Role: "human"}, p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cursor := events[len(events)-2].Sequence
	streamCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(streamCtx, "GET", server.URL+"/api/projects/"+p.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", jsonNumber(cursor))
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var event store.Event
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil || event.Kind != "message.created" || event.Sequence <= cursor {
				t.Fatal("SSE replay cursor incorrect", line)
			}
			cancel()
			return
		}
	}
	t.Fatal("SSE did not replay a durable event", scanner.Err())
}
func jsonNumber(v int64) string { data, _ := json.Marshal(v); return string(data) }
