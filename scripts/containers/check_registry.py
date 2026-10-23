#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Exercise real multi-platform publication against an isolated local registry.

The fixture images have no executable code. Both architectures can be built on
either runner without emulation. No production registry is contacted or changed.
"""
import json
from pathlib import Path
import tempfile
import time
import uuid

from release import ARCHES, config, publish, remote_images, run, saved_config

REGISTRY = "registry:3@sha256:01cd312e67026f06f5d89c3e133a4a9adc7cb9eeb9b1194e0b2954dfd770efd2"


def main():
    name = "container-registry-test-" + uuid.uuid4().hex[:12]
    cfg = config()
    revision = "f" * 40
    tags = []
    try:
        run("docker", "run", "-d", "--name", name, "-p", "127.0.0.1::5000", REGISTRY)
        binding = json.loads(run("docker", "inspect", name))[0]["NetworkSettings"]["Ports"]["5000/tcp"][0]
        registry = "localhost:" + binding["HostPort"]
        for _ in range(30):
            if "listening on" in run("docker", "logs", name):
                break
            # Distribution logs may go to stderr, so readiness is also checked
            # by the first registry request, with an explicit retry.
            time.sleep(0.1)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "Dockerfile").write_text('''FROM scratch
ARG VERSION
ARG REVISION
LABEL org.opencontainers.image.version="$VERSION" org.opencontainers.image.revision="$REVISION"
COPY marker /marker
''')
            (root / "marker").write_text("publication fixture\n")
            expected = {}
            for arch in ARCHES:
                image = f"{cfg['app']}:tested-{arch}"
                run("docker", "build", "--provenance=false", "--platform", f"linux/{arch}",
                    "--build-arg", "VERSION=1.2.3", "--build-arg", f"REVISION={revision}",
                    "-t", image, str(root))
                tags.append(image)
                archive = root / f"image-{arch}.tar"
                run("docker", "save", "-o", str(archive), image)
                image_id = saved_config(archive, image)
                expected[arch] = image_id
                item = dict(app=cfg["app"], arch=arch, version="1.2.3", revision=revision,
                            image=image, id=image_id)
                (root / f"image-{arch}.json").write_text(json.dumps(item))
            publish(cfg, root, "v1.2.3", revision, registry=registry)
            before = remote_images(f"{registry}/{cfg['app']}:v1.2.3")
            assert before == expected
            # Identical saved images can safely resume an interrupted release.
            publish(cfg, root, "v1.2.3", revision, registry=registry)
            assert remote_images(f"{registry}/{cfg['app']}:v1.2.3") == before
            print("Real registry publication, both architectures and idempotent retry passed")
    finally:
        run("docker", "rm", "-fv", name)
        for image in tags:
            run("docker", "image", "rm", image)


if __name__ == "__main__":
    main()
