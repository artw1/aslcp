#!/bin/sh
# postrm for aslcp. POSIX sh; safe to run repeatedly.
#
# This script must never fail a remove or a purge: every step logs and goes on.
# The ASLCP_POSTRM_* variables exist for the unit tests (a fake systemctl and
# a temporary root directory); nothing sets them on a real system.

ROOT=${ASLCP_POSTRM_ROOT:-}
RECORD=$ROOT/var/lib/aslcp/disabled-units
DROPIN_DIR=$ROOT/etc/systemd/system/aslcp.service.d
SYSTEMD_RUN_DIR=${ASLCP_POSTRM_SYSTEMD_DIR:-/run/systemd/system}

have_systemd() { [ -d "$SYSTEMD_RUN_DIR" ]; }

daemon_reload() {
	have_systemd || return 0
	systemctl daemon-reload || echo "aslcp: systemctl daemon-reload failed (ignored)" >&2
	return 0
}

# The units the services switch may have turned off (the allowlist of
# internal/helper/sys_svc.go). The record sits in a directory the aslcp user
# owns, so nothing else in it is trusted.
valid_unit() {
	case "$1" in
	cockpit.socket | cockpit.service | apache2 | allmon3) return 0 ;;
	esac
	return 1
}

# Remove ASLCP's own port 80 drop-in: only that file, then the directory if it
# is empty.
remove_dropin() {
	rm -f "$DROPIN_DIR/port80.conf" || echo "aslcp: cannot remove $DROPIN_DIR/port80.conf (ignored)" >&2
	rmdir "$DROPIN_DIR" 2>/dev/null || true
	return 0
}

# Re-enable exactly the units ASLCP turned off, then delete the record. A unit
# that ASLCP did not disable, or that the operator disabled by hand, is not in
# the record and stays as it is.
restore_units() {
	[ -f "$RECORD" ] && [ ! -L "$RECORD" ] || return 0
	if have_systemd; then
		seen=" "
		while IFS= read -r line || [ -n "$line" ]; do
			unit=$(printf '%s' "$line" | tr -d ' \t\r')
			valid_unit "$unit" || continue
			case "$seen" in *" $unit "*) continue ;; esac
			seen="$seen$unit "
			echo "aslcp: turning $unit back on (ASLCP had turned it off)"
			systemctl enable --now "$unit" || echo "aslcp: could not enable $unit (ignored)" >&2
		done <"$RECORD"
	fi
	rm -f "$RECORD" || echo "aslcp: cannot remove $RECORD (ignored)" >&2
	return 0
}

case "$1" in
remove)
	daemon_reload
	;;
purge)
	# ASLCP's own port 80 drop-in goes first and systemd reloads, so port 80
	# is free when Apache comes back (aslcp.service was stopped by prerm).
	remove_dropin
	daemon_reload
	# The record lives in /var/lib/aslcp: read it before that goes away.
	restore_units
	rm -rf "$ROOT/var/lib/aslcp" "$ROOT/run/aslcp"
	# The root-only state of the helper: the config history (git), the
	# DVSwitch install and bridge-node records, pending restored DVSwitch
	# settings and the DVSwitch ini history (both hold passwords), and the
	# port 80 move state.
	# /etc/asterisk and DVSwitch's own files are not touched.
	rm -rf "$ROOT/var/lib/aslcp-config-history" "$ROOT/var/lib/aslcp-dvswitch" "$ROOT/var/lib/aslcp-dvswitch-history" "$ROOT/var/lib/aslcp-webport"
	# The hotspot setting (WiFi networks saved in NetworkManager stay).
	rm -rf "$ROOT/var/lib/aslcp-wifi"
	rmdir "$ROOT/etc/aslcp" 2>/dev/null || true
	if getent passwd aslcp >/dev/null 2>&1; then
		deluser --system aslcp >/dev/null 2>&1 || true
	fi
	if getent group aslcp >/dev/null 2>&1; then
		delgroup --system aslcp >/dev/null 2>&1 || true
	fi
	;;
upgrade | failed-upgrade | abort-install | abort-upgrade | disappear) ;;
*)
	echo "postrm called with unknown argument '$1'" >&2
	exit 1
	;;
esac

exit 0
