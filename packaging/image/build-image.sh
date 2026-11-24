#!/usr/bin/env bash
# Build "ASLCP for ASL3", a flashable Raspberry Pi image: the latest official
# Raspberry Pi OS Lite (64-bit) plus ASL3 (asl3, asl3-menu) and ASLCP.
#
#   sudo packaging/image/build-image.sh
#
# Run as root on an arm64 Debian/Ubuntu host (a native chroot, no emulation).
# On an x86-64 host it works with qemu-user-static and binfmt (qemu-aarch64)
# installed; that is slow and not used by the workflow.
# Host tools: curl jq xz parted e2fsprogs util-linux (losetup, mount); optional zerofree.
# The image is never booted here. See packaging/image/README.md.
#
# Environment (all optional):
#   CHANNEL=stable|beta   ASLCP apt channel configured in the image (default stable)
#   ASLCP_DEB=/path.deb   install this arm64 deb instead of the one in the repository
#   BASE_URL, BASE_SHA256 use this base image (.img.xz) instead of the latest Lite
#   OUT_DIR               where the results go (default ./image-out)
#   IMAGE_BASE_URL        where the .img.xz will be downloaded from (no trailing slash). The Raspberry
#                         Pi Imager repository file aslcp-imager.json links to it; without it the
#                         file is still written but its url is a placeholder and not usable
#   WORKDIR               scratch space, needs about 9 GB (default ./image-work)
#   IMAGE_DATE            YYYY.MM.DD in the name (default: today, UTC)
#   GROW_MIB              space added to the root file system for the build (2560)
#   MARGIN_MIB            free space left in the root file system when shrinking (300)
#   XZ_LEVEL              xz preset (6)
#   APT_URL               ASLCP apt repository (https://w1jar.github.io/aslcp)
#   SKIP_CHROOT=1         dry run: download, grow, mount, clean, shrink, compress, no packages
#
#   build-image.sh --select-base FILE   print "url<TAB>sha256<TAB>release_date<TAB>init_format<TAB>devices"
#                                       of the Lite (64-bit) entry of an os_list JSON, and exit
#                                       (devices is a JSON array of Imager device tags)
#   build-image.sh --imager-json FILE OUT
#                                       write the Imager repository file OUT for the base
#                                       entry in FILE with dummy sizes (used by the tests)
set -euo pipefail

