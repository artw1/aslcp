#!/bin/sh
# ASLCP installer: adds the signed apt repository and installs the package.
#   curl -fsSL https://artw1.github.io/aslcp/install.sh | sudo sh
# Run as root. Safe to run again (it also acts as an upgrade).
# Update channel: stable (default). ASLCP_CHANNEL=beta selects the beta suite;
# without it, the channel of an existing install is kept.
# Web port: 8080 by default. ASLCP_PORT=<1024-65535> picks another one:
#   curl -fsSL https://artw1.github.io/aslcp/install.sh | sudo ASLCP_PORT=8181 sh
# After the package is installed, the "listen" line in /etc/aslcp/aslcp.conf is
# rewritten (an address part is kept), aslcp is restarted and the port is opened
# in firewalld (if it is running). Without ASLCP_PORT the configured port is kept.
# The installer warns when another program already uses the port.
# DVSwitch: ASLCP_DVSWITCH=1 (or yes, true) also installs DVSwitch once ASLCP is
# running, with "aslcp dvswitch install" (the same install as the DVSwitch page;
# it takes a few minutes and follows the log). Any other non-empty value is
# refused. If that install fails the installer warns and still finishes; run
# "sudo aslcp dvswitch install" later to retry. It combines with ASLCP_PORT:
#   curl -fsSL https://artw1.github.io/aslcp/install.sh | sudo ASLCP_DVSWITCH=1 ASLCP_PORT=8181 sh
# Pre-flight: before anything is changed the installer refuses to run while
# ASL3 is still finishing its first-boot setup (systemd still starting, or
# asl3-boot-oneshot.service / resize2fs_once.service running) or while another
# package manager (apt, apt-get, dpkg, unattended-upgrades, aptitude) runs or
# holds the dpkg lock: a fresh ASL3 image installs updates and reboots by
# itself, which would cut an install in half. Wait 20-30 minutes after the
# first boot and run it again. ASLCP_SKIP_PREFLIGHT=1 skips this check. A
# pending reboot (/var/run/reboot-required) only produces a warning.
set -eu

# Base URL of the apt repository (GitHub Pages of the public repo).
APT_URL="${ASLCP_APT_URL:-https://artw1.github.io/aslcp}"
KEYRING=/usr/share/keyrings/aslcp-archive-keyring.gpg
LIST=/etc/apt/sources.list.d/aslcp.list

say() { printf '%s\n' "aslcp-install: $*"; }
die() { printf '%s\n' "aslcp-install: error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (for example: curl -fsSL $APT_URL/install.sh | sudo sh)"

# --- web port (ASLCP_PORT) -------------------------------------------------
PORT_WANT=${ASLCP_PORT:-}
if [ -n "$PORT_WANT" ]; then
	case "$PORT_WANT" in
	*[!0-9]* | 0*) die "ASLCP_PORT must be a number from 1024 to 65535 without leading zeros (got '$PORT_WANT')" ;;
	esac
	{ [ "${#PORT_WANT}" -le 5 ] && [ "$PORT_WANT" -le 65535 ]; } || die "ASLCP_PORT must be a number from 1024 to 65535 (got '$PORT_WANT')"
	# The service runs without capabilities, so it cannot listen below 1024
	# (port 80: docs/PORT80.md).
	[ "$PORT_WANT" -ge 1024 ] || die "ASLCP_PORT must be 1024 or higher: ASLCP runs without privileges and cannot listen on port $PORT_WANT (got '$PORT_WANT')"
fi

# --- DVSwitch (ASLCP_DVSWITCH) ----------------------------------------------
DV_WANT=0
case "${ASLCP_DVSWITCH:-}" in
"") ;;
1 | [Yy][Ee][Ss] | [Tt][Rr][Uu][Ee]) DV_WANT=1 ;;
*) die "ASLCP_DVSWITCH must be 1, yes or true (got '$ASLCP_DVSWITCH')" ;;
esac

