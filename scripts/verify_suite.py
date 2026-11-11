#!/usr/bin/env python3
"""Check synchronized suite assets without needing a Comfylib checkout."""
import hashlib
import json
from pathlib import Path


def verify(root):
    root = Path(root).resolve()
    manifest = json.loads((root / 'scripts/suite-assets.json').read_text())
    if set(manifest) != {'source', 'files'} or manifest['source'] != 'github.com/airencracken/comfylib' or not manifest['files']:
        raise ValueError('invalid suite manifest')
    for name, digest in manifest['files'].items():
        relative = Path(name)
        path = root / relative
        if relative.is_absolute() or '..' in relative.parts or not name.startswith('internal/') or not path.resolve().is_relative_to(root):
            raise ValueError('unsafe suite manifest path')
        if not isinstance(digest, str) or len(digest) != 64 or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError('suite asset differs: ' + name)


if __name__ == '__main__':
    import sys
    try:
        verify(Path(__file__).resolve().parents[1])
    except (OSError, ValueError, TypeError, KeyError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
