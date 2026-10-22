#!/usr/bin/env python3
"""Run pinned frontend tools with optional Codex local runtime discovery."""
import os
from pathlib import Path
import shutil
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
deps = Path.home() / ".cache/codex-runtimes/codex-primary-runtime/dependencies"
node = shutil.which("node") or str(deps / "node/bin/node")
pnpm = shutil.which("pnpm") or str(deps / "bin/fallback/pnpm")
if not Path(node).is_file() or not Path(pnpm).is_file():
    raise SystemExit("请安装 Node.js 24 和 pnpm 11.25+。")
env = dict(os.environ)
env["PATH"] = str(Path(node).parent) + os.pathsep + env.get("PATH", "")
env["ELECTRON_CACHE"] = str(root / "output/tool-cache/electron")
env["ELECTRON_BUILDER_CACHE"] = str(root / "output/tool-cache/electron-builder")
args = sys.argv[1:] or ["build:ui"]
if args[0] == "install":
    env["CI"] = "true"
    args += ["--store-dir", str(root / "output/tool-cache/pnpm-store")]
raise SystemExit(subprocess.call([pnpm, *args], cwd=root, env=env))
