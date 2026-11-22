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

# The digital voice engine runs as its own user, so it never gets access to
# /var/lib/aslcp. The aslcp user joins its group to reach the control socket
# (mode 0660, /run/aslcp-dv/ctl.sock).
configure_dv_user() {
	if ! getent passwd aslcp-dv >/dev/null 2>&1; then
		if getent group aslcp-dv >/dev/null 2>&1; then
			adduser --system --ingroup aslcp-dv --home /var/lib/aslcp-dv --no-create-home \
				--gecos "ASLCP digital voice engine" --shell /usr/sbin/nologin aslcp-dv
		else
			adduser --system --group --home /var/lib/aslcp-dv --no-create-home \
				--gecos "ASLCP digital voice engine" --shell /usr/sbin/nologin aslcp-dv
		fi
	fi
	if ! id -nG aslcp | tr ' ' '\n' | grep -qx aslcp-dv; then
		adduser aslcp aslcp-dv >/dev/null
		GROUP_CHANGED=1
	fi
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
	# The setup hotspot watchdog (it does nothing without NetworkManager and
	# a WiFi device). Enabled on first install only, so an admin who disabled
	# the timer keeps it off; started on every configure to run the new code.
	if [ -n "$FIRST_INSTALL" ]; then
		systemctl enable aslcp-wifi-watchdog.timer >/dev/null 2>&1 || true
	fi
	if systemctl is-enabled aslcp-wifi-watchdog.timer >/dev/null 2>&1; then
		systemctl restart aslcp-wifi-watchdog.timer >/dev/null 2>&1 || true
	fi
	# The engine is never enabled here (the web UI does that); a node that
	# enabled it runs the new binary after an upgrade.
	if systemctl is-enabled aslcp-dv.service >/dev/null 2>&1; then
		systemctl restart aslcp-dv.service >/dev/null 2>&1 || echo "aslcp: warning: could not restart aslcp-dv.service (see: journalctl -u aslcp-dv)"
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

# conf_port: TCP port of "listen" in the [server] section of aslcp.conf
# (":8080" or "addr:port"); 8080 when unset or invalid.
conf_port() {
	l=$(awk '
		/^[ \t]*\[/ { sec = ($0 ~ /^[ \t]*\[server\][ \t]*$/); next }
		sec && /^[ \t]*[;#]/ { next }
		sec { i = index($0, "="); if (i == 0) next
			k = substr($0, 1, i - 1); gsub(/[ \t]/, "", k)
			if (k == "listen") { val = substr($0, i + 1); sub(/[ \t]*[;#].*$/, "", val); gsub(/^[ \t]+|[ \t]+$/, "", val); out = val } }
		END { print out }' "${ASLCP_CONF:-/etc/aslcp/aslcp.conf}" 2>/dev/null || true)
	p=${l##*:}
	case "$p" in
	'' | *[!0-9]* | 0*) p=8080 ;;
	esac
	[ "${#p}" -le 5 ] && [ "$p" -le 65535 ] || p=8080
	printf '%s\n' "$p"
}

# Port 8080 uses the packaged firewalld service "aslcp"; any other port is
# opened directly (--add-port).
configure_firewalld() {
	command -v firewall-cmd >/dev/null 2>&1 || return 0
	port=$(conf_port)
	if [ "$port" = 8080 ]; then
		fw_query=--query-service=aslcp fw_add=--add-service=aslcp fw_what="service 'aslcp'"
	else
		fw_query=--query-port=$port/tcp fw_add=--add-port=$port/tcp fw_what="port"
	fi
	sum=$(fw_service_sum)
	# Fast path on upgrade (1 firewall-cmd call, no reload): the rule is
	# already active (port 8080: and the service definition is unchanged).
	if [ -z "$FIRST_INSTALL" ] && firewall-cmd "$fw_query" >/dev/null 2>&1; then
		if [ "$port" != 8080 ]; then
			return 0
		elif [ -n "$sum" ] && [ "$sum" = "$(cat "$FW_SUM" 2>/dev/null)" ]; then
			return 0
		fi
	fi
	firewall-cmd --state >/dev/null 2>&1 || return 0
	# Reload so firewalld knows the service definition shipped by this package.
	firewall-cmd --reload >/dev/null 2>&1 || true
	if firewall-cmd --permanent "$fw_query" >/dev/null 2>&1; then
		[ -z "$sum" ] || echo "$sum" >"$FW_SUM" 2>/dev/null || true
		return 0
	fi
	if firewall-cmd --permanent "$fw_add" >/dev/null 2>&1 && firewall-cmd --reload >/dev/null 2>&1; then
		touch "$FW_MARKER"
		[ -z "$sum" ] || echo "$sum" >"$FW_SUM" 2>/dev/null || true
		echo "aslcp: firewalld is running: opened TCP $port ($fw_what, permanent)."
	else
		echo "aslcp: warning: firewalld is running but opening TCP $port failed;"
		echo "aslcp:   run: firewall-cmd --permanent $fw_add && firewall-cmd --reload"
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
	port=$(conf_port)
	if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
		G=$(printf '\033[1;32m') Y=$(printf '\033[1;33m') R=$(printf '\033[0m')
	else
		G='' Y='' R=''
	fi
	printf '\n%saslcp: installed.%s\n' "$G" "$R"
	if [ -n "$code" ]; then
		printf 'aslcp: open the one-time setup link to create the administrator or restore a backup:\n'
		printf '    %shttp://%s:%s/#/setup?code=%s%s\n' "$Y" "$ip" "$port" "$code" "$R"
		echo "aslcp: show the code again:   sudo aslcp admin setup-code"
	else
		echo "aslcp: open http://$ip:$port and get the setup code with:   sudo aslcp admin setup-code"
	fi
	echo "aslcp: or create the admin in the terminal:   sudo aslcp admin create"
	echo
}

case "$1" in
configure)
	[ -n "$2" ] || FIRST_INSTALL=1
	migrate_urls
	configure_user
	configure_dv_user
	configure_asterisk_access
	prepare_services
	configure_firewalld
	restart_app
	# install.sh prints the setup link itself once everything (port, digital modes)
	# is done, so it sets ASLCP_INSTALLER=1 to keep this one from showing early.
	if [ -z "$2" ] && [ -z "${ASLCP_INSTALLER:-}" ]; then
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
