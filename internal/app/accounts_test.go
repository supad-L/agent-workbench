package app

import (
	"encoding/json"
	"testing"

	"agent-workbench/internal/store"
)

func TestNativeAccountControlIsHumanOnlyAndFenced(t *testing.T) {
	a, server := local(t)
	a.mu.Lock()
	a.agents["account-agent"] = store.Principal{ID: "agent", Role: "agent"}
	a.mu.Unlock()
	for _, route := range []struct{ method, path string }{{"GET", "/api/native-account"}, {"POST", "/api/native-account/auth/begin"}, {"POST", "/api/native-account/auth/finish"}} {
		status, _ := call(t, server, "account-agent", route.method, route.path, map[string]any{})
		if status != 403 {
			t.Fatal(route, status)
		}
	}
	status, _ := call(t, server, a.HumanToken, "POST", "/api/native-account/auth/begin", map[string]any{"kind": "check", "access_token": "MUST_NOT_ACCEPT"})
	if status != 400 {
		t.Fatal("accepted credentials", status)
	}
	status, raw := call(t, server, a.HumanToken, "POST", "/api/native-account/auth/begin", map[string]any{"kind": "check"})
	var receipt struct {
		Operation string `json:"operation"`
	}
	if status != 200 || json.Unmarshal(raw, &receipt) != nil || receipt.Operation == "" {
		t.Fatal(status, string(raw))
	}
	status, _ = call(t, server, a.HumanToken, "POST", "/api/native-account/auth/begin", map[string]any{"kind": "refresh"})
	if status != 409 {
		t.Fatal("parallel auth was not a conflict", status)
	}
	status, _ = call(t, server, a.HumanToken, "POST", "/api/native-account/auth/finish", map[string]any{"operation": "wrong", "healthy": true})
	if status != 409 {
		t.Fatal("stale owner accepted")
	}
	status, _ = call(t, server, a.HumanToken, "POST", "/api/native-account/auth/finish", map[string]any{"operation": receipt.Operation, "healthy": true, "authenticated": false})
	if status != 200 || a.Accounts.View().State != "unverified" {
		t.Fatal(status, a.Accounts.View())
	}
	status, _ = call(t, server, a.HumanToken, "GET", "/api/native-account", nil)
	if status != 200 {
		t.Fatal(status)
	}
}
