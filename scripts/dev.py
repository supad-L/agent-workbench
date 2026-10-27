#!/usr/bin/env python3
"""Repeatable M0 development commands; never reads model credentials."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
CACHE = Path.home() / ".cache" / "agent-workbench" / "tools"


def go_executable():
    candidates = [shutil.which("go"), str(CACHE / "go1.27.3" / "go" / "bin" / "go")]
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return candidate
    raise SystemExit("Go unavailable. Install Go 1.27.3 or run scripts/bootstrap-go.py.")


def run(command, env):
    print("+", " ".join(str(x) for x in command), flush=True)
    result = subprocess.run(command, cwd=ROOT, env=env)
    if result.returncode:
        raise SystemExit(result.returncode)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["test", "vet", "build", "doctor", "protocol", "contracts", "network", "sandbox", "lint", "verify"])
    parser.add_argument("--image", help="explicit local image for sandbox probe")
    parser.add_argument("--runtime", choices=["docker", "podman"], default="docker", help="explicit sandbox engine; no automatic fallback")
    parser.add_argument("--scratch-dir", help="existing fixture parent shared with the container engine")
    args = parser.parse_args()
    go = go_executable()
    env = dict(os.environ)
    env["PATH"] = str(Path(go).parent) + os.pathsep + env.get("PATH", "")
    env["GOTOOLCHAIN"] = "local"
    commands = {
        "test": [[go, "test", "-race", "-count=1", "-coverpkg=./...", "-coverprofile=coverage.out", "./..."]],
        "vet": [[go, "vet", "./..."]],
        "build": [[go, "build", "-o", "bin/agent-workbench", "./cmd/agent-workbench"]],
        "doctor": [[go, "run", "./cmd/agent-workbench", "doctor"]],
        "protocol": [[go, "run", "./cmd/agent-workbench", "protocol-probe"]],
        "contracts": [[go, "run", "./cmd/agent-workbench", "contracts-check"]],
        "network": [[go, "run", "./cmd/agent-workbench", "network-probe"]],
    }
    if args.command == "sandbox":
        if not args.image:
            parser.error("sandbox requires --image; no image is pulled automatically")
        command = [go, "run", "./cmd/agent-workbench", "sandbox-probe", "--image", args.image, "--runtime", args.runtime]
        if args.scratch_dir:
            command += ["--scratch-dir", args.scratch_dir]
        run(command, env)
    elif args.command == "lint":
        binary = shutil.which("golangci-lint") or str(CACHE / "bin" / "golangci-lint")
        if not Path(binary).is_file():
            raise SystemExit("Install golangci-lint v2.15.0 before running lint.")
        run([binary, "run", "./..."], env)
    elif args.command == "verify":
        gofmt = str(Path(go).parent / "gofmt")
        files = sorted(str(p.relative_to(ROOT)) for p in ROOT.rglob("*.go") if ".tools" not in p.parts)
        formatting = subprocess.run([gofmt, "-l", *files], cwd=ROOT, env=env, capture_output=True, text=True)
        if formatting.returncode or formatting.stdout.strip():
            raise SystemExit("gofmt verification failed:\n" + formatting.stdout + formatting.stderr)
        for group in ["vet", "test", "build", "contracts", "protocol", "network"]:
            for command in commands[group]:
                run(command, env)
    else:
        for command in commands[args.command]:
            run(command, env)

    if args.command in ["build", "verify"]:
        for arch in ["amd64", "arm64"]:
            helper_env = dict(env, GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
            run([go, "build", "-trimpath", "-ldflags=-s -w", "-o",
                 "bin/awb-network-probe-linux-" + arch, "./cmd/awb-network-probe"], helper_env)


if __name__ == "__main__":
    main()
