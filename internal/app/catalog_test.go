package app

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogHTTPRoleLifecycleAndSecretRejection(t *testing.T) {
	a, s := local(t)
	c := catalog.Connection{ID: "api-connection", Label: "API", Revision: 1, AccountID: "account", MaxConcurrent: 1, Enabled: true, Config: apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Messages, Address: "https://gateway.invalid", AddressKind: "base_url"}, Model: "custom", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}}, Models: []catalog.Model{{ID: "custom", Tools: "unknown"}}}
	status, raw := call(t, s, a.HumanToken, "PUT", "/api/connections/"+c.ID, c)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	status, raw = call(t, s, a.HumanToken, "POST", "/api/connections/validate", c)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	status, raw = call(t, s, a.HumanToken, "GET", "/api/role-templates", nil)
	if status != 200 || !strings.Contains(string(raw), "前端工程师") {
		t.Fatal(status)
	}
	r := catalog.Templates()[1]
	r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "custom"}
	r.Enabled = true
	status, raw = call(t, s, a.HumanToken, "PUT", "/api/roles/"+r.ID, r)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	status, raw = call(t, s, a.HumanToken, "GET", "/api/roles", nil)
	if status != 200 || !strings.Contains(string(raw), r.ID) {
		t.Fatal(status, string(raw))
	}
	status, _ = call(t, s, a.HumanToken, "DELETE", "/api/connections/"+c.ID+"?revision=1", nil)
	if status != 400 {
		t.Fatal("referenced connection deletion", status)
	}
	status, _ = call(t, s, a.HumanToken, "DELETE", "/api/roles/"+r.ID+"?revision=1", nil)
	if status != 200 {
		t.Fatal(status)
	}
	status, _ = call(t, s, a.HumanToken, "DELETE", "/api/connections/"+c.ID+"?revision=1", nil)
	if status != 200 {
		t.Fatal(status)
	}
	status, _ = call(t, s, a.HumanToken, "PUT", "/api/connections/secret", map[string]string{"id": "secret", "key": "not-for-database"})
	if status != 400 {
		t.Fatal("plaintext credential field accepted", status)
	}
}
