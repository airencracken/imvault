#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Build and publish tested images from the native release archives.

No source compilation occurs in the release image. Docker archives carry the
exact tested image between jobs; publication never rebuilds it.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
ARCHES = ("amd64", "arm64")


def run(*args, **kwargs):
    try:
        return subprocess.run(args, check=True, text=True, capture_output=True,
                              timeout=kwargs.pop("timeout", 600), **kwargs).stdout.strip()
    except subprocess.CalledProcessError as error:
        print(error.stdout or "")
        print(error.stderr or "")
        raise


def config(root=ROOT):
    value = json.loads((root / "scripts/containers/config.json").read_text())
    if set(value) != {"app", "previous", "port", "tmp_mb", "owner_command"}:
        raise ValueError("unexpected container configuration fields")
    if value["app"] not in ("imvault", "witmoot", "songstead"):
        raise ValueError("unknown app")
    version(value["previous"])
    if type(value["port"]) is not int or not 1024 <= value["port"] <= 65535:
        raise ValueError("invalid port")
    if type(value["tmp_mb"]) is not int or not 16 <= value["tmp_mb"] <= 1024:
        raise ValueError("invalid temporary storage limit")
    if value["owner_command"] not in ("create-admin", "create-owner"):
        raise ValueError("invalid owner command")
    return value


