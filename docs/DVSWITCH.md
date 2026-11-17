# DVSwitch (digital voice modes)

Status: M3 (install and full uninstall) is implemented; M1, M2 and M4 are design only. Goal: an admin can install DVSwitch Server from the panel, give the node a private "bridge" node wired to it through chan_usrp, and operators can switch the mode (DMR, YSF, P25, NXDN) and tune talkgroups and reflectors from the dashboard.

Placeholders used below: callsign `N0CALL`, DMR ID `1234567`, private bridge node `1999`, local node `2006`, documentation IPs from RFC 5737 (`192.0.2.x`).

## Scope

| In v1 | Out of v1 |
|---|---|
| Modes DMR, YSF, P25, NXDN | D-Star (needs a different gateway, ircDDBGateway or DStarGateway, config not examined; later), M17, STFU/ODMRT terminal mode, Quantar |
| Detect, status, mode switch, tune, unlink, favourites, reflector pickers | Last-heard from digital modes (no source in `dvswitch.sh`; would need the bridge logs) |
| Private bridge node + USRP ports, permanent or on-demand link | More than one bridge per node (one USRP port pair serves one Analog_Bridge) |
| Admin-initiated install from the official DVSwitch apt repo | Bundling, depending on, or recommending DVSwitch packages (closed binaries, licence unverified) |
| Identity and network credentials through typed forms | Editing arbitrary lines of the DVSwitch ini files |

Roles: viewer sees status; operator and admin switch mode, tune, unlink, manage favourites; admin only: install, uninstall, node setup, credentials, service restarts.

## Architecture

```
 radio / links (app_rpt, node 2006)
        |  link 2006 <-> 1999 (permanent or on demand)
 +------+--------------------------------+
 | Asterisk                              |
 |   node 1999  rxchannel =              |
 |   USRP/127.0.0.1:34001:32001          |
 |   (chan_usrp: sends to 34001,         |
 |    listens on 32001)                  |
 +------+--------------------------------+
        | UDP USRP audio, loopback
        |   Asterisk -> AB: 34001      AB -> Asterisk: 32001
 +------+--------------------------------+          +------------------------+
 | Analog_Bridge   [USRP] rxPort=34001   |  TLV     | MMDVM_Bridge           |
 |                        txPort=32001   +----------+  -> BrandMeister/TGIF  |
 | [AMBE_AUDIO] per-mode TLV ports       |  AMBE    +------------------------+
 |  DMR 31100/31103  YSF 35100/35103     |          | YSFGateway  P25Gateway |
 |  P25 34100/34103  NXDN 33100/33103    +----------+ NXDNGateway            |
 +------^--------------+-----------------+          +------------------------+
        |              | writes /tmp/ABInfo_<port>.json (state)
        | UDP TLV      v
        | command      aslcp-helper (root): dvswitch-info -> parsed JSON
 +------+--------------+
 | aslcp (unprivileged) |  sends 0x05,len,"key=value" to AB's TLV rx port
 +----------------------+
```

USRP direction (re-checked against `chan_usrp.c`): `rxchannel = USRP/HISIP:HISPORT[:MYPORT]` means Asterisk **sends** to `HISIP:HISPORT` and **binds** `MYPORT` (default `HISPORT`). So `USRP/127.0.0.1:34001:32001` gives:

| Side | Setting | Value | Role |
|---|---|---|---|
| Asterisk | HISPORT | 34001 | Asterisk sends audio here |
| Asterisk | MYPORT | 32001 | Asterisk listens here |
| Analog_Bridge `[USRP]` | `rxPort` | 34001 | AB listens (receives from Asterisk) |
| Analog_Bridge `[USRP]` | `txPort` | 32001 | AB sends (to Asterisk) |
| Analog_Bridge `[USRP]` | `address` | 127.0.0.1 | |

The stock Analog_Bridge.ini has 31001/31001 (and the DVSwitch dashboard also assumes 31001); the ASL convention above must be written by ASLCP, never assumed. The admin may pick any pair; ASLCP validates 1024-65535, `rx != tx`, and no clash with the DVSwitch TLV ports above or another `USRP/` rxchannel in rpt.conf.

## Detection and status

`dvswitch-status` (helper, read-only, cached 30 s in the server) reports what is there; the server never probes `/opt` itself.

| Field | Source |
|---|---|
| `installed`, `version`, `arch`, `osCodename` | `dpkg-query -W dvswitch-base analog-bridge` (installed when both are present, or `dvswitch-server` for a hand install); `version` is `dvswitch-base`'s; `dpkg --print-architecture`; `/etc/os-release` |
| `units[]` (`name`, `loaded`, `active`, `enabled`) | `systemctl show` for `analog_bridge`, `mmdvm_bridge`, `netcheck`, `ysfgateway`, `p25gateway`, `nxdngateway`, `md380-emu` |
| `files[]` (`id`, `present`) | the allowlisted ini files (see Credentials) |
| `modes.enabled[]` | `Enable=1` and the matching `... Network` `Enable=1` in MMDVM_Bridge.ini (what `dvswitch.sh getEnabledModes` reads) |
| `usrp` (`address`, `rxPort`, `txPort`) | Analog_Bridge.ini `[USRP]` |
| `modePorts{}` | DVSwitch.ini `[DMR] [YSF] [P25] [NXDN]` TXPort/RXPort (no other keys, in particular no `[STFU]`) |
| `hostFiles[]` | existence and mtime of the reflector/TG data files |
| `job`, `jobLog` | install job (M3) |

Never present in any reply: passwords, tokens. The reply is built from an explicit field list, not by forwarding file content.

`dvswitch-info` (helper, read-only, cheap) returns the parsed ABInfo: `{found, mtime, mode, tune, slot, muted, tlvRxPort, tlvTxPort, usrpRxPort, abVersion}` from `tlv.ambe_mode`, `last_tune`, `digital.ts`, `mute`, `tlv.rx_port`, `tlv.tx_port`, `usrp.rx_port`, `ab.version`. Fields missing in the JSON stay absent (the schema is inferred from `dvswitch.sh`; no real sample was seen).

"Configured" (the dashboard card shows) = a local node whose `rxchannel` starts with `USRP/` (astconf, effective values through templates) and `analog_bridge.service` loaded. `dvswitch.node` (setting) overrides the choice when several nodes match.

### The ABInfo and PrivateTmp problem

Analog_Bridge writes `/tmp/ABInfo_<port>.json` (neither bridge unit sets PrivateTmp). `aslcp.service` and `aslcp-helper.service` both have `PrivateTmp=yes`, so neither sees it.

| Option | Trade-off |
|---|---|
| **A (recommended): helper reads it through the bridge's own mount namespace**: `systemctl show -p MainPID analog_bridge`, then open `/proc/<pid>/root/tmp/ABInfo_*.json` as root | Keeps both sandboxes. Needs the helper to be allowed to read `/proc/<pid>/root` (root plus default caps should do; to verify). Also works if a future AB unit adds PrivateTmp. Slightly unusual, so isolated in one function with `O_NOFOLLOW`, regular-file check, 64 KiB cap, strict JSON into a typed struct. |
| B: `PrivateTmp=no` on `aslcp-helper.service`, read `/tmp/ABInfo_*.json` | One-line packaging change, but world-writable `/tmp` becomes visible to a root process (a local user can plant a fake file; mitigated by owner check). Helper currently needs no /tmp (temp dirs are chosen by `tempDirRoot`). Fallback if A fails. |
| C: redirect via `transferRootDir`/`ABINFO` to `/run/dvswitch` | Cleanest if AB honours it, but whether AB writes ABInfo there is unverified, and it edits AB config on existing installs. Not for v1. |
| D: no ABInfo, remember the last commanded mode/tune | Wrong after an AB restart (back to ini values) or a change from DVSwitch Mobile/`dvswitch.sh`. Kept only as the degraded display ("last set by ASLCP, unverified"). |

File choice mirrors `dvswitch.sh`: newest `ABInfo_*.json` by mtime, ignored if older than `analog_bridge`'s `ActiveEnterTimestamp` (the port in the file name changes with the mode, so stale files exist). After every own command the server sends `info` and re-reads after 300 ms; while SSE clients are connected it sends `info` at most every 30 s to catch external changes (whether AB refreshes the file by itself is to verify).

## Runtime control (native Go, no shell)

Replicates `dvswitch.sh` v1.6.2 `remoteControlCommand`: one UDP datagram to `127.0.0.1:<tlv.rx_port>` (the port AB listens on for TLV, from `dvswitch-info`; fallback Analog_Bridge.ini `[AMBE_AUDIO] rxPort`):

| Byte | Meaning |
|---|---|
| 0 | `0x05` (TLV tag: remote command) |
| 1 | length `n` of the command string, 1-255 |
| 2..n+1 | ASCII `key=value` command |

The server sends only the commands below, built from validated values, so nothing from a client is forwarded verbatim. `RestrictAddressFamilies` already allows UDP to loopback; no unit change.

