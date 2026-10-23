package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/store"
	"agent-workbench/internal/workspace"
)

func TestNativeSessionReadOnlyHTTPAndProjectScope(t *testing.T) {
	a, s := local(t)
	human := store.Principal{ID: "human", Role: "human"}
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "fixture.txt"), []byte("synthetic snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	project := store.Project{ID: "native-project", Name: "Native records", Snapshot: source, Manifest: []byte("[]")}
	if _, err = a.Store.CreateProject(context.Background(), human, "native-project", project); err != nil {
		t.Fatal(err)
	}
	session, err := a.Native.Create(context.Background(), human, project.ID, store.NativeBinding{Runtime: sandbox.Podman, Model: "fixture", Spec: sandbox.Spec{ImageID: "sha256:" + strings.Repeat("a", 64), SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}}}}, "create")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + project.ID + "/native-sessions"
	status, raw := call(t, s, a.HumanToken, "GET", path, nil)
	if status != http.StatusOK || !strings.Contains(string(raw), session.ID) || !strings.Contains(string(raw), `"live_ready":false`) {
		t.Fatal(status, string(raw))
	}
	status, raw = call(t, s, a.HumanToken, "GET", path+"/"+session.ID, nil)
	var detail struct {
		Attempts []store.NativeAttempt `json:"attempts"`
	}
	if status != 200 || json.Unmarshal(raw, &detail) != nil || len(detail.Attempts) != 0 {
		t.Fatal(status, string(raw))
	}
	status, _ = call(t, s, a.HumanToken, "POST", path+"/"+session.ID+"/resume", map[string]string{"input": "must not run"})
	if status == 200 {
		t.Fatal("read-only API launched execution")
	}
	status, _ = call(t, s, a.HumanToken, "GET", "/api/projects/other/native-sessions/"+session.ID, nil)
	if status != 404 {
		t.Fatal(status)
	}
	a.mu.Lock()
	a.agents["native-agent"] = store.Principal{ID: "agent", Role: "agent", ProjectID: project.ID}
	a.mu.Unlock()
	for _, route := range []string{path, path + "/" + session.ID} {
		status, _ = call(t, s, "native-agent", "GET", route, nil)
		if status != 403 {
			t.Fatal("agent read native conversation", status)
		}
	}
}
