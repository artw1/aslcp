#!/bin/sh
# postrm for aslcp. POSIX sh; safe to run repeatedly.
set -e

case "$1" in
remove)
	if [ -d /run/systemd/system ]; then
		systemctl daemon-reload || true
	fi
	;;
purge)
	if [ -d /run/systemd/system ]; then
		systemctl daemon-reload || true
	fi
	rm -rf /var/lib/aslcp /run/aslcp
	# The root-only state of the helper: the config history (git), the
	# DVSwitch install and bridge-node records, pending restored DVSwitch
	# settings and the DVSwitch ini history (both hold passwords).
	# /etc/asterisk and DVSwitch's own files are not touched.
	rm -rf /var/lib/aslcp-config-history /var/lib/aslcp-dvswitch /var/lib/aslcp-dvswitch-history
	# The hotspot setting (WiFi networks saved in NetworkManager stay).
	rm -rf /var/lib/aslcp-wifi
	rmdir /etc/aslcp 2>/dev/null || true
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