| Action | Commands (from `dvswitch.sh`) |
|---|---|
| mode `X` | `rxport=30000`, then (using `DVSwitch.ini [X]` TXPort/RXPort from `modePorts`) `ambeMode=X`, `txport=<RXPort>`, `rxport=<TXPort>`, then `info` |
| tune `T` | `txTg=T`, then `info` |
| unlink | tune with the mode's unlink value: DMR `4000`, YSF `disconnect`, P25 `9999`, NXDN `9999` |

Mode names sent are `DMR`, `YSF`, `P25`, `NXDN`, as the script does (AB also knows `YSFN`/`YSFW`/`DMR_IPSC`; not used). Tune value validation per mode (all also `^[A-Za-z0-9.:_-]{1,64}$`, which excludes `=` and control characters):

| Mode | Tune value | List source (read directly by the server, data files only) |
|---|---|---|
| DMR | `^[0-9]{1,8}$` (TG) | optional `TGList_BM.txt` / `TGList_TGIF.txt` in `/var/lib/mmdvm`; favourites |
| YSF | `host:port` (hosts file fields 4:5) or `disconnect` | `/var/lib/mmdvm/YSFHosts.txt` (`;`-separated), optional `FCSRooms.txt` later |
| P25 | `^[0-9]{1,5}$` | `/var/lib/mmdvm/P25Hosts.txt` (`id host port`) |
| NXDN | `^[0-9]{1,5}$` | `/var/lib/mmdvm/NXDNHosts.txt` |

Behaviour rules:

- One control mutex; the mode sequence is serialised with the spacing the script uses (measure it, see "Verify"). Rate limit: one mode switch per 2 s, tune per 1 s, per server.
- The sequence is data in `internal/dvswitch/tlv.go` and a conformance test replays it against a fake UDP listener; a fixture captured from the real script (tcpdump, see Verify) is the reference.
- A mode must be in `modes.enabled`, else `409 mode_disabled`.
- `tune` changes AB's memory only, not Analog_Bridge.ini (as `dvswitch.sh`). Persisting is an explicit admin action ("Use current mode and talkgroup as startup", M4: writes `ambeMode`, `txTg`).
- An Analog_Bridge restart resets mode and TG to the ini values; the UI says so before any restart.
- The audit log records mode, tune value and user (no secrets involved).

## Install (M3)

Helper op `dvswitch-install`, no arguments, same pattern as `osupdate.go`: fixed script in a transient unit, log from the journal, busy lock shared with apt jobs.

