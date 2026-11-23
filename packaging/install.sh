#!/bin/sh
# ASLCP installer: adds the signed apt repository and installs the package.
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo sh
# Run as root. Safe to run again (it also acts as an upgrade).
# Update channel: stable (default). ASLCP_CHANNEL=beta selects the beta suite;
# without it, the channel of an existing install is kept.
# Web port: 8080 by default. ASLCP_PORT=<1024-65535, or 80> picks another one:
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_PORT=8181 sh
# Port 80 is the only port below 1024 that is accepted. ASLCP runs without
# privileges, so for port 80 the installer writes the systemd drop-in
# /etc/systemd/system/aslcp.service.d/port80.conf (AmbientCapabilities and
# CapabilityBoundingSet = CAP_NET_BIND_SERVICE); default installs stay as they
# are. It refuses to switch while another program holds port 80 (and names it).
# Run ASLCP on port 80 and turn off the other ASL web services:
# ASLCP_TAKEOVER=1 (or yes, true) implies ASLCP_PORT=80 unless ASLCP_PORT is
# given, and stops and disables the services that would compete for the web
# ports or duplicate the panel, whichever of them exist and are enabled or
# running: Allmon3 (allmon3), the web server of the ASL web UIs (apache2,
# lighttpd or nginx), the asl-web-ui backend and Supermon, if they run as
# services. It prints each unit it turned off and records them in
# /var/lib/aslcp/takeover.list. Asterisk, app_rpt and everything else the node
# needs to stay on the air are never touched, nor is Cockpit (port 9090).
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_TAKEOVER=1 sh
# Undo (ASLCP goes back to its previous port and the recorded units are
# enabled and started again; nothing is installed or removed):
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_TAKEOVER=undo sh
# Any other non-empty value is refused.
# After the package is installed, the "listen" line in /etc/aslcp/aslcp.conf is
# rewritten (an address part is kept), aslcp is restarted and the port is opened
# in firewalld (if it is running). Without ASLCP_PORT the configured port is kept.
# The installer warns when another program already uses the port.
# Digital modes: ASLCP_DV=1 (or yes, true) also enables digital modes once ASLCP
# is running, with "aslcp dv enable" (the same job as the Enable button of the
# digital modes page; it takes a few minutes and follows the log). Any other
# non-empty value is refused. If
# the job fails the installer warns and still finishes; run "sudo aslcp dv
# enable" later to retry. It combines with ASLCP_PORT:
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_DV=1 ASLCP_PORT=8181 sh
# 64-bit only: arm64 and amd64. On a 32-bit system (dpkg architecture armhf,
# including a 32-bit userland on a 64-bit kernel) the installer refuses before
# anything is changed; reinstall with Raspberry Pi OS Lite (64-bit).
# AllStarLink 3: ASLCP_ASL3 controls whether the installer installs ASL3 itself.
# auto (default, also 1, yes, true): if the asl3 package is missing, install
# asl3 and asl3-menu; if asl3 is there but asl3-menu is not, install asl3-menu.
# This happens before aslcp is installed. If ASL's apt repository is not set up
# yet, the installer downloads asl-apt-repos from https://repo.allstarlink.org/public/,
# checks its signing key against a pinned fingerprint and installs it, then runs
# apt-get update. It can take several minutes. allmon3 and the Pi appliance
# package are not installed. 0 (or no, false) never touches ASL3 (panel only).
# ASL3 exists for Debian 12/13 on arm64 and amd64. On armhf or another release
# the installer says so and carries on with a panel-only install, unless
# ASLCP_ASL3=1 (yes, true) was given: then it stops with an error. Any other
# value is refused.
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_ASL3=0 sh
# ASL3 telemetry: ASL3 ships asl-telemetry.timer, which once a day sends
# AllStarLink an anonymous usage report (asl-telemetry.service). ASLCP leaves
# it as it is by default. ASLCP_ASL_TELEMETRY=off (also 0, no, false) turns it
# off: "systemctl disable --now asl-telemetry.timer" and "systemctl mask" of the
# timer and the service, if they exist (otherwise it says there is nothing to
# turn off). ASLCP_ASL_TELEMETRY=on (also 1, yes, true) undoes that (unmask and
# "enable --now" of the timer). Unset or empty changes nothing. It runs right
# after the ASL3 step, so it also works when ASL3 was just installed. A failure
# only produces a warning. Any other value is refused, and so is a combination
# with ASLCP_TAKEOVER=undo (which does no package work and stops before this).
#   curl -fsSL https://w1jar.github.io/aslcp/install.sh | sudo ASLCP_ASL_TELEMETRY=off sh
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
APT_URL="${ASLCP_APT_URL:-https://w1jar.github.io/aslcp}"
KEYRING=/usr/share/keyrings/aslcp-archive-keyring.gpg
LIST=/etc/apt/sources.list.d/aslcp.list

