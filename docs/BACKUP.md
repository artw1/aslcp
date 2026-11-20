# Backup and full restore (2.0)

Goal: download one file from a working node, re-image the SD card with a fresh ASL3 image, install ASLCP, upload the file, and end up with the same node: same Asterisk configuration, same ASLCP users, favorites and settings, same config history. Target: 1.1.3 (the settings forms move to beta.4, guided actions to beta.5).

## What a backup contains

| Part | Source | Default | Notes |
|---|---|---|---|
| Asterisk configuration | all of `/etc/asterisk` (every file and subdirectory, not only `*.conf`: `allmon.ini.php`, `extensions.ael`, `custom/`, `broadcastify/`, ...) | always | Owner, group and mode are kept as names plus mode. Each file is also classified as stock or changed (see "Restoring onto a newer ASL3"). |
| AstDB | `/var/lib/asterisk/astdb.sqlite3` (confirmed on ASL3, asterisk:asterisk 0644, ~12 KB) | always | Allow/deny lists and other state `app_rpt` keeps in AstDB. Copied consistently (SQLite backup API, read-only) while Asterisk runs. |
| ASLCP database | `/var/lib/aslcp/aslcp.db` | always | Users (password hashes), roles, favorites, schedules, settings, public page, audit log. Snapshot with `VACUUM INTO`. Sessions are not restored (everyone logs in again). |
| ASLCP config file | `/etc/aslcp/aslcp.conf` | always | Port, AMI settings, update URL. |
| Config history | `/var/lib/aslcp-config-history` | on | The git history of config changes, so history and Restore keep working after the re-image. Can be unticked to make the file smaller. |
| Other AMI clients | `/etc/allmon3/` if present | on when present | Allmon3 config (it shares the AMI login). |
| Custom sounds | `/var/lib/asterisk/sounds/custom` and `/usr/share/asterisk/sounds/custom` (both exist on ASL3; de-duplicated by real path in case one links to the other) | on when present | Announcements, IDs. |
| SA818 radio settings | `/etc/sa818.conf` if present (written by `sa818-menu`) | always when present | The SA818 keeps its programmed settings in its own EEPROM, which survives a re-image, but they cannot be read back from the module; this file is the only copy. Restored to the same path, root-owned. |
| DVSwitch | `/opt/Analog_Bridge/Analog_Bridge.ini`, `/opt/Analog_Bridge/dvsm.macro`, `/opt/MMDVM_Bridge/MMDVM_Bridge.ini`, `/opt/YSFGateway/YSFGateway.ini`, `/opt/P25Gateway/P25Gateway.ini`, `/opt/NXDNGateway/NXDNGateway.ini` (those that exist) | on when `/opt/Analog_Bridge` exists | Exactly the files the DVSwitch setup page may edit, plus the macro file, and nothing else. Not redacted: `MMDVM_Bridge.ini` holds the DVSwitch (DMR master) password. The setup page's own history copies (`/var/lib/aslcp-dvswitch-history`) are not included: they hold older passwords, and the restore makes a fresh copy of every file it replaces. Symlinks, non-regular files and files over 1 MiB are skipped and listed as skipped. |
| Update channel | suite in `/etc/apt/sources.list.d/aslcp.list` | always | Stored in the manifest, re-applied on restore. |

**Not included** (listed on the restore page as "set up separately"): network and Wi-Fi, hostname, timezone, SSH keys and OS user accounts, other software (for example an M17 bridge and its service), firewall rules other than ASLCP's own, cron jobs, anything installed outside the packages above. The manifest records hostname, timezone and the list of installed `asl3*`, `allmon3` and related package names, and the restore page shows them as a checklist so nothing is forgotten.

## File format

`aslcp-backup-<hostname>-<YYYYMMDD-HHMM>.tar.gz`.

