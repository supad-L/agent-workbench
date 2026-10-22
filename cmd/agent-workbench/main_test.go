package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDevelopmentCommands(t *testing.T) {
	for _, command := range []string{"contracts-check", "protocol-probe", "network-probe", "doctor"} {
		t.Run(command, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), []string{command}, &out); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(out.Bytes()) {
				t.Fatal("command did not return valid JSON")
			}
		})
	}
	for _, args := range [][]string{nil, {"serve"}, {"doctor", "unexpected"}, {"sandbox-probe", "--unknown"}, {"sandbox-probe", "extra"}} {
		var out bytes.Buffer
		if run(context.Background(), args, &out) == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
}

func TestMissingSandboxIsReportedAsBlocked(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, runtimeKind := range []string{"docker", "podman"} {
		var out bytes.Buffer
		if run(context.Background(), []string{"sandbox-probe", "--image", "fixture", "--runtime", runtimeKind}, &out) == nil {
			t.Fatal("missing infrastructure returned success")
		}
		if !strings.Contains(out.String(), `"status": "blocked"`) || !strings.Contains(out.String(), `"runtime": "`+runtimeKind+`"`) {
			t.Fatal("selected runtime blocked report missing")
		}
	}
}