say() { printf '%s\n' "aslcp-install: $*"; }
die() { printf '%s\n' "aslcp-install: error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (for example: curl -fsSL $APT_URL/install.sh | sudo sh)"

# ---8<--- arch-check (exercised by the Go tests in internal/pkgscripts; keep self-contained)
# check_arch: succeeds on arm64 and amd64 (the dpkg architecture, i.e. the
# userland); refuses everything else. A 32-bit userland on a 64-bit kernel
# reports armhf too. Read-only; changes nothing.
check_arch() {
	command -v dpkg >/dev/null 2>&1 || die "dpkg not found"
	ARCH=$(dpkg --print-architecture)
	case "$ARCH" in
	arm64 | amd64) ;;
	armhf | armel)
		die "this system is 32-bit ($ARCH, kernel $(uname -m 2>/dev/null || echo unknown)). ASL3 and ASLCP need a 64-bit OS: reinstall with Raspberry Pi OS Lite (64-bit), then install ASL3 and ASLCP again. Nothing was changed."
		;;
	*) die "unsupported architecture '$ARCH' (supported: arm64, amd64)" ;;
	esac
}
# --->8--- arch-check

# --- takeover (ASLCP_TAKEOVER) ----------------------------------------------
TAKEOVER_WANT=0
TAKEOVER_UNDO=0
TK_VAL="${ASLCP_TAKEOVER:-}"
case "$TK_VAL" in
"") ;;
1 | [Yy][Ee][Ss] | [Tt][Rr][Uu][Ee]) TAKEOVER_WANT=1 ;;
undo) TAKEOVER_UNDO=1 ;;
*) die "ASLCP_TAKEOVER must be 1, yes, true or undo (got '$TK_VAL')" ;;
esac

# --- web port (ASLCP_PORT) -------------------------------------------------
PORT_WANT=${ASLCP_PORT:-}
if [ -n "$PORT_WANT" ]; then
	case "$PORT_WANT" in
	*[!0-9]* | 0*) die "ASLCP_PORT must be a number from 1024 to 65535, or 80, without leading zeros (got '$PORT_WANT')" ;;
	esac
	{ [ "${#PORT_WANT}" -le 5 ] && [ "$PORT_WANT" -le 65535 ]; } || die "ASLCP_PORT must be a number from 1024 to 65535, or 80 (got '$PORT_WANT')"
	# The service runs without capabilities. Port 80 works through a systemd
	# drop-in this installer writes (see set_port); other low ports are refused.
	{ [ "$PORT_WANT" -ge 1024 ] || [ "$PORT_WANT" = 80 ]; } || die "ASLCP_PORT must be 1024 or higher, or exactly 80: ASLCP runs without privileges and cannot listen on port $PORT_WANT (got '$PORT_WANT')"
fi
if [ "$TAKEOVER_UNDO" -eq 1 ]; then
	[ "$PORT_WANT" != 80 ] || die "ASLCP_TAKEOVER=undo moves ASLCP off port 80; ASLCP_PORT can only name the port to go back to (1024-65535)"