```
manifest.json
asterisk/etc/...            (the /etc/asterisk tree)
asterisk/astdb.sqlite3
aslcp/aslcp.db
aslcp/aslcp.conf
history/...                 (optional, the bare git repository)
allmon3/...                 (optional)
sounds/...                  (optional)
dvswitch/opt/...            (optional, the six DVSwitch files at their /opt paths)
```

`manifest.json`: format version (1), created time, created by (ASLCP user), ASLCP version, ASL3 and Asterisk versions, hostname, timezone, architecture, the Debian release (`osId`, `osCodename`, `osName`: `ID`, `VERSION_CODENAME` and `PRETTY_NAME` of `/etc/os-release`; optional, backups made before they existed lack them and the format stays 1), local node numbers, update channel, `parts` (including `dvswitch`, true when the archive has the DVSwitch files; added after format 1 shipped, so older backups lack it and the format stays 1), installed package list, and one entry per file: path, size, sha256, owner and group name, mode, type (file, dir, symlink with target), and for files in `/etc/asterisk` the stock/changed classification with the owning package and its version.

**No encryption in beta.3** (maintainer's decision). A backup holds every secret of the node (node passwords, IAX and AMI secrets, EchoLink password, ASLCP password hashes); the download dialog says so and the file should be stored like a password. Passphrase encryption ([age](https://age-encryption.org), scrypt mode) can be added later without changing the inner format.

## Restoring onto a newer ASL3

A backup made on an older ASL3 can be restored onto a newer image. A blind mirror would then put back old stock files and delete files the newer ASL3 added, so the restore distinguishes **your configuration** from **stock files**.

**Classification at backup time.** Files shipped by Debian packages are *conffiles*; dpkg records the checksum of the version each package shipped (`dpkg-query -W -f='${Package} ${Version}\n${Conffiles}\n'`, run by the helper with a fixed argv). For every file under `/etc/asterisk`:

- **stock**: a conffile whose content still matches what its package shipped;
- **changed**: a conffile you (or `asl3-menu`) edited;
- **local**: not a conffile of any package (`custom/`, files created by node setup, `allmon.ini.php`, ...);
- **missing stock**: a conffile of an installed package that you deleted (recorded in the manifest).

**Rules at restore time:**

| Case | Result |
|---|---|
| changed or local file | Restored from the backup: your configuration wins. |
| changed file whose stock version differs between the backup's package version and the new one | Restored from the backup, and the new image's stock version is kept beside it as `<file>.dpkg-dist`, as dpkg itself does on upgrades. The preview and result list these files ("ASL3 changed the default; review"). A later "Upstream changes" view (DESIGN 7) compares them key by key. |
| stock file | The new image's version is kept (it is the newer default). If both are the same version nothing changes. |
| file on the new image that is not in the backup | Kept (for example a file a newer ASL3 added). Nothing is deleted by default. |
| missing stock (you deleted it on the old node) | Listed in the preview with a checkbox "also delete here", off by default. |
| `*.dpkg-dist`, `*.dpkg-old`, `*.dpkg-new` from the backup | Not restored: they belong to the old package versions. They stay in the archive and in the history. |
| permissions | The backup's owner, group and mode are applied to every restored file. |

Same ASL3 version on both sides: stock files are identical anyway, so the result is the exact old tree (minus stale `dpkg-*` leftovers). The preview shows, per file, which rule applied.

## Creating a backup

Backup tab (admin only): tick the optional parts, Download. Audited as `backup.create` (no content).

1. The server asks the helper for the system part (new op `backup-create` with a typed request: which optional parts). The helper reads the root-only files (`/etc/asterisk` contains `root:www-data 0660` files, the history repository is root 0700, AstDB belongs to asterisk) and streams a tar over the socket after a JSON header. No temp files in directories the `aslcp` user can write.
2. The server adds its own parts (`VACUUM INTO` a temp file in its private data dir, `aslcp.conf`), writes `manifest.json` first, gzip, streaming straight to the HTTP response. Size limit 256 MiB (typical size: a few MB).

Later (not in beta.3): scheduled automatic backups kept on the node (last N) and optional copy to a USB stick or network share.

## Restoring

### Where

- **Fresh install (the main case):** the first-run page that asks to create the admin account gets a second choice, "Restore from a backup". It needs the one-time setup code (the same code that allows creating the first admin; the installer prints it as a one-time setup link, and `sudo aslcp admin setup-code` shows it again), so nobody on the network can push a configuration onto a new node.
- **Running node:** Backup tab > Restore (admin, password re-entry).

### Steps

1. **Upload and check.** Upload the file. The server checks the manifest format, every sha256, paths (no absolute paths, no `..`, no device files, symlinks only inside the tree), sizes, and versions:
   - Backup from a newer ASLCP than the one installed: refused ("update ASLCP first").
   - Different ASL3 or Asterisk version, or different architecture: warning only.
   - Different Debian release (for example a bookworm backup onto a trixie node): a separate, stronger warning (`os_release`). Your edited files replace the new release's defaults, which are kept beside them as `.dpkg-dist`; ASL3 changes some configs between releases (for example DAHDI timing is no longer needed), so if Asterisk or app_rpt does not start, compare your files with the `.dpkg-dist` copies. A backup without OS fields gets its release from the `.debNN` suffix of the ASL3 or Asterisk version (`deb12` bookworm, `deb13` trixie, `deb14` forky); if neither tells, there is no warning.
2. **Preview.** The page shows:
   - Node numbers, hostname, creation date and ASLCP version of the backup.
   - `/etc/asterisk`: per file what will happen (restored, kept as the new stock version, restored with a `.dpkg-dist` beside it, kept because it is new), following the rules in "Restoring onto a newer ASL3".
   - ASLCP: the users that will exist after the restore (you will log in with your old password), number of favorites and of schedules. A schedule window that was running when the backup was taken is not acted on after the restore (the `schedules.state` setting and the cancel notice `schedules.last_cancel` are dropped; the "pause for today" resume time `schedules.resume_at` is kept with the global switch it belongs to).
   - Packages present in the backup but missing now (for example `allmon3`), with the `apt install` command to run.
   - The "set up separately" checklist (network, hostname, timezone, ...).
3. **Apply** (helper op `backup-restore`, the server streams the checked archive back to the helper; nothing outside the fixed destinations can be written):
   1. Stop Asterisk.
   2. Restore the config history repository (if in the backup), then commit the current `/etc/asterisk` to it as "Before restore from backup <date>", so the restore itself can be undone from History.
   3. Apply the `/etc/asterisk` rules above: write each restored file with its owner, group (by name, falling back to `asterisk:asterisk` when a name does not exist) and mode, write `.dpkg-dist` copies where needed, delete only files the user ticked. Atomic per file; on error the snapshot is written back.
   4. Replace AstDB, Allmon3 config and sounds if present in the backup.
   5. Restore the DVSwitch files (if in the backup), step "Restore DVSwitch configuration". Every target is a fixed path of the allowlist, the same size limits and no symlinks apply either way.
      - DVSwitch installed (`/opt/Analog_Bridge` exists): the files are written. A file whose own directory is missing (a gateway that is not installed) is skipped and named in the step. Every target is checked first: a symlink or non-regular file at a target refuses the step and nothing is written; a missing target file is created. Each replaced file first gets a history copy (the same `/var/lib/aslcp-dvswitch-history`, last 10 kept, so the setup page can roll it back), then is replaced atomically with the owner, group (by name, else the replaced file's owner, else root) and mode of the manifest. Identical files are left alone. The services are not restarted; the step does not touch them.
      - DVSwitch not installed: nothing is written to `/opt` (it is not created). The files are saved as a pending set in `/var/lib/aslcp-dvswitch/restore-pending/` (root only: directory 0700, files 0600, plus `manifest.json` with each file's target, owner, group, mode and the backup date; a pending set from an earlier restore is replaced). The step reads "DVSwitch is not installed: its settings were saved and will be applied when you install DVSwitch". The preview warns that DVSwitch was set up on the old node and that its settings are applied automatically when you install DVSwitch from the DVSwitch page. The DVSwitch install (page, `aslcp dvswitch install` or `ASLCP_DVSWITCH=1`) applies the set at its end with the same safe write path (history copy, atomic replace, owner, group, mode, a missing gateway directory skipped), logs the file names (never the content) in the install log, deletes the pending directory and starts the DVSwitch services that are installed (Analog_Bridge, MMDVM_Bridge and the gateways; STFU stays on demand). The DVSwitch page shows "Settings from your backup (<date>) will be applied when DVSwitch is installed" with a Discard button (`DELETE /api/dvswitch/restore-pending`). The uninstall leaves a pending set alone.
   6. Set the update channel.
   7. Commit "Restored from backup <date>" to the history.
   8. Start Asterisk.
   8. ASLCP's own database and config: the server stages `aslcp.db.restore` and `aslcp.conf` and restarts itself; on start it swaps the database in (the previous one is kept as `aslcp.db.pre-restore`) and runs migrations forward.
4. **Result.** After the restart the page reconnects and shows the health check (Asterisk running, AMI answers, the app_rpt module is loaded, every node in the backup is up and registered) and the "set up separately" checklist. Log in with the old ASLCP account.

## Hardware notes

- A USB radio interface (`simpleusb`/`usbradio`) is referenced by device settings in the config; restoring onto the same hardware just works. On different hardware the device lines may need editing (Config page).
- The node keeps its node numbers and passwords, so the old SD card must not be running at the same time (registration would flap between the two).
- SA818 radio modules (SHARI and similar): frequency, CTCSS/DCS, squelch, volume and filters live in the module's own EEPROM, not on the SD card, so they survive the re-image unchanged. There is no command to read them from the module; `sa818-menu` saves what it last wrote in `/etc/sa818.conf`, which the backup includes. After a restore onto a different module, the result page offers the command `sudo sa818-menu --apply` to program it from the restored file (not run automatically: it keys the serial port and the module must be the right one). Settings written with older tools (for example the HamVOIP `SA818-prog` script) are not in that file. The USB audio chip's EEPROM (CM108/CM119) only holds USB identifiers; levels live in `simpleusb.conf` / `usbradio.conf`, already covered by `/etc/asterisk`.

## Security

- Admin only (or setup code on a fresh install); every backup, upload and restore audited.
- Not encrypted in beta.3: the download dialog warns that the file contains every secret of the node, and names the DVSwitch password when the DVSwitch part is selected.
- Older ASLCP versions reject an archive with a DVSwitch part ("not part of the backup layout"): restore it with this version or newer.
- The helper validates the archive again itself (paths, types, sizes, sha256 from the manifest) and writes only to the fixed destinations above; it never trusts the server's check.
- Upload size limit 256 MiB; decompression limit (total size from the manifest, checked while extracting) against archive bombs.

## Test plan

- Round trip in a temp root: create, restore onto a different tree, compare byte-for-byte including owner names and modes.
- Archive validation: traversal, absolute paths, symlink escape, device files, sha256 mismatch, truncated file, oversized entries, newer format version.
- AstDB copy while it is being written.
- Restore rules: a fake dpkg conffile list for an old and a new package version; stock/changed/local/missing-stock cases and the `.dpkg-dist` copy.
- DVSwitch part: round trip in temp roots (secrets kept, modes kept, history copies of replaced files), target directory missing (skipped, named in the step), symlink at a target (refused, nothing written, Asterisk started), part not requested; DVSwitch not installed (staged as a pending set, root-only modes, replaced by a later restore, applied by the install step, discarded), older manifest without the flag, only the six fixed paths valid in the archive.
- Demo mode: backup and restore of the in-memory files and the demo database.
- On hardware: back up a node, re-image a second SD card, install ASLCP, restore, compare `/etc/asterisk` (`diff -r` against the old card), check the nodes come up and favorites and users are there.

## Checked on a typical ASL3 node

- `savenode.conf` lives in `/etc/asterisk`, so it is covered by the `/etc/asterisk` part. `allmon.ini.php` too (Allmon3 itself need not be installed).
- Custom sounds: both `/var/lib/asterisk/sounds/custom` and `/usr/share/asterisk/sounds/custom` exist.
- AstDB: `/var/lib/asterisk/astdb.sqlite3`.
- DVSwitch packages (`dvswitch*`, `analog-bridge`, `mmdvm-bridge`, `md380-emu`, `ysfgateway`, `p25gateway`, `nxdngateway`, `stfu`) are never offered under "Packages to install" and are not listed under "Other software": DVSwitch is installed with ASLCP's own DVSwitch install, not with `apt install`.
- Packages recorded in the manifest and compared on restore: installed packages matching `asl3*`, `asterisk*`, `allmon*`, `supermon*`, `dvswitch*`, `*m17*` (on a typical ASL3 node: `asl3`, `asl3-asterisk`, `asl3-asterisk-config`, `asl3-asterisk-modules`, `asl3-menu`, `asl3-tts`, `asl3-update-nodelist`, `asterisk-moh-opsound-gsm`).
- SA818: `/etc/sa818.conf` exists (root:root 0644, plain `KEY=value` lines: `CURRENT_BAND`, `CURRENT_FREQ_RX`/`_TX`, `CURRENT_CTCSS_*`, `CURRENT_DCS_*`, `CURRENT_SQUELCH`, `CURRENT_VOLUME`, filters, `CURRENT_PORT`, ...). `sa818-menu` and `sa818` come from the `asl3` package and work with SA818 USB interfaces such as SHARI, which typically appear as a CH340 USB serial port (for example `/dev/serial/by-id/usb-XXXX_USB_Serial-if00-port0` -> `/dev/ttyUSB0`). The restore preview shows the radio settings from the file (band, frequencies, tone) and warns when `CURRENT_PORT` does not exist on the new system.

## Implementation notes

- **Self-restart.** After a successful apply the server answers, waits half a second and cancels its own context; it shuts down cleanly (HTTP server drained, database closed) and exits with status 75. `aslcp.service` has `SuccessExitStatus=75` and `RestartForceExitStatus=75` (with `Restart=on-failure`, `RestartSec=3`), so systemd starts it again. The swap of `aslcp.db.restore` for `aslcp.db` happens at the very start, before the database is opened; a staged file that fails its checks is set aside as `aslcp.db.restore-rejected` and the current database stays. No restart in demo mode.
- **`aslcp.conf`.** The server runs sandboxed and can only write to `/var/lib/aslcp`, so the helper (root) writes `/etc/aslcp/aslcp.conf` as part of the apply. Its mode is kept to at most 0664.
- **Helper operations.** Besides `backup-create` and `backup-restore` there is a small `backup-info` operation that tells the Backup page which optional parts exist and their sizes; the dry run of `backup-restore` also reports the node's versions and whether the SA818 serial port exists (the server has `PrivateDevices`).
- **Spooling.** The server writes the created archive to a private temporary directory under its data directory before sending it, so a failure maps to a proper HTTP status and `Content-Length` is known. Uploads are spooled the same way (256 MiB limit; credentials are checked after the form fields have been read, but a request that can never be authorised is refused before the body is read).
- **Result.** The last restore (steps, health checks, checklist) is kept in `restore-result.json` in the data directory. `health.pending` is `true` until the post-restart checks (ASLCP up, Asterisk connection, app_rpt module, one check per node) have passed or three minutes have gone by.
- **Step names** shown to the user: Stop Asterisk, Restore config history, Snapshot current /etc/asterisk to history, Restore /etc/asterisk, Replace AstDB, Replace Allmon3 config, Replace custom sounds, Restore DVSwitch configuration, Restore SA818 settings, Restore ASLCP settings, Set update channel, Commit restored configuration to history, Start Asterisk, Stage ASLCP database.
