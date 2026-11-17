#!/bin/sh
# postinst for aslcp. POSIX sh; safe to run repeatedly.
set -e

DATADIR=/var/lib/aslcp
MANAGER_CONF=/etc/asterisk/manager.conf
FW_MARKER="$DATADIR/.firewalld-rule-added"
FW_SUM="$DATADIR/.firewalld-service-sum"
FW_SERVICE_FILE=/usr/lib/firewalld/services/aslcp.xml

# Timing notes (Raspberry Pi 3): every firewall-cmd call starts Python
# (~1 s), a firewalld reload takes several seconds, runuser/su start a PAM
# session. An upgrade therefore skips everything that is already in place and
# restarts aslcp.service LAST, so the web interface is back as early as
# possible and the upgrade's own journal stays short.
FIRST_INSTALL=
GROUP_CHANGED=

have_systemd() { [ -d /run/systemd/system ]; }

configure_user() {
	if ! getent passwd aslcp >/dev/null 2>&1; then
		if getent group aslcp >/dev/null 2>&1; then
			adduser --system --ingroup aslcp --home "$DATADIR" --no-create-home \
				--gecos "ASLCP web control panel" --shell /usr/sbin/nologin aslcp
		else
			adduser --system --group --home "$DATADIR" --no-create-home \
				--gecos "ASLCP web control panel" --shell /usr/sbin/nologin aslcp
		fi
	fi
	install -d -m 0750 -o aslcp -g aslcp "$DATADIR"
}

configure_asterisk_access() {
	getent group asterisk >/dev/null 2>&1 || return 0
	if ! id -nG aslcp | tr ' ' '\n' | grep -qx asterisk; then
		adduser aslcp asterisk >/dev/null
		GROUP_CHANGED=1
		echo "aslcp: added user aslcp to group asterisk"
	fi
	[ -e "$MANAGER_CONF" ] || return 0
	# The readability check starts a PAM session (slow on a Pi): only on a
	# first install or when the group membership just changed.
	[ -n "$FIRST_INSTALL" ] || [ -n "$GROUP_CHANGED" ] || return 0
	# Test as the aslcp user (fresh group list, directory traversal included).
	if command -v runuser >/dev/null 2>&1; then
		if runuser -u aslcp -- test -r "$MANAGER_CONF" 2>/dev/null; then
			return 0
		fi
	elif su -s /bin/sh aslcp -c "test -r '$MANAGER_CONF'" 2>/dev/null; then
		return 0
	fi
	cat <<EOM
aslcp: WARNING: $MANAGER_CONF is not readable by user aslcp, so ASLCP cannot
aslcp: read the AMI credentials ("auto"). Permissions were NOT changed. To fix it:
aslcp:     chgrp asterisk $MANAGER_CONF && chmod 640 $MANAGER_CONF
aslcp:     systemctl restart aslcp
aslcp: or set explicit credentials in the [ami] section of /etc/aslcp/aslcp.conf.
EOM
}

# Everything the restart of aslcp.service depends on. daemon-reload is fast
# and needed in case unit files changed.
prepare_services() {
	have_systemd || return 0
	systemctl daemon-reload || true
	if [ -n "$FIRST_INSTALL" ]; then
		systemctl enable aslcp-helper.socket aslcp.service >/dev/null 2>&1 || true
	fi
	# Restart (not just start) so an upgrade runs the new helper binary.
	systemctl stop aslcp-helper.service >/dev/null 2>&1 || true
	systemctl restart aslcp-helper.socket || echo "aslcp: warning: could not start aslcp-helper.socket"
}

# The LAST step: the web interface is down only for the restart itself.
restart_app() {
	have_systemd || return 0
	systemctl restart aslcp.service || echo "aslcp: warning: could not start aslcp.service (see: journalctl -u aslcp)"
}

fw_service_sum() {
	cksum <"$FW_SERVICE_FILE" 2>/dev/null | awk '{print $1 ":" $2}'
}

