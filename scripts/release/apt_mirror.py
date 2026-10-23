#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Use Ubuntu's standard HTTPS mirror on disposable GitHub runners.

Azure's archive mirror can transfer large ffmpeg dependencies at only tens of
KB/s. Handle both direct source URIs and the runner's mirror+file lists without
changing suites, components, signing keys, architectures, or unrelated sources.
"""
import argparse
from pathlib import Path
import re

AZURE = re.compile(r"https?://azure\.archive\.ubuntu\.com/ubuntu(?=[/\s]|$)")


def configure(root):
    files = [root / "sources.list", *sorted(root.glob("apt-mirrors*.txt"))]
    for pattern in ("*.list", "*.sources"):
        files.extend(sorted((root / "sources.list.d").glob(pattern)))
    changes = []
    for path in files:
        if path.is_symlink():
            raise ValueError(f"refusing a symlinked APT configuration: {path}")
        if not path.is_file():
            continue
        original = path.read_text()
        replacement = AZURE.sub("https://archive.ubuntu.com/ubuntu", original)
        if replacement != original:
            changes.append((path, replacement))
    for path, content in changes:
        path.write_text(content)
    return [path.name for path, _ in changes]


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("/etc/apt"))
    args = parser.parse_args()
    for name in configure(args.root):
        print(f"Selected Ubuntu HTTPS mirror in {name}")
