# Copyright 2026 Marcus J. Hildum
# Distributed under the terms of the GNU Affero General Public License v3

# Live ebuild for imvault: builds the master branch.
#
# Gentoo does not read ebuilds from a source tree, so this belongs in an overlay:
#
#   mkdir -p /var/db/repos/local/profiles /var/db/repos/local/app-admin/imvault
#   echo local > /var/db/repos/local/profiles/repo_name
#   cp contrib/gentoo/imvault-9999.ebuild /var/db/repos/local/app-admin/imvault/
#   emerge -av app-admin/imvault
#
# There is no manifest to generate and no EGO_SUM to keep in step: git-r3 fetches
# the repository rather than a distfile, and go-module_live_vendor resolves the
# modules from go.mod at build time. That is also why this is a live ebuild — a
# versioned one needs its EGO_SUM regenerated from the tag it packages, and the
# eclass now deprecates EGO_SUM in favour of a dependency tarball anyway.
#
# This has not been built on a Gentoo system. Treat it as a starting point and
# expect to adjust it. The OpenRC script it installs is the same one Alpine uses,
# and that one is tested.

EAPI=8

inherit git-r3 go-module systemd user

DESCRIPTION="Self-hosted image and short clip host with an htmx front end"
HOMEPAGE="https://github.com/airencracken/imvault"
EGIT_REPO_URI="https://github.com/airencracken/imvault.git"
# The project's default branch is master rather than main.
EGIT_BRANCH="master"

# imvault, then the licence of every module linked into the binary: Go links
# statically, and the eclass asks for this to be accurate. Worked out with
#
#   go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' ./cmd/imvault
#
# which is the list of what actually ends up in the binary rather than
# everything in the module graph. The Apache-2.0 pair are the OpenID Connect
# libraries and the BSD-2-Clause one is pkg/errors; nothing here is copyleft
# beyond imvault's own licence.
LICENSE="AGPL-3.0-or-later Apache-2.0 BSD-2-Clause BSD-3-Clause MIT"
SLOT="0"
# A live ebuild has no version to keyword.
KEYWORDS=""
# go-module_live_vendor refuses to run without this.
PROPERTIES="live"

IUSE="ffmpeg +openrc systemd"

# ffmpeg is optional. Without it clips are still accepted, but they get a
# placeholder poster instead of a frame from the video, and the duration limit
# cannot be enforced.
RDEPEND="
	ffmpeg? ( media-video/ffmpeg )
	openrc? ( sys-apps/openrc )
	systemd? ( sys-apps/systemd )
"

# The eclass asks for the Go it knows about. go.mod asks for 1.26, so add that
# rather than replacing the eclass's line, which carries the slot operator and a
# packaging workaround of its own.
BDEPEND+=" >=dev-lang/go-1.26"

src_unpack() {
	git-r3_src_unpack
	# Vendors the modules from go.mod, so the build itself needs no network.
	go-module_live_vendor
}

src_compile() {
	# The SQLite driver is pure Go, so no cgo and no cross-compilation trouble.
	ego build -trimpath -ldflags="-s -w" -o imvault ./cmd/imvault
}

src_test() {
	ego test ./...
}

src_install() {
	dobin imvault
	einstalldocs

	# The database and the uploaded bytes live here.
	keepdir /var/lib/imvault
	fowners imvault:imvault /var/lib/imvault
	fperms 0750 /var/lib/imvault

	if use openrc; then
		newinitd contrib/openrc/imvault imvault
		newconfd contrib/openrc/imvault.confd imvault
	fi

	if use systemd; then
		systemd_dounit contrib/systemd/imvault.service
		insinto /etc/imvault
		newins contrib/systemd/imvault.env imvault.env
		fowners root:imvault /etc/imvault/imvault.env
		fperms 0640 /etc/imvault/imvault.env
	fi
}

pkg_setup() {
	# A system account with no shell and no home of its own.
	enewgroup imvault
	enewuser imvault -1 -1 /var/lib/imvault imvault
}