elif [ "$TAKEOVER_WANT" -eq 1 ] && [ -z "$PORT_WANT" ]; then
	PORT_WANT=80
fi

# --- digital modes (ASLCP_DV) -------------------------------------------------
DV_WANT=0
DV_VAL="${ASLCP_DV:-}"
case "$DV_VAL" in
"") ;;
1 | [Yy][Ee][Ss] | [Tt][Rr][Uu][Ee]) DV_WANT=1 ;;
*) die "ASLCP_DV must be 1, yes or true (got '$DV_VAL')" ;;
esac
[ "$TAKEOVER_UNDO" -eq 0 ] || [ "$DV_WANT" -eq 0 ] || die "ASLCP_TAKEOVER=undo cannot be combined with ASLCP_DV"

# --- ASL3 (ASLCP_ASL3) -------------------------------------------------------
# ASL3_MODE: auto (install what is missing, carry on if unavailable), force
# (the same, but unavailable is an error) or skip.
ASL3_MODE=auto
case "${ASLCP_ASL3:-}" in
"" | [Aa][Uu][Tt][Oo]) ;;
1 | [Yy][Ee][Ss] | [Tt][Rr][Uu][Ee]) ASL3_MODE=force ;;
0 | [Nn][Oo] | [Ff][Aa][Ll][Ss][Ee]) ASL3_MODE=skip ;;
*) die "ASLCP_ASL3 must be auto, 1, yes, true, 0, no or false (got '${ASLCP_ASL3}')" ;;
esac

# --- ASL3 telemetry (ASLCP_ASL_TELEMETRY) ------------------------------------
# TELEM_MODE: empty (leave as is), off or on.
TELEM_MODE=
case "${ASLCP_ASL_TELEMETRY:-}" in
"") ;;
[Oo][Ff][Ff] | 0 | [Nn][Oo] | [Ff][Aa][Ll][Ss][Ee]) TELEM_MODE=off ;;
[Oo][Nn] | 1 | [Yy][Ee][Ss] | [Tt][Rr][Uu][Ee]) TELEM_MODE=on ;;
*) die "ASLCP_ASL_TELEMETRY must be off, 0, no, false, on, 1, yes or true (got '${ASLCP_ASL_TELEMETRY}')" ;;
esac
[ "$TAKEOVER_UNDO" -eq 0 ] || [ -z "$TELEM_MODE" ] || die "ASLCP_TAKEOVER=undo cannot be combined with ASLCP_ASL_TELEMETRY"

