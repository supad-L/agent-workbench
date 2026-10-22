// Package doctor reports infrastructure readiness without reading account credentials.
package doctor

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Capability reports what was actually observed, not inferred product support.
type Capability struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report lists the checks that still prevent real account and sandbox acceptance.
type Report struct {
	Stage     string       `json:"stage"`
	OS        string       `json:"os"`
	Arch      string       `json:"arch"`
	GoVersion string       `json:"go_version"`
	Checks    []Capability `json:"checks"`
}

// Inspect checks both container engines, never login files or public-IP sites.
func Inspect(ctx context.Context) Report {
	report := Report{Stage: "M0 prototype", OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version()}
	for _, name := range []string{"docker", "podman"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			report.Checks = append(report.Checks, Capability{name, "blocked", "CLI unavailable; choose another runtime explicitly; no host fallback"})
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		version, err := exec.CommandContext(probeCtx, executable, "version", "--format", "{{.Server.Version}}").Output()
		cancel()
		if err != nil || strings.TrimSpace(string(version)) == "" {
			report.Checks = append(report.Checks, Capability{name, "blocked", "container engine unavailable or access denied"})
		} else {
			report.Checks = append(report.Checks, Capability{name, "available_unverified", "engine responds; run the explicit image isolation probe for this runtime"})
		}
	}
	for _, name := range []string{"codex", "claude"} {
		status, detail := "blocked", "CLI executable unavailable; login and model access not tested"
		if _, err := exec.LookPath(name); err == nil {
			status, detail = "unverified", "CLI executable found; credentials were not read and no request was sent"
		}
		report.Checks = append(report.Checks, Capability{name, status, detail})
	}
	report.Checks = append(report.Checks, Capability{"network_enforcement", "blocked", "dedicated proxy kill switch and login browser have not been verified"}, Capability{"real_api", "unverified", "no real credentials configured or consumed by this inspection"})
	return report
}
