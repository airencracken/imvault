# SPDX-License-Identifier: AGPL-3.0-or-later
import hashlib
import io
import json
import os
from pathlib import Path
import random
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import release
import lifecycle


class ArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.dist = self.root / "dist"
        self.dist.mkdir()
        self.output = self.root / "context"
        self.app = release.config()["app"]
        self.prefix = f"{self.app}_1.2.3_linux_amd64"
        self.archive = self.dist / f"{self.prefix}.tar.gz"

    def archive_files(self, change=None):
        elf = bytearray(64)
        elf[:6] = b"\x7fELF\x02\x01"
        elf[18:20] = (62).to_bytes(2, "little")
        files = [(f"{self.prefix}/{name}", content) for name, content in (
            (self.app, bytes(elf)), ("LICENSE", b"license"), ("README.md", b"readme"),
            ("THIRD_PARTY_NOTICES.txt", b"notices"))]
        return change(files) if change else files

    def write_archive(self, files):
        with tarfile.open(self.archive, "w:gz") as archive:
            for name, content in files:
                member = tarfile.TarInfo(name)
                if content is None:
                    member.type, member.linkname = tarfile.SYMTYPE, "/tmp/escape"
                    archive.addfile(member)
                else:
                    member.size = len(content)
                    archive.addfile(member, io.BytesIO(content))
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        (self.dist / f"{self.app}_1.2.3_checksums.txt").write_text(f"{digest}  {self.archive.name}\n")

    def test_verified_archive_stages_only_required_files(self):
        self.write_archive(self.archive_files(lambda files: files + [(f"{self.prefix}/docs/example.md", b"extra")]))
        self.assertEqual(release.stage(self.dist, self.output, self.app, "amd64"), "1.2.3")
        self.assertEqual((self.output / "bin" / self.app).stat().st_mode & 0o777, 0o755)
        self.assertEqual((self.output / "docs/THIRD_PARTY_NOTICES.txt").read_bytes(), b"notices")
        self.assertFalse((self.output / "docs/example.md").exists())

    def test_archive_mutations_fail_atomically(self):
        mutations = {
            "missing license": lambda f: [x for x in f if not x[0].endswith("/LICENSE")],
            "empty notices": lambda f: [(n, b"" if n.endswith("NOTICES.txt") else c) for n, c in f],
            "wrong architecture": lambda f: [(n, b"x" * 64 if n.endswith("/" + self.app) else c) for n, c in f],
            "duplicate binary": lambda f: f + [f[0]],
            "traversal": lambda f: f + [(f"{self.prefix}/../escape", b"bad")],
            "absolute path": lambda f: f + [("/tmp/escape", b"bad")],
            "backslash": lambda f: f + [(f"{self.prefix}/..\\escape", b"bad")],
            "symlink": lambda f: f + [(f"{self.prefix}/link", None)],
            "wrong root": lambda f: f + [("another/root", b"bad")],
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                self.write_archive(self.archive_files(mutate))
                with self.assertRaises(ValueError):
                    release.stage(self.dist, self.output, self.app, "amd64")
                self.assertFalse(self.output.exists(), "invalid archive exposed partial output")
                self.assertEqual(set(self.root.iterdir()), {self.dist})

    def test_checksum_mutations_and_ambiguous_inputs_are_rejected(self):
        self.write_archive(self.archive_files())
        manifest = self.dist / f"{self.app}_1.2.3_checksums.txt"
        original = manifest.read_text()
        for text in (original.replace(original[:64], "0" * 64), original + original, original.replace(original[:64], "bad")):
            manifest.write_text(text)
            with self.assertRaises(ValueError):
                release.stage(self.dist, self.output, self.app, "amd64")
            self.assertFalse(self.output.exists())
        manifest.write_text(original)
        (self.dist / f"{self.app}_2.0.0_linux_amd64.tar.gz").touch()
        with self.assertRaises(ValueError):
            release.stage(self.dist, self.output, self.app, "amd64")

    def test_existing_context_is_never_replaced(self):
        self.write_archive(self.archive_files())
        self.output.mkdir()
        marker = self.output / "keep"
        marker.write_text("original")
        with self.assertRaises(ValueError):
            release.stage(self.dist, self.output, self.app, "amd64")
        self.assertEqual(marker.read_text(), "original")


class ConfigurationTests(unittest.TestCase):
    def test_schema_rejects_unknown_fields_types_and_adversarial_values(self):
        original = release.config()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            dest = root / "scripts/containers/config.json"
            dest.parent.mkdir(parents=True)
            for key, value in (("app", "../../escape"), ("port", True), ("port", 65536),
                               ("tmp_mb", -1), ("previous", "$(id)"), ("owner_command", "sh"), ("extra", 1)):
                candidate = dict(original, **{key: value})
                dest.write_text(json.dumps(candidate))
                with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                    release.config(root)

    def test_version_properties_and_adversarial_inputs(self):
        rng = random.Random(2048)
        for _ in range(150):
            value = ".".join(str(rng.randrange(10000)) for _ in range(3))
            self.assertEqual(release.version("v" + value), value)
            self.assertEqual(release.version(value), value)
        for value in ("latest", "main", "1", "1.2", "01.2.3", "v1.2.3\n", "1.2.3;id", "$(id)", "../../tag", "1.2.3 bad"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                release.version(value)

    @unittest.skipUnless(shutil.which("docker"), "Docker CLI is required to validate Compose")
    def test_compose_schema_and_runtime_contract(self):
        cfg = release.config()
        env = {k: v for k, v in os.environ.items() if not k.startswith(cfg["app"].upper() + "_") and k not in ("PORT", "VERSION", "COMPOSE_FILE", "COMPOSE_PROFILES")}
        raw = subprocess.run(["docker", "compose", "-f", str(release.ROOT / "docker-compose.yml"),
                              "config", "--format", "json"], env=env, capture_output=True, text=True, check=True).stdout
        stack = json.loads(raw)
        service = stack["services"][cfg["app"]]
        self.assertNotIn("build", service)
        self.assertRegex(service["image"], rf"^ghcr.io/airencracken/{cfg['app']}:v[0-9]+\.[0-9]+\.[0-9]+$")
        self.assertTrue(service["read_only"])
        self.assertIn("ALL", service["cap_drop"])
        self.assertIn("no-new-privileges:true", service["security_opt"])
        self.assertEqual(service["ports"][0]["host_ip"], "127.0.0.1")
        self.assertEqual(service["ports"][0]["target"], 8080)
        self.assertTrue(any(v["target"] == "/data" and v["type"] == "volume" for v in service["volumes"]))
        self.assertTrue(service["tmpfs"])
        override = json.loads(subprocess.run(["docker", "compose", "-f", str(release.ROOT / "docker-compose.yml"),
                             "-f", str(release.ROOT / "docker-compose.build.yml"), "config", "--format", "json"],
                            env=env, check=True, capture_output=True, text=True).stdout)
        local = override["services"][cfg["app"]]
        self.assertEqual(local["pull_policy"], "build")
        self.assertEqual(local["image"], cfg["app"] + ":local")

    def test_healthcheck_shell_uses_the_port_without_executing_configuration(self):
        cfg = release.config()
        for filename in ("Dockerfile", "Dockerfile.release"):
            text = (release.ROOT / filename).read_text()
            command = next(line.strip()[4:] for line in text.splitlines() if line.strip().startswith('CMD port='))
            for address, port, status in ((":8080", "8080", 0), ("[::]:9090", "9090", 0),
                                          ("", "8080", 0), ("$(touch injected):9999", "9999", 0),
                                          ("`touch injected`;:8080", "8080", 0), (":8080", "8080", 9)):
                with self.subTest(filename=filename, address=address, status=status), tempfile.TemporaryDirectory() as temporary:
                    directory = Path(temporary)
                    stub = directory / 'wget'
                    stub.write_text('#!/bin/sh\nfor arg; do last=$arg; done\nprintf "%s" "$last" > probe\nexit ' + str(status) + '\n')
                    stub.chmod(0o755)
                    result = subprocess.run(['sh', '-c', command], cwd=directory, env={
                        'PATH': temporary + ':/usr/bin:/bin', cfg['app'].upper() + '_ADDR': address},
                        capture_output=True, timeout=5)
                    self.assertEqual(result.returncode, 0 if status == 0 else 1)
                    self.assertEqual((directory / 'probe').read_text(), f'http://127.0.0.1:{port}/healthz')
                    self.assertFalse((directory / 'injected').exists())

    def test_dockerfiles_and_release_workflow_contract(self):
        cfg = release.config()
        for name in ("Dockerfile", "Dockerfile.release"):
            dockerfile = (release.ROOT / name).read_text()
            self.assertRegex(dockerfile, r"FROM alpine:3\.23@sha256:[a-f0-9]{64}")
            for required in ("USER 10001:10001", "ca-certificates tzdata zstd", "chmod 0700 /data",
                             "HEALTHCHECK", cfg["app"].upper() + "_ADDR##*:", "org.opencontainers.image.source"):
                self.assertIn(required, dockerfile)
        workflow = (release.ROOT / ".github/workflows/containers.yml").read_text()
        for required in ("arch: amd64", "arch: arm64", "ubuntu-24.04-arm", "needs: images",
                         "if: inputs.publish", "packages: write", "container-publication", "release.py build", "release.py publish"):
            self.assertIn(required, workflow)
        parent = (release.ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("./.github/workflows/containers.yml", parent)
        self.assertIn(f"github.repository == 'airencracken/{cfg['app']}'", parent)
        self.assertRegex(parent, r"needs: \[build, (?:packages, )?containers\]")
        self.assertIn("docs/containers.md", (release.ROOT / "README.md").read_text())


class BackupTests(unittest.TestCase):
    def test_only_completed_snapshots_are_used_for_restore_checks(self):
        app = release.config()["app"]
        complete = f"/data/backups/{app}-20261022T000000Z/snapshot.tar.zst"
        paths = [f"/data/backups/.{app}-autobackup-stage-abcd/snapshot.tar.zst",
                 "/data/backups/other-20261022/snapshot.tar.zst", complete,
                 f"/data/{app}-20261022/snapshot.tar.zst", f"/data/backups/{app}-20261022/manifest.json"]
        self.assertEqual(lifecycle.completed_backups('\n'.join(paths), app), [complete])


class SavedImageTests(unittest.TestCase):
    def test_saved_config_identity_is_independent_of_docker_storage_backend(self):
        image = 'example:tested-amd64'
        content = b'{"architecture":"amd64","os":"linux","rootfs":{"diff_ids":["sha256:123"]}}'
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'image.tar'
            for config_name in ('config.json', 'blobs/sha256/configdigest'):
                for manifests in ([dict(RepoTags=[image], Config=config_name)],
                                  [dict(RepoTags=['other:image'], Config=config_name)],
                                  [dict(RepoTags=[image], Config=config_name)] * 2):
                    with tarfile.open(path, 'w') as archive:
                        for name, data in (('manifest.json', json.dumps(manifests).encode()), (config_name, content)):
                            member = tarfile.TarInfo(name)
                            member.size = len(data)
                            archive.addfile(member, io.BytesIO(data))
                    if len(manifests) == 1 and manifests[0]['RepoTags'] == [image]:
                        self.assertEqual(release.saved_config(path, image), 'sha256:' + hashlib.sha256(content).hexdigest())
                    else:
                        with self.assertRaises(ValueError):
                            release.saved_config(path, image)


class PublishTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.path = Path(self.temporary.name)
        self.cfg = release.config()
        self.app = self.cfg["app"]
        self.revision = "a" * 40
        self.ids = {"amd64": "sha256:" + "1" * 64, "arm64": "sha256:" + "2" * 64}
        for arch in release.ARCHES:
            item = dict(app=self.app, arch=arch, version="1.2.3", revision=self.revision,
                        image=f"{self.app}:tested-{arch}", id=self.ids[arch])
            (self.path / f"image-{arch}.json").write_text(json.dumps(item))
        self.calls = []
        self.original_package_exists = release.package_exists
        package_patch = patch.object(release, "package_exists", return_value=True)
        package_patch.start()
        self.addCleanup(package_patch.stop)
        print_patch = patch('builtins.print')
        print_patch.start()
        self.addCleanup(print_patch.stop)
        config_patch = patch.object(release, "local_config", side_effect=lambda image: self.ids[image.rsplit("-", 1)[-1]])
        config_patch.start()
        self.addCleanup(config_patch.stop)

    def fake_run(self, *args, **kwargs):
        self.calls.append(args)
        if args[:3] == ("docker", "image", "inspect"):
            arch = args[-1].rsplit("-", 1)[-1]
            return json.dumps([dict(Id=self.ids[arch], Architecture=arch, Os="linux",
                                   Config=dict(Labels={"org.opencontainers.image.version": "1.2.3",
                                                       "org.opencontainers.image.revision": self.revision}))])
        if args[:5] == ("docker", "buildx", "imagetools", "inspect", "--format"):
            return json.dumps(dict(digest="sha256:" + "3" * 64))
        return ""

    def test_publication_is_atomic_and_never_rebuilds_images(self):
        responses = [None, {"amd64": self.ids["amd64"]}, {"arm64": self.ids["arm64"]}, None, self.ids]
        with patch.object(release, "run", side_effect=self.fake_run), patch.object(release, "remote_images", side_effect=responses):
            release.publish(self.cfg, self.path, "v1.2.3", self.revision)
        pushes = [i for i, c in enumerate(self.calls) if c[:2] == ("docker", "push")]
        creates = [i for i, c in enumerate(self.calls) if c[:4] == ("docker", "buildx", "imagetools", "create")]
        self.assertEqual(len(pushes), 2)
        self.assertEqual(len(creates), 1)
        self.assertGreater(creates[0], max(pushes))
        self.assertFalse(any(c[:2] == ("docker", "build") for c in self.calls))
        self.assertEqual(self.calls[-1][:4], ("docker", "buildx", "imagetools", "inspect"))

    def test_existing_tag_is_idempotent_only_for_identical_tested_images(self):
        for existing, accepted in ((self.ids, True), ({"amd64": self.ids["amd64"]}, False),
                                   (dict(self.ids, arm64="sha256:" + "9" * 64), False)):
            self.calls.clear()
            with patch.object(release, "run", side_effect=self.fake_run), patch.object(release, "remote_images", return_value=existing):
                if accepted:
                    release.publish(self.cfg, self.path, "v1.2.3", self.revision)
                else:
                    with self.assertRaisesRegex(ValueError, "refusing to replace"):
                        release.publish(self.cfg, self.path, "v1.2.3", self.revision)
            self.assertFalse(any(c[:2] == ("docker", "push") for c in self.calls))

    def test_missing_or_mismatched_architecture_prevents_all_registry_writes(self):
        path = self.path / "image-arm64.json"
        item = json.loads(path.read_text())
        for field, bad in (("app", "other"), ("version", "9.9.9"), ("revision", "b" * 40),
                           ("arch", "amd64"), ("id", "sha256:" + "0" * 64)):
            broken = dict(item, **{field: bad})
            path.write_text(json.dumps(broken))
            with patch.object(release, "run", side_effect=self.fake_run), patch.object(release, "remote_images") as remote:
                with self.assertRaises(ValueError):
                    release.publish(self.cfg, self.path, "v1.2.3", self.revision)
                remote.assert_not_called()
            self.assertFalse(any(c[:2] == ("docker", "push") for c in self.calls))

    def test_push_failure_does_not_publish_partial_release(self):
        def fail(*args, **kwargs):
            result = self.fake_run(*args, **kwargs)
            if args[:2] == ("docker", "push") and args[-1].endswith("arm64"):
                raise subprocess.CalledProcessError(1, args)
            return result
        with patch.object(release, "run", side_effect=fail), patch.object(release, "remote_images", side_effect=[None, {"amd64": self.ids["amd64"]}]):
            with self.assertRaises(subprocess.CalledProcessError):
                release.publish(self.cfg, self.path, "v1.2.3", self.revision)
        self.assertFalse(any(c[:4] == ("docker", "buildx", "imagetools", "create") for c in self.calls))

    def test_first_ghcr_publication_requires_confirmed_package_absence(self):
        responses = [{"amd64": self.ids["amd64"]}, {"arm64": self.ids["arm64"]}, None, self.ids]
        with patch.object(release, "package_exists", return_value=False), patch.object(release, "run", side_effect=self.fake_run), patch.object(release, "remote_images", side_effect=responses):
            release.publish(self.cfg, self.path, "v1.2.3", self.revision)
        self.assertEqual(len([c for c in self.calls if c[:2] == ("docker", "push")]), 2)

    def test_package_absence_is_distinct_from_authentication_and_network_failures(self):
        for status, present in ((0, True), (404, False)):
            response = subprocess.CompletedProcess([], int(bool(status)), json.dumps({"status": str(status)}), '')
            with patch.object(release.subprocess, 'run', return_value=response):
                self.assertEqual(self.original_package_exists(self.app), present)
        for status in (401, 403, 500):
            response = subprocess.CompletedProcess([], 1, json.dumps({"status": str(status)}), 'failed')
            with patch.object(release.subprocess, 'run', return_value=response), self.assertRaises(RuntimeError):
                self.original_package_exists(self.app)

    def test_registry_auth_and_network_errors_are_not_missing_tags(self):
        for message in ("unauthorized", "denied", "timeout", "connection refused"):
            result = subprocess.CompletedProcess([], 1, stdout="", stderr=message)
            with patch.object(release.subprocess, "run", return_value=result), self.assertRaises(RuntimeError):
                release.remote_images("ghcr.io/example/image:v1.2.3")
        result = subprocess.CompletedProcess([], 1, stdout="", stderr="manifest unknown")
        with patch.object(release.subprocess, "run", return_value=result):
            self.assertIsNone(release.remote_images("ghcr.io/example/image:v1.2.3"))


class MutationTests(unittest.TestCase):
    def test_regression_tests_kill_deliberate_publication_and_archive_defects(self):
        mutations = (
            ("checksum verification", 'if hashlib.sha256(archive.read_bytes()).hexdigest() != checksums.get(archive.name):',
             'if False:', 'ArchiveTests.test_checksum_mutations_and_ambiguous_inputs_are_rejected'),
            ("release immutability", 'if existing != expected:', 'if False:',
             'PublishTests.test_existing_tag_is_idempotent_only_for_identical_tested_images'),
            ("tested artifact identity", 'local_config(item["image"]) != item["id"]', 'False',
             'PublishTests.test_missing_or_mismatched_architecture_prevents_all_registry_writes'),
        )
        original = Path(release.__file__).read_text()
        with tempfile.TemporaryDirectory() as temporary:
            scripts = Path(temporary) / 'scripts/containers'
            scripts.mkdir(parents=True)
            shutil.copyfile(Path(__file__), scripts / 'test_release.py')
            shutil.copyfile(Path(lifecycle.__file__), scripts / 'lifecycle.py')
            shutil.copyfile(release.ROOT / 'scripts/containers/config.json', scripts / 'config.json')
            target = scripts / 'release.py'
            for name, before, after, test in mutations:
                with self.subTest(mutation=name):
                    self.assertEqual(original.count(before), 1, 'mutation anchor must be unique')
                    command = [sys.executable, '-B', '-m', 'unittest', 'test_release.' + test]
                    target.write_text(original)
                    baseline = subprocess.run(command, cwd=scripts, capture_output=True, text=True, timeout=30)
                    self.assertEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
                    target.write_text(original.replace(before, after, 1))
                    mutant = subprocess.run(command, cwd=scripts, capture_output=True, text=True, timeout=30)
                    self.assertNotEqual(mutant.returncode, 0, 'mutation survived: ' + name)
                    self.assertIn('FAIL:', mutant.stderr, mutant.stdout + mutant.stderr)


if __name__ == "__main__":
    unittest.main()
