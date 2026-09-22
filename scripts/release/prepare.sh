#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
app=imvault
mkdir -p .release || exit 1
sed -e "s|/usr/local/bin/$app|/usr/bin/$app|g" -e 's/StateDirectoryMode=0750/StateDirectoryMode=0700/' \
	"contrib/systemd/$app.service" > ".release/$app.service" || exit 1
# Package defaults bind only to loopback. Existing conffiles remain dpkg-managed.
{
	printf '%s\n' '# Debian package defaults.' 'IMVAULT_ADDR=127.0.0.1:8080'
	cat "contrib/systemd/$app.env" || exit 1
} > ".release/$app.env" || exit 1

# Include notices from the modules actually linked on either supported platform.
: > .release/modules.unsorted || exit 1
for arch in amd64 arm64; do
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go list -buildvcs=false -deps \
		-f '{{with .Module}}{{if not .Main}}{{.Path}}|{{.Dir}}{{end}}{{end}}' \
		"./cmd/$app" >> .release/modules.unsorted || exit 1
done
sort -u .release/modules.unsorted > .release/modules || exit 1
{
	printf '%s\n\n' 'Go runtime and linked dependency license notices'
	cat contrib/licenses/go.LICENSE || exit 1
	while IFS='|' read -r module directory; do
		[ -n "$module" ] || continue
		found=no
		for license in "$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE*; do
			[ -f "$license" ] || continue
			found=yes
			printf '\n\n--- %s: %s ---\n\n' "$module" "$(basename "$license")"
			cat "$license" || exit 1
		done
		if [ "$found" != yes ]; then
			printf 'No license notice found for %s\n' "$module" >&2
			exit 1
		fi
	done < .release/modules
	for license in internal/web/static/js/alpine.LICENSE internal/web/static/js/htmx.LICENSE; do
		printf '\n\n--- %s ---\n\n' "$license"
		cat "$license" || exit 1
	done
} > .release/THIRD_PARTY_NOTICES.txt || exit 1
{
	printf '%s\n\n' 'Copyright (C) 2026 Marcus J. Hildum.' \
		'Licensed under AGPL-3.0-or-later. Third-party components retain their own licenses.'
	cat LICENSE .release/THIRD_PARTY_NOTICES.txt || exit 1
} > .release/copyright || exit 1