# --- architecture: 64-bit only (before anything is changed) -----------------
[ "$TAKEOVER_UNDO" -eq 1 ] || check_arch

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

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# ASLCP_TAKEOVER=undo needs no package work: it only restores what an earlier
# takeover turned off (see takeover_undo below).
if [ "$TAKEOVER_UNDO" -eq 0 ]; then
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

	# --- ASL3 and asl3-menu (ASLCP_ASL3) ---------------------------------------
	# ---8<--- asl3-install (exercised by the Go tests in internal/pkgscripts; keep self-contained)
	# Fingerprint of the ASL repository signing key: the key shipped in
	# asl-apt-repos (deb12 and deb13 carry the same one), uid "AllStarLink
	# Repository <repo@allstarlink.org>". Checked after asl-apt-repos is installed.
	ASL_KEY_FPR=F2AF416869932ABB39E165E5409D50181BD84509
	ASL_KEYRING="${ASLCP_ASL_KEYRING:-/etc/apt/keyrings/allstarlink.gpg}"
	ASL_REPO_URL=https://repo.allstarlink.org/public

	# pkg_installed PKG: succeeds when dpkg says PKG is fully installed.
	pkg_installed() {
		st=$(dpkg-query -W -f='${Status}' "$1" 2>/dev/null || true)
		[ "$st" = "install ok installed" ]
	}

	# asl3_unavailable WHY: ASL3 cannot be installed here. Fatal when it was
	# asked for explicitly, else the install goes on with the panel only.
	asl3_unavailable() {
		if [ "$ASL3_MODE" = force ]; then
			die "ASL3 is not available for this system ($1) and ASLCP_ASL3=1 was given"
		fi
		say "ASL3 is not available for this system ($1); installing the panel only."
		say "  (ASL3 needs Debian 12 or 13 on arm64 or amd64, for example Raspberry Pi OS 64-bit.)"
		return 0
	}

	asl3_ensure() {
		[ "$ASL3_MODE" != skip ] || return 0
		missing=
		pkg_installed asl3 || missing="asl3 asl3-menu"
		if [ -z "$missing" ] && ! pkg_installed asl3-menu; then
			missing=asl3-menu
		fi
		if [ -z "$missing" ]; then
			say "ASL3 and asl3-menu are already installed"
			return 0
		fi
		case "${VERSION_CODENAME:-}" in
		bookworm) asl_nn=12 ;;
		trixie) asl_nn=13 ;;
		*)
			asl3_unavailable "release '${VERSION_CODENAME:-unknown}'"
			return 0
			;;
		esac
		case "$ARCH" in
		arm64 | amd64) ;;
		*)
			asl3_unavailable "architecture $ARCH"
			return 0
			;;
		esac
		say "installing $missing; this can take several minutes and downloads many packages"
		if ! pkg_installed asl-apt-repos; then
			if ! command -v gpg >/dev/null 2>&1; then
				# gpg is not part of a minimal Debian/Raspberry Pi OS Lite (apt only needs
				# gpgv) but is required to check the repository key. Get it first so a
				# failure leaves nothing half-installed.
				say "gpg is not installed; installing it to verify the AllStarLink repository key"
				apt-get update -o DPkg::Lock::Timeout=600 || die "apt-get update failed (needed to install gpg)"
				DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends -o DPkg::Lock::Timeout=600 gpg || die "could not install gpg, which is needed to verify the AllStarLink repository key"
				command -v gpg >/dev/null 2>&1 || die "gpg is still not available after installing it"
			fi
			asl_deb="$tmp/asl-apt-repos.deb${asl_nn}_all.deb"
			say "adding the AllStarLink apt repository ($ASL_REPO_URL)"
			fetch "$ASL_REPO_URL/asl-apt-repos.deb${asl_nn}_all.deb" "$asl_deb" || die "could not download $ASL_REPO_URL/asl-apt-repos.deb${asl_nn}_all.deb"
			[ "$(dpkg-deb -f "$asl_deb" Package 2>/dev/null || true)" = asl-apt-repos ] || die "the downloaded file is not the asl-apt-repos package"
			dpkg -i "$asl_deb" || die "could not install asl-apt-repos"
			fpr=$(gpg --show-keys --with-colons --fingerprint "$ASL_KEYRING" 2>/dev/null | awk -F: '$1 == "pub" { p = 1; next } p && $1 == "fpr" { print $10; exit }' || true)
			if [ "$fpr" != "$ASL_KEY_FPR" ]; then
				dpkg --purge asl-apt-repos || true
				die "the AllStarLink repository key does not match the expected fingerprint (got '${fpr:-none}', expected $ASL_KEY_FPR); asl-apt-repos was removed again"
			fi
			say "repository key verified ($ASL_KEY_FPR)"
		fi
		say "updating package lists"
		apt-get update -o DPkg::Lock::Timeout=600 || die "apt-get update failed"
		# shellcheck disable=SC2086
		DEBIAN_FRONTEND=noninteractive apt-get install -y -o DPkg::Lock::Timeout=600 $missing || die "could not install $missing"
		say "installed $missing"
	}
	# --->8--- asl3-install

	asl3_ensure

	# ---8<--- asl-telemetry (exercised by a local test harness; keep self-contained)
	# asl_telemetry_apply: acts on TELEM_MODE (off, on or empty). Never fatal.
	asl_telemetry_apply() {
		[ -n "$TELEM_MODE" ] || return 0
		telem_t=0
		telem_s=0
		if command -v systemctl >/dev/null 2>&1; then
			systemctl cat asl-telemetry.timer >/dev/null 2>&1 && telem_t=1
			systemctl cat asl-telemetry.service >/dev/null 2>&1 && telem_s=1
		fi
		if [ "$telem_t" -eq 0 ] && [ "$telem_s" -eq 0 ]; then
			say "ASL3 telemetry: asl-telemetry.timer is not installed, nothing to turn $TELEM_MODE"
			return 0
		fi
		if [ "$TELEM_MODE" = off ]; then
			if [ "$telem_t" -eq 1 ] && ! systemctl disable --now asl-telemetry.timer >/dev/null 2>&1; then
				say "warning: could not disable asl-telemetry.timer"
			fi
			if systemctl mask asl-telemetry.timer asl-telemetry.service >/dev/null 2>&1; then
				say "ASL3 telemetry turned off (asl-telemetry.timer masked)"
			else
				say "warning: could not mask asl-telemetry.timer and asl-telemetry.service"
			fi
		else
			if ! systemctl unmask asl-telemetry.timer asl-telemetry.service >/dev/null 2>&1; then
				say "warning: could not unmask asl-telemetry.timer and asl-telemetry.service"
			fi
			if [ "$telem_t" -eq 1 ]; then
				if systemctl enable --now asl-telemetry.timer >/dev/null 2>&1; then
					say "ASL3 telemetry turned on (asl-telemetry.timer enabled)"
				else
					say "warning: could not enable asl-telemetry.timer"
				fi
			fi
		fi
		return 0
	}
	# --->8--- asl-telemetry

	asl_telemetry_apply

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
fi

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
	[ "$port" != 80 ] || url="http://$addr"

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

