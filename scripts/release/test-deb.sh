#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Run only in an expendable Debian/Ubuntu VM or container.
fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$RELEASE_PACKAGE_TEST" = 1 ] || fail 'Set RELEASE_PACKAGE_TEST=1 inside a disposable test system.'
[ "$(id -u)" = 0 ] || fail 'Package lifecycle tests need root in the disposable system.'
[ "$#" = 1 ] || fail 'Usage: test-deb.sh PACKAGE.deb'
package=$(realpath "$1") || exit 1
app=imvault
upper=IMVAULT
data="/var/lib/$app"
config="/etc/$app/$app.env"
[ ! -e "$data" ] && [ ! -e "/usr/bin/$app" ] || fail 'Refusing to test over an existing installation.'
if getent passwd "$app" >/dev/null || getent group "$app" >/dev/null; then
	fail 'Refusing to test with an existing service account.'
fi
work=$(mktemp -d) || exit 1
daemon_pid=
cleanup() {
	if [ -n "$daemon_pid" ]; then
		kill "$daemon_pid" 2>/dev/null || true
		wait "$daemon_pid" 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
init=no
[ ! -d /run/systemd/system ] || init=yes
health() {
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if curl --max-time 1 -fsS "http://127.0.0.1:18080/healthz" > "$work/health" 2>/dev/null; then
			grep -qx ok "$work/health" && return 0
		fi
		sleep 0.2
		attempt=$((attempt + 1))
	done
	if [ "$init" = yes ]; then journalctl -u "$app" --no-pager -n 40; fi
	fail 'Installed server did not become healthy.'
}
inactive() {
	if [ "$init" = yes ] && systemctl is-active --quiet "$app.service"; then
		fail 'Installation started an inactive service.'
	fi
}
apt-get install -y --no-install-recommends "$package" || fail 'Package installation failed.'
inactive
service_uid=$(id -u "$app") || exit 1
[ "$service_uid" -ne 0 ] || fail 'Service account is root.'
[ "$(stat -c '%U:%G:%a' "$data")" = "$app:$app:700" ] || fail 'Data directory has incorrect ownership or permissions.'
[ "$(stat -c '%U:%G:%a' "$config")" = root:root:600 ] || fail 'Configuration permissions are not private.'
test -s "/usr/share/doc/$app/copyright" || fail 'License notices are missing.'
grep -q "/usr/bin/$app" "/usr/lib/systemd/system/$app.service" || fail 'Service uses the wrong binary path.'
for setting in StandardOutput=journal StandardError=journal SyslogIdentifier=$app; do
	grep -qx "$setting" "/usr/lib/systemd/system/$app.service" || fail "Missing journal logging setting: $setting"
done
env "$upper"_DATA_DIR="$work/no-data" "/usr/bin/$app" --help > "$work/help" || fail 'Installed help failed.'
grep -q 'proxy-config' "$work/help" || fail 'Help omits the proxy helper.'
for proxy in caddy nginx apache; do
	env "$upper"_DATA_DIR="$work/no-data" "/usr/bin/$app" proxy-config "$proxy" --domain board.example.net > "$work/$proxy.conf" || fail "Installed $proxy helper failed."
	grep -q 'board.example.net' "$work/$proxy.conf" || fail 'Generated proxy config has the wrong hostname.'
done
[ ! -e "$work/no-data" ] || fail 'Help or config generation created application data.'
printf '\n# Local operator setting\n%s_ADDR=127.0.0.1:18080\n' "$upper" >> "$config" || exit 1
cp "$config" "$work/config" || exit 1
printf 'keep this board\n' > "$data/release-test-marker" || exit 1
printf '%s\n' 'release-test-password' | runuser -u "$app" -- env "$upper"_DATA_DIR="$data" \
	"/usr/bin/$app" create-admin --username release_test --password-stdin || fail 'Owner provisioning failed.'

if [ "$init" = yes ]; then
	systemd-analyze verify "/usr/lib/systemd/system/$app.service" || fail 'Invalid systemd unit.'
	systemctl start "$app.service" || fail 'Service startup failed.'
	[ "$(systemctl show -p StandardOutput --value "$app")" = journal ] || fail 'Service output is not journal-managed.'
	[ "$(systemctl show -p StandardError --value "$app")" = journal ] || fail 'Service errors are not journal-managed.'
else
	runuser -u "$app" -- env "$upper"_DATA_DIR="$data" "$upper"_ADDR=127.0.0.1:18080 \
		"/usr/bin/$app" > "$work/server.log" 2>&1 &
	daemon_pid=$!
fi
health
[ "$(stat -c '%U:%G:%a' "$data")" = "$app:$app:700" ] || fail 'Service startup changed private data permissions.'
if [ "$init" = yes ]; then old_pid=$(systemctl show -p MainPID --value "$app"); fi

# Exercise an actual newer package with a changed upstream conffile.
dpkg-deb --raw-extract "$package" "$work/upgrade" || exit 1
version=$(dpkg-deb --field "$package" Version) || exit 1
sed -i "s/^Version:.*/Version: $version+upgrade-test/" "$work/upgrade/DEBIAN/control" || exit 1
printf '\n# New upstream configuration default\n' >> "$work/upgrade$config" || exit 1
(
	cd "$work/upgrade" || exit 1
	find etc usr -type f -exec md5sum '{}' + > DEBIAN/md5sums
) || exit 1
dpkg-deb --root-owner-group --build "$work/upgrade" "$work/upgrade.deb" || exit 1
dpkg --force-confdef --force-confold -i "$work/upgrade.deb" || fail 'Package upgrade failed.'
cmp "$config" "$work/config" || fail 'Upgrade overwrote local configuration.'
health
if [ "$init" = yes ]; then
	new_pid=$(systemctl show -p MainPID --value "$app") || exit 1
	[ "$old_pid" != "$new_pid" ] || fail 'Upgrade did not restart the running service.'
	systemctl stop "$app" || exit 1
else
	kill "$daemon_pid" || exit 1
	wait "$daemon_pid" || true
	daemon_pid=
fi
dpkg --force-confdef --force-confold -i "$work/upgrade.deb" || fail 'Package reinstall failed.'
inactive
dpkg --remove "$app" || fail 'Package removal failed.'
cmp "$config" "$work/config" || fail 'Removal lost the conffile.'
dpkg --purge "$app" || fail 'Package purge failed.'
[ ! -e "$config" ] || fail 'Purge retained package configuration.'
grep -qx 'keep this board' "$data/release-test-marker" || fail 'Removal or purge deleted application data.'
[ "$(id -u "$app")" = "$service_uid" ] || fail 'Removal or purge changed the service account.'
dpkg -i "$package" || fail 'Reinstall after purge failed.'
inactive
grep -qx 'keep this board' "$data/release-test-marker" || fail 'Reinstall deleted application data.'
[ "$(id -u "$app")" = "$service_uid" ] || fail 'Reinstall changed the service account.'
dpkg --purge "$app" || exit 1
printf '%s package install, upgrade, removal, purge, and reinstall passed (systemd=%s).\n' "$app" "$init"
