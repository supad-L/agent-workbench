package app

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/store"
	"agent-workbench/prototypes/protocol"
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

func TestM2ThreeProtocolRoleTrialsAndBudgetHTTP(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			a, s := local(t)
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer SYNTHETIC_M2_PRIVATE_KEY" {
					t.Error("credential not resolved")
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				second := requests.Add(1) == 2
				if second && !strings.Contains(string(raw), "read_only_trial") {
					t.Error("tool result not continued")
				}
				fixture := protocol.Fixture(p, second)
				fixture = strings.ReplaceAll(fixture, `"name":"probe"`, `"name":"get_status"`)
				fixture = strings.ReplaceAll(fixture, `{\"value\":`, `{`)
				fixture = strings.ReplaceAll(fixture, `42}`, `}`)
				fixture = strings.ReplaceAll(fixture, "工具结果：42", "只读试运行完成")
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err = fmt.Fprint(w, fixture); err != nil {
					t.Error(err)
				}
			}))
			defer gateway.Close()
			status, raw := call(t, s, a.HumanToken, "POST", "/api/projects", map[string]string{"name": "M2 fixture", "source": "", "idempotency_key": "project"})
			if status != 200 {
				t.Fatal(status, string(raw))
			}
			var project store.Project
			if err := json.Unmarshal(raw, &project); err != nil {
				t.Fatal(err)
			}
			c := catalog.Connection{ID: "connection", Label: "合成中转", Revision: 1, AccountID: "fixture-account", MaxConcurrent: 1, Enabled: true, HasKey: true, Config: apiconfig.Config{Endpoint: connection.Endpoint{Protocol: p, Address: gateway.URL + "/company", AddressKind: "base_url", AllowLocalHTTP: true}, Model: "fixture", AuthKind: "bearer", KeyEnv: "AWB_M2_KEY", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, MaxOutputTokens: 64}, Models: []catalog.Model{{ID: "fixture", Tools: "supported", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}}
			status, raw = call(t, s, a.HumanToken, "PUT", "/api/connections/connection", c)
			if status != 200 {
				t.Fatal(status, string(raw))
			}
			r := catalog.Templates()[0]
			r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "fixture"}
			r.Tools = []string{"get_status"}
			r.Enabled = true
			status, raw = call(t, s, a.HumanToken, "PUT", "/api/roles/"+r.ID, r)
			if status != 200 {
				t.Fatal(status, string(raw))
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			status, raw = call(t, s, a.HumanToken, "PUT", "/api/budgets/CNY", store.Budget{Currency: "CNY", Enabled: true, Limit: 1000000})
			if status != 200 {
				t.Fatal(status, string(raw))
			}
			path := "/api/projects/" + project.ID + "/runs"
			input := map[string]any{"role_id": r.ID, "role_revision": r.Revision, "input": "仅进行合成验证", "scope": "conversation_only", "idempotency_key": "trial", "connection_revisions": map[string]int{c.ID: 1}, "values": map[string]map[string]string{c.ID: {"AWB_M2_KEY": "SYNTHETIC_M2_PRIVATE_KEY"}}}
			status, raw = call(t, s, a.HumanToken, "POST", path, input)
			if status != 200 || strings.Contains(string(raw), "SYNTHETIC_M2_PRIVATE_KEY") {
				t.Fatal(status, string(raw))
			}
			var run store.Run
			if err := json.Unmarshal(raw, &run); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(4 * time.Second)
			for run.Status == "running" && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				status, raw = call(t, s, a.HumanToken, "GET", path+"/"+run.ID, nil)
				if status != 200 {
					t.Fatal(status, string(raw))
				}
				if err := json.Unmarshal(raw, &run); err != nil {
					t.Fatal(err)
				}
			}
			if run.Status != "completed" || run.State.Text != "只读试运行完成" || requests.Load() != 2 {
				t.Fatal(run.Status, run.Error, run.State.Text, requests.Load())
			}
			for _, endpoint := range []string{path, "/api/usage"} {
				status, raw = call(t, s, a.HumanToken, "GET", endpoint, nil)
				if status != 200 || strings.Contains(string(raw), "SYNTHETIC_M2_PRIVATE_KEY") {
					t.Fatal(status, string(raw))
				}
			}
			status, _ = call(t, s, a.HumanToken, "POST", path+"/"+run.ID+"/resume", map[string]any{})
			if status != 200 || requests.Load() != 2 {
				t.Fatal("completed resume issued another model request", status)
			}
			status, _ = call(t, s, a.HumanToken, "POST", path+"/"+run.ID+"/cancel", nil)
			if status != 200 {
				t.Fatal(status)
			}
			status, _ = call(t, s, a.HumanToken, "POST", "/api/connections/connection/revoke", nil)
			if status != 200 {
				t.Fatal(status)
			}
			a.mu.Lock()
			a.agents["scoped-worker"] = store.Principal{ID: "worker", Role: "agent", ProjectID: project.ID}
			a.mu.Unlock()
			for _, route := range []string{path, path + "/" + run.ID + "/resume", path + "/" + run.ID + "/cancel", "/api/connections/connection/revoke"} {
				status, _ = call(t, s, "scoped-worker", "POST", route, input)
				if status != 403 {
					t.Fatal("agent gained trial/account control", route, status)
				}
			}
		})
	}
}