# ---8<--- port-takeover (exercised by the Go tests in internal/pkgscripts; keep self-contained)
# Paths. The ASLCP_* overrides exist for the tests; nothing sets them for real.
SD_DIR="${ASLCP_SYSTEMD_DIR:-/run/systemd/system}"
DROPIN_DIR="${ASLCP_DROPIN_DIR:-/etc/systemd/system/aslcp.service.d}"
DROPIN="$DROPIN_DIR/port80.conf"
WEBPORT_STATE="${ASLCP_WEBPORT_STATE:-/var/lib/aslcp-webport}"
TK_LIST="${ASLCP_TAKEOVER_LIST:-/var/lib/aslcp/takeover.list}"
TK_RECORD="${ASLCP_DISABLED_UNITS:-/var/lib/aslcp/disabled-units}"

# The units a takeover may turn off, dependents first. Nothing else is ever
# touched: not asterisk, app_rpt, the asl3-* units, aslcp or Cockpit.
TK_UNITS="supermon.service supermon2.service supermon-ng.service asl-web-ui.service allmon3.service apache2.service lighttpd.service nginx.service"

have_systemd() { [ -d "$SD_DIR" ]; }

daemon_reload() {
	have_systemd || return 0
	systemctl daemon-reload || say "warning: systemctl daemon-reload failed"
}

restart_aslcp() {
	if have_systemd; then
		systemctl restart aslcp || say "warning: could not restart aslcp (see: journalctl -u aslcp)"
	else
		say "systemd is not running here; restart aslcp yourself to use the new port"
	fi
}

# write_dropin: the same file the System tab (Web port) writes: it lets the
# unprivileged service bind a port below 1024 and nothing else.
write_dropin() {
	mkdir -p "$DROPIN_DIR" || return 1
	cat >"$DROPIN" <<'EOD'
# Written by ASLCP (System tab, Web port). Removed again by "move back" and
# by "apt purge aslcp".
[Service]
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
EOD
}

