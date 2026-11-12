#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
exec python3 scripts/comfy.py check-artifacts "$@"