# --- pre-flight: first-boot setup / other package managers ------------------
# preflight_busy: prints what is busy (one line) and succeeds, or fails when
# nothing is. Read-only; changes nothing.
preflight_busy() {
	if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
		st=$(systemctl is-system-running 2>/dev/null || true)
		if [ "$st" = starting ]; then
			printf '%s\n' "the system is still starting up (systemctl is-system-running: starting)"
			return 0
		fi
		for u in asl3-boot-oneshot.service resize2fs_once.service; do
			st=$(systemctl is-active "$u" 2>/dev/null || true)
			if [ "$st" = activating ]; then
				printf '%s\n' "$u"
				return 0
			fi
		done
	fi
	for n in apt apt-get dpkg unattended-upgrade unattended-upgrades aptitude; do
		if command -v pgrep >/dev/null 2>&1; then
			pids=$(pgrep -x "$n" 2>/dev/null | tr '\n' ' ' || true)
		elif command -v pidof >/dev/null 2>&1; then
			pids=$(pidof "$n" 2>/dev/null || true)
		else
			pids=
		fi
		pids=${pids%"${pids##*[! ]}"}
		if [ -n "$pids" ]; then
			printf '%s\n' "$n (pid ${pids%% *})"
			return 0
		fi
	done
	if command -v fuser >/dev/null 2>&1; then
		for f in /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock; do
			if [ -e "$f" ] && fuser "$f" >/dev/null 2>&1; then
				printf '%s\n' "the dpkg lock $f is held"
				return 0
			fi
		done
	fi
	return 1
}

if [ "${ASLCP_SKIP_PREFLIGHT:-}" != 1 ]; then
	if busy=$(preflight_busy); then
		hint=
		case "$busy" in
		*"pid "* | *"dpkg lock"*) hint=" If you have an apt prompt open in another terminal, finish it first." ;;
		esac
		die "ASL3 is still finishing its first-boot setup or another package manager is running ($busy). It installs updates and may reboot the node. Wait until it is done (usually 20-30 minutes after the first boot), then run this installer again.$hint (To skip this check: ASLCP_SKIP_PREFLIGHT=1.)"
	fi
fi
if [ -e /var/run/reboot-required ]; then
	say "warning: a reboot is pending (updates were installed); reboot first if anything misbehaves"
fi

# --- platform checks -------------------------------------------------------
[ -r /etc/os-release ] || die "cannot read /etc/os-release; this installer supports Debian 12 (bookworm), Debian 13 (trixie) and Raspberry Pi OS"
# shellcheck disable=SC1091
. /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
*" debian "* | *" raspbian "*) ;;
*) die "unsupported OS '${PRETTY_NAME:-unknown}'; ASLCP packages target Debian 12 (bookworm) or 13 (trixie)" ;;
esac
case "${VERSION_CODENAME:-}" in
bookworm | trixie) ;;
*) say "warning: this is '${VERSION_CODENAME:-unknown}', the packages are built for Debian 12 (bookworm) or 13 (trixie); continuing" ;;
esac
command -v dpkg >/dev/null 2>&1 || die "dpkg not found"
ARCH=$(dpkg --print-architecture)
case "$ARCH" in
arm64 | armhf | amd64) ;;
*) die "unsupported architecture '$ARCH' (supported: arm64, armhf, amd64)" ;;
esac

# --- downloader ------------------------------------------------------------
fetch() { # fetch URL DEST
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget (apt-get install curl)"
	fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# --- signing key -----------------------------------------------------------
say "downloading the repository key"
fetch "$APT_URL/aslcp-archive-keyring.gpg" "$tmp/keyring.gpg" || die "could not download $APT_URL/aslcp-archive-keyring.gpg"
[ -s "$tmp/keyring.gpg" ] || die "downloaded key is empty"
if head -c 64 "$tmp/keyring.gpg" | grep -q 'BEGIN PGP'; then
	die "downloaded key is ASCII-armored, expected a binary keyring"
fi
install -d -m 0755 /usr/share/keyrings
install -m 0644 "$tmp/keyring.gpg" "$KEYRING"

# --- apt source ------------------------------------------------------------
SUITE=${ASLCP_CHANNEL:-}
case "$SUITE" in
"" | stable | beta) ;;
*) die "ASLCP_CHANNEL must be 'stable' or 'beta'" ;;
esac
if [ -z "$SUITE" ] && [ -r "$LIST" ]; then
	# keep the channel chosen earlier (Settings > Updates or an earlier install)
	SUITE=$(awk '$1 == "deb" && ($(NF-1) == "stable" || $(NF-1) == "beta") { print $(NF-1); exit }' "$LIST")
fi
[ -n "$SUITE" ] || SUITE=stable
printf '%s\n' "deb [signed-by=$KEYRING] $APT_URL $SUITE main" >"$tmp/aslcp.list"
if ! cmp -s "$tmp/aslcp.list" "$LIST" 2>/dev/null; then
	install -m 0644 "$tmp/aslcp.list" "$LIST"
	say "wrote $LIST"
fi

# --- install ---------------------------------------------------------------
say "updating package lists (aslcp source only)"
apt-get update -o DPkg::Lock::Timeout=600 \
	-o Dir::Etc::sourcelist=sources.list.d/aslcp.list \
	-o Dir::Etc::sourceparts=- \
	-o APT::Get::List-Cleanup=0