remove_dropin() {
	rm -f "$DROPIN"
	rmdir "$DROPIN_DIR" 2>/dev/null || true
}

# port_report PORT: the listening sockets on PORT with their programs.
port_report() {
	command -v ss >/dev/null 2>&1 || return 0
	ss -ltnpH "sport = :$1" 2>/dev/null | sed 's/^/    /' || true
}

# set_port PORT: point "listen" in the config at PORT (an address part is
# kept), restart aslcp and open the port in firewalld. Port 80 also writes the
# capability drop-in first and is refused (status 1, nothing changed) while
# another program holds it; a port of 1024 or more removes the drop-in again.
set_port() {
	cur=$(conf_get listen :8080)
	curport=${cur##*:}
	if [ "$curport" = "$1" ]; then
		if [ "$1" -lt 1024 ] && [ ! -f "$DROPIN" ]; then
			say "port $1 needs the capability drop-in; writing $DROPIN"
			write_dropin || die "cannot write $DROPIN"
			daemon_reload
			restart_aslcp
		fi
		return 0
	fi
	say "setting the web port to $1 (was $curport)"
	o=$(port_owner "$1")
	case "$o" in
	"" | aslcp) ;;
	*)
		if [ "$1" -lt 1024 ]; then
			printf '\naslcp-install: WARNING: TCP port %s is in use by "%s", so ASLCP was NOT moved there.\n' "$1" "$o"
			port_report "$1"
			printf '  Stop that program, or pick another port with ASLCP_PORT=<port>, then run the installer again.\n'
			return 1
		fi
		say "warning: TCP port $1 is already in use by \"$o\""
		;;
	esac
	case "$cur" in
	*:*) addr=${cur%:*} ;;
	*) addr= ;;
	esac
	new="listen = $addr:$1"
	[ -f "$CONF" ] || die "$CONF not found; set listen = $addr:$1 in it yourself"
	if [ "$1" -lt 1024 ]; then
		# Remember where to go back to: the System tab's "Move back" and
		# ASLCP_TAKEOVER=undo read it.
		{ mkdir -p "$WEBPORT_STATE" && chmod 0700 "$WEBPORT_STATE"; } || die "cannot create $WEBPORT_STATE"
		printf '%s\n' "$cur" >"$WEBPORT_STATE/previous-listen"
		write_dropin || die "cannot write $DROPIN"
		daemon_reload
	fi
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
	if [ "$1" -ge 1024 ] && [ -f "$DROPIN" ]; then
		remove_dropin
		rm -f "$WEBPORT_STATE/previous-listen"
		daemon_reload
	fi
	restart_aslcp
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

# --- takeover: turn off the other ASL web services --------------------------
tk_valid_unit() {
	for v in $TK_UNITS; do
		[ "$v" != "$1" ] || return 0
	done
	return 1
}

# tk_known: the service unit files systemd knows, one per line.
tk_known() { systemctl list-unit-files --type=service --no-legend --no-pager 2>/dev/null | awk '{ print $1 }' || true; }

# tk_unrecord UNIT: drop UNIT from the takeover list.
tk_unrecord() {
	[ -f "$TK_LIST" ] || return 0
	awk -v u="$1" '$1 != u' "$TK_LIST" >"$tmp/takeover.list.new"
	cat "$tmp/takeover.list.new" >"$TK_LIST"
}

# tk_record UNIT STATE: STATE is "enabled" (it was disabled and stopped) or
# "active" (it was only running and was stopped). The list sits in a directory
# the aslcp user owns, so reading it back trusts only the fixed unit names.
tk_record() {
	[ ! -L "$TK_LIST" ] || die "$TK_LIST is a symbolic link; not using it"
	mkdir -p "${TK_LIST%/*}"
	tk_unrecord "$1"
	printf '%s %s\n' "$1" "$2" >>"$TK_LIST"
}

