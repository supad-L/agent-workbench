#!/usr/bin/env python3
"""Check packaged Electron startup using its own renderer/preload/service handshake."""
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    executable = ROOT / "output/desktop/mac-arm64/Agent Workbench.app/Contents/MacOS/Agent Workbench"
    if not executable.is_file():
        raise SystemExit("先运行 python3 scripts/frontend.py package:mac")
    with tempfile.TemporaryDirectory(prefix="desktop-smoke-", dir=ROOT / "output") as temporary:
        result = subprocess.run(
            [str(executable), "--smoke-check", "--data-dir=" + temporary],
            capture_output=True, text=True, timeout=30, cwd=ROOT,
        )
        reports = []
        for line in result.stdout.splitlines():
            try:
                report = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(report, dict) and report.get("check") == "desktop_startup":
                reports.append(report)
        if result.returncode != 0 or len(reports) != 1 or reports[0].get("status") != "passed":
            raise SystemExit("桌面启动检查失败：" + result.stderr[-2000:])
        # Reopening the same state proves the quitting app released SQLite's lock.
        restart = subprocess.run(
            [str(executable), "--smoke-check", "--data-dir=" + temporary],
            capture_output=True, text=True, timeout=30, cwd=ROOT,
        )
        if restart.returncode != 0 or '"status":"passed"' not in restart.stdout:
            raise SystemExit("桌面重启检查失败：" + restart.stderr[-2000:])
        reports[0]["restart_and_instance_lock"] = "passed"
        output = ROOT / "output/verification/desktop-startup.json"
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(reports[0], ensure_ascii=False, indent=2) + "\n")
        print(output.read_text())


if __name__ == "__main__":
    main()
