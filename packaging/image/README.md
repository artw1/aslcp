# ASLCP for ASL3: Raspberry Pi image

A flashable Raspberry Pi image with ASL3 (`asl3`, `asl3-menu`) and ASLCP on top
of the latest official Raspberry Pi OS Lite (64-bit). Boot it, open the setup
link, create the administrator, and configure the node in ASLCP.

This is an unofficial image: it is based on Raspberry Pi OS Lite and AllStarLink 3,
and is not an official AllStarLink or Raspberry Pi product. It contains no
Allmon3, no Apache landing page and none of the AllStarLink appliance image
(asl3-appliance-pi) additions.

Status: step 1 of the image plan. The build is manual and nothing is published
(no release, no Imager list). The image has not been booted on hardware yet.

## Build

Locally, on an arm64 Debian or Ubuntu host (a native chroot, no emulation), as root:

    sudo packaging/image/build-image.sh

Host tools: curl, jq, xz, parted, e2fsprogs, util-linux; zerofree is optional (a
smaller download). About 9 GB of free disk. Result in `./image-out/`:

- `aslcp-asl3-arm64-YYYY.MM.DD.img.xz` and `.img.xz.sha256` (download)
- `aslcp-asl3-arm64-YYYY.MM.DD.img.sha256` (the unpacked image)
- `aslcp-asl3-arm64-YYYY.MM.DD.manifest.txt` (package list, see "Licences")
- `image-info.json` (base image, versions, sizes and sha256 values, in the field
  names of a Raspberry Pi Imager OS list)

Options (environment): `CHANNEL=stable|beta` (the ASLCP apt channel inside the
image, default stable), `ASLCP_DEB=/path/aslcp_..._arm64.deb` (install this
package instead of the repository's), `BASE_URL`/`BASE_SHA256` (another base
image), `OUT_DIR`, `WORKDIR`, `GROW_MIB`, `MARGIN_MIB`, `XZ_LEVEL`, `SKIP_CHROOT=1`
(dry run without packages). The script header lists them all.

On GitHub: Actions, "Pi image", Run workflow. Inputs: `channel` (stable or beta)
and `aslcp_source` (`build`: build the arm64 deb from the selected ref, or `repo`:
install the published package). The result is a run artifact kept for 14 days.
The workflow runs on `ubuntu-24.04-arm`, needs no secrets and has read-only
permissions.

What the script does:

1. Reads `os_list_imagingutility_v4.json`, takes the "Raspberry Pi OS Lite (64-bit)"
   entry, downloads it and verifies the sha256 (the official list has none, so
   the `.sha256` file next to the image is used).
2. Grows the image, mounts root and boot (boot where the image's fstab puts it,
   `/boot/firmware`), and enters a chroot with `policy-rc.d` (no service starts)
   and a private `/run`.
3. `apt full-upgrade`; installs `asl-apt-repos.debNN_all.deb` (12 bookworm, 13
   trixie) and checks the AllStarLink key fingerprint; adds the ASLCP apt
   repository exactly like `install.sh` (same keyring and source file, so later
   updates come from the repository); installs `asl3 asl3-menu` and `aslcp`.
4. Enables `asterisk`, `aslcp`, `aslcp-helper.socket`, `aslcp-wifi-watchdog.timer`
   and the two first-boot units (a chroot has no running systemd, so the package
   scripts enable nothing themselves).
5. Cleans (apt lists and cache, logs, SSH host keys, `machine-id`), checks that no
   per-card state exists, shrinks the file system (300 MiB free), compresses with
   `xz`, writes the checksums, `image-info.json` and the manifest.

cloud-init is left alone: `cloud-init`, `rpi-cloud-init-mods`, `raspberrypi-sys-mods`
and `user-data`, `meta-data`, `network-config` on the boot partition stay as in
the base image, and the image is never booted during the build.

## Flash