say "installing aslcp"
# --force-confold: keep the edited /etc/aslcp/aslcp.conf (for example a changed
# port) if a later package ships a different default, instead of prompting.
# ASLCP_INSTALLER=1: the package skips its own "installed" banner; the setup link is printed once at the end.
ASLCP_INSTALLER=1 DEBIAN_FRONTEND=noninteractive apt-get install -y -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confold aslcp

# --- first-run setup link ---------------------------------------------------
# ---8<--- admin-setup (exercised by a local test harness; keep self-contained)
CONF="${ASLCP_CONF:-/etc/aslcp/aslcp.conf}"

# conf_get KEY DEFAULT: value of KEY in the [server] section of the config.
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

# Host to show in URLs: a specific listen address, else the first host address.
pick_host() { # pick_host LISTEN
	h=${1%:*}
	case "$h" in
	"" | "0.0.0.0" | "[::]" | "::" | "*") h= ;;
	esac
	if [ -z "$h" ]; then
		h=$(hostname -I 2>/dev/null | awk '{print $1}') || h=
		[ -n "$h" ] || h=$(hostname 2>/dev/null) || h=
		[ -n "$h" ] || h=localhost
	fi
	printf '%s\n' "$h"
}

# port_owner PORT: name of the program listening on TCP PORT ("another
# program" when ss cannot tell), empty when the port is free or ss is missing.
port_owner() {
	command -v ss >/dev/null 2>&1 || return 0
	l=$(ss -ltnpH "sport = :$1" 2>/dev/null || true)
	[ -n "$l" ] || return 0
	n=$(printf '%s\n' "$l" | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | head -n 1)
	printf '%s\n' "${n:-another program}"
}

# warn_port_clash PORT: warning when a program other than aslcp holds PORT.
warn_port_clash() {
	o=$(port_owner "$1")
	case "$o" in
	"" | aslcp) return 0 ;;
	esac
	printf '\naslcp-install: WARNING: TCP port %s is already in use by "%s", so ASLCP cannot listen there.\n' "$1" "$o"
	printf '  Pick another port: edit "listen" in %s (for example  listen = :8181 ),\n' "$CONF"
	printf '  then  sudo systemctl restart aslcp ,  or run the installer again with ASLCP_PORT=<port>.\n'
}

# no_users: succeeds when "aslcp admin list --quiet" exits 3 (no accounts).
no_users() { aslcp admin list --quiet --config "$CONF" >/dev/null 2>&1 && return 1 || [ $? -eq 3 ]; }

# setup_banner LINK: boxed one-time setup link. Colors only on a terminal.
setup_banner() {
	if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
		B=$(printf '\033[1;36m') G=$(printf '\033[1;32m') Y=$(printf '\033[1;33m') R=$(printf '\033[0m')
	else
		B='' G='' Y='' R=''
	fi
	bar='================================================================'
	printf '\n%s%s%s\n' "$B" "$bar" "$R"
	printf '%s  ASLCP is installed. One more step: open this link%s\n' "$G" "$R"
	printf '\n    %s%s%s\n\n' "$Y" "$1" "$R"
	printf '  The one-time link lets you create the administrator, or\n'
	printf '  restore a backup from an existing node.\n\n'
	printf '  Show the code again:  sudo aslcp admin setup-code\n'
	printf '  Create the admin in the terminal instead:  sudo aslcp admin create\n'
	printf '%s%s%s\n' "$B" "$bar" "$R"
}

setup_admin() {
	listen=$(conf_get listen :8080)
	datadir=$(conf_get datadir /var/lib/aslcp)
	port=${listen##*:}
	case "$port" in
	'' | *[!0-9]*) port=8080 ;;
	esac
	addr=$(pick_host "$listen")
	url="http://$addr:$port"

	# The service creates the data directory and database on its first start.
	n=0
	while [ ! -f "$datadir/aslcp.db" ] && [ "$n" -lt "${ASLCP_WAIT:-10}" ]; do
		sleep 1
		n=$((n + 1))
	done

	up=1
	[ -f "$datadir/aslcp.db" ] || up=0
	if [ "$up" -eq 1 ] && [ -d /run/systemd/system ] && ! systemctl is-active --quiet aslcp 2>/dev/null; then
		up=0
	fi
	warn_port_clash "$port"
	if [ "$up" -eq 0 ]; then
		printf '\naslcp-install: WARNING: ASLCP is not running (no database yet, or the service is not active).\n'
		printf '  See why:  sudo systemctl status aslcp   and   journalctl -u aslcp -n 30\n'
		printf '  Once it runs, show the setup link code:  sudo aslcp admin setup-code\n'
		return 0
	fi

	if no_users; then
		n=0
		while [ ! -s "$datadir/setup-code" ] && [ "$n" -lt "${ASLCP_WAIT:-10}" ]; do
			sleep 1
			n=$((n + 1))
		done
		code=$(head -n 1 "$datadir/setup-code" 2>/dev/null || true)
		if [ -n "$code" ]; then
			setup_banner "$url/#/setup?code=$code"
		else
			printf '\nNo administrator account yet. Run:  sudo aslcp admin setup-code\n'
		fi
	fi

	printf '\nASLCP is running: %s\n' "$url"
	printf 'Lost the password? Run:  sudo aslcp admin reset-password <user>\n'
}
# --->8--- admin-setup

