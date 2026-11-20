#!/bin/bash
# Build/refresh the apt repository: the gh-pages branch of the code repository,
# served by GitHub Pages (packaging/apt-checkout.sh prepares the checkout).
#
#   packaging/publish-apt.sh DEBS_DIR REPO_DIR
#       add the new aslcp_*.deb files to the beta pool (what a vX.Y.Z tag does)
#   packaging/publish-apt.sh --promote VERSION REPO_DIR
#       move the already published debs of VERSION from the beta pool to the
#       stable pool (no rebuild: the tested package becomes the release)
#   packaging/publish-apt.sh --remove GLOB [--apply] REPO_DIR
#       delete beta-pool versions matching GLOB (a bash pattern on the Debian
#       version, for example '2.0.0~beta*'); a dry run unless --apply is given
#
#   DEBS_DIR  directory containing the new aslcp_*.deb files
#   REPO_DIR  checkout of the gh-pages branch (may be empty/new on the very first
#             release); updated in place, the caller commits and pushes
#
# Environment:
#   GPG_KEY_ID    key to sign with (default: first secret key in the keyring)
#   GNUPGHOME     keyring directory (default: gpg's default)
#   APT_URL       public base URL (default https://artw1.github.io/aslcp)
#   KEEP          versions retained per architecture and pool directory (default 5)
#   TAG           release tag (vX.Y.Z) for the release notes
#   CODE_DIR      checkout of the code repo, for the tag annotation and CHANGELOG.md
#                 (default: the repo containing this script)
#   NOTES_FILE    explicit release notes file (overrides TAG/CHANGELOG lookup)
#   REQUIRE_VERSION  with --remove: abort unless this version is still in the
#                 pool afterwards (a guard against emptying the repository)
#
# Two suites live in one repository:
#   beta    indexes all of pool/ (new builds in pool/beta, plus everything
#           promoted to pool/main)
#   stable  indexes pool/main only (promoted releases)
# Every version tag builds into pool/beta. Promotion moves the same files to
# pool/main, so a package is in one pool at a time and the beta suite still
# offers it. latest-beta.json is the newest version in the whole pool,
# latest.json the newest in pool/main.
# install.sh is only refreshed when the version being published is the newest in
# the whole pool (a backport must not revert it).
#
# Requires apt-ftparchive (apt-utils), gpg, dpkg-deb and python3.
set -euo pipefail

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; exit 2; }

MODE=add
PROMOTE_VERSION=""
REMOVE_GLOB=""
APPLY=0
args=()
while [ $# -gt 0 ]; do
	case "$1" in
	--promote) [ $# -ge 2 ] || usage; MODE=promote; PROMOTE_VERSION=${2#v}; shift 2 ;;
	--remove) [ $# -ge 2 ] || usage; MODE=remove; REMOVE_GLOB=$2; shift 2 ;;
	--apply) APPLY=1; shift ;;
	-h | --help) usage ;;
	*) args+=("$1"); shift ;;
	esac