NAME="ASLCP for ASL3 (Raspberry Pi image)"
OS_LIST_URL=${OS_LIST_URL:-https://downloads.raspberrypi.com/os_list_imagingutility_v4.json}
LITE_ENTRY="Raspberry Pi OS Lite (64-bit)"
ASL_KEY_FPR=F2AF416869932ABB39E165E5409D50181BD84509
ASL_REPOS_URL=https://repo.allstarlink.org/public

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

say() { printf '%s\n' "build-image: $*" >&2; }
die() { printf '%s\n' "build-image: error: $*" >&2; exit 1; }

# select_base FILE: url, sha256 (empty when the list has none), release date,
# init_format and devices (JSON array; defaults cloudinit-rpi and the three 64-bit
# tags) of the Lite (64-bit) entry, tab separated. The entry may sit at any depth
# (the official list nests older releases in sub-lists).
select_base() {
	jq -er --arg n "$LITE_ENTRY" '
		[.. | objects | select(.name? == $n and ((.url? // "") | length > 0))]
		| if length == 0 then error("no entry") else .[0] end
		| [.url, (.image_download_sha256 // ""), (.release_date // ""),
		   ((.init_format // "") | if . == "" then "cloudinit-rpi" else . end),
		   ((.devices // []) | if length == 0 then ["pi5-64bit", "pi4-64bit", "pi3-64bit"] else . end | tojson)]
		| @tsv' "$1"
}

# write_imager_json OUT DATE FILE SIZE_DL SHA_DL SIZE_EXTRACT SHA_EXTRACT INIT_FORMAT DEVICES_JSON BASE_URL
# Raspberry Pi Imager repository file (Content Repository, custom URL): the device
# list plus one OS entry. Raspberry Pi device icons only; no OS icon.
write_imager_json() {
	local out=$1 date=$2 file=$3 dl_size=$4 dl_sha=$5 ex_size=$6 ex_sha=$7 init=$8 devs=$9 base=${10}
	local icons=https://downloads.raspberrypi.com/imager/icons
	jq -n --arg date "$date" --arg file "$file" --arg base "$base" --arg icons "$icons" \
		--argjson dl_size "$dl_size" --arg dl_sha "$dl_sha" --argjson ex_size "$ex_size" --arg ex_sha "$ex_sha" \
		--arg init "$init" --argjson devs "$devs" '
		{imager: {latest_version: "2.0.0", url: "https://www.raspberrypi.com/software/", devices: [
			{name: "Raspberry Pi 5", tags: ["pi5-64bit"], default: true, icon: ($icons + "/RPi_5.png"),
			 description: "Raspberry Pi 5, 500 / 500+, and Compute Module 5", matching_type: "exclusive"},
			{name: "Raspberry Pi 4", tags: ["pi4-64bit"], icon: ($icons + "/RPi_4.png"),
			 description: "Raspberry Pi 4 Model B, 400, and Compute Module 4 / 4S", matching_type: "inclusive"},
			{name: "Raspberry Pi 3", tags: ["pi3-64bit"], icon: ($icons + "/RPi_3.png"),
			 description: "Raspberry Pi 3 Model A+ / B / B+ and Compute Module 3 / 3+", matching_type: "inclusive"},
			{name: "Raspberry Pi Zero 2 W", tags: ["pi3-64bit"], icon: ($icons + "/RPi_Zero_2_W.png"),
			 description: "Raspberry Pi Zero 2 W", matching_type: "inclusive"}]},
		 os_list: [{name: ("ASLCP for ASL3 " + $date),
			description: "Unofficial image: Raspberry Pi OS Lite (64-bit) with AllStarLink 3 (ASL3) and ASLCP, a web control panel for ASL3 nodes. Not an official AllStarLink or Raspberry Pi product.",
			website: "https://github.com/w1jar/aslcp",
			url: ($base + "/" + $file),
			extract_size: $ex_size, extract_sha256: $ex_sha,
			image_download_size: $dl_size, image_download_sha256: $dl_sha,
			release_date: ($date | gsub("\\."; "-")),
			devices: $devs, init_format: $init}]}' >"$out"
}

if [ "${1:-}" = "--select-base" ]; then
	[ -n "${2:-}" ] || die "usage: $0 --select-base FILE"
	select_base "$2" || die "no '$LITE_ENTRY' entry with a url in $2"
	exit 0
fi

if [ "${1:-}" = "--imager-json" ]; then
	[ $# -eq 3 ] || die "usage: $0 --imager-json FILE OUT"
	line=$(select_base "$2") || die "no '$LITE_ENTRY' entry with a url in $2"
	d=${IMAGE_DATE:-2000.01.02}
	write_imager_json "$3" "$d" "aslcp-asl3-arm64-$d.img.xz" 1000 "$(printf '%064d' 1)" 4000 "$(printf '%064d' 2)" \
		"$(printf '%s\n' "$line" | cut -f4)" "$(printf '%s\n' "$line" | cut -f5)" \
		"${IMAGE_BASE_URL:-https://example.invalid/images}"
	exit 0
fi

[ "$(id -u)" -eq 0 ] || die "run as root (sudo $0)"

CHANNEL=${CHANNEL:-stable}
case "$CHANNEL" in
stable | beta) ;;
*) die "CHANNEL must be stable or beta" ;;
esac
APT_URL=${APT_URL:-https://w1jar.github.io/aslcp}
ASLCP_DEB=${ASLCP_DEB:-}
OUT_DIR=$(realpath -m "${OUT_DIR:-./image-out}")
WORKDIR=$(realpath -m "${WORKDIR:-./image-work}")
IMAGE_DATE=${IMAGE_DATE:-$(date -u +%Y.%m.%d)}
IMAGE_BASE_URL=${IMAGE_BASE_URL:-}
IMAGE_BASE_URL=${IMAGE_BASE_URL%/}
GROW_MIB=${GROW_MIB:-2560}
MARGIN_MIB=${MARGIN_MIB:-300}
XZ_LEVEL=${XZ_LEVEL:-6}
SKIP_CHROOT=${SKIP_CHROOT:-}
case "$IMAGE_DATE" in
[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]) ;;
*) die "IMAGE_DATE must look like 2026.01.31" ;;
esac
[ -z "$ASLCP_DEB" ] || [ -f "$ASLCP_DEB" ] || die "ASLCP_DEB=$ASLCP_DEB does not exist"

for t in curl jq xz parted losetup mount umount e2fsck resize2fs dumpe2fs truncate sha256sum chroot awk; do
	command -v "$t" >/dev/null 2>&1 || die "missing host tool: $t"
done

BASENAME="aslcp-asl3-arm64-$IMAGE_DATE"
IMG="$WORKDIR/$BASENAME.img"
MNT="$WORKDIR/mnt"
LOOPS=()
RESOLV_STATE=none # none | file | link
RESOLV_LINK=
QEMU_COPIED=

detach_loops() {
	local l
	for l in "${LOOPS[@]}"; do losetup -d "$l" 2>/dev/null || true; done
	LOOPS=()
}

# --- cleanup --------------------------------------------------------------
cleanup() {
	local rc=$?
	set +e
	if mountpoint -q "$MNT" 2>/dev/null; then
		umount -R "$MNT" 2>/dev/null || umount -R -l "$MNT" 2>/dev/null
	fi
	detach_loops
	[ "$rc" -eq 0 ] || say "FAILED (exit $rc); mounts and loop devices were released"
	return "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

fetch() { curl -fsSL --retry 4 --retry-delay 3 -o "$2" "$1"; }

# --- base image -------------------------------------------------------------
mkdir -p "$WORKDIR" "$OUT_DIR" "$MNT"
BASE_URL=${BASE_URL:-}
BASE_SHA256=${BASE_SHA256:-}
BASE_DATE=
BASE_INIT=cloudinit-rpi
BASE_DEVICES='["pi5-64bit","pi4-64bit","pi3-64bit"]'
if [ -z "$BASE_URL" ]; then
	say "resolving the latest '$LITE_ENTRY' from $OS_LIST_URL"
	fetch "$OS_LIST_URL" "$WORKDIR/os_list.json"
	line=$(select_base "$WORKDIR/os_list.json") || die "no '$LITE_ENTRY' entry in the os list"
	# cut, not read: an empty middle field must stay empty.
	BASE_URL=$(printf '%s\n' "$line" | cut -f1)
	list_sha=$(printf '%s\n' "$line" | cut -f2)
	BASE_INIT=$(printf '%s\n' "$line" | cut -f4)
	BASE_DEVICES=$(printf '%s\n' "$line" | cut -f5)
	BASE_DATE=$(printf '%s\n' "$line" | cut -f3)
	[ -n "$BASE_SHA256" ] || BASE_SHA256=$list_sha
fi
[ -n "$BASE_DATE" ] || BASE_DATE=$(printf '%s\n' "$BASE_URL" | grep -Eo '[0-9]{4}-[0-9]{2}-[0-9]{2}' | tail -n 1 || true)
[ -n "$BASE_DATE" ] || BASE_DATE=unknown
if [ -z "$BASE_SHA256" ]; then
	# The official list carries no image_download_sha256; the checksum sits next to the image.
	say "fetching $BASE_URL.sha256"
	BASE_SHA256=$(curl -fsSL --retry 4 "$BASE_URL.sha256" | awk 'NR == 1 { print $1 }') || true
fi
[[ "$BASE_SHA256" =~ ^[0-9a-f]{64}$ ]] || die "no usable sha256 for the base image ($BASE_URL); set BASE_SHA256"
say "base image: $BASE_URL ($BASE_DATE)"

XZFILE="$WORKDIR/base-${BASE_SHA256:0:12}.img.xz"
if [ -f "$XZFILE" ] && echo "$BASE_SHA256  $XZFILE" | sha256sum -c --status -; then
	say "using the cached download $XZFILE"
else
	say "downloading the base image"
	rm -f "$XZFILE"
	fetch "$BASE_URL" "$XZFILE"
fi
echo "$BASE_SHA256  $XZFILE" | sha256sum -c --status - || die "sha256 of the base image does not match $BASE_SHA256"
say "sha256 verified"

rm -f "$IMG"
say "decompressing"
xz -dc "$XZFILE" >"$IMG"

# --- grow ---------------------------------------------------------------------
say "growing the image by $GROW_MIB MiB"
truncate -s "+${GROW_MIB}M" "$IMG"
parted -s -a minimal "$IMG" resizepart 2 100%
# part_geometry N: start and size in bytes of partition N.
part_geometry() {
	parted -ms "$IMG" unit B print | awk -F: -v n="$1" '$1 == n { sub(/B$/, "", $2); sub(/B$/, "", $4); print $2, $4 }'
}
attach_loops() {
	local l i
	l=$(losetup -f --show -P "$IMG")
	LOOPS=("$l")
	if command -v udevadm >/dev/null 2>&1; then udevadm settle || true; fi
	for i in 1 2 3 4 5; do [ -b "${l}p2" ] && break; sleep 1; done
	if [ -b "${l}p2" ]; then
		BOOTDEV=${l}p1 ROOTDEV=${l}p2
		return
	fi
	# No partition scanning here (a container without udev): one loop device per partition.
	say "no partition devices for $l; using offset loop devices"
	losetup -d "$l"
	LOOPS=()
	local g start size
	for i in 1 2; do
		g=$(part_geometry "$i")
		start=${g% *} size=${g#* }
		[[ "$start" =~ ^[0-9]+$ && "$size" =~ ^[0-9]+$ ]] || die "could not read the geometry of partition $i"
		l=$(losetup -f --show -o "$start" --sizelimit "$size" "$IMG")
		LOOPS+=("$l")
		if [ "$i" = 1 ]; then BOOTDEV=$l; else ROOTDEV=$l; fi
	done
}
attach_loops
e2fsck -fy "$ROOTDEV" || [ $? -le 1 ] || die "e2fsck failed on the root file system"
resize2fs "$ROOTDEV"

mount "$ROOTDEV" "$MNT"
# The FAT partition is mounted where the image's own fstab puts it (/boot/firmware, or /boot).
BOOTMP=$(awk '$1 !~ /^#/ && $3 == "vfat" && $2 ~ /^\/boot/ { print $2; exit }' "$MNT/etc/fstab")
[ -n "$BOOTMP" ] || BOOTMP=/boot/firmware
mkdir -p "$MNT$BOOTMP"
if ! mount "$BOOTDEV" "$MNT$BOOTMP"; then
	# A dry run does not touch the boot partition; a real build needs it (kernel updates).
	[ -n "$SKIP_CHROOT" ] || die "cannot mount the boot partition $BOOTDEV (is vfat supported by this kernel?)"
	say "warning: boot partition not mounted (dry run)"
fi
# shellcheck disable=SC1091 # the image's os-release
CODENAME=$(. "$MNT/etc/os-release" && echo "${VERSION_CODENAME:-}")
say "root: $ROOTDEV ($CODENAME), boot: $BOOTDEV at $BOOTMP"

# --- chroot -------------------------------------------------------------------
in_chroot() {
	chroot "$MNT" /usr/bin/env -i HOME=/root LC_ALL=C TERM=dumb \
		PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
		DEBIAN_FRONTEND=noninteractive APT_LISTCHANGES_FRONTEND=none \
		"$@"
}

apt_get() {
	in_chroot apt-get -y -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef \
		-o APT::Install-Recommends=true "$@"
}

chroot_enter() {
	if [ "$(uname -m)" != aarch64 ]; then
		command -v qemu-aarch64-static >/dev/null 2>&1 || die "this host is not arm64 and qemu-aarch64-static is missing (install qemu-user-static)"
		[ -e /proc/sys/fs/binfmt_misc/qemu-aarch64 ] || die "binfmt for qemu-aarch64 is not registered"
		install -m 0755 "$(command -v qemu-aarch64-static)" "$MNT/usr/bin/qemu-aarch64-static"
		QEMU_COPIED=1
	fi
	mount --bind /dev "$MNT/dev"
	mount -t devpts devpts "$MNT/dev/pts"
	mount -t proc proc "$MNT/proc"
	mount -t sysfs sysfs "$MNT/sys"
	# A private /run: nothing from the host, and the packages see that systemd is not running
	# (their postinst scripts then enable nothing and start nothing).
	mount -t tmpfs tmpfs "$MNT/run"
	# Name resolution only for the build.
	if [ -L "$MNT/etc/resolv.conf" ]; then
		RESOLV_STATE="link"
		RESOLV_LINK=$(readlink "$MNT/etc/resolv.conf")
		rm -f "$MNT/etc/resolv.conf"
	elif [ -e "$MNT/etc/resolv.conf" ]; then
		RESOLV_STATE="file"
		mv "$MNT/etc/resolv.conf" "$MNT/etc/resolv.conf.aslcp-build"
	fi
	cp /etc/resolv.conf "$MNT/etc/resolv.conf"
	# No service may start in the chroot.
	printf '#!/bin/sh\nexit 101\n' >"$MNT/usr/sbin/policy-rc.d"
	chmod 0755 "$MNT/usr/sbin/policy-rc.d"
}

chroot_leave() {
	rm -f "$MNT/usr/sbin/policy-rc.d" "$MNT/etc/resolv.conf"
	case "$RESOLV_STATE" in
	link) ln -s "$RESOLV_LINK" "$MNT/etc/resolv.conf" ;;
	file) mv "$MNT/etc/resolv.conf.aslcp-build" "$MNT/etc/resolv.conf" ;;
	esac
	[ -z "$QEMU_COPIED" ] || rm -f "$MNT/usr/bin/qemu-aarch64-static"
	umount "$MNT/run" "$MNT/sys" "$MNT/proc" "$MNT/dev/pts" "$MNT/dev"
}

ASLCP_VERSION=n/a
ASL3_VERSION=n/a

install_packages() {
	case "$CODENAME" in
	bookworm) local nn=12 ;;
	trixie) local nn=13 ;;
	*) die "unsupported base release '$CODENAME' (bookworm or trixie)" ;;
	esac

	say "apt update, full-upgrade"
	apt_get update
	apt_get full-upgrade
	apt_get install --no-install-recommends ca-certificates gpg

	say "AllStarLink apt repository (asl-apt-repos.deb${nn}_all.deb)"
	fetch "$ASL_REPOS_URL/asl-apt-repos.deb${nn}_all.deb" "$MNT/tmp/asl-apt-repos.deb"
	apt_get install /tmp/asl-apt-repos.deb
	local fpr
	fpr=$(in_chroot gpg --batch --show-keys --with-colons /etc/apt/keyrings/allstarlink.gpg |
		awk -F: '$1 == "pub" { p = 1; next } p && $1 == "fpr" { print $10; exit }')
	[ "$fpr" = "$ASL_KEY_FPR" ] || die "AllStarLink key fingerprint is '$fpr', expected $ASL_KEY_FPR"
	say "AllStarLink key verified ($fpr)"

	say "ASLCP apt repository ($APT_URL, $CHANNEL), set up like install.sh does"
	fetch "$APT_URL/aslcp-archive-keyring.gpg" "$WORKDIR/aslcp-keyring.gpg"
	[ -s "$WORKDIR/aslcp-keyring.gpg" ] || die "the ASLCP key is empty"
	if head -c 64 "$WORKDIR/aslcp-keyring.gpg" | grep -q 'BEGIN PGP'; then
		die "the ASLCP key is ASCII-armored, a binary keyring is expected"
	fi
	install -d -m 0755 "$MNT/usr/share/keyrings"
	install -m 0644 "$WORKDIR/aslcp-keyring.gpg" "$MNT/usr/share/keyrings/aslcp-archive-keyring.gpg"
	printf '%s\n' "deb [signed-by=/usr/share/keyrings/aslcp-archive-keyring.gpg] $APT_URL $CHANNEL main" \
		>"$MNT/etc/apt/sources.list.d/aslcp.list"
	chmod 0644 "$MNT/etc/apt/sources.list.d/aslcp.list"
	say "ASLCP key fingerprint(s): $(in_chroot gpg --batch --show-keys --with-colons /usr/share/keyrings/aslcp-archive-keyring.gpg | awk -F: '$1 == "fpr" { printf "%s ", $10 }')"

	apt_get update
	say "installing asl3 and asl3-menu"
	apt_get install asl3 asl3-menu

	say "installing aslcp"
	if [ -n "$ASLCP_DEB" ]; then
		[ "$(dpkg-deb -f "$ASLCP_DEB" Architecture)" = arm64 ] || die "ASLCP_DEB is not an arm64 package"
		install -m 0644 "$ASLCP_DEB" "$MNT/tmp/aslcp.deb"
		# A local file: apt resolves its dependencies; the repository stays configured for updates.
		ASLCP_INSTALLER=1 apt_get install /tmp/aslcp.deb
	else
		ASLCP_INSTALLER=1 apt_get install aslcp
	fi
	# shellcheck disable=SC2016 # dpkg-query format, not shell
	ASLCP_VERSION=$(in_chroot dpkg-query -W -f='${Version}' aslcp)
	# shellcheck disable=SC2016 # dpkg-query format, not shell
	ASL3_VERSION=$(in_chroot dpkg-query -W -f='${Version}' asl3)
	say "aslcp $ASLCP_VERSION, asl3 $ASL3_VERSION"
}

# First-boot units and scripts, then every unit that has to run on a fresh card.
# The package postinst scripts enable nothing here (no systemd in a chroot), so
# this does what aslcp's postinst does on a running system, and the same for asterisk.
install_firstboot() {
	install -d -m 0755 "$MNT/usr/libexec/aslcp-image" "$MNT/var/lib/aslcp-image"
	install -m 0755 "$SCRIPT_DIR/aslcp-firstboot.sh" "$MNT/usr/libexec/aslcp-image/aslcp-firstboot.sh"
	install -m 0755 "$SCRIPT_DIR/aslcp-setup-link.sh" "$MNT/usr/libexec/aslcp-image/aslcp-setup-link.sh"
	install -m 0644 "$SCRIPT_DIR/aslcp-firstboot.service" "$MNT/etc/systemd/system/aslcp-firstboot.service"
	install -m 0644 "$SCRIPT_DIR/aslcp-setup-link.service" "$MNT/etc/systemd/system/aslcp-setup-link.service"
	: >"$MNT/var/lib/aslcp-image/firstboot-needed"

	local u
	for u in asterisk.service aslcp-helper.socket aslcp.service aslcp-wifi-watchdog.timer \
		aslcp-firstboot.service aslcp-setup-link.service; do
		in_chroot systemctl enable "$u" >/dev/null || die "systemctl enable $u failed"
		compgen -G "$MNT/etc/systemd/system/*.wants/$u" >/dev/null || die "$u is not enabled (no symlink)"
	done
	say "enabled: asterisk, aslcp, aslcp-helper.socket, aslcp-wifi-watchdog.timer, first-boot units"
}

# Checks on the finished root: nothing per-card may exist yet.
verify_clean() {
	local bad=0 f
	for f in var/lib/aslcp/setup-code var/lib/aslcp/aslcp.db; do
		if [ -e "$MNT/$f" ]; then say "ASSERT FAILED: /$f exists"; bad=1; fi
	done
	if compgen -G "$MNT/etc/ssh/ssh_host_*" >/dev/null; then say "ASSERT FAILED: SSH host keys present"; bad=1; fi
	[ "$(cat "$MNT/etc/machine-id")" = uninitialized ] || { say "ASSERT FAILED: machine-id is not 'uninitialized'"; bad=1; }
	[ -e "$MNT/var/lib/aslcp-image/firstboot-needed" ] || { say "ASSERT FAILED: first-boot marker missing"; bad=1; }
	grep -q '^secret = make4An1ce-secret' "$MNT/etc/asterisk/manager.conf" 2>/dev/null ||
		say "note: manager.conf no longer has the stock AMI secret; the first-boot script will still replace the [admin] secret"
	for f in user-data meta-data network-config; do
		[ -e "$MNT$BOOTMP/$f" ] || say "note: $BOOTMP/$f is not in the base image"
	done
	[ "$bad" -eq 0 ] || die "the image contains per-card state, see above"
}

cleanup_image() {
	say "cleaning the image"
	in_chroot apt-get clean
	rm -rf "$MNT"/var/lib/apt/lists/* "$MNT"/var/cache/apt/*.bin "$MNT"/var/cache/apt/archives/*.deb
	rm -f "$MNT"/etc/ssh/ssh_host_*
	# "uninitialized": systemd sees a first boot, commits a new machine-id, and the
	# image's first-boot services (ssh host keys, resize) are keyed to that.
	printf 'uninitialized\n' >"$MNT/etc/machine-id"
	rm -f "$MNT/var/lib/dbus/machine-id"
	rm -rf "$MNT"/tmp/* "$MNT"/var/tmp/* "$MNT"/root/.bash_history "$MNT"/root/.cache
	rm -f "$MNT"/var/lib/dpkg/*-old "$MNT"/var/cache/debconf/*-old "$MNT"/etc/*-
	rm -rf "$MNT"/var/log/journal/*
	find "$MNT/var/log" -type f \( -name '*.gz' -o -name '*.[0-9]' -o -name '*.old' \) -delete
	find "$MNT/var/log" -type f -exec truncate -s 0 {} +
	rm -rf "$MNT"/run/* 2>/dev/null || true
}

if [ -n "$SKIP_CHROOT" ]; then
	say "SKIP_CHROOT=1: no packages are installed (dry run)"
else
	chroot_enter
	install_packages
	install_firstboot
	# The manifest is taken before the cleanup, while dpkg is intact.
	# shellcheck disable=SC2016 # dpkg-query format, not shell
	in_chroot dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\t${source:Package}\n' |
		sort >"$OUT_DIR/$BASENAME.manifest.txt"
	cleanup_image
	verify_clean
	chroot_leave
fi
if [ -n "$SKIP_CHROOT" ]; then
	printf 'dry run, no package list\n' >"$OUT_DIR/$BASENAME.manifest.txt"
fi
sync

# --- unmount, zero free blocks, shrink -------------------------------------------
say "unmounting"
umount -R "$MNT"
if command -v zerofree >/dev/null 2>&1; then
	say "zerofree"
	e2fsck -fy "$ROOTDEV" >/dev/null || [ $? -le 1 ]
	zerofree "$ROOTDEV"
fi
e2fsck -fy "$ROOTDEV" || [ $? -le 1 ] || die "e2fsck failed"
min_blocks=$(resize2fs -P "$ROOTDEV" 2>/dev/null | awk -F': ' '/Estimated minimum size/ { print $2 }')
[[ "$min_blocks" =~ ^[0-9]+$ ]] || die "could not read the minimum file system size"
bsize=$(dumpe2fs -h "$ROOTDEV" 2>/dev/null | awk -F': *' '/^Block size/ { print $2 }')
[[ "$bsize" =~ ^[0-9]+$ ]] || die "could not read the file system block size"
new_blocks=$((min_blocks + MARGIN_MIB * 1048576 / bsize))
say "shrinking the root file system to $new_blocks blocks of $bsize bytes"
resize2fs "$ROOTDEV" "$new_blocks"
e2fsck -fy "$ROOTDEV" || [ $? -le 1 ] || die "e2fsck failed after the shrink"
# resize2fs may settle on a slightly different size than asked: use the real one.
new_blocks=$(dumpe2fs -h "$ROOTDEV" 2>/dev/null | awk -F': *' '/^Block count/ { print $2 }')
[[ "$new_blocks" =~ ^[0-9]+$ ]] || die "could not read the file system size"
detach_loops
part_start=$(part_geometry 2 | cut -d" " -f1)
[[ "$part_start" =~ ^[0-9]+$ ]] || die "could not read the start of partition 2"
part_end=$((part_start + new_blocks * bsize))
# Shrinking asks "are you sure"; -s refuses it, so the answer is fed through a pretend tty.
printf 'Yes\n' | parted ---pretend-input-tty -a minimal "$IMG" resizepart 2 "${part_end}B" >/dev/null
truncate -s "$(((part_end + 1 + 511) / 512 * 512))" "$IMG"

# --- results ---------------------------------------------------------------------
say "checksums and compression"
extract_size=$(stat -c %s "$IMG")
extract_sha=$(sha256sum "$IMG" | awk '{ print $1 }')
XZOUT="$OUT_DIR/$BASENAME.img.xz"
xz -T0 -"$XZ_LEVEL" -c "$IMG" >"$XZOUT"
dl_size=$(stat -c %s "$XZOUT")
dl_sha=$(sha256sum "$XZOUT" | awk '{ print $1 }')
(cd "$OUT_DIR" && sha256sum "$BASENAME.img.xz" >"$BASENAME.img.xz.sha256")
printf '%s  %s\n' "$extract_sha" "$BASENAME.img" >"$OUT_DIR/$BASENAME.img.sha256"
jq -n \
	--arg name "$NAME" --arg version "$IMAGE_DATE" --arg file "$BASENAME.img.xz" \
	--arg base_release_date "$BASE_DATE" --arg base_url "$BASE_URL" --arg base_sha256 "$BASE_SHA256" \
	--arg aslcp_version "$ASLCP_VERSION" --arg asl3_version "$ASL3_VERSION" --arg channel "$CHANNEL" \
	--arg build_date "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	--argjson image_download_size "$dl_size" --arg image_download_sha256 "$dl_sha" \
	--argjson extract_size "$extract_size" --arg extract_sha256 "$extract_sha" \
	'{name: $name, version: $version, file: $file, base_release_date: $base_release_date, base_url: $base_url,
	  base_sha256: $base_sha256, aslcp_version: $aslcp_version, asl3_version: $asl3_version, channel: $channel,
	  build_date: $build_date, image_download_size: $image_download_size, image_download_sha256: $image_download_sha256,
	  extract_size: $extract_size, extract_sha256: $extract_sha256}' >"$OUT_DIR/image-info.json"
# Raspberry Pi Imager repository (Content Repository, custom URL), see packaging/image/README.md.
if [ -z "${IMAGE_BASE_URL:-}" ]; then
	say "warning: IMAGE_BASE_URL is not set: the url in aslcp-imager.json is a placeholder"
fi
write_imager_json "$OUT_DIR/aslcp-imager.json" "$IMAGE_DATE" "$BASENAME.img.xz" "$dl_size" "$dl_sha" \
	"$extract_size" "$extract_sha" "$BASE_INIT" "$BASE_DEVICES" "${IMAGE_BASE_URL:-https://example.invalid/set-IMAGE_BASE_URL}"
rm -f "$IMG"
say "done:"
ls -l "$OUT_DIR" >&2
