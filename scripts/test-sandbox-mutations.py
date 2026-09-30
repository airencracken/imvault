#!/usr/bin/env python3
"""Check that sandbox regressions detect policy and fail-open defects."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MUTATIONS = [
 ("media networking", "internal/sandbox/sandbox.go", '"--unshare-net", "--die-with-parent"', '"--die-with-parent"', "./internal/sandbox", "TestRealBubblewrapMediaBoundary"),
 ("environment filtering", "internal/sandbox/sandbox.go", 'strings.HasPrefix(key, s.Prefix) && key != s.Prefix+"DATA_DIR"', 'key != s.Prefix+"DATA_DIR"', "./internal/sandbox", "TestServicePolicyKeepsSecretsOutOfArguments"),
 ("broad filesystem mounts", "internal/sandbox/sandbox.go", 'name == "/" ||', 'false ||', "./internal/sandbox", "TestWritableDirectoriesRejectBroadAndAdversarialPaths"),
]
if (ROOT / "internal/media/sandbox.go").exists():
 MUTATIONS += [
  ("requested confinement", "internal/media/sandbox.go", "if enabled {", "if false && enabled {", "./internal/media", "TestRequestedSandboxCannotFallBack"),
  ("symlink output race", "internal/media/output_linux.go", "os.O_RDONLY|syscall.O_NOFOLLOW", "os.O_RDONLY|0*syscall.O_NOFOLLOW", "./internal/media", "TestSandboxOutputRejectsSymlinksAndDirectories"),
 ]

def check():
 env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local", COMFYWARE_SANDBOX_TEST="1")
 env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -buildvcs=false").strip()
 with tempfile.TemporaryDirectory(prefix="comfyware-sandbox-mutations-") as temporary:
  checkout = Path(temporary) / "source"
  shutil.copytree(ROOT, checkout, ignore=shutil.ignore_patterns(".git", "node_modules", "bin", "dist", ".release", "*.db", "*.db-wal", "*.db-shm", "__pycache__"))
  for name, file, before, after, package, regression in MUTATIONS:
   command = ["go", "test", package, "-count=1", "-run", "^"+regression+"$", "-timeout=30s"]
   baseline = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
   if baseline.returncode: raise RuntimeError("baseline failed:\n"+baseline.stdout+baseline.stderr)
   path = checkout / file
   original = path.read_text()
   if original.count(before) != 1: raise RuntimeError("mutation anchor changed: "+name)
   path.write_text(original.replace(before, after))
   try:
    result = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
    if result.returncode == 0 or "--- FAIL: "+regression not in result.stdout:
     raise RuntimeError("mutation was not caught: "+name+"\n"+result.stdout+result.stderr)
    print("PASS: regression rejects broken "+name, flush=True)
   finally: path.write_text(original)

if __name__ == "__main__": check()