With Raspberry Pi Imager: Choose OS, Use custom, select the `.img.xz`. Imager
2.x offers its customisation (host name, user, Wi-Fi, SSH) only for images it
knows the `init_format` of; a locally chosen file may get no customisation page.
Without it, set the user, Wi-Fi and SSH by editing `user-data` and
`network-config` on the boot partition (cloud-init), or use the ASLCP setup
hotspot for Wi-Fi. Any other tool that writes `.img.xz` (balenaEtcher, `xzcat`
and `dd`) works too.

## First boot

1. `aslcp-firstboot.service` (once, before asterisk and aslcp) gives the card its
   own secrets and removes its marker `/var/lib/aslcp-image/firstboot-needed`.
2. Raspberry Pi OS creates the SSH host keys and the machine-id, applies cloud-init
   and expands the root file system, as on the stock image.
3. asterisk and aslcp start. ASLCP creates its one-time setup code. The second
   unit, `aslcp-setup-link.service`, writes the setup link to the console, to the
   login prompt (`/run/issue.d`, root only, gone after a reboot) and to its journal
   (`journalctl -u aslcp-setup-link`). It prints nothing once an administrator
   exists. The code is also shown with `sudo aslcp admin setup-code`.
4. Open `http://<address>:8080/#/setup?code=...` (or `http://<host name>.local:8080/...`).

There is no apt upgrade and no reboot at first boot. Updates are the usual apt
updates (ASLCP also updates itself from its Settings page).

### What is per card

| Secret | In the image | Regenerated at first boot |
| --- | --- | --- |
| AMI `admin` password | `secret` in `[admin]` of `/etc/asterisk/manager.conf` (stock value of the package) | 16 random hex digits; ASLCP reads it with `username = auto`, `secret = auto` |
| IAX placeholder passwords | `Your_Secret_Password_Here` in `/etc/asterisk/iax.conf`, only if a package version still ships it (the current one does not; its postinst fills it at install time) | 32 random hex digits |
| ASLCP setup code and database | not present (created by the service on its first start) | created by aslcp |
| SSH host keys, machine-id | removed / `uninitialized` | by Raspberry Pi OS |
| Node numbers, node passwords, `rpt.conf` | not seeded | entered by the owner in ASLCP |

Left as shipped on purpose: `secret = allstar` of `[allstar-public]` in `iax.conf`,
the well-known password of AllStarLink's public IAX user (the same on every node).
The `asl-telemetry` timer of the asl3 package is enabled as on any ASL3 node.

## Open items to verify on hardware

The test board is a Raspberry Pi 3B+. Pi 4 and Pi 5 are untested.

- Boots; root file system expands; SSH host keys appear; cloud-init applies the
  user and Wi-Fi from Imager or from `user-data`.
- `machine-id` handling: the build writes `uninitialized` (as pi-gen does).
  Confirm the base image used the same and that `regenerate_ssh_host_keys` runs.
- `aslcp-firstboot` ran before asterisk (`journalctl -u aslcp-firstboot`), the AMI
  secret changed, aslcp connects ("auto" credentials).
- The setup link appears on the console and at the login prompt (`agetty --reload`
  needs a recent util-linux), and `.local` resolves (avahi in the base image).
- The kernel and firmware packages upgraded in the chroot (`full-upgrade`) boot
  correctly; a stock base image of the same date is the reference.
- Setup hotspot (`aslcp-wifi-watchdog`) works on the 3B+ without Ethernet.
- Imager 2.x with `init_format` `cloudinit-rpi` once an Imager list exists (a later step).

## Licences and sources

The image contains Raspberry Pi OS Lite and AllStarLink packages, many of them
under the GPL and other licences. The package list with versions and source
package names is in the `.manifest.txt` of each build. Sources: Debian
(https://deb.debian.org and its source archive), Raspberry Pi
(https://archive.raspberrypi.com) and AllStarLink (https://repo.allstarlink.org
and https://github.com/AllStarLink). ASLCP itself is GPL-3.0-or-later (see
`/usr/share/doc/aslcp/copyright`). A release of the image must carry the manifest
and a pointer to these sources. The build scripts here are original work, not
copied from AllStarLink's image scripts (which are AGPL-3.0).
