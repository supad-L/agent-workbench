package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectionNeverAuthenticatesAvailableCLIs(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	marker := filepath.Join(bin, "unexpected-login")
	for _, name := range []string{"claude", "codex"} {
		script := "#!/bin/sh\nprintf unexpected > '" + marker + "'\nexit 42\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"docker", "podman"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf 'synthetic-version\\n'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	report := Inspect(context.Background())
	if len(report.Checks) != 6 {
		t.Fatalf("incomplete report: %+v", report)
	}
	if report.Checks[0].Status != "available_unverified" || report.Checks[1].Status != "available_unverified" || report.Checks[2].Status != "unverified" || report.Checks[3].Status != "unverified" {
		t.Fatal("executable presence confused with real support")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("inspection executed an account CLI")
	}
}

func TestPodmanIsIndependentOfDockerAvailability(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nprintf 'synthetic-version\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	report := Inspect(context.Background())
	if report.Checks[0].Name != "docker" || report.Checks[0].Status != "blocked" || report.Checks[1].Name != "podman" || report.Checks[1].Status != "available_unverified" {
		t.Fatalf("engines conflated: %+v", report)
	}
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if report := Inspect(context.Background()); report.Checks[1].Status != "blocked" {
		t.Fatal("empty server version accepted")
	}
}

func TestMissingOrUnavailableDaemonIsBlocked(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if report := Inspect(context.Background()); report.Checks[0].Status != "blocked" {
		t.Fatal("missing Docker accepted")
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if report := Inspect(context.Background()); report.Checks[0].Status != "blocked" {
		t.Fatal("failed Docker daemon accepted")
	}
}
