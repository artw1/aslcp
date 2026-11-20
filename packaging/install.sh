#!/bin/sh
# ASLCP installer: adds the signed apt repository and installs the package.
#   curl -fsSL https://artw1.github.io/aslcp/install.sh | sudo sh
# Run as root. Safe to run again (it also acts as an upgrade).
# Update channel: stable (default). ASLCP_CHANNEL=beta selects the beta suite;
# without it, the channel of an existing install is kept.
set -eu

# Base URL of the apt repository (GitHub Pages of the public repo).
APT_URL="${ASLCP_APT_URL:-https://artw1.github.io/aslcp}"
KEYRING=/usr/share/keyrings/aslcp-archive-keyring.gpg
LIST=/etc/apt/sources.list.d/aslcp.list

say() { printf '%s\n' "aslcp-install: $*"; }
die() { printf '%s\n' "aslcp-install: error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (for example: curl -fsSL $APT_URL/install.sh | sudo sh)"

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
apt-get update \
	-o Dir::Etc::sourcelist=sources.list.d/aslcp.list \
	-o Dir::Etc::sourceparts=- \
	-o APT::Get::List-Cleanup=0
say "installing aslcp"
DEBIAN_FRONTEND=noninteractive apt-get install -y aslcp

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

setup_admin

cat <<EOM

aslcp-install: done.
  Configuration:  /etc/aslcp/aslcp.conf     Logs:  journalctl -u aslcp
  Updates:  Settings > Updates in the web UI, or: sudo apt update && sudo apt install --only-upgrade aslcp
EOM