| Item | Decision |
|---|---|
| Unit | `systemd-run --no-block --unit=aslcp-dvswitch-install --collect --property=Type=oneshot --setenv=DEBIAN_FRONTEND=noninteractive --setenv=APT_LISTCHANGES_FRONTEND=none /bin/sh -c <fixed script>`. The fixed script holds only `apt-get update --allow-releaseinfo-change`, a simulation `apt-get -s install --no-install-recommends <set>` (the job stops before any install when the `Inst` lines name `apache2`, `libapache2-mod-php`, `php*`, `mosquitto` or `nodejs`; log line `unexpected_deps`), and `apt-get install -y --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0 dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway wget` (the conffile options of `osUpgradeScript`, so ASLCP's edits to the inis survive; `wget` because `netcheck` needs it, see Package contents). The package list is a constant in the helper, never a parameter. `dvswitch-server` is not installed (see Dependencies). State and log via `jobUnit` like `osUpgradeUnit` |
| Refusals | `aptJobRunning` (shared with ASLCP/OS jobs, `ErrBusy`), already installed (every package of the set present, or `dvswitch-server` present; a partial set runs the same idempotent apt command), unsupported platform (see Platform gating), `clock_unset`, `key_mismatch`, keyring download failure. Every refusal before the unit writes nothing |
| Preparation (in the helper, before the unit) | In order: (1) clock: a year below 2026 refuses with `clock_unset`. ASLCP does not change clock or NTP settings in v1; the admin fixes the clock. (2) Fetch `https://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg` with the Go HTTP client (fixed URL, redirects refused, 30 s timeout, 1 MiB cap). (3) Key check, Go stdlib only, **no gpg dependency** (chosen over `gpg --show-keys` because it needs no extra package): dearmor if ASCII (base64 with CRC-24), refuse partial-length packets and non-v4 keys, compute the SHA-1 v4 fingerprint of each primary key packet. The set must be exactly `6DDFC33CD5FC678659642DF572147EC1E788D4C3`; otherwise `key_mismatch`, nothing written. If the upstream keyring ever carries a second primary key, the check fails closed and the decision is revisited. (4) Write the binary keyring to `/usr/share/keyrings/dvswitch-keyring.gpg` (0644, temp file in the same directory, rename), then the sources list (next row) |
| Source | `/etc/apt/sources.list.d/dvswitch.list` with `deb [signed-by=/usr/share/keyrings/dvswitch-keyring.gpg] http://dvswitch.org/DVSwitch_Repository bookworm hamradio`. The suite is always `bookworm`, on bookworm and trixie hosts alike, as upstream does; `VERSION_CODENAME` from `/etc/os-release` only gates the host (below), it never picks the suite. Before `apt-get update`, remove `/var/lib/apt/lists/dvswitch.org*` (as upstream does). The deb line stays over HTTP as upstream uses it: apt verifies the Release signatures against the pinned keyring, so the transport cannot inject packages. HTTP still exposes the package list and timing; hardening: use https for `dists` once verified (not checked yet) |
| Platform gating | Host codenames allowed for Install: `bookworm` and `trixie`; any other codename: `supported: false`, Install hidden with a pointer. Architectures come from the `bookworm` suite's Release file (the only suite written): amd64, arm64, armhf, i386. Install is offered on **amd64, arm64 and armhf**, the three ASLCP ships. i386 is listed in the Release file but ASLCP does not build it, so Install is hidden there. armhf and arm64 are checked per package (see Package facts); amd64 is only in the Release file, its per-package builds are open (see Unverified). Whether the bookworm binaries run correctly on a trixie host is to verify on a trixie node; until then trixie is gated like the rest (Install shown only after that check passes) |
| Dependencies | The community recipe installs `dvswitch-server`, whose Recommends pull in `dvswitch-dashboard` (Depends `php-cgi \| php-fpm`, `libapache2-mod-php`, `nodejs`: Apache and a new listener on port 80) and `analog-reflector` (Depends `mosquitto`). ASLCP does not use the dashboard, and Apache could clash with ports used by ASLCP and Allmon3. So the install is the explicit set with `--no-install-recommends` (see Install unit), which pulls in neither Apache nor mosquitto. Left out of v1: `dvswitch-server`, `dvswitch`, `dvswitch-dashboard`, `dvswitch-menu`, `dvswitch-monit`, `analog-reflector`, `stfu` (MMDVM_Bridge connects to BrandMeister directly), `ircddbgateway`, `quantar-bridge` and the parrot packages. The confirm dialog lists the eight packages (with `wget`), says that `md380-emu` pulls `qemu-user-static` on both architectures (a sizeable dependency; size to measure), and says that the job stops if the simulation shows a web server or broker. |
| After | Do not start or configure anything beyond what the packages do. The status shows units, and "Not configured" until node setup (M2) and identity (M4) are done |
| Not done | No change to Asterisk config, no `sed` of the DVSwitch dashboard ports (`/usr/share/dvswitch/include/config.php` keeps 31001; documented, not touched) |
| Removal | Implemented in M3, see Uninstall (M3) below. Only an install ASLCP made (the marker) can be removed from the panel |
| Packaging | ASLCP gains no Depends/Recommends/Suggests on any DVSwitch package; the install is a runtime action |

### Implemented in M3 (2026-11-17)

Code: `internal/helper/dvswitch.go` (status, install, uninstall, scripts, marker, parsers), `dvswitch_key.go` (key check), `dvswitch_step.go` (guards and plans), `dvswitch_demo.go`; `internal/api/handlers_dvswitch.go`; `web/src/components/Dvswitch.svelte`, `web/mock/dvswitch.ts`. Tests: `internal/helper/dvswitch_test.go`, `internal/api/dvswitch_test.go`. Deviations from the design above:

- **Guards and plans run in the helper binary, not in the shell.** The unit script only sequences apt; the install and uninstall units call `aslcp-helper dvswitch-step install-plan | install-check | uninstall-plan` (root, no arguments, no client input; they read and write fixed files in the state directory). The parsing and the guards are therefore Go code with unit tests.
- **Status and job are two ops.** `dvswitch-status` (full status, no apt run) and `dvswitch-job` (both jobs' state and log, polled while one runs). `dvswitch-install-status` does not exist. There is no `DVSwitch` request field: these ops take no arguments.
- **Marker location:** `/var/lib/aslcp-dvswitch/install.json` (root, 0600, directory 0700), outside `/var/lib/aslcp` so the unprivileged service cannot forge it; same pattern as the config history. `wget`-was-there and the recorded packages live in it; `install-sim.txt` and `purge.list` are scratch files of the same directory.
- **Helper sandbox:** no change to `packaging/systemd/aslcp-helper.service`. It has no `ProtectSystem`, so writing `/etc/apt/sources.list.d` and `/usr/share/keyrings` is allowed; the transient units run outside the helper's namespace (they see the host `/tmp`).
- **Install is refused over a hand install** (`already_installed`): a set that is installed without a marker is never adopted, so a later uninstall can never purge packages ASLCP did not install. A partial set with a marker (an interrupted install) can be resumed; the marker then merges.
- **Admin only:** `GET /api/dvswitch` and `/api/dvswitch/job` are admin here (the design's viewer snapshot of M1 will need to extend this reply or take another path).
- **Host files checked:** `YSFHosts.txt`, `P25Hosts.txt`, `NXDNHosts.txt` in `/var/lib/mmdvm`. This list is a guess (see the checklist); a missing file only warns in the log and shows in the status, it never fails the install.

### Uninstall (M3)

Helper op `dvswitch-uninstall`, no arguments, admin only, transient unit `aslcp-dvswitch-uninstall` (same `systemd-run` shape and busy lock as the install; the install, uninstall, ASLCP and OS update units exclude each other).

| Item | Decision |
|---|---|
| Offered when | The marker exists (`installedBy: "aslcp"`) and no job runs. Without a marker the op returns `409 not_aslcp_installed` and the tab says "installed outside ASLCP". A marker that cannot be parsed (unknown field, bad name, wrong version) counts as no marker |
| Marker | Written by `install-plan` before anything is installed: `{version, installedAt, wgetPreinstalled, packages[]}`. `packages` are the simulation's `Inst` lines with no current version (the set members that were missing plus their dependencies, `qemu-user-static` among them). `wgetPreinstalled` is whether wget was installed before the first run. A repeated install merges: it keeps the first install time and the first wget fact |
| Order | (1) `uninstall-plan`: simulations only, nothing changed; (2) `systemctl disable --now` for `analog_bridge mmdvm_bridge netcheck ysfgateway p25gateway nxdngateway md380-emu`; (3) `apt-get purge -y` of the purge list; (4) remove `dvswitch.list` and the keyring, remove `/var/lib/apt/lists/dvswitch.org*`, `apt-get update` (a failure only warns); (5) remove the fixed paths; (6) remove the purge list and, **last**, the marker. A failed step leaves the marker, so the uninstall can be run again |
| Purge list | The install set plus the recorded packages, minus wget when `wgetPreinstalled`, minus any name matching `^(asterisk\|allstarlink\|asl3\|asl-\|aslcp)`, minus anything not installed. At most 60 names. Package names are validated against `^[a-z0-9][a-z0-9+.-]{0,99}$` (never an option) |
| Guard `unexpected_removals` | `apt-get -s purge <core set>` must remove nothing outside the list; otherwise the job ends with the log line `unexpected_removals: the purge would also remove ...` before any unit is stopped or anything is removed. Asterisk and allstarlink packages always count as unexpected |
| Shared dependencies | Each recorded dependency is added only if `apt-get -s purge` of the list plus that dependency still removes nothing outside the list (repeated until nothing changes, at most 10 rounds). A dependency that something outside the recorded set still needs stays installed; the log says `keeping X: another package still needs it` |
| No autoremove | No `apt-get autoremove` anywhere, global or otherwise |
| Fixed paths | `/var/lib/mmdvm`, `/var/lib/dvswitch`, `/var/log/dvswitch`, `/var/log/mmdvm`, `/opt/Analog_Bridge`, `/opt/MMDVM_Bridge` (`rm -rf`); `/etc/cron.d/dvswitch`, `/etc/cron.daily/dvswitch-log-cleanup`, `/tmp/ABInfo_*.json`, the apt source and the keyring (`rm -f`). The list is a constant in the helper; the confirm dialog shows it from the status reply |
| **TODO (M2/M4)** | The script has a marked hook (`TODO(M2/M4)` before the marker is removed). When the private node and USRP link (M2: `rpt.conf` node stanza, `modules.conf` chan_usrp, reconciler state) and the credentials (M4: ini edits and their history copies) exist, the uninstall must revert them here, in the same job, before the marker goes. Until then the uninstall leaves Asterisk configuration alone |

### Verify on a real node (M3 checklist)

None of this was run against a real host (the build container must never run the install). Do it on a spare ASL3 node, bookworm first, with a console session open. Replace `N0CALL`-like values; paste no secrets.

Install:

1. Before: `dpkg -l | grep -E 'dvswitch|analog-bridge|mmdvm-bridge|md380|gateway|qemu-user-static|^ii  wget'` (record whether wget is there); `ls /etc/apt/sources.list.d/`; `date` (year is right).
2. Panel, DVSwitch tab: Install, confirm. The log should show `recorded N newly installed packages: ...` (check it lists `qemu-user-static` and no `apache2`, `php*`, `mosquitto`, `nodejs`), then `host files ok` or the warnings, then `dvswitch install finished`.
3. `cat /var/lib/aslcp-dvswitch/install.json` (root only, mode 600): packages match the log; `wgetPreinstalled` matches step 1.
4. Key: `gpg --show-keys --with-colons /usr/share/keyrings/dvswitch-keyring.gpg | grep '^fpr'` shows exactly `6DDFC33CD5FC678659642DF572147EC1E788D4C3` and one primary key.
5. `systemctl is-enabled analog_bridge mmdvm_bridge; systemctl is-active analog_bridge mmdvm_bridge netcheck`; `ls -l /var/lib/mmdvm/` (which host files really exist and are non-empty: correct the `dvHostFiles` list in `internal/helper/dvswitch.go` if the names differ).
6. Status shows installed by ASLCP, versions, arch, units. Asterisk and `rpt.conf` are unchanged (`dpkg -V asl3 asterisk`, `git diff` of your config backup).
7. Negative: with a wrong key (point `DVKeyURL` at a test file in a scratch build) the job is refused with `key_mismatch` and `/etc/apt/sources.list.d/dvswitch.list` is not created. With the clock set to 1970, `clock_unset`.

Uninstall:

1. Panel: Uninstall, read the dialog (units, packages, paths), confirm.
2. Log: `purge plan: ...` (no wget when step 1 said it was there; no package something else needs), then removal lines and `dvswitch uninstall finished`. If you see `unexpected_removals`, nothing was changed: report the names.
3. After: `dpkg -l | grep -E 'dvswitch|analog-bridge|mmdvm-bridge|md380|gateway|qemu-user-static'` is empty (except packages the log said it kept); `systemctl list-unit-files | grep -E 'analog_bridge|mmdvm_bridge|netcheck|md380|gateway'` is empty; `ls /etc/apt/sources.list.d/dvswitch.list /usr/share/keyrings/dvswitch-keyring.gpg /var/lib/mmdvm /var/lib/dvswitch /var/log/dvswitch /var/log/mmdvm /opt/Analog_Bridge /opt/MMDVM_Bridge /etc/cron.d/dvswitch /etc/cron.daily/dvswitch-log-cleanup /tmp/ABInfo_*.json /var/lib/aslcp-dvswitch/install.json` reports every path missing.
4. `apt-get -s install allstarlink asl3` style check: `dpkg -l | grep -E 'asterisk|asl3|allstarlink'` is unchanged from before the install; `systemctl is-active asterisk`.
5. Hand-installed node (no marker, e.g. `apt install dvswitch-server` by hand): the tab says installed outside ASLCP, Install and Uninstall are disabled, and `POST /api/dvswitch/uninstall` returns `409 not_aslcp_installed`.
6. Shared dependency: `apt-get install` a package that depends on `qemu-user-static` before the uninstall; the uninstall must keep `qemu-user-static` and say so.
7. Interrupted install: kill the install unit midway (`systemctl stop aslcp-dvswitch-install`), then run Install again (resumes, marker merges), then Uninstall.

### Upstream facts (verified 2026-11-17)

Checked by the maintainer on a real node. The DVSwitch installer script has no licence header, so it is **not copied**; ASLCP implements the equivalent steps itself from these facts. The upstream script is never run as root.

- Installer scripts exist at `http://dvswitch.org/bookworm` and `http://dvswitch.org/trixie`. The bookworm script:
  - requires root; if the clock year is below 2026 it warns and runs `timedatectl set-ntp true`;
  - installs `gnupg curl host net-tools file` if missing (ASLCP needs none of them: the helper does the HTTP and key work itself, and apt verifies with the signed-by keyring);
  - downloads the keyring from `http://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg` to `/usr/share/keyrings/dvswitch-keyring.gpg`, dearmors it if ASCII, chmod 644;
  - checks size >= 1000 bytes, file type, and the key ID `72147EC1E788D4C3` (16 hex digits, the long form: the last 16 of the fingerprint);
  - writes `/etc/apt/sources.list.d/dvswitch.list` with `deb [signed-by=/usr/share/keyrings/dvswitch-keyring.gpg] http://dvswitch.org/DVSwitch_Repository bookworm hamradio`;
  - removes `/var/lib/apt/lists/dvswitch.org*` and runs `apt update --allow-releaseinfo-change`.
  - It computes the distribution with `lsb_release` but hard-codes `bookworm` in the deb line.
- The trixie script (dated 2022, older) differs from the bookworm one only in keyring handling: no dearmor step, the key is written directly. Its deb line also hard-codes `bookworm`: `deb [signed-by=/usr/share/keyrings/dvswitch-keyring.gpg] http://dvswitch.org/DVSwitch_Repository bookworm hamradio`. So upstream installs the bookworm suite on trixie hosts too.
- Key: rsa4096, created 2017-07-28, full fingerprint `6DDF C33C D5FC 6786 5964 2DF5 7214 7EC1 E788 D4C3`, uid "ASL Repository (For signing packages)". The repo is signed with the old AllStarLink repository key.
- Repo: `dists/bookworm/Release` has Origin DVSwitch, Codename bookworm, Architectures amd64 armhf arm64 i386, Components hamradio. `dists/trixie/Release` exists (HTTP 200); its architectures are not yet checked.
- The keyring is also served over HTTPS (`https://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg`, HTTP 200). HTTPS for the `dists` paths is not checked.

#### Package contents (inspected 2026-11-17)

From the arm64 `.deb` files, downloaded over HTTPS from `https://dvswitch.org/DVSwitch_Repository` and inspected with `dpkg-deb`; nothing installed.

- **dvswitch-base** (all, 6.7 KB): `/etc/cron.d/dvswitch` (`@reboot root /usr/local/sbin/DVSwitch-startup`: logs host info and runs `DVSM_Update.sh` if `subscriber_ids.csv` is a day old); `/etc/cron.daily/dvswitch-log-cleanup`; `netcheck.service` and `/usr/local/sbin/netcheck`; `host-info`; creates `/var/lib/dvswitch`, `/var/lib/mmdvm`, `/var/log/dvswitch`, `/var/log/mmdvm`. postinst: `systemctl enable netcheck.service`. No conffiles.
- **netcheck.service** (Type=notify): a loop that fetches `https://google.com/robots.txt` with `wget` (up to 9 rounds, 5 s apart) and sends READY only on success. `analog_bridge.service` and `mmdvm_bridge.service` both have `Requires=netcheck.service` and `After=netcheck.service`, so **without internet access to google.com at boot, the bridges do not start.** `dvswitch-base` declares no Depends, so `wget` is not pulled in; the install list adds it.
- **analog-bridge** (arm64 1.6.4-20240701-60): `/opt/Analog_Bridge/{Analog_Bridge, Analog_Bridge.ini (conffile), parrot.sh, dvsm.macro}`, `analog_bridge.service`, `/usr/local/sbin/{AMBEtest4_p3.py, gpio-reset}`, logrotate. Unit: Type=simple, Restart=on-failure, `RestartPreventExitStatus=252 253 254` (254 matching IDs, 253 ini parse error, 252 DV3000 not found with fallback disabled), StandardOutput=null, WorkingDirectory=/opt/Analog_Bridge, `Environment=AnalogBridgeLogDir=/var/log/dvswitch`, `ExecStartPost=-/usr/local/sbin/update-config.sh`, `ExecReload=kill -HUP`, KillMode=process. **No PrivateTmp**, so `ABInfo_*.json` is in the host `/tmp`. postinst: ldconfig, enable `analog_bridge.service`, touch the log.
- Stock `Analog_Bridge.ini` `[USRP]`: `address 127.0.0.1`, `txPort 31001`, `rxPort 31001`, `usrpAudio AUDIO_UNITY`, `usrpGain 1.10`, `usrpAGC -20,10,100`. ASLCP must write both ports.
- **mmdvm-bridge** (arm64 1.6.8-20241231-96): `/opt/MMDVM_Bridge/{MMDVM_Bridge, MMDVM_Bridge.ini, DVSwitch.ini (both conffiles), RemoteCommand, dvswitch.sh}` (`dvswitch.sh` version 1.6.2, 1363 lines, the version the research analysed); `mmdvm_bridge.service` (`Restart=always`, then `Restart=on-abort` later in the same file, which wins); `/usr/local/sbin/DVSM_Update.sh` with a `/etc/cron.daily/DVSM_Update` symlink; logrotate. postinst runs `DVSM_Update.sh` (= `dvswitch.sh update`: downloads host and ID files, needs internet; creates empty `private_NXDNHosts.txt` and `private_P25Hosts.txt` in `/var/lib/mmdvm`), then enables `mmdvm_bridge.service`. The postinst runs under `set -e`, but `DVSM_Update.sh` has `set -o errexit` commented out and always ends with `exit 0`, so a failed download never fails the install.

So `dvswitch.sh`, the inis, the units and the host-file updates all come with the minimal set. The "minimal set works" open item narrows to runtime behaviour; its `dpkg -L` part is verified.

Design impacts:

- **Conffiles.** The inis are dpkg conffiles. After ASLCP edits them, a later DVSwitch upgrade that changes the packaged default makes dpkg prompt, which hangs a non-interactive job. The install and any upgrade job run apt with `DEBIAN_FRONTEND=noninteractive` and `-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold`, keeping ASLCP's edits. ASLCP's OS-update path already does this: `internal/helper/osupdate.go` passes both options in `osUpgradeScript` and sets `DEBIAN_FRONTEND=noninteractive` on the unit (`osUpgradeArgv`). The install uses the same options.
- **Internet at install and at boot.** The mmdvm-bridge postinst needs internet (`DVSM_Update`). A failed host-file download does not fail the install, and apt cannot show it: `DVSM_Update.sh` exits 0 even when the download fails. After install the job therefore checks that the host files in `/var/lib/mmdvm` exist and are non-empty, and reports a missing or empty file as a warning, not a failure. At boot, netcheck gates both bridges: the status shows the netcheck unit state and explains a failed netcheck (no internet at boot, so the bridges wait for it). A drop-in override of the upstream units is a possible later option; v1 does not change upstream units.
- **Host files.** `/var/lib/mmdvm` is refreshed daily by cron (`DVSM_Update`). The reflector pickers read the files from there. The status `hostFiles[]` shows each file's mtime, so a stale list is visible.

### Package facts (verified 2026-11-17)

From the apt index, bookworm suite, component `hamradio`. Versions as listed; architecture where stated.

| Package | Version | Depends | Recommends |
|---|---|---|---|
| `dvswitch-server` | 1.6.0-20260716-12 (all, metapackage) | | dvswitch, dvswitch-menu, dvswitch-monit, dvswitch-dashboard, analog-reflector, stfu |
| `dvswitch` | 1.6.0-20240701-20 (all, metapackage) | | dvswitch-base, analog-bridge, md380-emu, mmdvm-bridge, nxdngateway, nxdnparrot, p25gateway, p25parrot, ysfgateway, ysfparrot, ircddbgateway, quantar-bridge |
| `dvswitch-dashboard` | 1.6.1-20241231-98 (all) | php-cgi \| php-fpm, libapache2-mod-php, nodejs, node-ws | apache2 \| lighttpd \| httpd |
| `analog-reflector` | 1.1.30 | mosquitto, lsof | |
| `stfu` | 0.3.4 ("Connect to BM") | dvswitch-base | |
| `dvswitch-base` | 1.6.0-20250507-99 (Architecture: all; listed in the armhf, arm64 and amd64 indexes) | none | |
| `analog-bridge` | armhf 1.6.4-20210517-62; arm64 1.6.4-20240701-60; amd64 1.6.4-20240701-60 | armhf: dvswitch-base, libsndfile1, libasound2, python-serial \| python3-serial. arm64: dvswitch-base, libsndfile1-dev, libasound2-dev, python3-serial | md380-emu |
| `mmdvm-bridge` | armhf 1.6.8-20230108-97; arm64 1.6.8-20241231-96; amd64 1.6.8-20241231-94 | dvswitch-base, curl | |
| `md380-emu` | armhf 20201009-13; arm64 20240812-21; amd64 20240812-20 | qemu-user-static (all three arches) | |
| `ysfgateway` | 20201028-18 (armhf); 20240701-20 (amd64) | dvswitch-base | |
| `p25gateway` | 20250909-32 (armhf); 20240701-32 (amd64) | dvswitch-base | |
| `nxdngateway` | 20201028-30 (armhf); 20240701-30 (amd64) | dvswitch-base | |

- Every binary package in the install set exists for armhf. The armhf builds are older than arm64 for `analog-bridge`, `mmdvm-bridge` and `md380-emu`.
- amd64 builds exist in the bookworm suite for every package of the set; `dvswitch-base` is Architecture: all and is in the amd64 index (see the table).
- The install set's own Depends pull in neither Apache nor mosquitto. Those come only through the `dvswitch-server` Recommends (`dvswitch-dashboard`, `analog-reflector`).
- Believed, unverified: `md380-emu` is the software AMBE vocoder needed for DMR, YSF and NXDN without a hardware AMBE dongle; P25 IMBE is handled in software by Analog_Bridge.

## Private node and USRP (M2)

Admin chooses `node` (1 to 1999, private, must not exist in rpt.conf; the stock ASL3 config already ships a `[1999]` stanza, so the form suggests the first free number in 1900-1997 and warns that 1998/1999 are commonly used in examples and loop protection can bite when linking to public nodes), `rxPort`/`txPort`, and the link mode.

One apply (preview first, like Config): the rpt.conf and modules.conf parts go through the existing `configsvc` pipeline (history, conflict check, health, rollback); ini ports go through the helper.

| Step | What | How |
|---|---|---|
| 1 | rpt.conf: `[1999](node-main)` with `rxchannel = USRP/127.0.0.1:34001:32001`, `duplex = 0`, `callerid = "DVSwitch 1999" <0000000000>`; `[nodes]` line `1999 = radio@127.0.0.1/1999,NONE` | `astconf.AddSection(name, template, parents)` using the template the existing local node inherits (stock: `node-main`). Without a template (flat layout) a self-contained minimal stanza is written (`rxchannel`, `duplex`, `context`, `callerid`, the stanza names of `functions`, `telemetry`, `morse`, `wait_times` copied from the linked local node). Inherited keys that make no sense on a bridge (`idrecording`, `idtalkover`, tail messages) are unset in the stanza, list to verify |
| 2 | modules.conf: `noload = chan_usrp.so` becomes `load = chan_usrp.so` | `astconf` entry edit; if the line is absent, add `load = chan_usrp.so`. `ReloadMap` has no `modules.conf` entry, so `PlanReload` already returns `restart: true`: the plan says "Restart Asterisk (links drop)" and requires explicit confirmation |
| 3 | Analog_Bridge.ini `[USRP]` `address`, `rxPort`, `txPort` | helper `dvswitch-config-apply` (typed, see below) |
| 4 | restart | Asterisk (existing `asterisk-restart`), then `dvswitch-restart` scope `bridge` |
| 5 | health | existing configsvc health check plus `dvswitch-status` units active; failure offers rollback of rpt.conf/modules.conf (existing) and of the ini (`dvswitch-config-rollback`) |

The order protects against a half state: Asterisk config first (it is the only part with history and rollback), then the ini, then restarts.

Duplex: no official recommendation. Default `0` (half duplex, no telemetry or hang time, "preferred when interfacing with an external multiport controller" per the stock comment); unverified, see below.

### Link mode

| Mode | Behaviour |
|---|---|
| `ondemand` (default) | The bridge node is a normal local node: connect it from the dashboard like any link (ConnectBar). The DVSwitch card has "Link bridge to 2006" / "Unlink" for convenience, using the existing connect code (`rpt.Client`) |
| `permanent` | ASLCP issues a permanent connect (`ilink 13`, transceive) from the chosen local node (`dvswitch.link_node`, `2006`) to `1999`; app_rpt restores permanent links itself on restart. A reconciler (injectable `Linker` interface like `schedules`) re-issues it when the link is missing at ASLCP start, after Asterisk restarts, and after a setup change. It does **not** fight a manual disconnect: a disconnect of that link made through the panel API pauses the reconciler (setting `dvswitch.link_paused`, cleared by saving setup or by an Asterisk restart), exactly like `Schedules.ManualAction` treats manual actions. Its own commands never count as manual actions. See Decision 1 |

Terminology in the UI: "Unlink" on the DVSwitch card sends the mode's unlink talkgroup/reflector; it never touches the app_rpt link. The bridge node also shows up in the dashboard's status table; it is tagged "DVSwitch bridge" there.

## Credentials and ini files (M4; M2 uses the ports subset)

Only a typed helper op writes these files; the server never does.

### Allowlist (file id, section, key)

| File id | Path | Section | Keys | Validation |
|---|---|---|---|---|
| `analog_bridge` | `/opt/Analog_Bridge/Analog_Bridge.ini` | `USRP` | `address`, `rxPort`, `txPort` | `127.0.0.1` only; ports 1024-65535 |
| | | `AMBE_AUDIO` | `gatewayDmrId` (DMR ID, 7 digits), `repeaterID` (DMR ID + 2-digit ESSID, 9 digits), `ambeMode` (startup mode), `txTg`, `txTs`, `colorCode` | digits; `ambeMode` in the v1 modes; ts 1-2; cc 0-15 |
| `mmdvm_bridge` | `/opt/MMDVM_Bridge/MMDVM_Bridge.ini` | `General` | `Callsign`, `Id` | callsign `^[A-Z0-9/]{3,10}$`; `Id` 7 digits (ESSID rules as above, verify) |
| | | `DMR Network` | `Address`, `Port`, **`Password`**, `Options` | host `^[A-Za-z0-9.-]{1,253}$` or IPv4; port; password: printable ASCII 0x21-0x7E, 1-64, none of `" # ;`; options: no control chars, <= 200 |
| | | `DMR`, `System Fusion`, `P25`, `NXDN` and their `... Network` sections | `Enable` | `0`/`1`; a mode is switched on or off in both sections together |
| `ysfgateway` | `/opt/YSFGateway/YSFGateway.ini` | `General` | `Callsign`, `Id` | as above |
| | | `Network` | `Startup` | same charset as the YSF tune value |
| `p25gateway` | `/opt/P25Gateway/P25Gateway.ini` | `Network` | `Startup` | digits |
| `nxdngateway` | `/opt/NXDNGateway/NXDNGateway.ini` | `Network` | `Startup` | digits |

DMR ID and callsign live in three places (AB `gatewayDmrId`/`repeaterID`, MMDVM_Bridge `Callsign`/`Id`, YSFGateway `Callsign`/`Id`); the Identity form writes all of them in one op so they cannot drift. `DVSwitch.ini` is read only (ports); its `[STFU] BMPassword` is not editable in v1 (DMR goes through MMDVM_Bridge). The callsign/ID keys of P25Gateway and NXDNGateway are not verified; they are added to the allowlist only after the node check below.

`file` in the request is an enum id, never a path; the helper maps it to the fixed path and refuses symlinks and non-regular files (`Lstat`).

### Edit semantics

- Line-oriented, lossless editor (`internal/dvswitch/ini`, shared by helper and tests): finds `[section]`, then `key` (MMDVM files `Key=Value`, AB file `key = value`; spacing style preserved), replaces only the value, keeps comments and everything else byte for byte. A missing key in an existing section is appended to the section; a missing section is an error (no file restructuring).
- Optimistic concurrency: request carries `base` (sha256 of the file as read by `dvswitch-info`-style status, which exposes hashes only); mismatch is `conflict`.
- Atomic write (temp file in the same directory, owner/mode copied, rename), like `ops.go`.
- Previous version kept in `/var/lib/aslcp-dvswitch-history/<file id>/<UTC stamp>` (root 0700, last 10 per file; same reason as the config history: not writable by the `aslcp` user). `dvswitch-config-rollback` restores the newest copy. No git for this.
- `analog_bridge.service` has `ExecStartPost=-/usr/local/sbin/update-config.sh`. The script is not in any of the five packages inspected (see Package contents), and the leading `-` ignores its absence. Still to verify on a node that has it: if it rewrites ini values at each start it can undo our edits.

### Secrets handling

| Surface | Rule |
|---|---|
| API | Passwords are write-only: GET returns `passwordSet: true/false`, never a value; no reveal feature (unlike the Asterisk config editor) because the DVSwitch files are never shown raw. DMR ID and callsign are public identifiers (RadioID), returned to admin only; viewers/operators see only whether the node is configured |
| Helper replies | `dvswitch-status`/`-info` use explicit field lists; there is no op that returns file content |
| Audit | Records file id, section, key; values only for non-secret keys; never for `Password` |
| Logs | Request bodies are not logged; the helper logs op names only |
| Diagnostics | `internal/diag/redact.go` already redacts any key containing `password`/`secret`/`token` (`BMPassword`, `Password=` included) and callsigns. The ASLCP report does not read DVSwitch files in v1; the status section only lists unit states |
| `configsvc/mask.go` | Gap: `reSecretKey` matches only a key that is exactly `secret\|password\|passwd\|pass\|md5secret\|dbsecret`, so `BMPassword` and `DMRPassword` would not be masked. DVSwitch inis never pass through configsvc, but add a `\w*password\w*` rule with a test now as defence in depth (a raw-editor user can create such keys in custom files too) |
| Backup | See below |

### Backups

Backups currently contain every secret in plain text, unencrypted, with a warning (docs/BACKUP.md). DVSwitch is treated the same way: new optional part "DVSwitch" (default on when `/opt/Analog_Bridge` exists), containing the allowlisted ini files plus `dvsm.macro`, stored under `dvswitch/opt/...` with owner/group/mode in the manifest, and the download dialog's secrets warning names the DVSwitch password. No redaction (a redacted backup could not restore the node, the same reason the Asterisk secrets are kept). Restore writes them only when the target directory exists (DVSwitch installed); otherwise the restore page lists "install DVSwitch, then restore this part again" and the manifest already records `dvswitch*` package names. Lands in M2, before ASLCP writes any of these files.

## Service restarts

`dvswitch-restart` takes an enum scope, no names from clients; one fixed `systemctl restart` argv, only units that exist and are enabled or active.

| Change | Restart |
|---|---|
| AB `[USRP]` ports, `gatewayDmrId`, `repeaterID`, startup `ambeMode`/`txTg` | `analog_bridge` |
| MMDVM_Bridge Callsign/Id/`DMR Network`/Enable flags | `mmdvm_bridge` (and `analog_bridge` when the mode enable set changed) |
| Gateway ini | that gateway only |
| rpt.conf node stanza | `module reload app_rpt.so` (existing map) |
| modules.conf (`chan_usrp` load) | Asterisk restart, then `analog_bridge` |

Scopes: `bridge` (analog_bridge, mmdvm_bridge), `gateways` (ysf/p25/nxdn), `all`. A failure state (`analog_bridge` exits 252/253/254 and is not restarted) is shown in the status with the unit state; `dvswitch-config-apply` checks the units 10 s after the restart and returns `unitFailed` so the UI can offer the rollback. Both bridge units `Require` `netcheck.service` (see Package contents). Whether they require each other is not checked; until then the order is analog_bridge, mmdvm_bridge, gateways.

## Helper ops

New file `internal/helper/dvswitch.go`; constants in `proto.go`; dispatch in `Ops.HandleRequest`. A new optional typed field `DVSwitch *DVSwitchRequest` in `Request` (rejected on every other op, like `Config`). All ops take no free-form paths or command lines.

| Op | Arguments (`dvswitch` object) | Reply | Notes |
|---|---|---|---|
| `dvswitch-status` | none | status fields (see Detection) | read-only, no secrets |
| `dvswitch-info` | none | parsed ABInfo | option A above |
| `dvswitch-install` | none | status (job running) | M3, transient unit; refuses `busy`, `already_installed`, `unsupported_platform`, `clock_unset`, `key_mismatch` |
| `dvswitch-uninstall` | none | status (job running) | M3, transient unit `aslcp-dvswitch-uninstall`; refuses `busy`, `not_aslcp_installed` |
| `dvswitch-job` | none | `{install:{job,log}, uninstall:{job,log}}` | M3, polled while a job runs (replaces the planned `dvswitch-install-status`) |
| `dvswitch-config-apply` | `changes: [{file, section, key, value}]` (max 32), `base: {file: sha256}`, `author` | `{files, restartHint[]}` | allowlist + validators; atomic; keeps history copy; `conflict`, `invalid: <file/section/key>` errors never echo values |
| `dvswitch-config-rollback` | `file` (enum) | `{file}` | restores the newest history copy |
| `dvswitch-restart` | `scope: bridge\|gateways\|all` | `{units: [{name, active}]}` | fixed argv |

Request size stays at the default small limit. Rollback granularity per file; apply across files is best effort (same stance as DESIGN 6).

## API

Base `/api/dvswitch`. Errors `{error, code}` as everywhere; all non-GET need `X-ASLCP: 1`. `503 dvswitch_unavailable` when the service or helper is not wired (`Deps.DVSwitch == nil`).

| Method | Path | Role | Body | Response / errors |
|---|---|---|---|---|
| GET | `/api/dvswitch` | viewer (M1); **admin in M3** | | `200` Snapshot (below). M3 ships the install/uninstall status here (admin only, see docs/API.md); M1 extends it |
| POST | `/api/dvswitch/mode` | operator | `{"mode": "dmr\|ysf\|p25\|nxdn"}` | `200` Snapshot plus `confirmed: bool` (ABInfo shows the new mode); `400 bad_request`; `409 mode_disabled`; `409 not_configured`; `502 bridge_unreachable` (no ABInfo/TLV port) |
| POST | `/api/dvswitch/tune` | operator | `{"mode": "ysf", "target": "192.0.2.10:42000"}` | `200` Snapshot + `confirmed`; `400 bad_target` (validator); `409 mode_mismatch` (bridge is in another mode now) |
| POST | `/api/dvswitch/unlink` | operator | `{}` | `200` Snapshot; sends the unlink value of the current mode |
| GET | `/api/dvswitch/lists?mode=ysf` | viewer | | `200` `{"mode", "items": [{"value", "name", "detail"}], "updated"}` (max 5000, `ETag`); `404 no_list` when the file is missing (the UI falls back to free input) |
| GET | `/api/dvswitch/favorites` | viewer | | `200` `[{"id","mode","value","name","sort"}]` |
| POST | `/api/dvswitch/favorites` | operator | `{"mode","value","name"}` | `201` favourite; `400`; `409 duplicate` (mode+value) |
| PUT | `/api/dvswitch/favorites/{id}` | operator | `{"name","sort"}` | `200`; `404 not_found` |
| DELETE | `/api/dvswitch/favorites/{id}` | operator | | `204`; `404` |
| GET | `/api/dvswitch/setup` | admin | | `200` Setup view (below) |
| POST | `/api/dvswitch/install` | admin | none | M3, implemented: `202` status (job running); `409 job_running`, `409 apt_busy`, `409 already_installed`, `409 unsupported_platform`, `409 clock_unset`, `502 key_mismatch`, `503 dvswitch_unavailable` |
| POST | `/api/dvswitch/uninstall` | admin | none | M3, implemented: `202` status; `409 not_aslcp_installed`, `409 job_running`, `409 apt_busy`, `503` |
| GET | `/api/dvswitch/job` | admin | | M3, implemented: `{install:{job,log}, uninstall:{job,log}}` |
| PUT | `/api/dvswitch/private-node` | admin | `{"node": "1999", "rxPort": 34001, "txPort": 32001, "duplex": 0, "link": {"mode": "permanent\|ondemand", "node": "2006"}, "dryRun": bool, "restart": bool}` | `dryRun`: `200` plan `{files:[{path, diff}], restartAsterisk, restarts[]}`; apply (needs `restart: true` when the plan restarts Asterisk, else `409 restart_required`): `200` `{applied, health}`; `400 invalid_node` (>= 2000 or not a number), `409 node_in_use`, `409 port_conflict`, `409 conflict` (file changed on disk), `422 syntax_error` |
| PUT | `/api/dvswitch/identity` | admin | `{"callsign": "N0CALL", "dmrId": "1234567", "essid": "01"}` | `200` Setup view; `400` (field in message) |
| PUT | `/api/dvswitch/credentials` | admin | `{"dmrAddress"?, "dmrPort"?, "dmrPassword"?: "..."}` | `200` Setup view (password only as `passwordSet`); empty password `400`; write-only |
| PUT | `/api/dvswitch/modes` | admin | `{"dmr": true, "ysf": true, "p25": false, "nxdn": false}` | `200` Setup view; restarts proposed, not forced |
| PUT | `/api/dvswitch/startup` | admin | `{"useCurrent": true}` | `200`; writes `ambeMode`/`txTg` |
| POST | `/api/dvswitch/restart` | admin | `{"scope": "bridge\|gateways\|all"}` | `200` `{units}` |
| POST | `/api/dvswitch/rollback` | admin | `{"file": "analog_bridge"}` | `200`; `404 no_history` |

Snapshot (also the SSE payload):

```json
{
  "supported": true,
  "installed": true,
  "configured": true,
  "version": "1.0.0",
  "node": "1999",
  "link": { "mode": "permanent", "localNode": "2006", "linked": true, "paused": false },
  "bridge": { "mode": "ysf", "target": "192.0.2.10:42000", "targetName": "Example room", "slot": 2, "muted": false,
              "source": "abinfo", "updated": "2026-11-17T18:42:00+01:00" },
  "modes": [ { "id": "dmr", "enabled": true, "unlink": "4000" }, { "id": "ysf", "enabled": true, "unlink": "disconnect" } ],
  "services": [ { "unit": "analog_bridge", "active": true, "enabled": true } ],
  "install": { "job": "idle", "jobLog": "" }
}
```

`bridge.source` is `abinfo`, `commanded` (option D, unverified) or `none`. `supported` is false when arch/codename are not in the verified matrix (the setup page hides Install then). Setup view (admin): `{identity: {callsign, dmrId, essid}, dmr: {address, port, passwordSet}, usrp: {rxPort, txPort}, privateNode: {node, exists, moduleLoaded}, link, modes, gateways: {ysf: {present, startup}, ...}, files: [{id, present, writable}]}`; no secret values anywhere.

SSE: event `dvswitch` (all roles) carries the Snapshot, sent on change (own commands, service state changes, job log lines) and not in the initial state: the client fetches `GET /api/dvswitch` after login, like schedules.

## Settings and storage

| Key / table | Content |
|---|---|
| `dvswitch.node` | optional override of the detected bridge node |
| `dvswitch.link_mode` | `ondemand` (default) or `permanent` |
| `dvswitch.link_node` | local node the permanent link starts from |
| `dvswitch.link_paused` | reconciler pause after a manual disconnect |
| `dvswitch.last_cmd` | JSON `{mode, target, at, by}` for the degraded display |
| table `dvswitch_favorites` (new migration, appended to `migrations`) | `id INTEGER PRIMARY KEY, mode TEXT, value TEXT, name TEXT, sort INTEGER, created INTEGER`, `UNIQUE(mode, value)`; part of the ASLCP database, so already in backups |

The source of truth for node, ports and identity is always the config files, never these settings.

## Package layout

| Path | Content |
|---|---|
| `internal/dvswitch/` | `Service` (poller, snapshot, SSE `OnChange`, link reconciler), `Backend` interface (`Info`, `Status`, `Send`, helper calls), `tlv.go` (encoder, validators, mode sequences), `lists.go` (host and TG file parsers), `state.go` (ABInfo types), `favorites` store interface |
| `internal/dvswitch/ini/` | lossless ini editor shared by helper and tests |
| `internal/dvswitch/fake/` | in-memory backend for demo and tests |
| `internal/helper/dvswitch.go` | ops, allowlist table, install script, history copies; `backup*.go` gains the DVSwitch part |
| `internal/astconf` | helpers `AddBridgeNode`, `SetModuleLoad` (pure edits, with corpus-based tests) |
| `internal/configsvc` | optional `ApplyFiles` entry reused by the DVSwitch plan (no new reload logic; modules.conf stays "restart") |
| `internal/api/handlers_dvswitch.go` | `dvswitchRoutes()`, `Deps.DVSwitch` |
| `internal/store` | favourites CRUD, migration |
| `web/src/components/DvswitchCard.svelte`, `DvswitchSetup.svelte`, `web/src/lib/dvswitch.ts`, `types.ts`, `api.ts`, `store.svelte.ts` | UI |
| `web/mock/dvswitch.ts` | mock API |

Docs touched at implementation time: `docs/API.md`, `docs/BACKUP.md`, `docs/CONFIG.md` (pointer), changelog; `packaging/` only for the optional option B unit change.

## UI

### Dashboard card

`DvswitchCard.svelte` after `SchedulesCard`, rendered only when `snapshot.configured`.

- Header "DVSwitch" with service dots (analog_bridge, mmdvm_bridge, active gateways); amber when a unit is down or the state is unknown.
- Line: "Mode YSF, tuned to Example room (192.0.2.10:42000)", name resolved from the lists or a favourite; "Mode unknown" when `source` is `none`.
- Mode buttons DMR / YSF / P25 / NXDN (current one highlighted; disabled with a tooltip when the mode is not enabled in MMDVM_Bridge). Pending state until the SSE snapshot confirms; "Not confirmed" toast if it does not within 5 s.
- Tune: input with per-mode validation and "Go"; picker (search over `lists` for YSF/P25/NXDN, TG list or free number for DMR); favourites as chips for the current mode (star button saves the current target, long-press or menu to rename/delete); "Unlink" button.
- Link line: "Bridge node 1999 linked to 2006 (permanent)" or, on demand, a Link/Unlink-node button.
- Operator/admin get controls; viewers get text only. Admin sees a "Setup" link to the DVSwitch tab.

### Setup page

Recommendation: **its own top-level tab "DVSwitch" (`#/dvswitch`, admin, lazy-loaded)** next to Config, not inside Config. Config is the `/etc/asterisk` editor and snapshot history; DVSwitch spans non-Asterisk files, services and an apt install, which that scope (and its history) does not cover. The tab is visible to admins even before anything is installed (so Install is discoverable) unless `supported` is false. Sections: 1 Install (button, confirm dialog, live log), 2 Node and link (M2), 3 Identity and network (M4: callsign, DMR ID, ESSID, DMR master and password write-only field showing "set"/"not set", enabled modes, startup mode), 4 Services (status, restart buttons with the "mode resets to startup values" warning, rollback). If the guided node actions of CONFIG beta.5 ship, section 2 gets a link from there; the page stays.

## Demo mode and mock

- `--demo`: `Deps.DVSwitch` is a `Service` over `internal/dvswitch/fake`: configured, in-memory state (mode, target, slot), commands applied by parsing the same `key=value` strings the real path builds (so the TLV encoder and validators are exercised), embedded sample host lists with placeholder entries on `192.0.2.x`, a few favourites, services all green, an install job that replays scripted log lines over ~10 s, setup forms that mutate the fake and report `passwordSet`. A demo switch needs no helper.
- `web/mock/dvswitch.ts` implements the same endpoints for the Vite mock; one fixture is "installed but not configured" to show the setup flow.
- Go tests: conformance test of the TLV sequence against a loopback UDP listener; ABInfo parsing against a fixture; ini editor round-trip (comments, spacing styles); allowlist table tests (traversal impossible by enum, bad keys, bad values never echoed); astconf bridge-node edit on the `asl3-dist` corpus; reconciler with a fake `Linker`.

## Phasing

Each milestone is shippable alone and useful on a node where DVSwitch was installed by hand.

| M | Content | Must be verified on a real node first |
|---|---|---|
| **M1 Panel** | `dvswitch-status`, `dvswitch-info`, native TLV control, snapshot/SSE, lists, favourites, card, demo/mock, API docs. Configured = a USRP node exists and `analog_bridge` is loaded; nothing is written to any config | ABInfo sample, option A works (or B), exact datagram sequence and timing of `mode`, a real mode switch and tune per mode, host file formats and permissions |
| **M2 Private node + USRP** | astconf node and modules edits, preview/apply plan, ports through `dvswitch-config-apply` (USRP keys only), `dvswitch-restart`, link toggle + reconciler, DVSwitch tab (Node section), DVSwitch part in backups, mask.go rule | Stanza and `duplex` on a working bridge, chan_usrp load behaviour, `update-config.sh` effect, unit dependencies, backup/restore round trip |
| **M3 Installer (implemented, install and uninstall unverified on a real node)** | `dvswitch-install`, `dvswitch-uninstall`, job UI, platform gating, explicit package set (no `dvswitch-server`) | Minimal set works without recommends at runtime (the packages ship the scripts, inis, units and host files: verified 2026-11-17), armhf and amd64 against arm64 behaviour, trixie architectures, keyring holds one primary key, licence, whether Asterisk config is touched. Installer content, key, bookworm repo and package facts: verified 2026-11-17 |
| **M4 Credentials and config** | Identity, DMR network password, enabled modes, startup values, gateway startup reflectors, rollback | Which keys exist in P25/NXDN gateway ini, password character handling, DMR connect with a test hotspot account, restarts per change |

## Unverified / to check on a real node

The installer, the repository key, the bookworm repo and the package facts were checked by the maintainer on 2026-11-17 (see Install, Upstream facts and Package facts). Resolved items below are marked with that date. The closed binaries and everything else remain second-hand. Each open item lists what the maintainer can run. All commands are read-only; where output may contain a secret or a callsign, replace it by hand (`N0CALL`, `1234567`) before sharing. Never paste a password.

| Item | Why | Commands |
|---|---|---|
| **Verified 2026-11-17:** installer script content, bookworm and trixie (diffed: trixie differs only in keyring handling and is the older 2022 script; both write the bookworm suite) | design of `dvswitch-install` (apt source, key, packages) | none, done |
| **Verified 2026-11-17:** repository key and HTTPS keyring (fingerprint, HTTP 200). Open sub-check: the keyring holds no other primary key (the helper refuses one) | pinned fingerprint and the helper's key check | `d=$(mktemp -d) && curl -fsSL -o "$d/k.gpg" https://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg && gpg --show-keys --with-colons "$d/k.gpg" \| grep -E '^(pub\|fpr)'` |
| Repo, packages, dependencies, arch | **Package facts verified 2026-11-17** (Package facts). Open: the real `Inst` set on a node, and the simulation check in the install job | after an install: `grep -rh '^deb' /etc/apt/sources.list.d/ \| grep -i dvswitch`; `apt-cache policy analog-bridge`; `apt-cache show analog-bridge \| grep -E '^(Package\|Version\|Architecture\|Depends\|Recommends\|Homepage\|License)'`; `apt-get -s install --no-install-recommends dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway \| grep ^Inst` (before installing); `dpkg --print-architecture; . /etc/os-release; echo $VERSION_CODENAME; uname -m` |
| **Verified 2026-11-17:** armhf on bookworm (`dists/bookworm/Release` lists armhf; every package of the set has an armhf build). Open: a real install on a 32-bit Pi, and whether the older armhf builds of `analog-bridge`, `mmdvm-bridge` and `md380-emu` behave like arm64 | `supported` gating | `curl -fsSL http://dvswitch.org/DVSwitch_Repository/dists/bookworm/Release \| grep ^Architectures`; on a 32-bit Pi after an install, `apt-cache policy analog-bridge mmdvm-bridge md380-emu`, then `journalctl -u analog_bridge -n 20 --no-pager \| sed -E 's/[0-9]{7,9}/1234567/g'` and compare with an arm64 node |
| **Open (runtime only):** the minimal set works without the recommended packages. The packages ship the scripts, inis, units and host-file updates (Package contents, verified 2026-11-17). The install uses `--no-install-recommends` and no `dvswitch-server`; what is still open is how they behave at runtime | Install design (no metapackage) | after an install of the set: `dpkg -L dvswitch-base analog-bridge mmdvm-bridge \| grep -E '\.(sh\|ini\|service\|timer)$\|cron'`; `systemctl list-unit-files \| grep -i -E 'analog\|mmdvm\|gateway\|md380'`; `systemctl is-enabled analog_bridge mmdvm_bridge` |
| **Open:** md380-emu and `qemu-user-static` | md380-emu pulls `qemu-user-static` on all three arches; its size is unknown; the confirm dialog's size statement depends on it | `apt-cache show md380-emu \| grep -E '^(Version\|Depends\|Installed-Size)'`; `apt-cache show qemu-user-static \| grep -E '^(Version\|Installed-Size)'`; `apt-get -s install --no-install-recommends md380-emu \| grep -c ^Inst` |
| **Verified 2026-11-17:** amd64 builds exist in the bookworm suite for every package of the set (`dvswitch-base` is Architecture: all) (Package facts). Open: a real amd64 install | `supported` gating on amd64 | on an amd64 node after `apt-get update`: `apt-cache policy dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway`; `apt-get -s install --no-install-recommends dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway wget \| grep -c ^Inst` |
| **Open:** netcheck without internet | The bridges `Require` netcheck; the status must explain a failed netcheck. Not seen on a node | `systemctl is-active netcheck analog_bridge mmdvm_bridge`; `systemctl status netcheck --no-pager \| tail -n 5`; `cat /usr/local/sbin/netcheck` |
| **Believed, unverified:** P25 IMBE is done in software by Analog_Bridge, so P25 needs no md380-emu | Decides whether P25 works in the minimal set | none (needs a P25 test call on a node, not a read-only check) |
| trixie host: **the bookworm suite is what upstream installs there** (script diff verified 2026-11-17). Open: `dists/trixie/Release` exists (HTTP 200) but upstream does not use it; whether the bookworm binaries run correctly on a trixie node | `supported` gating for trixie | on a trixie node, after an install: `. /etc/os-release; echo $VERSION_CODENAME; dpkg --print-architecture`; `apt-cache policy analog-bridge`; then check the services (`systemctl is-active analog_bridge mmdvm_bridge`) and `journalctl -u analog_bridge -n 20 --no-pager` (replace digit runs) |
| Postinst touching Asterisk | "does the installer change Asterisk config" | `ls /var/lib/dpkg/info/*.postinst \| grep -E 'dvswitch\|analog\|mmdvm\|md380\|ysf\|p25\|nxdn'`; `cat /var/lib/dpkg/info/analog-bridge.postinst /var/lib/dpkg/info/mmdvm-bridge.postinst`; `dpkg -L dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway \| grep -i -E 'asterisk\|rpt'`; with ASLCP's history repo: `sudo git --git-dir=/var/lib/aslcp-config-history --work-tree=/etc/asterisk status --short` right after an install |
| Real ABInfo sample | schema, field presence, freshness | `ls -l --time-style=full-iso /tmp/ABInfo_*.json`; `python3 -m json.tool /tmp/ABInfo_*.json` (it should hold no secrets; check for the string "pass" first: `grep -ci pass /tmp/ABInfo_*.json`); `grep -nE '^\s*(transferRootDir\|exportMetadata)' /opt/Analog_Bridge/Analog_Bridge.ini` |
| Does AB refresh ABInfo by itself | `info` polling rate | `stat -c %y /tmp/ABInfo_*.json; sleep 60; stat -c %y /tmp/ABInfo_*.json` while idle, then again after `/opt/MMDVM_Bridge/dvswitch.sh tune 91` (use any harmless TG) |
| Option A (helper reads via /proc) | sandbox decision | `systemctl show analog_bridge -p MainPID -p PrivateTmp -p ActiveEnterTimestamp`; `sudo ls -l /proc/$(systemctl show -p MainPID --value analog_bridge)/root/tmp/ \| grep ABInfo`; as the helper's sandbox: `sudo systemd-run --wait --pipe -p PrivateTmp=yes -p ProtectHome=yes sh -c 'ls -l /proc/$(systemctl show -p MainPID --value analog_bridge)/root/tmp/ \| grep ABInfo'` |
| Exact datagrams and timing of `mode`/`tune` | conformance fixture | run the capture while the bridge is idle (audio frames would also show): `sudo timeout 30 tcpdump -i lo -nn -tt -X 'udp and portrange 30000-36000'` in one shell, and in another `/opt/MMDVM_Bridge/dvswitch.sh mode YSF; /opt/MMDVM_Bridge/dvswitch.sh tune 192.0.2.10:42000; /opt/MMDVM_Bridge/dvswitch.sh mode DMR`; plus `grep -n -E 'rxport\|txport\|ambeMode\|remoteControlCommand\|sleep' /opt/MMDVM_Bridge/dvswitch.sh` |
| Ini file modes and owners | who can read/write, helper vs server | `stat -c '%U:%G %a %n' /opt/Analog_Bridge/*.ini /opt/MMDVM_Bridge/*.ini /opt/*Gateway/*.ini /var/lib/mmdvm/* /var/lib/dvswitch/* 2>/dev/null`; `ls -ld /opt/Analog_Bridge /opt/MMDVM_Bridge` |
| Unit details and dependencies | restart order, PrivateTmp, `update-config.sh` | `systemctl cat analog_bridge mmdvm_bridge ysfgateway p25gateway nxdngateway`; `systemctl list-dependencies analog_bridge mmdvm_bridge`; `systemctl list-unit-files \| grep -i -E 'analog\|mmdvm\|gateway\|md380\|dvswitch'`; `cat /usr/local/sbin/update-config.sh` |
| Gateway ini keys | allowlist for P25/NXDN | `grep -n -E '^\[\|^(Callsign\|Id\|Suffix\|Startup\|HostsFile1)\s*=' /opt/YSFGateway/YSFGateway.ini /opt/P25Gateway/P25Gateway.ini /opt/NXDNGateway/NXDNGateway.ini` (replace the callsign and ID values in the output) |
| MMDVM_Bridge keys | allowlist, password character rules | `grep -n -E '^\[\|^(Callsign\|Id\|Enable\|Address\|Port\|Local\|Options\|Duplex)\s*=' /opt/MMDVM_Bridge/MMDVM_Bridge.ini` (this pattern deliberately excludes `Password`; test special characters on a spare setup, not by posting values) |
| Host and TG file formats | picker parsers | `ls -l /var/lib/mmdvm/`; `head -n 4 /var/lib/mmdvm/YSFHosts.txt /var/lib/mmdvm/P25Hosts.txt /var/lib/mmdvm/NXDNHosts.txt /var/lib/mmdvm/TGList_BM.txt 2>&1`; `wc -l /var/lib/mmdvm/*Hosts.txt` |
| Bridge node stanza and `duplex` | M2 defaults | `awk '/^\[1999\]/,/^\[[^1]/' /etc/asterisk/rpt.conf \| grep -v -i -E 'pass\|secret'` (use your bridge node's number); `grep -n usrp /etc/asterisk/modules.conf`; `asterisk -rx 'module show like usrp'`; the stock `[node-main]` comment recommends `duplex = 0` for external controllers, check what works with real traffic |
| Inherited template keys on a bridge | which keys to unset | `asterisk -rx 'rpt show variables 1999' 2>&1 \| head -40` and compare `idrecording`, `idtalkover`, `tailmessagetime` against the stock `[node-main]` |
| Licence | whether ASLCP may even mention/offer the install | `dpkg -S /opt/Analog_Bridge/Analog_Bridge /opt/MMDVM_Bridge/MMDVM_Bridge`; then `ls /usr/share/doc/<package>/` and `head -n 60 /usr/share/doc/<package>/copyright` for each owning package |
| Services after a fresh install | "Not configured" state | `systemctl is-enabled analog_bridge mmdvm_bridge; systemctl is-active analog_bridge mmdvm_bridge; journalctl -u analog_bridge -n 20 --no-pager \| sed -E 's/[0-9]{7,9}/1234567/g'` (exit 252/253/254 meanings) |

## Decisions (2026-11-17)

The maintainer accepted the recommendations: "the recommendations are good, we can revise later". Each item records the chosen option. They may be revised later; a revision means editing this section and the design text it refers to.

1. **Permanent link and manual disconnect.** Chosen: after a manual disconnect of the permanent bridge link, ASLCP pauses its maintenance until setup is saved or Asterisk restarts (matches the schedules manual-action behaviour). Not chosen: restoring the link within a minute.
2. **Startup values.** Chosen: an explicit "Use current mode and talkgroup as startup" button only. ASLCP does not write the ini on every tune.
3. **Sandbox change if option A fails.** Chosen: fall back to option B (`PrivateTmp=no` on `aslcp-helper.service`, with the owner check on the file). Option D only if B is also refused. Confirmed by the maintainer 2026-11-17.
4. **Installer trust.** Chosen: replicate the apt source and key steps as fixed helper content from the verified facts (Install, Upstream facts), with the pinned full fingerprint `6DDFC33CD5FC678659642DF572147EC1E788D4C3`, checked with Go stdlib. The upstream script is **not copied** (no licence header) and never run as root.
5. **Backups.** Chosen: include the DVSwitch inis (BrandMeister/TGIF password) in the existing unencrypted backup like every other secret, with the warning. Not chosen: an opt-in part.
6. **Unsupported platforms.** Chosen: hide Install and show a pointer for any host codename other than bookworm or trixie, or an arch outside the matrix.
7. **Install package set (decided 2026-11-17, confirmed by the maintainer).** Chosen: ASLCP installs the explicit set `dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway wget` with `--no-install-recommends`, not `dvswitch-server`. `wget` was added on 2026-11-17 after the package contents check (`netcheck` needs it, and `dvswitch-base` declares no Depends); the maintainer confirms this addition. Reasons: `dvswitch-dashboard` would add PHP, Apache (`libapache2-mod-php`) and Node.js, which ASLCP replaces, and Apache could clash with ports used by ASLCP/Allmon3; `analog-reflector` adds mosquitto; monit, menu, ircDDBGateway, Quantar and the parrots are not used in v1. `stfu` is left out (MMDVM_Bridge connects to BrandMeister directly). An operator who installed `dvswitch-server` by hand is still detected and supported. The confirm dialog lists the packages and mentions `qemu-user-static`.
