#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
export GOWORK=off
go mod download || exit 1
go mod verify || exit 1
git diff --exit-code -- go.mod go.sum || exit 1
exec python3 scripts/comfy.py prepare-release "$@"
