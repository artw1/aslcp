#!/bin/sh
# ASLCP image: show the one-time setup link on the console after aslcp started.
# Runs on every boot (aslcp-setup-link.service); prints nothing once an
# administrator exists, because aslcp then has no setup code any more.
#
# The code is a secret, so the link goes to the console, to /run/issue.d (shown
# by the login prompt; root-only file, gone after a reboot) and to the journal
# of this unit (readable by administrators). It is not written to /etc/motd or
# any file that survives a reboot.
#
# Test hooks: ROOT=<dir> prefixes the paths; CONSOLE=<file> replaces /dev/console;
# WAIT=<seconds> limits each wait (default 60).
set -eu

ROOT=${ROOT:-}
CONSOLE=${CONSOLE:-/dev/console}
WAIT=${WAIT:-60}
CONF=$ROOT/etc/aslcp/aslcp.conf
CODE_FILE=$ROOT/var/lib/aslcp/setup-code
ISSUE_DIR=$ROOT/run/issue.d
ISSUE=$ISSUE_DIR/50-aslcp-setup.issue

# conf_get KEY DEFAULT: value of KEY in the [server] section of aslcp.conf.
conf_get() {
	v=$(awk -v key="$1" '
		/^[ \t]*\[/ { sec = ($0 ~ /^[ \t]*\[server\][ \t]*$/); next }
		sec && /^[ \t]*[;#]/ { next }
		sec { i = index($0, "="); if (i == 0) next
			k = substr($0, 1, i - 1); gsub(/[ \t]/, "", k)
			if (k == key) { val = substr($0, i + 1); sub(/[ \t]*[;#].*$/, "", val); gsub(/^[ \t]+|[ \t]+$/, "", val); out = val } }
		END { print out }' "$CONF" 2>/dev/null || true)
	[ -n "$v" ] || v=$2
	printf '%s\n' "$v"
}

rm -f "$ISSUE"

# The service writes the code during its first start.
n=0
while [ ! -s "$CODE_FILE" ] && [ "$n" -lt "$WAIT" ]; do
	sleep 1
	n=$((n + 1))
done
code=$(head -n 1 "$CODE_FILE" 2>/dev/null || true)
[ -n "$code" ] || exit 0

listen=$(conf_get listen :8080)
port=${listen##*:}
case "$port" in
'' | *[!0-9]*) port=8080 ;;
esac
suffix=":$port"
[ "$port" != 80 ] || suffix=

# A network address, if one shows up in time; the name works with mDNS (avahi).
n=0
ip=
while [ "$n" -lt "$WAIT" ]; do
	ip=$(hostname -I 2>/dev/null | awk '{print $1}') || ip=
	[ -z "$ip" ] || break
	n=$((n + 1))
	sleep 1
done
name=$(hostname 2>/dev/null || echo localhost)

msg() {
	printf '\n'
	printf '  ASLCP is ready. Open the one-time setup link in a browser:\n\n'
	if [ -n "$ip" ]; then
		printf '    http://%s%s/#/setup?code=%s\n' "$ip" "$suffix" "$code"
		printf '    http://%s.local%s/#/setup?code=%s\n\n' "$name" "$suffix" "$code"
	else
		printf '    http://%s.local%s/#/setup?code=%s\n\n' "$name" "$suffix" "$code"
	fi
	printf '  It creates the administrator account (or restores a backup).\n'
	printf '  Show the code again: sudo aslcp admin setup-code\n\n'
}

install -d -m 0755 "$ISSUE_DIR"
msg >"$ISSUE"
chmod 0600 "$ISSUE"
msg
msg >"$CONSOLE" 2>/dev/null || true
# Ask a running login prompt to show the new text (util-linux agetty).
if [ -z "$ROOT" ] && command -v agetty >/dev/null 2>&1; then
	agetty --reload >/dev/null 2>&1 || true
fi
