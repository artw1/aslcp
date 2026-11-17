#!/bin/sh
# prerm for aslcp. POSIX sh; safe to run repeatedly.
set -e

DATADIR=/var/lib/aslcp
FW_MARKER="$DATADIR/.firewalld-rule-added"

remove_firewalld_rule() {
	[ -e "$FW_MARKER" ] || return 0
	if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
		firewall-cmd --permanent --remove-service=aslcp >/dev/null 2>&1 || true
		firewall-cmd --reload >/dev/null 2>&1 || true
		echo "aslcp: removed the firewalld rule for the 'aslcp' service."
	elif command -v firewall-offline-cmd >/dev/null 2>&1; then
		firewall-offline-cmd --remove-service=aslcp >/dev/null 2>&1 || true
	fi
	rm -f "$FW_MARKER"
}

case "$1" in
remove | deconfigure)
	if [ -d /run/systemd/system ]; then
		systemctl stop aslcp.service aslcp-helper.service aslcp-helper.socket >/dev/null 2>&1 || true
		systemctl disable aslcp.service aslcp-helper.socket >/dev/null 2>&1 || true
	fi
	remove_firewalld_rule
	;;
upgrade | failed-upgrade) ;;
*)
	echo "prerm called with unknown argument '$1'" >&2
	exit 1
	;;
esac

exit 0