# tk_mirror_add / tk_mirror_remove UNIT: the services page of ASLCP (System,
# ASL services) and "apt purge aslcp" know Apache and Allmon3 by the names in
# disabled-units; keeping that file in step lets them show and restore them.
tk_mirror_name() {
	case "$1" in
	apache2.service) printf '%s\n' apache2 ;;
	allmon3.service) printf '%s\n' allmon3 ;;
	esac
}
tk_mirror_add() {
	m=$(tk_mirror_name "$1")
	[ -n "$m" ] || return 0
	[ ! -L "$TK_RECORD" ] || return 0
	mkdir -p "${TK_RECORD%/*}"
	if [ -f "$TK_RECORD" ] && grep -qxF "$m" "$TK_RECORD"; then
		return 0
	fi
	printf '%s\n' "$m" >>"$TK_RECORD"
}
tk_mirror_remove() {
	m=$(tk_mirror_name "$1")
	[ -n "$m" ] || return 0
	{ [ -f "$TK_RECORD" ] && [ ! -L "$TK_RECORD" ]; } || return 0
	grep -vxF "$m" "$TK_RECORD" >"$tmp/disabled-units.new" || true
	cat "$tmp/disabled-units.new" >"$TK_RECORD"
}

# takeover_disable: stop and disable each unit of TK_UNITS that exists and is
# enabled or running, and record it. A failure only warns.
takeover_disable() {
	if ! have_systemd || ! command -v systemctl >/dev/null 2>&1; then
		say "takeover: systemd is not running here, so there is nothing to turn off"
		return 0
	fi
	say "takeover: turning off the other ASL web services"
	known=$(tk_known)
	n=0
	for u in $TK_UNITS; do
		printf '%s\n' "$known" | grep -qxF "$u" || continue
		en=$(systemctl is-enabled "$u" 2>/dev/null || true)
		ac=$(systemctl is-active "$u" 2>/dev/null || true)
		case "$en" in
		enabled | enabled-runtime)
			if systemctl disable --now "$u"; then
				tk_record "$u" enabled
				tk_mirror_add "$u"
				say "  disabled and stopped: $u"
				n=$((n + 1))
			else
				say "  warning: could not disable $u; left as it is"
			fi
			continue
			;;
		esac
		case "$ac" in
		active | activating)
			if systemctl stop "$u"; then
				tk_record "$u" active
				say "  stopped (it was running but not enabled): $u"
				n=$((n + 1))
			else
				say "  warning: could not stop $u; left as it is"
			fi
			;;
		esac
	done
	if [ "$n" -eq 0 ]; then
		say "  none of the known units was enabled or running; nothing was turned off"
	fi
	say "  not touched: asterisk and everything else the node needs on the air, aslcp, Cockpit (port 9090)"
}

