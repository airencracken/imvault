#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
fail() { printf '%s\n' "$*" >&2; exit 1; }
set -- dist/imvault_*_checksums.txt
[ "$#" = 1 ] && [ -f "$1" ] || fail 'Expected one checksum manifest.'
manifest=$(basename "$1")
(cd dist && sha256sum -c "$manifest") || exit 1
[ "$(wc -l < "$1" | tr -d ' ')" = 5 ] || fail 'Expected two archives, two packages, and one source archive.'
work=$(mktemp -d) || exit 1
trap 'rm -rf "$work"' EXIT
trap 'exit 130' HUP INT TERM
for arch in amd64 arm64; do
	set -- dist/imvault_*_linux_"$arch".tar.gz
	[ "$#" = 1 ] && [ -f "$1" ] || fail "Missing $arch binary archive."
	mkdir "$work/$arch" || exit 1
	tar -xzf "$1" -C "$work/$arch" --strip-components=1 || exit 1
	test -x "$work/$arch/imvault" || fail 'Archive binary is not executable.'
	test -s "$work/$arch/LICENSE" || fail 'Archive license is missing.'
	test -s "$work/$arch/THIRD_PARTY_NOTICES.txt" || fail 'Dependency notices are missing.'
	test -s "$work/$arch/contrib/openrc/imvault" || fail 'OpenRC example is missing.'
	test -s "$work/$arch/docs/releases.md" || fail 'Archive installation guide is missing.'
	readelf -l "$work/$arch/imvault" > "$work/elf" || exit 1
	if grep -q INTERP "$work/elf"; then fail 'Release binary needs a dynamic loader.'; fi
	set -- dist/imvault_*_"$arch".deb
	[ "$#" = 1 ] && [ -f "$1" ] || fail "Missing $arch Debian package."
	[ "$(dpkg-deb -f "$1" Architecture)" = "$arch" ] || fail 'Incorrect Debian architecture.'
	[ "$(dpkg-deb -f "$1" Package)" = imvault ] || fail 'Incorrect Debian package name.'
	dpkg-deb -e "$1" "$work/control-$arch" || exit 1
	grep -qx '/etc/imvault/imvault.env' "$work/control-$arch/conffiles" || fail 'Settings are not registered as a conffile.'
	for script in postinst prerm postrm; do
		test -x "$work/control-$arch/$script" || fail "Missing executable $script."
		sh -n "$work/control-$arch/$script" || exit 1
	done
	dpkg-deb -x "$1" "$work/deb-$arch" || exit 1
	cmp "$work/$arch/imvault" "$work/deb-$arch/usr/bin/imvault" || fail 'Archive and Debian binaries differ.'
done
set -- dist/imvault_*_source.tar.gz
[ "$#" = 1 ] && [ -f "$1" ] || fail 'Missing source archive.'
mkdir "$work/source" || exit 1
tar -xzf "$1" -C "$work/source" --strip-components=1 || exit 1
cmp go.mod "$work/source/go.mod" || fail 'Source archive does not match the build.'
cmp .goreleaser.yaml "$work/source/.goreleaser.yaml" || fail 'Source archive is missing current release configuration.'
test -f "$work/source/cmd/imvault/main.go" || fail 'Source archive is incomplete.'
printf '%s release artifact checks passed.\n' imvault
