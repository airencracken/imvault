#!/usr/bin/env python3
# Synced from github.com/airencracken/comfylib v0.1.0 tools/mutate.py; do not edit here. scripts/mutate_sync_test.go holds it to that copy.
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Check that regression tests reject deliberate defects.

Each mutation table is a JSON list of objects:

    {"name": "...", "file": "repo/relative/path.go",
     "before": "text that occurs exactly once", "after": "replacement",
     "package": "./some/package", "run": "^TestRegexp$",
     "env": {"OPTIONAL": "variables"}}

For every entry the repository is copied to a temporary directory, the
mutation is applied to the copy, and `go test -run RUN PACKAGE` must fail
with a test failure. A mutation that still passes, or that breaks the build
instead of a test, is reported. The same test must pass on an unmutated copy
first, so a failure is known to come from the mutation. The real tree is
never modified.

Usage: python3 tools/mutate.py [--root DIR] [--jobs N] [--list] TABLE...
"""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

# Skipped at the top of the tree: VCS data, workspace files that would point
# the copy at other checkouts, and local data directories.
TOP_LEVEL_IGNORED = {".git", "go.work", "go.work.sum", "data", "bin", "dist", ".release"}
# Skipped at any depth.
IGNORED_PATTERNS = ("node_modules", "__pycache__", "*.db", "*.db-wal", "*.db-shm", "*.test")
FIELDS = {"name", "file", "before", "after", "package", "run", "env"}
REQUIRED = FIELDS - {"env"}
TEST_TIMEOUT = "120s"
PROCESS_TIMEOUT = 300


class TableError(Exception):
    """A mutation table is malformed or its anchors do not match the tree."""


def default_root():
    """Return the nearest directory at or above the working one with a go.mod."""
    here = Path.cwd().resolve()
    for directory in (here, *here.parents):
        if (directory / "go.mod").is_file():
            return directory
    raise TableError("no go.mod found; pass --root")


def check_entry(entry, where):
    """Return the problems with one table entry, as readable strings."""
    if not isinstance(entry, dict):
        return [where + ": an entry must be an object"]
    problems = []
    for key in sorted(set(entry) - FIELDS):
        problems.append(where + ": unknown field " + json.dumps(key))
    for key in sorted(REQUIRED - set(entry)):
        problems.append(where + ": missing field " + json.dumps(key))
    for key in sorted(REQUIRED & set(entry)):
        if not isinstance(entry[key], str):
            problems.append(where + ": " + key + " must be a string")
        elif key != "after" and not entry[key]:
            problems.append(where + ": " + key + " must not be empty")
    if problems:
        return problems
    if entry["before"] == entry["after"]:
        problems.append(where + ": before and after are the same")
    file = Path(entry["file"])
    if file.is_absolute() or ".." in file.parts:
        problems.append(where + ": file must be relative to the repository")
    if not entry["package"].startswith("./") or any(c.isspace() for c in entry["package"]):
        problems.append(where + ": package must be a ./relative Go package path")
    env = entry.get("env", {})
    if not isinstance(env, dict) or not all(isinstance(k, str) and k and isinstance(v, str) for k, v in env.items()):
        problems.append(where + ": env must map variable names to strings")
    return problems


def load_tables(paths, root):
    """Read and check every table. Raise TableError listing all problems."""
    entries = []
    problems = []
    names = {}
    for path in paths:
        try:
            table = json.loads(Path(path).read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            problems.append(str(path) + ": " + str(error))
            continue
        if not isinstance(table, list) or not table:
            problems.append(str(path) + ": a table must be a non-empty list")
            continue
        for index, entry in enumerate(table):
            where = "{}[{}]".format(path, index)
            found = check_entry(entry, where)
            if found:
                problems.extend(found)
                continue
            where = "{} ({})".format(where, entry["name"])
            if entry["name"] in names:
                problems.append(where + ": name already used at " + names[entry["name"]])
            names[entry["name"]] = where
            target = root / entry["file"]
            try:
                count = target.read_text(encoding="utf-8").count(entry["before"])
            except OSError as error:
                problems.append(where + ": " + str(error))
                continue
            if count != 1:
                problems.append("{}: anchor occurs {} times in {}, want exactly once".format(where, count, entry["file"]))
                continue
            entries.append(entry)
    if problems:
        raise TableError("\n".join(problems))
    return entries


def ignore(root):
    """Return a copytree ignore function for a tree rooted at root."""
    patterns = shutil.ignore_patterns(*IGNORED_PATTERNS)

    def ignored(directory, names):
        skipped = set(patterns(directory, names))
        if Path(directory).resolve() == root:
            skipped |= TOP_LEVEL_IGNORED & set(names)
        return skipped

    return ignored


def copy_tree(root, destination):
    """Copy the repository at root to destination, leaving out ignored paths."""
    shutil.copytree(root, destination, ignore=ignore(root.resolve()), symlinks=True)


def go_env(entry):
    env = dict(os.environ)
    env.update(GOWORK="off", GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -mod=mod -buildvcs=false").strip()
    env.update(entry.get("env", {}))
    return env


def go_test(checkout, entry):
    command = ["go", "test", "-count=1", "-timeout=" + TEST_TIMEOUT, "-run", entry["run"], entry["package"]]
    try:
        return subprocess.run(command, cwd=checkout, env=go_env(entry), text=True,
                              capture_output=True, timeout=PROCESS_TIMEOUT, check=False)
    except subprocess.TimeoutExpired as error:
        return subprocess.CompletedProcess(command, -1, error.stdout or "", "go test did not finish")


def verdict(result):
    """Classify a go test result as passed, failed (a test failed) or broken."""
    output = result.stdout + result.stderr
    if result.returncode == 0:
        return "passed"
    if "--- FAIL:" in output or "panic: test timed out" in output:
        return "failed"
    return "broken"


def run_entry(root, entry):
    """Return (ok, message) for one mutation, tested in its own copy."""
    with tempfile.TemporaryDirectory(prefix="mutate-") as temporary:
        checkout = Path(temporary) / "source"
        copy_tree(root, checkout)
        baseline = go_test(checkout, entry)
        if verdict(baseline) != "passed":
            return False, "BASELINE FAILED: {}\n{}{}".format(entry["name"], baseline.stdout, baseline.stderr)
        if "no tests to run" in baseline.stdout:
            return False, "NO TESTS: {} selects nothing with -run {}".format(entry["name"], entry["run"])
        path = checkout / entry["file"]
        source = path.read_text(encoding="utf-8")
        path.write_text(source.replace(entry["before"], entry["after"], 1), encoding="utf-8")
        result = go_test(checkout, entry)
    outcome = verdict(result)
    if outcome == "failed":
        return True, "PASS: regression rejects broken " + entry["name"]
    if outcome == "broken":
        return False, "BROKEN: {} does not build, so it proves nothing\n{}{}".format(entry["name"], result.stdout, result.stderr)
    return False, "SURVIVED: {} passed {} -run {}".format(entry["name"], entry["package"], entry["run"])


def main(argv=None):
    parser = argparse.ArgumentParser(description="Check that regression tests reject deliberate defects.")
    parser.add_argument("tables", nargs="+", help="JSON mutation tables")
    parser.add_argument("--root", type=Path, help="repository root (default: nearest go.mod)")
    parser.add_argument("--jobs", type=int, default=min(4, os.cpu_count() or 1), help="mutations tested at once")
    parser.add_argument("--list", action="store_true", help="list the mutations without running them")
    args = parser.parse_args(argv)
    try:
        root = (args.root or default_root()).resolve()
        entries = load_tables(args.tables, root)
    except TableError as error:
        print(error, file=sys.stderr)
        return 2
    if args.list:
        for entry in entries:
            print("{}: {} -run {} ({})".format(entry["name"], entry["package"], entry["run"], entry["file"]))
        return 0
    failures = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, args.jobs)) as pool:
        for ok, message in pool.map(lambda entry: run_entry(root, entry), entries):
            print(message, flush=True)
            failures += not ok
    if failures:
        print("{} of {} mutations were not caught".format(failures, len(entries)), file=sys.stderr)
        return 1
    print("all {} mutations were caught".format(len(entries)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
