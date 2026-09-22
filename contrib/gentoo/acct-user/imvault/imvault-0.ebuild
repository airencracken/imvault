# Copyright 2026 Marcus J. Hildum
# SPDX-License-Identifier: AGPL-3.0-or-later

EAPI=8

inherit acct-user

DESCRIPTION="Service account for imvault"
KEYWORDS="~amd64 ~arm64"
# Overlay accounts use dynamically allocated IDs.
ACCT_USER_ID=-1
ACCT_USER_GROUPS=( imvault )
ACCT_USER_HOME=/var/lib/imvault
ACCT_USER_HOME_PERMS=0750

acct-user_add_deps
