#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Verify a provision command hides input before showing either terminal prompt."""
import argparse
import errno
import os
from pathlib import Path
import pty
import select
import subprocess
import tempfile
import termios
import time


def check(binary, command, data_env, first_label, second_label, mismatch):
    with tempfile.TemporaryDirectory(prefix="comfyware-terminal-") as directory:
        data = Path(directory) / "data"
        environment = dict(os.environ, **{data_env: str(data)})
        master, slave = pty.openpty()
        original = termios.tcgetattr(slave)
        process = subprocess.Popen(
            [str(binary), command, "--username", "alex", "--password-prompt"],
            env=environment, stdin=slave, stdout=slave, stderr=slave,
        )
        transcript = bytearray()
        secret = b"a-long-private-password"
        confirmation = b"a-different-private-password" if mismatch else secret
        deadline = time.monotonic() + 15
        try:
            for label, value in ((first_label, secret), (second_label, confirmation)):
                while label.encode() not in transcript:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0 or not select.select([master], [], [], remaining)[0]:
                        raise AssertionError("prompt did not appear")
                    chunk = os.read(master, 4096)
                    if not chunk:
                        raise AssertionError("terminal closed before prompt")
                    transcript.extend(chunk)
                if termios.tcgetattr(slave)[3] & (termios.ECHO | termios.ECHONL):
                    raise AssertionError("echo was enabled when a prompt became visible")
                os.write(master, value + b"\n")
            code = process.wait(timeout=15)
            while select.select([master], [], [], 0)[0]:
                try:
                    chunk = os.read(master, 4096)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
                if not chunk:
                    break
                transcript.extend(chunk)
            if secret in transcript or confirmation in transcript:
                raise AssertionError("terminal echoed a password")
            if termios.tcgetattr(slave) != original:
                raise AssertionError("terminal state was not restored")
            if mismatch:
                if code == 0 or b"passwords do not match" not in transcript or data.exists():
                    raise AssertionError("mismatched confirmation mutated account data")
            elif code != 0 or not data.exists():
                raise AssertionError("hidden prompt failed to provision an account")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            os.close(master)
            os.close(slave)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--command", required=True)
    parser.add_argument("--data-env", required=True)
    parser.add_argument("--prompt", required=True)
    parser.add_argument("--confirmation", required=True)
    arguments = parser.parse_args()
    for mismatch in (False, True):
        check(arguments.binary.resolve(), arguments.command, arguments.data_env,
              arguments.prompt, arguments.confirmation, mismatch)
    print("PASS: hidden terminal prompts, confirmation failure and terminal restoration")


if __name__ == "__main__":
    main()
