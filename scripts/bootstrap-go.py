#!/usr/bin/env python3
"""Install the locked Go archive in the user's project tool cache, with SHA-256 verification."""
import argparse
import hashlib
import os
from pathlib import Path
import platform
import tarfile
import tempfile
import urllib.request

VERSION = "go1.27.3"
FILENAME = "go1.27.3.darwin-arm64.tar.gz"
DIGEST = "dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mirror", default="https://go.dev/dl", help="HTTPS archive directory; bytes still must match the official digest")
    args = parser.parse_args()
    if (platform.system(), platform.machine()) != ("Darwin", "arm64"):
        raise SystemExit("This bootstrap lock is for macOS arm64; install Go 1.27.3 for your platform.")
    if not args.mirror.startswith("https://"):
        raise SystemExit("HTTPS download source required")
    cache = Path.home() / ".cache" / "agent-workbench" / "tools"
    destination = cache / VERSION
    if (destination / "go" / "bin" / "go").is_file():
        print(destination / "go" / "bin" / "go")
        return
    if destination.exists():
        raise SystemExit("Existing incomplete tool directory preserved; inspect it before retrying.")
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / FILENAME
    if not archive.is_file() or hashlib.sha256(archive.read_bytes()).hexdigest() != DIGEST:
        with urllib.request.urlopen(args.mirror.rstrip("/") + "/" + FILENAME, timeout=30) as response, archive.open("wb") as output:
            shutil_copy(response, output)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != DIGEST:
        raise SystemExit("Go archive SHA-256 mismatch; archive was not executed.")
    with tempfile.TemporaryDirectory(dir=cache, prefix="go-extract-") as staging:
        with tarfile.open(archive, "r:gz") as source:
            for item in source.getmembers():
                if not (item.name == "go" and item.isdir() or item.name.startswith("go/")) or ".." in Path(item.name).parts or item.isdev():
                    raise SystemExit("Invalid archive member")
            source.extractall(staging)
        os.rename(staging, destination)
    print(destination / "go" / "bin" / "go")


def shutil_copy(source, target):
    while True:
        chunk = source.read(1024 * 1024)
        if not chunk:
            break
        target.write(chunk)


if __name__ == "__main__":
    main()
