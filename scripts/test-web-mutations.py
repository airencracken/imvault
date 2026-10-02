#!/usr/bin/env python3
"""Require the web regression tests to catch deliberate breaks of the rules they guard.

Each mutation undoes one protection in a scratch copy of the tree. The named
regression must pass before the change and fail after it; a mutation that goes
unnoticed means the test does not guard what it claims to.
"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
WEB = "./internal/web"
STORE = "./internal/store"

# (name, file, before, after, package, regression)
MUTATIONS = [
    ("provider links only confirmed addresses", "internal/web/handlers_oidc.go",
     "\t\tif user.EmailVerified {", "\t\tif true {", WEB, "TestOIDCDoesNotLinkToAnUnconfirmedLocalAddress"),
    ("emailed links need a base URL", "internal/web/helpers.go",
     "\tif s.cfg.BaseURL == \"\" {\n\t\treturn \"\", errNoBaseURL", "\tif false {\n\t\treturn \"\", errNoBaseURL",
     WEB, "TestEmailedLinksNeedABaseURL"),
    ("album API lists only visible files", "internal/web/api_albums.go",
     "\t\tVisibleTo: &user.ID,\n", "", WEB, "TestAPIAlbumListingAuthorizationMatrix"),
    ("album API withholds hidden details", "internal/web/api_albums.go",
     "\t\tif !canChangeFile(user, f) {", "\t\tif false {", WEB, "TestAPIAlbumListingAuthorizationMatrix"),
    ("redirect targets refuse backslashes", "internal/web/handlers_auth.go",
     "c >= 0x7f || c == '\\\\' {", "c >= 0x7f {", WEB, "TestSafeNextRefusesEverythingElse"),
    ("tokenless multipart reads only its first part", "internal/web/middleware.go",
     "\treturn firstPartToken(r)\n}", "\treturn \"\", r.ParseMultipartForm(multipartMemory)\n}",
     WEB, "TestTokenlessMultipartReadsOnlyTheFirstPart"),
    ("passwordless accounts must confirm first", "internal/web/reauth.go",
     "\t\tif s.recentlyReauthenticated(r, user) {", "\t\tif true {", WEB, "TestProviderAccountsHaveNoPasswordToAskFor"),
    ("a confirmation is bound to its session", "internal/web/reauth.go",
     "proof.Session == session &&", "true &&", WEB, "TestConfirmationIsBoundToSessionAndWindow"),
    ("the last way in cannot be removed", "internal/web/handlers_account.go",
     "\tif len(identities) <= 1 {", "\tif len(identities) < 0 {", WEB, "TestTheLastWayInCannotBeDisconnected"),
    ("provider accounts start without a password", "internal/store/password_state.go",
     "`UPDATE users SET password_set = 0 WHERE id = ?`", "`UPDATE users SET password_set = password_set WHERE id = ?`",
     STORE, "TestProviderAccountsStartWithoutAPassword"),
    ("provider accounts record their invitation", "internal/store/password_state.go",
     "\t\t\tin, err = invitedUser(ctx, tx, in, *inviteID)", "\t\t\terr = redeemInvite(ctx, tx, *inviteID)",
     STORE, "TestProviderAccountsRecordTheirInvitation"),
    ("member invitations are capped", "internal/web/handlers_admin_invites.go",
     "\t\tcase limited && (parsed < 1 || parsed > usesCap):", "\t\tcase false:", WEB, "TestMemberInvitationsAreCapped"),
    ("album reports record the album id", "internal/web/handlers_moderation.go",
     "\t\ttargetID = albumReportTarget(album)\n", "", WEB, "TestAnAlbumReportStillReachesItsAlbum"),
    ("plain delete forms get a real redirect", "internal/web/handlers_files.go",
     "\tcase isHTMX(r) && next != \"\":", "\tcase next != \"\":", WEB, "TestPlainDeleteFormRedirects"),
    ("a failed export is aborted", "internal/web/handlers_export.go",
     "\t\t\ts.abortExport(user, file.ID, \"write file\", err)", "\t\t\tcontinue", WEB, "TestAFailedExportIsNotAValidArchive"),
    ("queued mail is claimed before delivery", "internal/web/mailqueue.go",
     "\tclaimed, err := q.store.ClaimMail(ctx, record.ID, claimedAt, claimedAt.Add(mailClaimLease))",
     "\tclaimed, err := !claimedAt.IsZero(), error(nil)", WEB, "TestASlowDeliveryIsNotSentTwice"),
    ("reset requests share the sign-in budget", "internal/web/server.go",
     "s.rateLimitLogins(s.handleForgot)", "s.handleForgot", WEB, "TestCredentialRoutesShareTheSignInBudget"),
    ("flash messages are signed", "internal/web/flash.go",
     "\t\thmac.Equal([]byte(sig), []byte(s.flashSignature(flashNotice,", "\t\ttrue || hmac.Equal([]byte(sig), []byte(s.flashSignature(flashNotice,",
     WEB, "TestTamperedFlashMessagesAreNotShown"),
    ("forwarded scheme needs a trusted proxy", "internal/web/helpers.go",
     "\treturn s.cfg.TrustProxyHeaders && strings.EqualFold(", "\treturn strings.EqualFold(",
     WEB, "TestForwardedProtoNeedsATrustedProxy"),
    ("client address is the proxy's own entry", "internal/web/middleware.go",
     "entries[len(entries)-1]", "entries[0]", WEB, "TestClientIPTakesTheProxysOwnEntry"),
    ("pages refuse to be framed", "internal/web/middleware.go",
     "\t\theader.Set(\"X-Frame-Options\", \"DENY\")\n", "", WEB, "TestEveryResponseCarriesSecurityHeaders"),
    ("CSRF tokens are bound to the session", "internal/web/middleware.go",
     "\t\ttoken = sessionCSRFToken(session.Value)", "\t\t_ = session", WEB, "TestPlantedCSRFCookieIsRefusedForASession"),
    ("enabling a second factor rotates the session", "internal/web/handlers_twofactor.go",
     "\tr, err = s.rotateSession(w, r, user.ID)", "\terr = nil", WEB, "TestEnablingTwoFactorRotatesTheSession"),
    ("the enrolment code is spent", "internal/web/handlers_twofactor.go",
     "s.store.AcceptTOTPStep(r.Context(), user.ID, step)", "s.store.AcceptTOTPStep(r.Context(), user.ID, step-step)",
     WEB, "TestTheEnrolmentCodeIsSpent"),
    ("next survives the second factor safely", "internal/web/handlers_twofactor.go",
     "\tnext := safeNext(r.PostFormValue(\"next\"))", "\tnext := r.PostFormValue(\"next\")",
     WEB, "TestNextSurvivesTheSecondFactor"),
    ("uploads join only contributable albums", "internal/web/handlers_upload.go",
     "\tif err != nil || !canContributeToAlbum(user, album) {", "\tif err != nil {",
     WEB, "TestUploadingIntoAnAlbumFollowsTheAlbumRules"),
    ("recovery codes discard biased bytes", "internal/web/handlers_twofactor.go",
     "\t\tif buf[0] >= limit {", "\t\tif buf[0] >= limit && limit == 0 {", WEB, "TestRecoveryCodesRejectBiasedBytes"),
    ("administrator limits cannot overflow", "internal/web/handlers_admin.go",
     "value < 0 || value > maxAdminMegabytes {", "value < 0 {", WEB, "TestAdminMegabytesRefusesOverflow"),
    ("clean copies wait for the content lock", "internal/web/metadata.go",
     "\tunlock, err := s.content.acquire(ctx, file.SHA256)", "\tunlock, err := func() {}, error(nil)",
     WEB, "TestCleanCopyWaitsForTheContentLock"),
    ("administrator API removals are logged", "internal/web/api.go",
     "\ts.recordFileRemoval(r.Context(), currentUser(r.Context()), file, \"\")\n", "",
     WEB, "TestAPIRemovalsReachTheModerationLog"),
]


def run(command, cwd, env):
    return subprocess.run(command, cwd=cwd, env=env, text=True, capture_output=True, timeout=300)


def check():
    env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -buildvcs=false").strip()
    with tempfile.TemporaryDirectory(prefix="imvault-web-mutations-") as temporary:
        checkout = Path(temporary) / "source"
        shutil.copytree(ROOT, checkout, ignore=shutil.ignore_patterns(
            ".git", "node_modules", "bin", "dist", ".release", "*.db", "*.db-wal", "*.db-shm", "__pycache__"))
        for name, file, before, after, package, regression in MUTATIONS:
            command = ["go", "test", package, "-count=1", "-run", "^" + regression + "$", "-timeout=120s"]
            baseline = run(command, checkout, env)
            if baseline.returncode:
                raise RuntimeError("baseline failed for " + name + ":\n" + baseline.stdout + baseline.stderr)
            path = checkout / file
            original = path.read_text()
            if original.count(before) != 1:
                raise RuntimeError("mutation anchor changed: " + name)
            path.write_text(original.replace(before, after))
            try:
                result = run(command, checkout, env)
                if result.returncode == 0 or "--- FAIL: " + regression not in result.stdout:
                    raise RuntimeError("mutation was not caught: " + name + "\n" + result.stdout + result.stderr)
                print("PASS: regression rejects broken " + name, flush=True)
            finally:
                path.write_text(original)


if __name__ == "__main__":
    check()