def version(value):
    # Full release versions only: moving aliases would silently migrate databases.
    if not re.fullmatch(r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", value):
        raise ValueError("expected a full semantic version")
    return value.removeprefix("v")


def stage(dist, output, app, arch):
    if arch not in ARCHES:
        raise ValueError("unsupported architecture")
    archives = list(dist.glob(f"{app}_*_linux_{arch}.tar.gz"))
    if len(archives) != 1:
        raise ValueError("expected exactly one native archive")
    archive = archives[0]
    release = archive.name[len(app) + 1:-len(f"_linux_{arch}.tar.gz")]
    # Snapshot versions from GoReleaser are also full prerelease versions.
    release = version(release)
    manifest = dist / f"{app}_{release}_checksums.txt"
    checksums = {}
    for line in manifest.read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        name = name.removeprefix("*")
        if name in checksums or not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise ValueError("invalid or duplicate checksum")
        checksums[name] = digest
    if hashlib.sha256(archive.read_bytes()).hexdigest() != checksums.get(archive.name):
        raise ValueError("native archive checksum mismatch")
    if output.exists():
        raise ValueError("staging directory already exists")
    output.parent.mkdir(parents=True, exist_ok=True)
    # Validate the entire archive before exposing any output. Never extract paths
    # supplied by an archive: copy only these four regular files by exact name.
    with tarfile.open(archive) as source, tempfile.TemporaryDirectory(dir=output.parent) as temporary:
        wanted = {app, "LICENSE", "README.md", "THIRD_PARTY_NOTICES.txt"}
        prefix = archive.name.removesuffix(".tar.gz")
        found, seen = set(), set()
        target = Path(temporary) / "context"
        (target / "bin").mkdir(parents=True)
        (target / "docs").mkdir()
        for member in source:
            path = PurePosixPath(member.name)
            if (path.is_absolute() or ".." in path.parts or "\\" in member.name
                    or not path.parts or path.parts[0] != prefix
                    or member.name in seen or not (member.isfile() or member.isdir())):
                raise ValueError("unsafe archive member")
            seen.add(member.name)
            if path.parent != PurePosixPath(prefix) or path.name not in wanted:
                continue
            if not member.isfile() or not 0 < member.size <= 128 * 1024 * 1024:
                raise ValueError("invalid release file")
            content = source.extractfile(member).read()
            dest = target / ("bin" if path.name == app else "docs") / path.name
            dest.write_bytes(content)
            dest.chmod(0o755 if path.name == app else 0o644)
            found.add(path.name)
        if found != wanted:
            raise ValueError("native archive lacks binary or license notices")
        # Check ELF architecture before any image is built or published.
        binary = (target / "bin" / app).read_bytes()
        machine = {"amd64": 62, "arm64": 183}[arch]
        if binary[:6] != b"\x7fELF\x02\x01" or int.from_bytes(binary[18:20], "little") != machine:
            raise ValueError("binary does not match the requested Linux architecture")
        (target / "version").write_text(release + "\n")
        target.rename(output)
    return release


def saved_config(path, image):
    # Docker's containerd image store reports an index/manifest as .Id; the
    # classic store reports a config digest. The saved config digest is portable
    # across both engines and binds the runtime configuration and layer diff IDs.
    with tarfile.open(path) as archive:
        manifests = json.load(archive.extractfile("manifest.json"))
        if len(manifests) != 1 or manifests[0]["RepoTags"] != [image]:
            raise ValueError("expected exactly one saved image with the tested tag")
        name = manifests[0]["Config"]
        member = archive.getmember(name)
        if not member.isfile() or not 0 < member.size < 16 * 1024 * 1024:
            raise ValueError("invalid saved image configuration")
        content = archive.extractfile(member).read()
        return "sha256:" + hashlib.sha256(content).hexdigest()


def local_config(image):
    with tempfile.TemporaryDirectory() as temporary:
        archive = Path(temporary) / "image.tar"
        run("docker", "save", "-o", str(archive), image)
        return saved_config(archive, image)


def build(cfg, arch, dist, output, revision, root=ROOT):
    if not re.fullmatch(r"[a-f0-9]{40}", revision):
        raise ValueError("expected a full Git revision")
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as temporary:
        context = Path(temporary) / "context"
        release = stage(dist, context, cfg["app"], arch)
        image = f"{cfg['app']}:tested-{arch}"
        run("docker", "build", "--platform", f"linux/{arch}", "--provenance=false",
            "-f", str(root / "Dockerfile.release"), "--build-arg", f"VERSION={release}",
            "--build-arg", f"REVISION={revision}", "-t", image, str(context))
        run("python3", str(root / "scripts/containers/lifecycle.py"), "--image", image,
            "--version", release, "--binary", str(context / "bin" / cfg["app"]),
            "--upgrade", timeout=600)
        archive = output / f"image-{arch}.tar"
        run("docker", "save", "-o", str(archive), image)
        image_id = saved_config(archive, image)
        metadata = {"app": cfg["app"], "arch": arch, "version": release,
                    "revision": revision, "image": image, "id": image_id}
        (output / f"image-{arch}.json").write_text(json.dumps(metadata) + "\n")


def remote_images(ref, expected_arch=None):
    result = subprocess.run(["docker", "buildx", "imagetools", "inspect", "--raw", ref],
                            text=True, capture_output=True, timeout=60)
    if result.returncode:
        message = result.stderr.lower()
        if any(word in message for word in ("manifest unknown", "no such manifest", ": not found")):
            return None
        raise RuntimeError(f"registry inspection failed: {result.stderr}")
    data = json.loads(result.stdout)
    if "manifests" not in data:
        if expected_arch not in ARCHES:
            raise ValueError("a release tag must contain a multi-platform index")
        return {expected_arch: data["config"]["digest"]}
    images = {}
    repository = ref.split("@", 1)[0]
    if ":" in repository.rsplit("/", 1)[-1]:
        repository = repository.rsplit(":", 1)[0]
    for entry in data["manifests"]:
        platform = entry["platform"]
        arch = platform["architecture"]
        if platform["os"] != "linux" or arch not in ARCHES or arch in images:
            raise ValueError("unexpected published image platform")
        manifest = json.loads(run("docker", "buildx", "imagetools", "inspect", "--raw",
                                  repository + "@" + entry["digest"]))
        images[arch] = manifest["config"]["digest"]
    return images


def publish(cfg, directory, tag, revision, registry="ghcr.io/airencracken"):
    release = version(tag)
    if tag != "v" + release:
        raise ValueError("publication requires a v-prefixed release tag")
    if not re.fullmatch(r"[a-f0-9]{40}", revision):
        raise ValueError("expected a full Git revision")
    app = cfg["app"]
    image = f"{registry}/{app}"
    expected, metadata = {}, {}
    # Validate both artifacts before any write to the registry.
    for arch in ARCHES:
        item = json.loads((directory / f"image-{arch}.json").read_text())
        if (item.get("app"), item.get("arch"), item.get("version"), item.get("revision"), item.get("image")) != (
                app, arch, release, revision, f"{app}:tested-{arch}"):
            raise ValueError("image metadata does not match this release")
        run("docker", "load", "-i", str(directory / f"image-{arch}.tar"))
        actual = json.loads(run("docker", "image", "inspect", item["image"]))[0]
        labels = actual["Config"]["Labels"]
        if (local_config(item["image"]) != item["id"] or actual["Architecture"] != arch or actual["Os"] != "linux"
                or labels["org.opencontainers.image.version"] != release
                or labels["org.opencontainers.image.revision"] != revision):
            raise ValueError("saved image differs from tested artifact")
        expected[arch], metadata[arch] = item["id"], item
    existing = remote_images(f"{image}:{tag}")
    if existing is not None:
        if existing != expected:
            raise ValueError("refusing to replace an existing release tag; publish a new version")
    else:
        references = []
        for arch in ARCHES:
            # Candidate tags are private implementation details. A single final
            # manifest update exposes both tested architectures together.
            candidate = f"{image}:candidate-{revision}-{arch}"
            run("docker", "tag", metadata[arch]["image"], candidate)
            run("docker", "push", candidate)
            pushed = remote_images(candidate, expected_arch=arch)
            if pushed != {arch: expected[arch]}:
                raise ValueError(f"pushed image differs from tested artifact: {pushed} != {expected[arch]}")
            # Resolve the manifest by digest, so another run cannot swap a candidate.
            descriptor = json.loads(run("docker", "buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}", candidate))
            references.append(f"{image}@{descriptor['digest']}")
        # Recheck after uploads. Workflow concurrency also serializes publication.
        existing = remote_images(f"{image}:{tag}")
        if existing is not None and existing != expected:
            raise ValueError("release tag appeared during publication")
        if existing is None:
            run("docker", "buildx", "imagetools", "create", "--tag", f"{image}:{tag}", *references)
        if remote_images(f"{image}:{tag}") != expected:
            raise ValueError("published manifest differs from tested images")
    # A public source repo does not automatically make its GHCR package public.
    # Check with an empty Docker config; never mistake authenticated access for
    # anonymous access. On the first release, set package visibility and rerun.
    with tempfile.TemporaryDirectory() as temporary:
        env = dict(os.environ, DOCKER_CONFIG=temporary)
        run("docker", "buildx", "imagetools", "inspect", f"{image}:{tag}", env=env)
    print(f"Published and verified anonymous access to {image}:{tag}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    builder = sub.add_parser("build")
    builder.add_argument("--arch", choices=ARCHES, required=True)
    builder.add_argument("--dist", type=Path, default=Path("dist"))
    builder.add_argument("--output", type=Path, default=Path("container-artifacts"))
    builder.add_argument("--revision", required=True)
    publisher = sub.add_parser("publish")
    publisher.add_argument("--directory", type=Path, default=Path("container-artifacts"))
    publisher.add_argument("--tag", required=True)
    publisher.add_argument("--revision", required=True)
    args = parser.parse_args()
    cfg = config()
    if args.command == "build":
        build(cfg, args.arch, args.dist.resolve(), args.output.resolve(), args.revision)
    else:
        publish(cfg, args.directory, args.tag, args.revision)


if __name__ == "__main__":
    main()
