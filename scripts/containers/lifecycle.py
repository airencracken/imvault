#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Exercise the actual image, including a previous-release upgrade and restore."""
import argparse
import hashlib
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import uuid

from release import ROOT, config, run, stage, version


def download_previous(cfg, arch, directory):
    app, previous = cfg["app"], version(cfg["previous"])
    base = f"https://github.com/airencracken/{app}/releases/download/v{previous}"
    for name in (f"{app}_{previous}_linux_{arch}.tar.gz", f"{app}_{previous}_checksums.txt"):
        with urllib.request.urlopen(f"{base}/{name}", timeout=120) as response:
            (directory / name).write_bytes(response.read())
    target = directory / "previous"
    stage(directory, target, app, arch)
    return target / "bin" / app


class Lifecycle:
    def __init__(self, cfg, image):
        self.cfg, self.image = cfg, image
        self.prefix = cfg["app"].upper()
        self.token = "container-test-" + uuid.uuid4().hex[:12]
        self.volumes, self.containers = [], []

    def volume(self):
        name = self.token + "-" + str(len(self.volumes))
        run("docker", "volume", "create", name)
        self.volumes.append(name)
        return name

    def options(self, volume):
        return ["--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true",
                "--tmpfs", f"/tmp:size={self.cfg['tmp_mb']}m,mode=1777",
                "-v", f"{volume}:/data", "-e", f"{self.prefix}_BACKUP_INTERVAL=1h"]

    def once(self, volume, *args, binary=None, stdin=None, entrypoint=None, env=()):
        command = ["docker", "run", "--rm", "-i", *self.options(volume)]
        for value in env:
            command.extend(["-e", value])
        if binary:
            command.extend(["--mount", f"type=bind,src={binary},dst=/usr/local/bin/{self.cfg['app']},readonly"])
        if entrypoint:
            command.extend(["--entrypoint", entrypoint])
        return run(*command, self.image, *args, input=stdin)

    def start(self, volume, binary=None):
        name = self.token + "-" + str(len(self.containers))
        command = ["docker", "run", "-d", "--name", name, *self.options(volume)]
        if binary:
            command.extend(["--mount", f"type=bind,src={binary},dst=/usr/local/bin/{self.cfg['app']},readonly"])
        run(*command, self.image)
        self.containers.append(name)
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            state = json.loads(run("docker", "inspect", name))[0]["State"]
            if not state["Running"]:
                raise AssertionError(run("docker", "logs", name))
            probe = subprocess.run(["docker", "exec", name, "wget", "-qO-",
                                    "http://127.0.0.1:8080/healthz"], capture_output=True, timeout=10)
            if probe.returncode == 0 and probe.stdout.strip():
                break
            time.sleep(0.5)
        else:
            raise AssertionError("container never became healthy")
        return name

    def stop(self, name):
        run("docker", "stop", "--time", "20", name)
        state = json.loads(run("docker", "inspect", name))[0]["State"]
        assert state["ExitCode"] == 0, state
        assert not state["OOMKilled"], state

    def database(self, volume):
        # The service must be stopped before copying a SQLite file.
        result = subprocess.run(["docker", "run", "--rm", "-v", f"{volume}:/data",
                                 "--entrypoint", "cat", self.image,
                                 f"/data/{self.cfg['app']}.db"],
                                check=True, capture_output=True, timeout=30)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "database.db"
            path.write_bytes(result.stdout)
            with sqlite3.connect(path) as database:
                assert database.execute("PRAGMA integrity_check").fetchone() == ("ok",)
                assert database.execute("SELECT count(*) FROM users WHERE username = ?",
                                        ("containerowner",)).fetchone() == (1,)
        return result.stdout

    def snapshot(self, volume):
        return subprocess.run(["docker", "run", "--rm", "-v", f"{volume}:/data:ro",
                               "--entrypoint", "tar", self.image, "-C", "/data", "-cf", "-", "."],
                              check=True, capture_output=True, timeout=60).stdout

    def restore(self, snapshot, volume):
        # New named volumes inherit /data's 10001:10001 ownership from the image.
        subprocess.run(["docker", "run", "--rm", "-i", "-v", f"{volume}:/data",
                        "--entrypoint", "tar", self.image, "-C", "/data", "-xf", "-"],
                       input=snapshot, check=True, capture_output=True, timeout=60)

    def check(self, release, binary=None, previous=None):
        image = json.loads(run("docker", "image", "inspect", self.image))[0]
        assert image["Config"]["User"] in ("10001:10001", f"{self.cfg['app']}:{self.cfg['app']}")
        assert image["Config"]["Healthcheck"]["Test"]
        volume = self.volume()
        assert self.once(volume, "--version").split()[-1].lstrip("v") == release
        if binary:
            actual = subprocess.run(["docker", "run", "--rm", "--entrypoint", "cat",
                                     self.image, f"/usr/local/bin/{self.cfg['app']}"],
                                    check=True, capture_output=True, timeout=30).stdout
            assert hashlib.sha256(actual).digest() == hashlib.sha256(binary.read_bytes()).digest()
        self.once(volume, "-c", "test \"$(id -u)\" = 10001 && test \"$(stat -c %a /data)\" = 700 && command -v zstd && test -s /etc/ssl/certs/ca-certificates.crt && test -f /usr/share/zoneinfo/UTC",
                  entrypoint="sh")
        if self.cfg["app"] == "imvault":
            self.once(volume, "-c", "ffmpeg -version && ffprobe -version", entrypoint="sh")
        # Initialize with the prior release, then upgrade using the candidate.
        self.once(volume, self.cfg["owner_command"], "--username", "containerowner",
                  "--password-stdin", stdin="container-test-password-long-enough\n", binary=previous)
        if previous:
            old = self.start(volume, previous)
            self.stop(old)
        before_upgrade = self.snapshot(volume)
        running = self.start(volume)
        # Route existence and API contracts, without exposing ports to the host.
        for path in ("/healthz", "/login"):
            body = run("docker", "exec", running, "wget", "-qO-", f"http://127.0.0.1:8080{path}")
            assert body == "ok" if path == "/healthz" else "<form" in body
        assert run("docker", "exec", running, "stat", "-c", "%u:%g:%a", "/data") == "10001:10001:700"
        # Exercise the image's actual HEALTHCHECK, including a non-default port.
        health = image["Config"]["Healthcheck"]["Test"]
        assert health[0] == "CMD-SHELL"
        run("docker", "exec", running, "sh", "-c", health[1])
        # Automatic backups must work with the runtime compression tools.
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            files = run("docker", "exec", running, "find", "/data", "-name", "*.tar.zst")
            if files:
                break
            time.sleep(0.5)
        else:
            raise AssertionError("automatic compressed backup was not created")
        archive = files.splitlines()[0]
        run("docker", "exec", running, "zstd", "--test", archive)
        manifest = json.loads(run("docker", "exec", running, "cat", str(Path(archive).parent / "manifest.json")))
        assert manifest["app"] == self.cfg["app"] and manifest["version"] == 1
        digest = run("docker", "exec", running, "sha256sum", archive).split()[0]
        assert manifest["sha256"] == digest
        self.stop(running)
        self.database(volume)
        snapshot = self.snapshot(volume)
        restarted = self.start(volume)
        self.stop(restarted)
        self.database(volume)
        restored = self.volume()
        self.restore(snapshot, restored)
        restored_server = self.start(restored)
        self.stop(restored_server)
        self.database(restored)
        if previous:
            rollback = self.volume()
            self.restore(before_upgrade, rollback)
            rollback_server = self.start(rollback, previous)
            self.stop(rollback_server)
            self.database(rollback)
        # Reject malformed configuration before starting a listener. The existing
        # volume and credentials must still work after a failed attempt.
        for bad in ("not-an-ip", "127.0.0.1;touch /tmp/injected", "$(id)"):
            try:
                self.once(volume, env=(f"{self.prefix}_TRUSTED_PROXIES={bad}",))
            except subprocess.CalledProcessError:
                pass
            else:
                raise AssertionError("adversarial proxy configuration was accepted")
        assert self.database(volume)
        # The health command derives its port from the configured listen address.
        custom = self.token + "-custom"
        run("docker", "run", "-d", "--name", custom, *self.options(volume),
            "-e", f"{self.prefix}_ADDR=:9090", self.image)
        self.containers.append(custom)
        for _ in range(60):
            probe = subprocess.run(["docker", "exec", custom, "sh", "-c", health[1]], capture_output=True, timeout=10)
            if probe.returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise AssertionError("healthcheck does not follow configured port")
        self.stop(custom)

    def close(self):
        for name in self.containers:
            subprocess.run(["docker", "rm", "-f", name], capture_output=True, timeout=30)
        for name in self.volumes:
            subprocess.run(["docker", "volume", "rm", name], capture_output=True, timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--upgrade", action="store_true")
    args = parser.parse_args()
    cfg = config()
    test = Lifecycle(cfg, args.image)
    with tempfile.TemporaryDirectory() as temporary:
        try:
            arch = json.loads(run("docker", "image", "inspect", args.image))[0]["Architecture"]
            previous = download_previous(cfg, arch, Path(temporary)) if args.upgrade else None
            test.check(version(args.version), args.binary, previous)
            print(f"{cfg['app']} container lifecycle checks passed ({arch})")
        except Exception:
            for name in test.containers:
                print(run("docker", "logs", name))
            raise
        finally:
            test.close()


if __name__ == "__main__":
    main()
