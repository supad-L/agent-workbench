package app

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func chatEvents(events ...string) string {
	events = append(events, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, `[DONE]`)
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

func TestTaskRoutesRunAPIRoleOnPrivateCopy(t *testing.T) {
	a, s := local(t)
	token := a.HumanToken
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			args, _ := json.Marshal(`{"path":"src/answer.txt","content":"42\n"}`)
			if _, err := fmt.Fprint(w, chatEvents(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"w1","function":{"name":"write_file","arguments":`+string(args)+`}}]},"finish_reason":"tool_calls"}]}`)); err != nil {
				t.Error(err)
			}
			return
		}
		if _, err := fmt.Fprint(w, chatEvents(`{"choices":[{"index":0,"delta":{"content":"已写入答案"},"finish_reason":"stop"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer gateway.Close()

	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "answer.txt"), []byte("41\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, raw := call(t, s, token, "POST", "/api/projects", map[string]string{"name": "Task project", "source": source, "idempotency_key": "create"})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var project store.Project
	_ = json.Unmarshal(raw, &project)
	config := apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: gateway.URL, AddressKind: "base_url", AllowLocalHTTP: true}, Model: "m", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, TimeoutSeconds: 5, MaxOutputTokens: 300}
	c := catalog.Connection{ID: "local", Label: "Local", Revision: 1, Config: config, AccountID: "local-account", MaxConcurrent: 1, Enabled: true, Models: []catalog.Model{{ID: "m", Tools: "supported"}}}
	if status, raw = call(t, s, token, "PUT", "/api/connections/local", c); status != 200 {
		t.Fatal(status, string(raw))
	}
	role := catalog.Templates()[1]
	role.Enabled, role.Primary, role.Tools = true, catalog.Choice{ConnectionID: "local", ModelID: "m"}, []string{"read_file", "write_file"}
	status, raw = call(t, s, token, "PUT", "/api/roles/"+role.ID, role)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	_ = json.Unmarshal(raw, &role)
	input := map[string]any{"role_id": role.ID, "role_revision": role.Revision, "title": "更新答案", "instructions": "把答案改成 42", "write_dirs": []string{"src"}, "idempotency_key": "task", "connection_revisions": map[string]int{"local": 1}}
	status, raw = call(t, s, token, "POST", "/api/projects/"+project.ID+"/tasks", input)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var run store.Run
	_ = json.Unmarshal(raw, &run)
	deadline := time.Now().Add(5 * time.Second)
	for run.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		_, raw = call(t, s, token, "GET", "/api/projects/"+project.ID+"/runs/"+run.ID, nil)
		_ = json.Unmarshal(raw, &run)
	}
	if run.Status != "completed" || run.State.Text != "已写入答案" || len(run.Task.Changes) != 1 {
		t.Fatalf("task run %s %s %+v", run.Status, run.Error, run.Task)
	}
	_, raw = call(t, s, token, "GET", "/api/projects/"+project.ID+"/tasks", nil)
	if !strings.Contains(string(raw), run.ID) {
		t.Fatal("task list missing run")
	}
	status, raw = call(t, s, token, "GET", "/api/projects/"+project.ID+"/tasks/"+run.ID+"/changes", nil)
	if status != 200 || !strings.Contains(string(raw), "-41") || !strings.Contains(string(raw), "+42") {
		t.Fatal(status, string(raw))
	}
	if original, _ := os.ReadFile(filepath.Join(source, "src", "answer.txt")); string(original) != "41\n" {
		t.Fatal("original project changed")
	}
	if snapshot, _ := os.ReadFile(filepath.Join(project.Snapshot, "src", "answer.txt")); string(snapshot) != "41\n" {
		t.Fatal("managed snapshot changed")
	}
}
