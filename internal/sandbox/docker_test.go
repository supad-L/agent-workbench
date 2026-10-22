package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-workbench/internal/workspace"
)

func TestArgumentsFailClosedAndMatchDeclaredScope(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writable, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Name: "awb-probe-test", ImageID: "sha256:" + strings.Repeat("a", 64), SourceRoot: source, Scope: workspace.Scope{ReadPaths: []string{"."}, WriteDirs: []string{"src/"}}, Writable: map[string]string{"src/": writable}, Command: []string{"true"}}
	args, err := Arguments(spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, required := range []string{"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--user=1000:1000", "target=/workspace,readonly", "target=/workspace/src"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %s", required)
		}
	}
	spec.ImageID = "alpine:latest"
	if _, err := Arguments(spec); err == nil {
		t.Fatal("mutable image accepted")
	}
	spec.ImageID = "sha256:" + strings.Repeat("a", 64)
	spec.Writable["src/"] = source
	if _, err := Arguments(spec); err == nil {
		t.Fatal("write alias of read-only source accepted")
	}
	spec.Writable["src/"] = writable
	spec.Writable["other/"] = source
	if _, err := Arguments(spec); err == nil {
		t.Fatal("undeclared mount accepted")
	}
	delete(spec.Writable, "other/")
	link := filepath.Join(source, "linked")
	if err := os.Symlink(writable, link); err != nil {
		t.Fatal(err)
	}
	spec.Writable["src/"] = link
	if _, err := Arguments(spec); err == nil {
		t.Fatal("symlink mount accepted")
	}
}

func TestMissingDockerDoesNotExecuteHostCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	report, err := Probe(context.Background(), "fixture")
	if err == nil || report.Status != "blocked" || report.Evidence != "not_run" {
		t.Fatalf("unexpected report: %+v %v", report, err)
	}
}
