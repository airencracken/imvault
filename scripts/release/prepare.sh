#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
app=imvault
# RELEASE_DIR lets tests prepare a release without touching the checkout.
out=${RELEASE_DIR:-.release}
mkdir -p "$out" || exit 1
sed -e "s|/usr/local/bin/$app|/usr/bin/$app|g" \
	"contrib/systemd/$app.service" > "$out/$app.service" || exit 1
# Package defaults bind only to loopback. Existing conffiles remain dpkg-managed.
{
	printf '%s\n' '# Debian package defaults.' 'IMVAULT_ADDR=127.0.0.1:8080'
	cat "contrib/systemd/$app.env" || exit 1
} > "$out/$app.env" || exit 1

# Include notices from the modules actually linked on either supported platform.
: > "$out/modules.unsorted" || exit 1
for arch in amd64 arm64; do
	CGO_ENABLED=0 GOWORK=off GOOS=linux GOARCH="$arch" go list -buildvcs=false -deps \
		-f '{{with .Module}}{{if not .Main}}{{.Path}}|{{.Dir}}{{end}}{{end}}' \
		"./cmd/$app" >> "$out/modules.unsorted" || exit 1
done
sort -u "$out/modules.unsorted" > "$out/modules" || exit 1
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
	done < "$out/modules"
	for license in internal/web/static/js/alpine.LICENSE internal/web/static/js/htmx.LICENSE; do
		printf '\n\n--- %s ---\n\n' "$license"
		cat "$license" || exit 1
	done
} > "$out/THIRD_PARTY_NOTICES.txt" || exit 1
{
	printf '%s\n\n' 'Copyright (C) 2026 Marcus J. Hildum.' \
		'Licensed under AGPL-3.0-or-later. Third-party components retain their own licenses.'
	cat LICENSE "$out/THIRD_PARTY_NOTICES.txt" || exit 1
} > "$out/copyright" || exit 1