done
case "$MODE" in
add) [ ${#args[@]} -eq 2 ] || usage; DEBS_DIR=$(cd "${args[0]}" && pwd); REPO_ARG=${args[1]} ;;
*) [ ${#args[@]} -eq 1 ] || usage; DEBS_DIR=""; REPO_ARG=${args[0]} ;;
esac
[ "$APPLY" -eq 0 ] || [ "$MODE" = remove ] || usage
if [ "$MODE" = promote ]; then
	[[ $PROMOTE_VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "publish-apt: --promote needs a version like 1.1.16 (got '$PROMOTE_VERSION')" >&2; exit 2; }
fi
if [ "$MODE" = remove ] && [ -z "$REMOVE_GLOB" ]; then echo "publish-apt: --remove needs a non-empty version glob" >&2; exit 2; fi
REPO_DIR=$(cd "$REPO_ARG" && pwd)
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
CODE_DIR=${CODE_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}
APT_URL=${APT_URL:-https://artw1.github.io/aslcp}
APT_URL=${APT_URL%/}
KEEP=${KEEP:-5}
ARCHES="arm64 armhf amd64"
COMPONENT=main
POOL_STABLE=pool/main/a/aslcp
POOL_BETA=pool/beta/a/aslcp

for t in apt-ftparchive gpg dpkg-deb python3; do
	command -v "$t" >/dev/null || { echo "publish-apt: missing required tool: $t" >&2; exit 1; }
done

key=${GPG_KEY_ID:-}
if [ -z "$key" ]; then
	key=$(gpg --batch --list-secret-keys --with-colons | awk -F: '$1=="fpr"{print $10; exit}')
fi
[ -n "$key" ] || { echo "publish-apt: no GPG secret key available (set GPG_KEY_ID / import the signing key)" >&2; exit 1; }

cd "$REPO_DIR"

mkdir -p "$POOL_STABLE" "$POOL_BETA"

# newest version in a pool directory ("" when empty)
newest_in() {
	local best="" f v
	for f in "$@"; do
		[ -e "$f" ] || continue
		v=$(dpkg-deb -f "$f" Version)
		if [ -z "$best" ] || dpkg --compare-versions "$v" gt "$best"; then best=$v; fi
	done
	printf '%s' "$best"
}
# deb of VERSION for ARCH in POOL ("" when there is none)
deb_of() { # deb_of POOL VERSION ARCH
	local f
	for f in "$1"/aslcp_*_"$3".deb; do
		[ -e "$f" ] || continue
		if [ "$(dpkg-deb -f "$f" Version)" = "$2" ]; then printf '%s' "$f"; return 0; fi
	done
	return 0
}

released=""
case "$MODE" in
add)
	# --- 1. add the new packages (always to the beta pool) ----------------------
	n=0
	for f in "$DEBS_DIR"/aslcp_*.deb; do
		[ -e "$f" ] || continue
		v=$(dpkg-deb -f "$f" Version)
		arch=$(dpkg-deb -f "$f" Architecture)
		if [ -n "$(deb_of "$POOL_STABLE" "$v" "$arch")" ]; then
			echo "publish-apt: $v ($arch) is already promoted to stable; refusing to publish another build of it" >&2
			exit 1
		fi
		cp -f "$f" "$POOL_BETA/"
		if [ -z "$released" ] || dpkg --compare-versions "$v" gt "$released"; then released=$v; fi
		n=$((n + 1))
	done
	[ "$n" -gt 0 ] || { echo "publish-apt: no aslcp_*.deb in $DEBS_DIR" >&2; exit 1; }
	echo "publish-apt: added $n package(s) to the beta pool, version $released"
	;;
promote)
	# --- 1. move the published debs of the version from the beta to the stable pool
	released=$PROMOTE_VERSION
	missing=""
	moves=0
	for arch in $ARCHES; do
		if [ -n "$(deb_of "$POOL_BETA" "$released" "$arch")" ]; then
			moves=$((moves + 1))
		elif [ -z "$(deb_of "$POOL_STABLE" "$released" "$arch")" ]; then
			missing="$missing $arch"
		fi
	done
	if [ -n "$missing" ]; then
		echo "publish-apt: $released is not published in the beta suite for:$missing (nothing promoted)" >&2
		echo "publish-apt: tag v$released first and wait for the Release workflow to finish" >&2
		exit 1
	fi
	if [ "$moves" -eq 0 ]; then
		echo "publish-apt: $released is already in the stable suite: nothing to do"
		exit 0
	fi
	for arch in $ARCHES; do
		src=$(deb_of "$POOL_BETA" "$released" "$arch")
		[ -n "$src" ] || continue
		if [ -n "$(deb_of "$POOL_STABLE" "$released" "$arch")" ]; then
			echo "publish-apt: $released ($arch) is already in stable; dropping the beta copy"
			rm -f -- "$src"
		else
			echo "publish-apt: promoting $src"
			mv -f -- "$src" "$POOL_STABLE/"
		fi
	done
	;;
remove)
	# --- 1. delete beta-pool versions matching the glob (stable is never touched)
	matches=()
	for f in "$POOL_BETA"/aslcp_*.deb; do
		[ -e "$f" ] || continue
		v=$(dpkg-deb -f "$f" Version)
		# shellcheck disable=SC2053 # the glob is the point
		if [[ $v == $REMOVE_GLOB ]]; then matches+=("$f"); fi
	done
	echo "publish-apt: ${#matches[@]} beta-pool package(s) match '$REMOVE_GLOB' (the stable pool is never touched):"
	for f in "${matches[@]}"; do echo "  $f ($(dpkg-deb -f "$f" Version))"; done
	# what would remain
	remaining=()
	for f in "$POOL_STABLE"/aslcp_*.deb "$POOL_BETA"/aslcp_*.deb; do
		[ -e "$f" ] || continue
		skip=0
		for m in "${matches[@]}"; do [ "$f" != "$m" ] || skip=1; done
		[ "$skip" -eq 1 ] || remaining+=("$f")
	done
	if [ "${#remaining[@]}" -eq 0 ]; then
		echo "publish-apt: refusing: nothing would be left in the pool" >&2
		exit 1
	fi
	if [ -n "${REQUIRE_VERSION:-}" ]; then
		found=0
		for f in "${remaining[@]}"; do [ "$(dpkg-deb -f "$f" Version)" != "$REQUIRE_VERSION" ] || found=1; done
		if [ "$found" -eq 0 ]; then
			echo "publish-apt: refusing: $REQUIRE_VERSION would no longer be in the pool (publish it first, or fix the glob)" >&2
			exit 1
		fi
		echo "publish-apt: $REQUIRE_VERSION stays in the pool"
	fi
	if [ "$APPLY" -ne 1 ]; then
		echo "publish-apt: dry run, nothing changed (use --apply to delete)"
		exit 0
	fi
	for f in "${matches[@]}"; do rm -f -- "$f"; done
	# republish the newest remaining version as such (refreshes install.sh and the notes)
	released=$(newest_in "${remaining[@]}")
	;;
esac

# --- 2. retention: newest $KEEP versions per architecture, per pool directory --
# (separately, so unpromoted builds never push a release out of the pool; the
# version just added or promoted is never pruned, whatever its age)
if [ "$MODE" != remove ]; then
	for pool in "$POOL_STABLE" "$POOL_BETA"; do
		for arch in $ARCHES; do
			files=()
			versions=()
			for f in "$pool"/aslcp_*_"$arch".deb; do
				[ -e "$f" ] || continue
				files+=("$f")
				versions+=("$(dpkg-deb -f "$f" Version)")
			done
			# selection sort, newest first (dpkg version order, so 0.10.0 > 0.9.0 and ~ works)
			count=${#files[@]}
			for ((i = 0; i < count; i++)); do
				best=$i
				for ((j = i + 1; j < count; j++)); do
					if dpkg --compare-versions "${versions[j]}" gt "${versions[best]}"; then best=$j; fi
				done
				if [ "$best" -ne "$i" ]; then
					tf=${files[i]}; files[i]=${files[best]}; files[best]=$tf
					tv=${versions[i]}; versions[i]=${versions[best]}; versions[best]=$tv
				fi
			done
			for ((i = KEEP; i < count; i++)); do
				[ "${versions[i]}" != "$released" ] || continue
				echo "publish-apt: pruning ${files[i]}"
				rm -f -- "${files[i]}"
			done
		done
	done
fi

newest_stable=$(newest_in "$POOL_STABLE"/aslcp_*.deb)
newest_all=$(newest_in "$POOL_STABLE"/aslcp_*.deb "$POOL_BETA"/aslcp_*.deb)
[ -n "$newest_stable" ] || echo "publish-apt: note: no stable version in the pool yet"

# --- 3. install.sh is refreshed only for the newest version in the pool -------
# (a 1.0.x backport published after 1.1.x must not revert it). Promotion never
# refreshes it (it was refreshed when the version was published to beta). The
# README, licence, SECURITY.md and issue forms live on main, not here.
sync_root=0
if [ "$MODE" = promote ]; then
	echo "publish-apt: promotion: not refreshing install.sh"
elif dpkg --compare-versions "$released" ge "$newest_all"; then
	sync_root=1
else
	echo "publish-apt: $released is older than the newest version in the pool ($newest_all): not refreshing install.sh"
fi
touch .nojekyll # serve dotfiles and paths verbatim

# --- 4. package indexes and signed Release, per suite --------------------------
conf=$(mktemp)
trap 'rm -f "$conf"' EXIT
publish_suite() { # publish_suite SUITE POOLROOT
	local suite=$1 root=$2 arch d
	for arch in $ARCHES; do
		d="dists/$suite/$COMPONENT/binary-$arch"
		mkdir -p "$d"
		apt-ftparchive --arch "$arch" packages "$root" >"$d/Packages"
		gzip -9nkf "$d/Packages"
	done
	cat >"$conf" <<EOC
APT::FTPArchive::Release::Origin "ASLCP";
APT::FTPArchive::Release::Label "ASLCP";
APT::FTPArchive::Release::Suite "$suite";
APT::FTPArchive::Release::Codename "$suite";
APT::FTPArchive::Release::Architectures "$ARCHES";
APT::FTPArchive::Release::Components "$COMPONENT";
APT::FTPArchive::Release::Description "ASLCP packages for Debian 12 (bookworm), Debian 13 (trixie) and Raspberry Pi OS ($suite)";
EOC
	apt-ftparchive -c "$conf" release "dists/$suite" >"dists/$suite/Release.new"
	mv -f "dists/$suite/Release.new" "dists/$suite/Release"
	rm -f "dists/$suite/InRelease" "dists/$suite/Release.gpg"
	gpg --batch --yes --default-key "$key" --clearsign -o "dists/$suite/InRelease" "dists/$suite/Release"
	gpg --batch --yes --default-key "$key" --armor --detach-sign -o "dists/$suite/Release.gpg" "dists/$suite/Release"
}
publish_suite stable pool/main
publish_suite beta pool

# --- 5. public key -----------------------------------------------------------
gpg --batch --yes --export "$key" >aslcp-archive-keyring.gpg
gpg --batch --yes --armor --export "$key" >aslcp-archive-keyring.asc
fpr=$(gpg --batch --with-colons --fingerprint "$key" | awk -F: '$1=="fpr"{print $10; exit}')

# --- 6. latest.json / latest-beta.json (version, release notes, date) ----------
# Notes of a version: NOTES_FILE or the tag annotation (only for the version
# being released), else its CHANGELOG section ("## [1.1.16]"). When nothing is
# found the notes already in the json for the same version are kept.
tag_version=""
[ -z "${TAG:-}" ] || tag_version=${TAG#v}
notes_for() { # notes_for VERSION
	local v=$1 notes=""
	if [ "$v" = "$released" ]; then
		if [ -n "${NOTES_FILE:-}" ] && [ -f "$NOTES_FILE" ]; then
			notes=$(cat "$NOTES_FILE")
		elif [ "$v" = "$tag_version" ] && [ -d "$CODE_DIR/.git" ]; then
			notes=$(git -C "$CODE_DIR" tag -l --format='%(contents)' "$TAG" 2>/dev/null || true)
		fi
	fi
	if [ -z "${notes//[[:space:]]/}" ] && [ -f "$CODE_DIR/CHANGELOG.md" ]; then
		# section "## [X.Y.Z] ..." or "## X.Y.Z ..." up to the next "## "
		notes=$(awk -v v="$v" '
			/^## / { if (on) exit; line=$0; gsub(/[\[\]]/, "", line); split(line, p, " "); if (p[2] == v) { on=1; next } }
			on { print }' "$CODE_DIR/CHANGELOG.md")
	fi
	printf '%s' "$notes"
}
write_latest() { # write_latest FILE VERSION
	local file=$1 v=$2
	[ -n "$v" ] || return 0
	JSON_FILE=$file VERSION_NEWEST=$v NOTES=$(notes_for "$v") python3 -I - <<'EOPY'
import json, os, datetime
path, version = os.environ["JSON_FILE"], os.environ["VERSION_NEWEST"]
notes = os.environ.get("NOTES", "").strip()
date = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")
try:
    with open(path) as f:
        old = json.load(f)
except (OSError, ValueError):
    old = {}
if old.get("version") == version:
    # same version as before: keep its date, and its notes if none were found
    date = old.get("date") or date
    notes = notes or (old.get("notes") or "").strip()
with open(path + ".new", "w") as f:
    print(json.dumps({"version": version, "notes": notes, "date": date}, indent=2), file=f)
os.replace(path + ".new", path)
EOPY
}
write_latest latest.json "$newest_stable"
write_latest latest-beta.json "$newest_all"

# --- 7. install script and landing page --------------------------------------
# The script already defaults to the standard URL; rewrite it if APT_URL differs.
if [ "$sync_root" -eq 1 ] || [ ! -f install.sh ]; then
	sed "s|https://artw1.github.io/aslcp|$APT_URL|g" "$SCRIPT_DIR/install.sh" >install.sh
	chmod 0755 install.sh
fi
beta_note=""
[ "$newest_all" != "$newest_stable" ] || beta_note=" (same as stable)"
cat >index.html <<EOH
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ASLCP - web control panel for AllStarLink 3</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:2rem auto;padding:0 1rem}pre{background:#8881;padding:.75rem;overflow:auto;border-radius:6px}code{font-family:ui-monospace,monospace}</style>
</head><body>
<h1>ASLCP</h1>
<p>A modern web control panel for AllStarLink 3 nodes: connect, monitor and manage favorites with live status. For Debian 12 (bookworm), Debian 13 (trixie) and Raspberry Pi OS on arm64, armhf and amd64.</p>
<p>Latest release: <strong>${newest_stable:-none yet}</strong>. Latest beta: <strong>$newest_all</strong>$beta_note.</p>
<p><a href="https://github.com/artw1/aslcp">Documentation, screenshots and bug reports on GitHub</a></p>
<h2>Install</h2>
<pre><code>curl -fsSL $APT_URL/install.sh | sudo sh</code></pre>
<p>The installer prints a one-time setup link at the end. Open it to create the administrator or to restore a backup from an existing node. <code>sudo aslcp admin setup-code</code> shows the code again.</p>
<h2>Web port</h2>
<p>The web interface listens on port <strong>8080</strong> by default. To pick another port, set <code>ASLCP_PORT</code> (1-65535) with the install command:</p>
<pre><code>curl -fsSL $APT_URL/install.sh | sudo ASLCP_PORT=9090 sh</code></pre>
<p>To change it later, run the same command again, or edit <code>listen</code> in <code>/etc/aslcp/aslcp.conf</code> (for example <code>listen = :9090</code>) and run <code>sudo systemctl restart aslcp</code>. The installer warns if another program already uses the port. If firewalld is running, the installer and the package open the configured port for you. With another firewall (ufw, nftables, a router) open the TCP port yourself, and if you edit <code>listen</code> by hand with firewalld, open the new port too.</p>
<h2>DVSwitch</h2>
<p>To install DVSwitch as well, set <code>ASLCP_DVSWITCH=1</code> (<code>yes</code> and <code>true</code> work too). It can be combined with <code>ASLCP_PORT</code>:</p>
<pre><code>curl -fsSL $APT_URL/install.sh | sudo ASLCP_DVSWITCH=1 ASLCP_PORT=9090 sh</code></pre>
<p>After ASLCP is running, the installer runs <code>aslcp dvswitch install</code> (the same install as the DVSwitch page; it takes a few minutes). If it fails the installer still finishes, and you can retry with <code>sudo aslcp dvswitch install</code>.</p>
<h2>Manual setup</h2>
<pre><code>sudo curl -fsSL -o /usr/share/keyrings/aslcp-archive-keyring.gpg $APT_URL/aslcp-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/aslcp-archive-keyring.gpg] $APT_URL stable main" | sudo tee /etc/apt/sources.list.d/aslcp.list
sudo apt update &amp;&amp; sudo apt install aslcp</code></pre>
<p>Signing key fingerprint: <code>$fpr</code> (<a href="aslcp-archive-keyring.asc">armored key</a>)</p>
<h2>Beta channel</h2>
<p>Every new version is published to the <code>beta</code> suite as soon as it is built. It becomes a release (suite <code>stable</code>) when it is promoted after testing: the same package, not a rebuild. The <code>beta</code> suite also carries every release, so beta users are never behind. Betas may be less stable. To use it, put <code>beta</code> instead of <code>stable</code> in the line above, or install with <code>curl -fsSL $APT_URL/install.sh | sudo ASLCP_CHANNEL=beta sh</code>, or switch in Settings &rarr; Updates in the web UI (the same setting: it changes the suite in <code>/etc/apt/sources.list.d/aslcp.list</code>). Going back to <code>stable</code> does not downgrade: the node stays on the installed version until a release newer than it is promoted.</p>
<p>Later updates: Settings &rarr; Updates in the web UI, or <code>sudo apt update &amp;&amp; sudo apt upgrade</code>.</p>
</body></html>
EOH
touch .nojekyll
echo "publish-apt: repository updated (released $released, stable ${newest_stable:-none}, beta channel $newest_all, key $fpr)"
