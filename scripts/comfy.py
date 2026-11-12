#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Run maintenance tools from this application's pinned Comfylib module."""
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
env = os.environ | {"GOWORK": "off", "COMFYWARE_APP_ROOT": str(ROOT)}
resolved = subprocess.run(["go", "list", "-m", "-f", "{{.Dir}}", "github.com/airencracken/comfylib"],
                          cwd=ROOT, env=env, text=True, capture_output=True, check=True)
entry = Path(resolved.stdout.strip()) / "tools/app.py"
if not entry.is_file():
    raise SystemExit("Pinned Comfylib has no app tooling; run go mod download and check go.mod")
raise SystemExit(subprocess.call([sys.executable, str(entry), *sys.argv[1:]], cwd=ROOT, env=env))
