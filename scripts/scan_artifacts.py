#!/usr/bin/env python3
"""Scan the actual Go executables in release archives without extracting paths."""
import argparse
from pathlib import Path
import subprocess
import tarfile
import tempfile


def binaries(archive):
    with tarfile.open(archive, 'r:*') as package:
        for member in package:
            path = Path(member.name)
            if path.is_absolute() or '..' in path.parts or member.issym() or member.islnk():
                raise ValueError('unsafe archive member: '+member.name)
            if member.isfile() and (member.mode & 0o111):
                if member.size > 256*1024*1024:
                    raise ValueError('executable exceeds scan size limit')
                stream = package.extractfile(member)
                data = stream.read()
                # Scripts are not Go binaries. Executable formats are ELF,
                # Mach-O (both byte orders), and PE.
                if data[:4] == b'\x7fELF' or data[:2] == b'MZ' or data[:4] in (b'\xcf\xfa\xed\xfe', b'\xfe\xed\xfa\xcf'):
                    yield member.name, data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archives', type=Path, nargs='+')
    parser.add_argument('--scanner', default='govulncheck')
    args = parser.parse_args()
    count = 0
    for archive in args.archives:
        scanned = 0
        for name, content in binaries(archive):
            with tempfile.TemporaryDirectory(prefix='comfyware-artifact-scan-') as directory:
                binary = Path(directory)/'application'
                binary.write_bytes(content)
                print(str(archive)+': '+name, flush=True)
                if subprocess.run([args.scanner, '-mode=binary', str(binary)]).returncode:
                    return 1
                scanned += 1
                count += 1
        if not scanned:
            parser.error('archive contains no executable binaries: '+str(archive))
    print(f'{count} release executables passed vulnerability scanning')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
