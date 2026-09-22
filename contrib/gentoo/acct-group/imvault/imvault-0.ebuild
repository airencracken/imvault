# Copyright 2026 Marcus J. Hildum
# SPDX-License-Identifier: AGPL-3.0-or-later

EAPI=8

inherit acct-group

DESCRIPTION="Service group for imvault"
KEYWORDS="~amd64 ~arm64"
# Overlay accounts use dynamically allocated IDs.
ACCT_GROUP_ID=-1
