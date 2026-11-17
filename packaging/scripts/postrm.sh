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
