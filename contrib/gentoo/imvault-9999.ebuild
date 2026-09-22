# Copyright 2026 Marcus J. Hildum
# Distributed under the terms of the GNU Affero General Public License v3

# Live ebuild for imvault: builds the master branch.
#
# Gentoo does not read ebuilds from a source tree, so this belongs in an overlay:
#
#   mkdir -p /var/db/repos/local/profiles /var/db/repos/local/app-admin/imvault
#   echo local > /var/db/repos/local/profiles/repo_name
#   cp contrib/gentoo/imvault-9999.ebuild contrib/gentoo/metadata.xml /var/db/repos/local/app-admin/imvault/
#   cp -R contrib/gentoo/acct-user contrib/gentoo/acct-group /var/db/repos/local/
#   emerge -av app-admin/imvault
#
# There is no manifest to generate and no EGO_SUM to keep in step: git-r3 fetches
# the repository rather than a distfile, and go-module_live_vendor resolves the
# modules from go.mod at build time. A versioned ebuild should instead provide
# a dependency tarball and a manifest for its release sources.
#
# Install the accompanying account packages in the same overlay. Gentoo manages
# service accounts through acct-user/acct-group rather than user.eclass.

EAPI=8

inherit git-r3 go-module systemd

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
LICENSE="AGPL-3+ Apache-2.0 BSD-2 BSD MIT"
SLOT="0"
# A live ebuild has no version to keyword.
KEYWORDS=""
# go-module_live_vendor refuses to run without this.
PROPERTIES="live"

IUSE="ffmpeg"

# ffmpeg is optional. Without it clips are still accepted, but they get a
# placeholder poster instead of a frame from the video, and the duration limit
# cannot be enforced.
RDEPEND="
	app-admin/logrotate
	acct-group/imvault
	acct-user/imvault
	ffmpeg? ( media-video/ffmpeg )
"

# The eclass asks for the Go it knows about. go.mod asks for 1.26, so add that
# rather than replacing the eclass's line, which carries the slot operator and a
# packaging workaround of its own.
BDEPEND+=" >=dev-lang/go-1.26 acct-group/imvault acct-user/imvault"

src_unpack() {
	git-r3_src_unpack
	# Vendors the modules from go.mod, so the build itself needs no network.
	go-module_live_vendor
}

src_compile() {
	# The SQLite driver is pure Go, so no cgo and no cross-compilation trouble.
	ego build -trimpath -ldflags="-s -w" -o imvault ./cmd/imvault
}

src_configure() {
	go-module_src_configure
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

	newinitd contrib/openrc/imvault imvault
	newconfd contrib/openrc/imvault.confd imvault
	insinto /etc/logrotate.d
	newins contrib/logrotate/imvault imvault

	# Packages install into /usr, unlike the source-install default.
	sed 's|/usr/local/bin/imvault|/usr/bin/imvault|' \
		contrib/systemd/imvault.service > "${T}/imvault.service" || die
	systemd_dounit "${T}/imvault.service"
	insinto /etc/imvault
	newins contrib/systemd/imvault.env imvault.env
	fowners root:imvault /etc/imvault/imvault.env
	fperms 0640 /etc/imvault/imvault.env
}
