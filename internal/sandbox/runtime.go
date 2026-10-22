package sandbox

import (
	"context"
	"errors"
	"os/exec"
)

// RuntimeKind selects one engine explicitly, without cross-engine fallback.
type RuntimeKind string

const (
	Docker RuntimeKind = "docker"
	Podman RuntimeKind = "podman"
)

// ProbeOptions selects the engine and a private fixture parent shared with its VM.
// An empty scratch directory uses the operating system temporary directory.
type ProbeOptions struct {
	Runtime    RuntimeKind
	Image      string
	ScratchDir string
}

// ProbeRuntime preserves Docker support and independently verifies Podman.
// It never starts an engine, pulls an image, or falls back to another engine/host.
func ProbeRuntime(ctx context.Context, options ProbeOptions) (Report, error) {
	kind := options.Runtime
	if kind == "" {
		kind = Docker
	}
	blocked := func(detail string) (Report, error) {
		return Report{Status: "blocked", Evidence: "not_run", Detail: detail, Runtime: kind}, errors.New(detail)
	}
	if kind != Docker && kind != Podman {
		return blocked("unsupported runtime; select docker or podman")
	}
	if options.ScratchDir != "" {
		if err := realDirectory(options.ScratchDir); err != nil {
			return blocked("scratch directory must be an existing canonical absolute directory shared with the selected engine")
		}
	}
	executable, err := exec.LookPath(string(kind))
	if err != nil {
		return blocked("selected container CLI is unavailable; engine and host fallback are disabled")
	}
	report, err := probeWithDriverAt(ctx, options.Image, nativeRuntime{executable: executable, kind: kind}, options.ScratchDir)
	report.Runtime = kind
	return report, err
}
