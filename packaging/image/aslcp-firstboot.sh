#!/bin/sh
# ASLCP image, first boot: give this card its own secrets.
#
# The image is built once and flashed many times, so every secret that the
# packages create at install time would be identical on every card. This script
# replaces them with random values before asterisk and aslcp start. It runs once
# (aslcp-firstboot.service, only while the marker file exists), edits the exact
# key lines in place (owner, mode and every other byte stay as they are), removes
# the marker and disables the unit. It never installs or upgrades anything and
# never reboots.
#
#   /etc/asterisk/manager.conf  [admin] secret    AMI password (aslcp reads it with "auto")
#   /etc/asterisk/iax.conf      Your_Secret_Password_Here placeholders, if a package
#                                                 version still ships them
#
# Not changed on purpose: "secret = allstar" of [allstar-public] in iax.conf, the
# well-known password of AllStarLink's public IAX user (the same on every node).
# SSH host keys, machine-id and the ASLCP setup code are created by their own
# first-boot mechanisms (see packaging/image/README.md).
#
# Test hooks: ROOT=<dir> prefixes every path; SYSTEMCTL=<cmd> replaces systemctl.
set -eu

ROOT=${ROOT:-}
SYSTEMCTL=${SYSTEMCTL:-systemctl}
MARKER=$ROOT/var/lib/aslcp-image/firstboot-needed
MANAGER_CONF=$ROOT/etc/asterisk/manager.conf
IAX_CONF=$ROOT/etc/asterisk/iax.conf

log() { printf 'aslcp-firstboot: %s\n' "$*"; }

[ -e "$MARKER" ] || { log "nothing to do (no marker)"; exit 0; }

# rand_hex BYTES: BYTES random bytes as lower-case hex.
rand_hex() {
	od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'
}

# set_in_place FILE NEWCONTENT_FILE: rewrite FILE through "cat >" so the inode,
# owner, group and mode are kept.
set_in_place() {
	cat "$2" >"$1"
}

# AMI secret: only the "secret" line inside [admin]; text after the value
# (a ";" comment) stays.
if [ -f "$MANAGER_CONF" ]; then
	new=$(rand_hex 8)
	tmp=$(mktemp)
	awk -v new="$new" '
		/^[ \t]*\[/ { sec = ($0 ~ /^[ \t]*\[admin\][ \t]*(;.*)?$/) }
		sec && !done && /^[ \t]*secret[ \t]*=/ {
			i = index($0, "=")
			head = substr($0, 1, i)
			rest = substr($0, i + 1)
			match(rest, /^[ \t]*/)
			ws = substr(rest, 1, RLENGTH)
			rest = substr(rest, RLENGTH + 1)
			tail = ""
			if (match(rest, /[ \t]*;.*$/)) tail = substr(rest, RSTART)
			print head ws new tail
			done = 1
			next
		}
		{ print }
		END { if (!done) exit 3 }' "$MANAGER_CONF" >"$tmp" || : >"$tmp"
	if [ -s "$tmp" ]; then
		set_in_place "$MANAGER_CONF" "$tmp"
		log "AMI secret replaced"
	else
		log "warning: no [admin] secret line in $MANAGER_CONF; left as it is"
	fi
	rm -f "$tmp"
fi

# IAX placeholders (one value for all of them, like the package's own postinst).
if [ -f "$IAX_CONF" ] && grep -q 'Your_Secret_Pas\{1,2\}word_Here' "$IAX_CONF"; then
	new=$(rand_hex 16)
	tmp=$(mktemp)
	sed -e "s/Your_Secret_Password_Here/$new/g" -e "s/Your_Secret_Pasword_Here/$new/g" "$IAX_CONF" >"$tmp"
	set_in_place "$IAX_CONF" "$tmp"
	rm -f "$tmp"
	log "IAX placeholder secret replaced"
fi

rm -f "$MARKER"
"$SYSTEMCTL" disable aslcp-firstboot.service >/dev/null 2>&1 || true
log "done"
