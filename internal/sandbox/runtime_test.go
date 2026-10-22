package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitRuntimeNeverFallsBack(t *testing.T) {
	for _, kind := range []RuntimeKind{Docker, Podman} {
		t.Run(string(kind), func(t *testing.T) {
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			marker := filepath.Join(bin, "invoked-engine")
			for _, name := range []RuntimeKind{Docker, Podman} {
				script := "#!/bin/sh\nprintf '" + string(name) + "' >> '" + marker + "'\nexit 1\n"
				if err := os.WriteFile(filepath.Join(bin, string(name)), []byte(script), 0755); err != nil {
					t.Fatal(err)
				}
			}
			report, err := ProbeRuntime(context.Background(), ProbeOptions{Runtime: kind, Image: "fixture"})
			if err == nil || report.Status != "blocked" || report.Runtime != kind || report.Evidence != "not_run" {
				t.Fatalf("unavailable engine accepted: %+v %v", report, err)
			}
			called, err := os.ReadFile(marker)
			if err != nil || string(called) != string(kind) {
				t.Fatalf("another engine invoked after failure: %q %v", called, err)
			}
			if err := os.Remove(filepath.Join(bin, string(kind))); err != nil {
				t.Fatal(err)
			}
			if _, err := ProbeRuntime(context.Background(), ProbeOptions{Runtime: kind, Image: "fixture"}); err == nil {
				t.Fatal("missing selected engine accepted")
			}
			called, err = os.ReadFile(marker)
			if err != nil || string(called) != string(kind) {
				t.Fatal("missing selected engine fell back to installed alternative")
			}
		})
	}
}

func TestRuntimeOptionsFailClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, options := range []ProbeOptions{{Runtime: "auto"}, {Runtime: "../podman"}, {Runtime: Podman, ScratchDir: "relative/path"}, {Runtime: Podman, ScratchDir: filepath.Join(t.TempDir(), "missing")}} {
		report, err := ProbeRuntime(context.Background(), options)
		if err == nil || report.Status != "blocked" || report.Evidence != "not_run" {
			t.Fatalf("invalid runtime options accepted: %+v %v", report, err)
		}
	}
	report, err := ProbeRuntime(context.Background(), ProbeOptions{})
	if err == nil || report.Runtime != Docker {
		t.Fatal("legacy default changed from Docker")
	}
	for _, kind := range []RuntimeKind{Docker, Podman} {
		if evidence := (nativeRuntime{kind: kind}).evidence(); evidence != "real_"+string(kind) {
			t.Fatalf("runtime evidence mislabeled: %s", evidence)
		}
	}
}