# takeover_undo: ASLCP leaves port 80 (back to the saved port, or 8080, or the
# port in ASLCP_PORT), then each recorded unit is enabled and started again
# (or only started, when it was merely running). Status 1 if a step failed.
takeover_undo() {
	if [ ! -f "$TK_LIST" ] || [ -L "$TK_LIST" ]; then
		say "takeover undo: nothing is recorded in $TK_LIST, so there is nothing to undo"
		return 0
	fi
	if ! have_systemd || ! command -v systemctl >/dev/null 2>&1; then
		say "takeover undo: systemd is not running here; the units in $TK_LIST stay as they are"
		return 1
	fi
	cur=$(conf_get listen :8080)
	if [ "${cur##*:}" = 80 ]; then
		back=${PORT_WANT:-}
		if [ -z "$back" ]; then
			prev=$(head -n 1 "$WEBPORT_STATE/previous-listen" 2>/dev/null || true)
			back=${prev##*:}
			case "$back" in
			'' | *[!0-9]* | 0*) back=8080 ;;
			esac
			{ [ "${#back}" -le 5 ] && [ "$back" -ge 1024 ] && [ "$back" -le 65535 ]; } || back=8080
		fi
		set_port "$back" || return 1
	fi
	: >"$tmp/takeover.failed"
	while IFS=' ' read -r u st || [ -n "$u" ]; do
		tk_valid_unit "$u" || {
			[ -z "$u" ] || say "  ignoring an unknown entry in $TK_LIST: $u"
			continue
		}
		case "$st" in
		enabled) act="enable --now" ;;
		active) act=start ;;
		*) continue ;;
		esac
		# shellcheck disable=SC2086
		if systemctl $act "$u"; then
			say "  turned back on: $u"
			tk_mirror_remove "$u"
		else
			say "  warning: could not turn $u back on"
			printf '%s %s\n' "$u" "$st" >>"$tmp/takeover.failed"
		fi
	done <"$TK_LIST"
	if [ -s "$tmp/takeover.failed" ]; then
		cat "$tmp/takeover.failed" >"$TK_LIST"
		say "takeover undo: some units are still off; they stay in $TK_LIST, run this again to retry"
		return 1
	fi
	rm -f "$TK_LIST"
	cur=$(conf_get listen :8080)
	say "takeover undone: ASLCP listens on port ${cur##*:} again and the recorded units are back"
}
# --->8--- port-takeover

# dv_enable: run "aslcp dv enable" once the service is running.
# Sets DV_RESULT for the closing lines; a failure only warns.
DV_RESULT=
dv_enable() {
	say "enabling digital modes (this takes a few minutes)"
	n=0
	if [ -d /run/systemd/system ]; then
		while ! systemctl is-active --quiet aslcp 2>/dev/null && [ "$n" -lt "${ASLCP_WAIT:-10}" ]; do
			sleep 1
			n=$((n + 1))
		done
	fi
	if aslcp dv enable --config "$CONF"; then
		DV_RESULT="enabled. Set it up on the digital modes page of the web UI."
	else
		DV_RESULT="NOT enabled (see the messages above). ASLCP itself is installed; retry with: sudo aslcp dv enable"
		say "warning: enabling digital modes failed; continuing"
	fi
}

if [ "$TAKEOVER_UNDO" -eq 1 ]; then
	rc=0
	takeover_undo || rc=$?
	exit "$rc"
fi
if [ "$TAKEOVER_WANT" -eq 1 ]; then
	takeover_disable
fi
# Port 80 is refused while another program holds it: ASLCP then stays where it
# is and the installer exits with status 1 after the closing lines.
PORT_FAIL=0
[ -z "$PORT_WANT" ] || set_port "$PORT_WANT" || PORT_FAIL=1
# Digital modes go first so the setup link stays the last thing on the screen.
[ "$DV_WANT" -eq 0 ] || dv_enable
setup_admin
if [ -n "$DV_RESULT" ]; then
	printf '\nDigital modes: %s\n' "$DV_RESULT"
fi

cat <<EOM

aslcp-install: done.
  Configuration:  /etc/aslcp/aslcp.conf     Logs:  journalctl -u aslcp
  Updates:  Settings > Updates in the web UI, or: sudo apt update && sudo apt install --only-upgrade aslcp
EOM
if [ "$TAKEOVER_WANT" -eq 1 ] && [ "$PORT_FAIL" -eq 0 ]; then
	cat <<EOM

Takeover: the units listed above are turned off and ASLCP answers on port 80.
  Undo:  curl -fsSL $APT_URL/install.sh | sudo ASLCP_TAKEOVER=undo sh
  (record: $TK_LIST)
EOM
fi
if [ "$PORT_FAIL" -ne 0 ]; then
	printf '\naslcp-install: ASLCP was NOT moved to port %s (see the warning above); it stays on its previous port.\n' "$PORT_WANT" >&2
	if [ "$TAKEOVER_WANT" -eq 1 ]; then
		printf '  The services listed above stay turned off; undo with ASLCP_TAKEOVER=undo.\n' >&2
	fi
	exit 1
fi
