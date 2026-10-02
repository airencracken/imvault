#!/usr/bin/env python3
"""Require invitation regressions to detect broken authorization and attribution."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MUTATIONS = [
    ("issuer authorization", "internal/store/invites.go", "WHERE u.id = ? AND u.disabled = 0 AND (u.role = 'admin' OR u.can_invite = 1)", "WHERE u.id = ?", "./internal/store", "TestInviteWritesRecheckIssuerPermission"),
    ("creator scope", "internal/store/invites.go", "WHERE id = ? AND created_by = ? AND revoked_at IS NULL", "WHERE id = ? AND ? > 0 AND revoked_at IS NULL", "./internal/store", "TestDelegatedInvitePermissionAndAttribution"),
    ("live revocation", "internal/store/invites.go", "AND u.disabled = 0 AND (u.role = 'admin' OR u.can_invite = 1))", "AND 1 = 1)", "./internal/store", "TestInviteWritesRecheckIssuerPermission"),
    ("disabled issuer admission", "internal/store/invites.go", "AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = invites.created_by AND u.disabled = 1)", "AND 1 = 1", "./internal/store", "TestDisabledIssuerCannotAdmitPasswordOrProvider"),
    ("provider attribution", "internal/store/identities.go", "in, err = invitedUser(ctx, tx, in, *inviteID)", "err = redeemInvite(ctx, tx, *inviteID)", "./internal/store", "TestDisabledIssuerCannotAdmitPasswordOrProvider"),
    ("ordinary form response", "internal/web/handlers_admin_invites.go", "if isHTMX(r) {", "if true {", "./internal/web", "TestMemberInvitationResponsesWorkWithAndWithoutHTMX"),
]


def check():
    env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -buildvcs=false").strip()
    with tempfile.TemporaryDirectory(prefix="imvault-invitation-mutations-") as temporary:
        checkout = Path(temporary) / "source"
        shutil.copytree(ROOT, checkout, ignore=shutil.ignore_patterns(
            ".git", "node_modules", "bin", "dist", ".release", "*.db", "*.db-wal", "*.db-shm", "__pycache__"))
        for name, file, before, after, package, regression in MUTATIONS:
            command = ["go", "test", package, "-count=1", "-run", "^" + regression + "$", "-timeout=30s"]
            baseline = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
            if baseline.returncode:
                raise RuntimeError("baseline failed:\n" + baseline.stdout + baseline.stderr)
            path = checkout / file
            original = path.read_text()
            if original.count(before) != 1:
                raise RuntimeError("mutation anchor changed: " + name)
            path.write_text(original.replace(before, after))
            try:
                result = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
                if result.returncode == 0 or "--- FAIL: " + regression not in result.stdout:
                    raise RuntimeError("mutation was not caught: " + name + "\n" + result.stdout + result.stderr)
                print("PASS: regression rejects broken " + name, flush=True)
            finally:
                path.write_text(original)


if __name__ == "__main__":
    check()
