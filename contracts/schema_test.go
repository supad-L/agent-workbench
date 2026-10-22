package contracts

import (
	"encoding/json"
	"testing"
)

func TestExamplesAndRejectedPrivilegeChanges(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names {
		t.Run(name, func(t *testing.T) {
			data, err := Files.ReadFile("examples/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(name, data); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, test := range []struct {
		name, contract string
		mutate         func(map[string]any)
	}{
		{"suggestion blocks delivery", "review", func(v map[string]any) { v["suggestions"].([]any)[0].(map[string]any)["blocks_delivery"] = true }},
		{"pass with open blocking finding", "review", func(v map[string]any) { v["verdict"] = "pass" }},
		{"blocked without reason", "review", func(v map[string]any) { v["verdict"] = "blocked" }},
		{"self-confirmed plan", "plan", func(v map[string]any) { v["confirmed_by"] = "agent" }},
		{"manual ordinary task gate", "plan", func(v map[string]any) { v["autonomy"].(map[string]any)["ordinary_tasks"] = "manual" }},
		{"plaintext API credential", "connection", func(v map[string]any) { v["api_key"] = "synthetic-secret" }},
		{"proxy mode without proxy", "connection", func(v map[string]any) { v["egress"] = map[string]any{"mode": "explicit_proxy", "dns_mode": "proxy"} }},
		{"proxy mode with system DNS", "connection", func(v map[string]any) {
			v["egress"] = map[string]any{"mode": "explicit_proxy", "dns_mode": "system", "proxy_ref": "proxy-1"}
		}},
		{"system mode with proxy", "connection", func(v map[string]any) { v["egress"].(map[string]any)["proxy_ref"] = "proxy-1" }},
		{"plaintext proxy password", "proxy_endpoint", func(v map[string]any) { v["password"] = "synthetic-secret" }},
		{"proxy authentication embedded in host", "proxy_endpoint", func(v map[string]any) { v["host"] = "user:synthetic-secret@proxy.invalid" }},
		{"proxy port outside range", "proxy_endpoint", func(v map[string]any) { v["port"] = 65536 }},
		{"binding missing proxy", "network_binding", func(v map[string]any) { delete(v, "proxy_id") }},
		{"conflicting binding choices", "network_binding", func(v map[string]any) { v["ui_mode"] = "system" }},
		{"UI protection downgrade", "network_binding", func(v map[string]any) { v["requires_dedicated_proxy"] = false }},
		{"missing base commit", "task", func(v map[string]any) { delete(v, "base_commit") }},
		{"candidate controls acceptance", "verification", func(v map[string]any) { v["candidate_scripts_are_authoritative"] = true }},
		{"message status escalation", "message", func(v map[string]any) { v["payload"].(map[string]any)["task_state"] = "integrated" }},
		{"missing lease epoch", "message", func(v map[string]any) { delete(v, "lease_epoch") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := Files.ReadFile("examples/" + test.contract + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			changed, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if validator.Validate(test.contract, changed) == nil {
				t.Fatal("invalid authority or baseline change accepted")
			}
		})
	}
}