# set_port PORT: point "listen" in the config at PORT (an address part is
# kept), restart aslcp and open the port in firewalld.
set_port() {
	cur=$(conf_get listen :8080)
	curport=${cur##*:}
	[ "$curport" != "$1" ] || return 0
	say "setting the web port to $1 (was $curport)"
	o=$(port_owner "$1")
	case "$o" in
	"" | aslcp) ;;
	*) say "warning: TCP port $1 is already in use by \"$o\"" ;;
	esac
	case "$cur" in
	*:*) addr=${cur%:*} ;;
	*) addr= ;;
	esac
	new="listen = $addr:$1"
	[ -f "$CONF" ] || die "$CONF not found; set listen = $addr:$1 in it yourself"
	awk -v new="$new" '
		/^[ \t]*\[/ { sec = ($0 ~ /^[ \t]*\[server\][ \t]*$/) }
		sec && !/^[ \t]*[;#]/ && /^[ \t]*listen[ \t]*=/ { print new; next }
		{ print }' "$CONF" >"$tmp/aslcp.conf.new"
	# Write in place (keeps owner and mode); if no listen line was found,
	# append one (a later [server] section wins).
	cat "$tmp/aslcp.conf.new" >"$CONF"
	if [ "$(conf_get listen '')" != "$addr:$1" ]; then
		printf '\n[server]\n%s\n' "$new" >>"$CONF"
	fi
	if [ -d /run/systemd/system ]; then
		systemctl restart aslcp || say "warning: could not restart aslcp (see: journalctl -u aslcp)"
	else
		say "systemd is not running here; restart aslcp yourself to use the new port"
	fi
	fw_open "$1"
}

# fw_open PORT: open PORT in firewalld, if it is running: the packaged service
# "aslcp" for 8080, else the port itself. Other firewalls are left alone.
fw_open() {
	command -v firewall-cmd >/dev/null 2>&1 || return 0
	firewall-cmd --state >/dev/null 2>&1 || return 0
	if [ "$1" = 8080 ]; then
		q=--query-service=aslcp a=--add-service=aslcp
	else
		q=--query-port=$1/tcp a=--add-port=$1/tcp
	fi
	firewall-cmd --permanent "$q" >/dev/null 2>&1 && return 0
	if firewall-cmd --permanent "$a" >/dev/null 2>&1 && firewall-cmd --reload >/dev/null 2>&1; then
		say "firewalld: opened TCP $1 (permanent)"
	else
		say "warning: could not open TCP $1 in firewalld; run: firewall-cmd --permanent $a && firewall-cmd --reload"
	fi
}

# dvswitch_install: run "aslcp dvswitch install" once the service is running.
# Sets DV_RESULT for the closing lines; a failure only warns.
DV_RESULT=
dvswitch_install() {
	say "installing DVSwitch (this takes a few minutes)"
	n=0
	if [ -d /run/systemd/system ]; then
		while ! systemctl is-active --quiet aslcp 2>/dev/null && [ "$n" -lt "${ASLCP_WAIT:-10}" ]; do
			sleep 1
			n=$((n + 1))
		done
	fi
	if aslcp dvswitch install --config "$CONF"; then
		DV_RESULT="installed. Set it up on the DVSwitch page of the web UI."
	else
		DV_RESULT="NOT installed (see the messages above). ASLCP itself is installed; retry with: sudo aslcp dvswitch install"
		say "warning: the DVSwitch install failed; continuing"
	fi
}

[ -z "$PORT_WANT" ] || set_port "$PORT_WANT"
# DVSwitch goes first so the setup link stays the last thing on the screen.
[ "$DV_WANT" -eq 0 ] || dvswitch_install
setup_admin
if [ -n "$DV_RESULT" ]; then
	printf '\nDVSwitch: %s\n' "$DV_RESULT"
fi

cat <<EOM

aslcp-install: done.
  Configuration:  /etc/aslcp/aslcp.conf     Logs:  journalctl -u aslcp
  Updates:  Settings > Updates in the web UI, or: sudo apt update && sudo apt install --only-upgrade aslcp
EOM
