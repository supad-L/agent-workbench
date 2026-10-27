package app

import (
	"agent-workbench/internal/store"
	"testing"
)

func TestEnvironmentDiagnosticAuthorityAndStrictInput(t *testing.T) {
	a, server := local(t)
	a.mu.Lock()
	a.agents["environment-agent"] = store.Principal{ID: "agent", Role: "agent"}
	a.mu.Unlock()
	for _, token := range []string{"", "environment-agent", a.HumanToken} {
		status, _ := call(t, server, token, "POST", "/api/native-environment/check", map[string]any{"provider": "codex", "command": []string{"must-not-execute"}})
		want := 400
		switch token {
		case "":
			want = 401
		case "environment-agent":
			want = 403
		}
		if status != want {
			t.Fatal(status, want)
		}
	}
}