configure_firewalld() {
	command -v firewall-cmd >/dev/null 2>&1 || return 0
	sum=$(fw_service_sum)
	# Fast path on upgrade (1 firewall-cmd call, no reload): the service
	# definition is unchanged and the rule is already active.
	if [ -z "$FIRST_INSTALL" ] && [ -n "$sum" ] && [ "$sum" = "$(cat "$FW_SUM" 2>/dev/null)" ] &&
		firewall-cmd --query-service=aslcp >/dev/null 2>&1; then
		return 0
	fi
	firewall-cmd --state >/dev/null 2>&1 || return 0
	# Reload so firewalld knows the service definition shipped by this package.
	firewall-cmd --reload >/dev/null 2>&1 || true
	if firewall-cmd --permanent --query-service=aslcp >/dev/null 2>&1; then
		[ -z "$sum" ] || echo "$sum" >"$FW_SUM" 2>/dev/null || true
		return 0
	fi
	if firewall-cmd --permanent --add-service=aslcp >/dev/null 2>&1 && firewall-cmd --reload >/dev/null 2>&1; then
		touch "$FW_MARKER"
		[ -z "$sum" ] || echo "$sum" >"$FW_SUM" 2>/dev/null || true
		echo "aslcp: firewalld is running: opened TCP 8080 (service 'aslcp', permanent)."
	else
		echo "aslcp: warning: firewalld is running but opening the 'aslcp' service failed;"
		echo "aslcp:   run: firewall-cmd --permanent --add-service=aslcp && firewall-cmd --reload"
	fi
}

# ---8<--- migrate-urls (the public repo moved from artw1/aslcp-apt to artw1/aslcp)
# Idempotent. Only the exact old URL is rewritten; the signed-by option stays.
migrate_urls() {
	list=${ASLCP_APT_LIST:-/etc/apt/sources.list.d/aslcp.list}
	conf=${ASLCP_CONF:-/etc/aslcp/aslcp.conf}
	if [ -f "$list" ] && grep -q 'artw1\.github\.io/aslcp-apt' "$list"; then
		sed -i 's#artw1\.github\.io/aslcp-apt#artw1.github.io/aslcp#g' "$list"
		echo "aslcp: note: the apt source $list now points to https://artw1.github.io/aslcp (the repository moved)."
	fi
	if [ -f "$conf" ] && grep -Eq '^[[:space:]]*url[[:space:]]*=[[:space:]]*https://artw1\.github\.io/aslcp-apt/?[[:space:]]*$' "$conf"; then
		sed -i -E 's#^([[:space:]]*url[[:space:]]*=[[:space:]]*)https://artw1\.github\.io/aslcp-apt/?[[:space:]]*$#\1https://artw1.github.io/aslcp#' "$conf"
		echo "aslcp: note: updated the [updates] url in $conf to https://artw1.github.io/aslcp (the repository moved)."
	fi
}
# --->8--- migrate-urls

first_install_message() {
	ip=$(hostname -I 2>/dev/null | awk '{print $1}')
	[ -n "$ip" ] || ip=$(hostname 2>/dev/null || echo localhost)
	code=
	n=0
	while [ "$n" -lt 5 ] && [ ! -s "$DATADIR/setup-code" ]; do
		sleep 1
		n=$((n + 1))
	done
	code=$(head -n 1 "$DATADIR/setup-code" 2>/dev/null || true)
	cat <<EOM

aslcp: installed. Open   http://$ip:8080
aslcp: Create the admin account:   sudo aslcp admin create
EOM
	if [ -n "$code" ]; then
		echo "aslcp: or use the one-time setup link:   http://$ip:8080/#/setup?code=$code"
	fi
	echo
}

case "$1" in
configure)
	[ -n "$2" ] || FIRST_INSTALL=1
	migrate_urls
	configure_user
	configure_asterisk_access
	prepare_services
	configure_firewalld
	restart_app
	if [ -z "$2" ]; then
		first_install_message
	fi
	;;
abort-upgrade | abort-remove | abort-deconfigure) ;;
*)
	echo "postinst called with unknown argument '$1'" >&2
	exit 1
	;;
esac

exit 0
