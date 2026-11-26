# Backup and restore (3.0, format 2)

Goal: download one file from a working node, re-image the SD card, install ASLCP, upload the file, and end up with the same node: the same station and mode settings, the same SA818 channel, the same ASLCP users, favorites, schedules and settings.

ASLCP 3 runs its own node (`aslcp-node`, see [NATIVE_NODE.md](NATIVE_NODE.md)), so a backup no longer holds any Asterisk files.

## What a backup contains

| Part | Source | Archive path | Notes |
|---|---|---|---|
| ASLCP database | `/var/lib/aslcp/aslcp.db` | `aslcp/aslcp.db` | Always. Users (password hashes), roles, favorites per mode, schedules, settings, public page, audit log. Snapshot with `VACUUM INTO`. Sessions are not restored (everyone logs in again). |
| ASLCP config file | `/etc/aslcp/aslcp.conf` | `aslcp/aslcp.conf` | Always. The web port (`listen` in `[server]`) is not restored: the node keeps its own. |
| Node config | `/etc/aslcp/node.json` | `node/node.json` | When present. Station (callsign, radio, IDs, active mode and target) and the settings of every mode. Mode passwords are left out unless the person creating the backup asks for them (`parts.secrets`). |
| SA818 settings | `/etc/aslcp/sa818.json` | `node/sa818.json` | When present. The SA818 keeps its channel in its own memory, which cannot be read back; this file is the only copy. |
| Update channel | suite in `/etc/apt/sources.list.d/aslcp.list` | manifest `channel` | Recorded and shown in the preview; not changed by a restore. |

**Not included** (listed on the restore page as "set up separately"): network and WiFi, hostname, timezone, SSH keys and OS user accounts, other software, firewall rules other than ASLCP's own.

## File format

`aslcp-backup-<hostname>-<YYYYMMDD-HHMM>.tar.gz`, with `manifest.json` first.

`manifest.json`: `format` (2), `created`, `createdBy`, `aslcpVersion`, `hostname`, `timezone`, `arch`, `osId`, `osCodename`, `osName` (from `/etc/os-release`), `callsign`, `channel`, `parts` (`node`, `sa818`, `secrets`), `skipped`, and one entry per file: path, type, size, sha256, owner and group name, mode.

- Format 1 (ASLCP 2.x, an Asterisk node) is refused with a message that says to set the node up again.
- A format newer than 2 is refused ("update ASLCP first").

**No encryption.** A backup made with passwords holds every network password of the node in plain text, and every backup holds the ASLCP password hashes. Store it like a password.

## Creating a backup

Settings, Maintenance, Backup and restore (admin only). The request body is `{"secrets": true|false}`; audited as `backup.create` with `secrets` and `size`.

1. The server asks the helper (`backup-create`). The helper reads `node.json` (removing the mode passwords unless `secrets` is set) and `sa818.json` and streams a JSON header plus a tar of those parts over its socket.
2. The server adds `aslcp.db` (`VACUUM INTO` a temp file in its private data directory) and `aslcp.conf`, writes `manifest.json` first, gzips and streams it to the HTTP response. Size limit 256 MiB.

## Restoring

### Where

- **Fresh install (the main case):** the first-run page offers "Restore from a backup". It needs the one-time setup code (`sudo aslcp admin setup-code`), so nobody on the network can push a configuration onto a new node.
- **Running node:** Settings, Maintenance, Backup and restore, Restore (admin, password re-entry).

### Steps

1. **Upload and check.** The server checks the manifest, every sha256, the paths (only the fixed archive paths above), the sizes and the versions. A different architecture or Debian release is a warning only.
2. **Preview** (the helper runs the restore as a dry run first). The page shows the backup (hostname, date, ASLCP version, callsign), this node, the users after the restore, the number of favorites and schedules, the modes that will be set up, the SA818 channel (bandwidth, RX and TX frequency, tones, squelch), which parts are present, warnings and the "set up separately" checklist.
   - Warning codes: `os_release`, `arch`, `skipped_files`, `no_secrets` (the backup has no passwords), `missing_secrets` (modes that will be left out, see below), `no_node` (the backup has no node config).
3. **Apply** (helper `backup-restore`; the request body is `{"code": "..."}`, the setup code on a fresh install):
   1. **Restore node settings.** A blank password in the backup is filled from this node's current config. A mode that still lacks a required password is left out and named in `missingSecrets`; if it was the active mode, the node starts with no active mode. The result is validated before it is written (0600, owned by `aslcp-node`).
   2. **Restore SA818 settings** (0600, owned by `aslcp-node`).
   3. **Restore aslcp.conf**, keeping this node's `listen` port.
   4. **Restart the node** (`systemctl restart aslcp-node`).
   5. The server stages `aslcp.db.restore` and restarts itself; on start it swaps the database in (the previous one is kept as `aslcp.db.pre-restore`) and runs migrations forward.
4. **Result.** After the restart the page reconnects and shows the health check and the checklist. Log in with the old ASLCP account. Set up again the modes listed in `missingSecrets`.

## Hardware notes

- The node keeps its node numbers and passwords, so the old SD card must not be running at the same time (registration would flap between the two).
- The restored SA818 settings are programmed into the module when the node starts after the restore; the radio tune page can program it again.

## Security

- Admin only (or the setup code on a fresh install); every backup, upload and restore is audited.
- Mode passwords are only in the file when asked for; the download dialog says so.
- The helper validates the archive again itself (paths, types, sizes, sha256 from the manifest, the node config with the node's own validation) and writes only to the fixed destinations above; it never trusts the server's check.
- Upload size limit 256 MiB; a decompression limit (total size from the manifest, checked while extracting) guards against archive bombs.
