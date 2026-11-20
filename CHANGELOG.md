# Changelog

Release notes are taken from the section of the tag being released
(`## [X.Y.Z]`) when the tag has no annotation.

## [1.2.3] - 2026-11-20

### Changed
- Installer: it refuses to run (and changes nothing) while ASL3 is still finishing its first-boot setup or while another package manager is running, instead of failing on the dpkg lock or being cut off by the first-boot reboot. It tells you to wait and run it again; a pending reboot only produces a warning.
- Package operations (install and update of ASLCP, DVSwitch and STFU, OS upgrade) wait up to 10 minutes for the apt lock instead of failing.
- The firewalld rule and the setup link in the install messages follow the configured port: the `aslcp` firewalld service for 8080, a plain TCP port for any other port. The firewall status in the diagnostics report shows whether the configured port is open (runtime or permanent configuration).
- Installer: it no longer asks for the administrator account in the terminal. It prints a one-time setup link instead, which opens the first-run page where you can create the administrator or restore a backup from an existing node.

### Added
- `aslcp admin setup-code` shows the one-time setup code again (until an account exists).
- Installer: `ASLCP_PORT=<1-65535>` picks the web port at install (for example `curl -fsSL https://artw1.github.io/aslcp/install.sh | sudo ASLCP_PORT=9090 sh`). It sets `listen` in `/etc/aslcp/aslcp.conf` (an address part is kept), restarts ASLCP and opens the port in firewalld. A bad value is refused before anything is installed.
- Installer: it warns, naming the program, when the web port is already in use, and says so when ASLCP did not start, instead of only showing the setup-code hint.
- Installer: `ASLCP_DVSWITCH=1` (or `yes`, `true`) also installs DVSwitch once ASLCP is running, and can be combined with `ASLCP_PORT`. If the DVSwitch install fails the installer warns and still finishes.
- `sudo aslcp dvswitch install` installs DVSwitch from the terminal (the same install as the DVSwitch page) and follows the install log until it ends.
- Restoring a backup onto a node without DVSwitch keeps the backup's DVSwitch settings and applies them automatically when you install DVSwitch, then starts the DVSwitch services, so no second restore is needed. The DVSwitch page shows the pending settings with a Discard button (`DELETE /api/dvswitch/restore-pending`; the status carries `restorePending`).

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- DVSwitch: an uninstall (or an install) that finished successfully could show as failed on the DVSwitch page.
- Restore preview: DVSwitch packages are no longer offered under "Packages to install" with a plain `apt install` command (DVSwitch is installed from the DVSwitch page) and no longer listed under "Other software".

## [1.2.2] - 2026-11-18

### Added
- DVSwitch: BrandMeister and DMR - Other side by side. The panel has a "BrandMeister" mode (through STFU, BrandMeister's Open DMR Terminal protocol) next to "DMR" (DMR - Other: any other DMR network through MMDVM_Bridge). STFU logs in to BrandMeister only while BrandMeister is the active mode (unless "Stay logged in" is set), so it does not take the connection from a phone app that uses the same DMR ID; switching takes a moment and needs no restart. Each has its own remembered talkgroup, favourites and labels (for example "BrandMeister TG 91" and "DMR TG 1"), and Parrot and private calls are offered on both. The panel offers BrandMeister once it is set up.
- DVSwitch setup: a BrandMeister section with a master picker (the BrandMeister masters of the host list, or type a host name; port 54006), the hotspot security password (write-only), talker alias, start talkgroup (default 4000), the login status and the last transmission heard. Saving checks the login; STFU then runs only while BrandMeister is the active mode, unless you tick "Stay logged in when not in BM mode" (default off). "Clear BrandMeister connection" stops it. While it is stopped on purpose the status reads "Stopped (not in use)". Switching to BM waits up to 10 seconds for the login and warns if it does not appear. BrandMeister SelfCare lists the connection under DMR Terminals as "Open DMR Terminal". STFU logs in with the 7-digit DMR ID (no ESSID).
- DVSwitch setup: the DMR master section is now "DMR - Other". Its picker leaves out BrandMeister masters, a BrandMeister master is refused there (with a warning when one is already saved), and "Clear DMR connection" turns it off. "Clear password" stays.
- DVSwitch: STFU is installed with DVSwitch (left stopped until BrandMeister is set up) and removed by the uninstall. Older installs keep the "Install STFU" button.
- DVSwitch: Switching modes now disconnects the mode you leave; it reconnects when you switch back.
- DVSwitch: BM Parrot (talkgroup 9990) is called as a private call, which is the only way it answers. The DVSwitch panel has a Parrot button and a "Private call" checkbox for DMR (a trailing `#` on the number does the same). A private call is never remembered or resumed after a restart. Verified through STFU; the MMDVM_Bridge path is not yet verified.
- DVSwitch: the bridge node's "RX keyed" line shows who is talking (for example "RX keyed · DMR TG 91 · N0CALL"), and the DVSwitch panel has a Last heard list of the last five callers. The callsign is looked up by DMR ID in the MMDVM_Bridge list when the log has none (as with STFU); without a callsign the DMR ID is shown. The Parrot's reply shows as "Parrot (private call)". The caller is shown while the bridge node is keyed.
- DVSwitch setup: Hotspot info fields (Location, Description, URL, Frequency, optional Latitude and Longitude), written to the `[Info]` section of `MMDVM_Bridge.ini` with the identity save (one frequency for receive and transmit, typed in MHz or Hz). The page warns while the stock sample values are still in place. MMDVM_Bridge is restarted after a change, like other identity edits.
- API: `PUT /api/dvswitch/brandmeister` takes `stayLoggedIn` and returns `bmLogin`; the setup carries `brandmeister.stayLoggedIn`; mode and tune replies carry `bmLoginPending` and `stfuStopFailed`; `dmr-status` `bm.state` can be `idle`.
- API: `PUT` and `DELETE /api/dvswitch/brandmeister`, `DELETE /api/dvswitch/dmr-connection` and `POST /api/dvswitch/stfu/install` (admin). The DVSwitch status carries `stfu`, the setup carries `brandmeister` and `dmr.enabled`/`dmr.pointsAtBm`, `GET /api/dvswitch/dmr-status` carries `via`, `rx` and `bm` (BrandMeister's login), the snapshot carries the `BM` mode, `caller`, `heard` and `bridge.private`, and a mode switch reply carries `left`. `PUT /api/dvswitch/credentials` refuses a BrandMeister master (`400 bm_master`). `PUT /api/dvswitch/identity` takes `info`; a DMR or BM tune accepts a trailing `#`.

### Security
- STFU runs as root and writes the BrandMeister password to its log when it starts. ASLCP drops every log line that mentions a password before it reads the log, and keeps `/var/log/dvswitch/STFU.log` and its rotated copies readable by root only: the permissions now persist across log rotation (a systemd drop-in gives STFU `UMask=0077`; it is removed when you clear the BrandMeister connection or uninstall DVSwitch).

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- DVSwitch: switching to a mode with no remembered target could show the previous mode's value as a talkgroup (a YSF reflector showed as "DMR TG 76"). The new mode is now tuned to its own "disconnected" value, and the panel never shows another mode's value as a talkgroup.

## [1.2.1] - 2026-11-18

### Added
- DVSwitch bridge node name: give the bridge node a display name (default "DVSwitch linked node") on the DVSwitch tab. It saves at once with no Asterisk restart and shows in the header of the bridge node's section in the connection status table (and on the DVSwitch panel).
- Connection status table: the bridge node's "RX keyed" line shows, live, the mode and the talkgroup or reflector the bridge is tuned to (for example "RX keyed · DMR TG 3100"). It is no longer a badge on the DVSwitch panel.
- API: `PUT /api/dvswitch/private-node/name` (admin); the DVSwitch snapshot and setup carry `bridgeNode.name`.

### Changed
- Dashboard: the Schedules card is now the first card, above the connection status tables.
- Dashboard: clicking a node number (status table, Network list) or anywhere on a Favorites row opens a popup with the same Connect, Disconnect, Monitor and Local Mon buttons and options as the Connect bar, prefilled with that node (with the local node selector when you have several). The popup closes after a successful action. Double-click-to-connect on Favorites rows is removed.
- DVSwitch panel: "Link node" is now a green button and "Unlink node" a red one.

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- DVSwitch: after disconnecting a mode's target (for example DMR from its talkgroup), switching to another mode and back reconnected the old target. A disconnect is now remembered; a new tune is remembered as before.

## [1.2.0] - 2026-11-18

### Added
- DVSwitch support (digital voice modes DMR, YSF, P25 and NXDN through Analog_Bridge / MMDVM_Bridge). The whole feature is built around the bridge node, a private USRP node that links to one of your local nodes.
- DVSwitch tab (`#/dvswitch`, between Audio Tuning and Settings, admin only). It detects whether DVSwitch is installed (by ASLCP or by hand), shows the version, services and host files, and offers Install and Uninstall with a confirm dialog and a live job log. Install works on Debian bookworm and trixie (amd64, arm64, armhf), uses a fixed package set with a pinned repository key, and refuses to run when the clock is unset or an apt job is running. Uninstall is offered only for installs made by ASLCP, reverts the bridge node it added and restarts Asterisk.
- DVSwitch setup: identity (callsign, DMR ID, ESSID, optional NXDN ID), DMR master address, port and password (write-only, never shown; "Clear password" for masters without one), which modes are enabled, and a startup card per mode (DMR talkgroup, slot and colour code; YSF, P25 and NXDN reflector or talkgroup). Picking a startup mode opens its tab. Searchable pickers for the DMR master, YSF reflectors and P25/NXDN talkgroups. Every save is a reviewed change with a per-file history and a rollback button, and nothing restarts by itself; the page lists which services need a restart and warns that a restart resets the running mode and talkgroup to the startup values. After saving credentials, the DMR login to the master is checked and reported.
- Dashboard DVSwitch panel (after the Connect bar): current mode, target and slot with a coloured status line (amber when a service is down or the state is unknown, green when all services run and a target is tuned). Operators and admins switch mode, tune a talkgroup or reflector, and unlink; viewers see the status only. Commands run one at a time and the panel is locked until the bridge confirms.
- DVSwitch favourites: save the current target with a name (up to 40 characters, 50 per mode) and tune it with one click from chips below the panel; rename and delete from the chip menu. Favourites are shared by the whole node.
- Remembered talkgroup per mode: switching back to a mode re-tunes the target last used in it, so a YSF reflector never leaks into DMR as a talkgroup. Optional "Resume last tune after restart" (admin, off by default) returns the node to the last mode and target after an Analog_Bridge restart or a reboot.
- The whole DVSwitch panel turns light green while the bridge node is linked to your local node.
- Bridge node setup (admin): add the private node with its number, USRP ports, local node and link mode. The apply shows a diff of `rpt.conf`, `modules.conf` and `Analog_Bridge.ini` first, restarts Asterisk once and checks the bridge; if it fails, "Roll back" restores the previous files. The node is written neutral, with no ID, telemetry, courtesy tone or hang time. Numbers already in `rpt.conf` are refused.
- Bridge node: Change number and Remove, each one reviewed apply with one Asterisk restart and a rollback. Remove disconnects the link, reverts what ASLCP added and restores the previous USRP ports. Only a bridge node added by ASLCP can be changed or removed.
- Link modes for the bridge node: on-demand (Link node / Unlink node button on the panel) or permanent (ASLCP keeps the link up, also after an Asterisk restart; a manual disconnect pauses it until the next save or restart through the panel).
- "Disconnect other links first" checkbox next to the Link node button on the panel (operators and admins, on by default like the Connect bar). It applies to the Link node button and to a save of the bridge section in permanent mode; automatic relinks never disconnect anything.
- Backup: new DVSwitch part with the five `.ini` files and `dvsm.macro` (passwords included, as the download dialog warns). It is restored only when DVSwitch is installed on the target node; older ASLCP versions cannot restore such an archive.
- Demo mode and `web/mock` simulate DVSwitch; `docs/DVSWITCH.md` holds the design and the checklist for a real node.
- API: `GET /api/dvswitch`, `GET /api/dvswitch/job`, `POST /api/dvswitch/install`, `POST /api/dvswitch/uninstall` (admin); `GET /api/dvswitch/setup`, `PUT /api/dvswitch/identity`, `PUT /api/dvswitch/credentials`, `PUT /api/dvswitch/modes`, `PUT /api/dvswitch/startup`, `POST /api/dvswitch/rollback`, `POST /api/dvswitch/restart` (admin), `GET /api/dvswitch/dmr-status` and `GET /api/dvswitch/lists` (operator and admin); `GET /api/dvswitch/snapshot`, `POST /api/dvswitch/mode`, `POST /api/dvswitch/tune`, `POST /api/dvswitch/unlink`, `GET/POST /api/dvswitch/favorites`, `PUT/DELETE /api/dvswitch/favorites/{id}`, `GET/PUT /api/dvswitch/resume`, `PUT /api/dvswitch/link`; `PUT/DELETE /api/dvswitch/private-node`, `POST /api/dvswitch/private-node/rollback` (admin); SSE event `dvswitch`; `GET /api/backup/info` and `POST /api/backup` gain the `dvswitch` part.

### Changed
- Header menu: DVSwitch tab added before Settings.
- A DVSwitch install or uninstall counts as an apt job: system and ASLCP updates are refused while one runs, and the other way round.

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Restart Asterisk: when the connection to Asterisk drops right after the restart command (an idle Asterisk restarts at once without replying), the panel now reports success instead of an error.
- Node status and the dashboard showed a node twice when Asterisk listed it twice (for example while reconnecting); each node now appears once, and an established link wins over a connecting one.
- Backup, restore and other panel actions could lose their reply with a "connection reset" error in rare cases; the helper now drains unread request bytes before closing.

## [1.1.24] - 2026-11-17

### Added
- Schedules: new setting "When I connect or disconnect manually" at the top of the Schedules tab (operators and admins; everyone sees the current choice). "Pause all until re-enabled" is the previous behaviour and stays the default. "Pause all for today" pauses the same way but switches the schedules on again by itself at the next midnight (node time); the Schedules tab and the dashboard card say "Paused until 00:00" / "Paused until midnight". "Cancel the running window, keep schedules on" only cancels the window that is running on the node you connected or disconnected on; schedules stay on, other nodes are untouched, and a notice such as "Tuesday net" cancelled by manual connect 41234 on 2006 by op is shown until the window would have ended.
- API: `PUT /api/schedules/settings` (`manualAction`); the schedules snapshot gains `manualAction`, `resumeAt` and `lastCancel`.

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Schedules: saving a schedule that overlaps another one on the same node is now refused when one of the two has "disconnect others first" on (both connects, both enabled). Before, a new exclusive schedule saved while another window was running connected at once and dropped the running link. The dialog shows the clash (for example `overlaps "Tuesday net" (Tue 19:00–20:30) on node 2006`) and disables Save; switching a schedule on shows the same message when it would clash. API: `409 schedule_conflict`. Schedules saved earlier are left alone.

## [1.1.23] - 2026-11-17

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Favorites scan showed a node that was transmitting continuously green (active) instead of red (keyed), because the stats service publishes a new report only about every 30 s; linked favorites now also turn red immediately from the local link, and a node that stops reporting no longer stays red.

## [1.1.22] - 2026-11-17

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Updates tab: the System packages list missed packages whose line in apt's simulated upgrade ends with an extra `[]` (seen with Raspberry Pi mesa updates), so it could show fewer packages than `apt list --upgradable`.

## [1.1.21] - 2026-11-17

### Added
- Schedules: scheduled connects and disconnects, for example a weekly net. New Schedules tab (`#/schedules`, visible to all roles, editing for operators and admins) and a one-line dashboard card between the connect bar and the favorites with the on/off switch, the next event with a countdown ("Running: ... until 20:30" during a window) and the pause reason.
- A schedule has a local node, a target, an action (connect or disconnect), the mode, "permanent" and "disconnect others first" like the manual Connect, weekly days or a single date, a start time and, for connects, an optional end time: the link is disconnected at the end (also when it was already linked at the start); an end before the start crosses midnight. A one-off schedule switches itself off after its last event.
- Times are node time (the time zone of the machine, DST included; a time skipped by the clock change runs at the first valid minute). The Schedules tab shows the zone name and notes it when the browser is in another zone.
- A manual connect, disconnect or disconnect all from the panel (any user) cancels the windows running at that moment and pauses all schedules, with the reason shown ("manual connect 41234 on 2006 by op"); only an operator switching schedules on again resumes them, and the cancelled windows are not caught up. Changes made outside the panel (DTMF, Allmon3, a link dropping) are not detected.
- Catch-up: when ASLCP starts or schedules are switched on, a window that is running now is connected now, and a window whose end passed while ASLCP was down is disconnected. Single events are never replayed. A failed command (Asterisk down, error) is retried every minute until the end of the window, or for 10 minutes for a single event; the result is shown per schedule (`ok`, `skipped: ...`, `error: ...`).
- Backups include schedules; the restore page shows how many; a window running at backup time is not acted on after a restore.
- API: `GET/POST /api/schedules`, `PUT/DELETE /api/schedules/{id}`, `PUT /api/schedules/enabled` (operator for writes), SSE event `schedules` with the snapshot, new `schedules` table (database migration 4) and `docs/SCHEDULES.md`. Demo mode (two example schedules on node 2006) and `web/mock` simulate the feature.
- Tests: the occurrence calculation (midnight crossing, one-off dates, DST gap and repeated hour, cancelled occurrences), the engine with a fake clock (window, skip, retries, catch-up, pause, restart, ordering, one-off), time zone name detection, the store, the REST endpoints with roles and validation, manual actions pausing, and an end-to-end run against the fake node.

## [1.1.20] - 2026-11-17

### Changed
- ASLCP is now open source under the GNU GPL v3.0 or later; source code at https://github.com/artw1/aslcp.
- The apt repository moved to the gh-pages branch of the same repository; the URL https://artw1.github.io/aslcp is unchanged, no action needed.

## [1.1.19] - 2026-11-17

### Added
- Config editor: find and replace. Press Ctrl+F (Cmd+F on a Mac) in the editor, or use the new Find button above it. Supports next / previous, match case, regular expressions, whole word, replace and replace all; other occurrences of the selected text are highlighted.

## [1.1.18] - 2026-11-17

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Restart ASLCP (and Reboot) no longer leave the "Restarting…" banner spinning. The page reloads as soon as the restarted panel answers, even when the panel is back before the restart request returns (the server now reports a per-process instance id in the `X-ASLCP-Instance` header and the SSE `hello` event). A panel that has not restarted after 60 seconds gives an error instead of spinning.

## [1.1.17] - 2026-11-16

### Changed
- Header menu order: Dashboard, Lookup, Config, Audio Tuning, Settings, Backup, Updates.
- Backup (with restore) is its own admin-only tab `#/backup`, moved out of Settings.
- Settings: About and Node list are now two cards side by side at the top (one column on narrow screens).
- "Disconnect before Connect" / "Disconnect before Monitor" reset to on / off every time the panel loads. Changing them in the connect bar lasts only until the next reload (it is no longer saved in the browser), so cross-linking is never left on by accident. The server no longer stores or serves these defaults.

### Removed
- Settings: the Updates card (the Updates tab stays) and the admin "Connecting" section.
- `PUT /api/settings` and the `exclusiveDefault` / `monitorExclusiveDefault` fields of `GET /api/settings` and the SSE `hello` settings.

## [1.1.16] - 2026-11-16

### Changed
- Version scheme: the `X.Y.Z-beta.N` tags and `X.Y.Z~beta.N` package versions are gone. Stable stays 1.0.0 and the former 2.0.0 betas continue as plain 1.1.x versions. Every `vX.Y.Z` tag now builds and publishes to the beta apt suite (and `latest-beta.json`) and creates a GitHub pre-release.
- A release is made by the new **Promote** workflow (Actions > Promote, input: version): the exact debs already published to beta move to the stable suite, `latest.json` is updated and the GitHub release becomes the latest full release. No rebuild; promoting a version that is not in beta is refused, and running it again changes nothing. Backport tags on `release/*` follow the same rule.
- New **Remove from beta** workflow (dry run by default) to delete beta packages by version glob from the apt repository.
- Settings > Updates text for the channel selector: "Beta gets new versions as soon as they are built and may be less stable. Release only gets versions promoted after testing."

## [1.1.15] - 2026-11-16

> 1.1.1–1.1.15 were published as 2.0.0-beta.1–15.

### Added
- Updates tab (`#/updates`, admin only) with two sections: ASLCP (the version, channel, check and update controls and the job log, moved here from Settings, which now has a link) and System packages.
- System package (apt) updates. The helper has new fixed ops (no arguments, no package names from the web): `os-check` (`apt-get update`, quiet and noninteractive, 5 minute timeout, then a simulated `apt-get upgrade` that lists upgradable packages with a security flag when the origin contains "security", the kept-back packages and the reboot-required state), `os-status`, `os-apply`, `reboot` and `aslcp-restart`. A held apt lock gives "apt is busy, try again later" (`409 apt_busy`).
- The server checks once a day (first check about 5 minutes after start if the last is older than 24 hours); the result and its time are stored, so restarts do not re-check. "Check now" runs a manual check (`POST /api/updates/os/check`, `202`, poll `GET /api/updates`). The ASLCP 6 hour check is unchanged.
- Installing runs a transient systemd unit with plain `apt-get upgrade` (never `full-upgrade`; `--force-confdef --force-confold`) after a confirmation that lists the packages and warns that Asterisk packages drop all links and that `aslcp` restarts the panel. Kept-back packages are shown. Only one apt job runs at a time, shared with the ASLCP update (`409 update_running`). The list is checked again automatically afterwards. Audited as `updates.os.apply`.
- Reboot: when a reboot is required (`/var/run/reboot-required`) the Updates tab says so, with a "Reboot now" button (`POST /api/system/reboot`, audited as `system.reboot`).
- Header: a compact badge with the number of updates links to the Updates tab, whose nav item shows a count in the accent colour (warning colour for security updates or a pending reboot). Admins only; new admin-only SSE event `updates` carries counts only, never package names.
- Dashboard footer: "Restart ASLCP" (`POST /api/system/restart-aslcp`, helper `systemctl --no-block restart aslcp`, audited as `aslcp.restart`), "Restart Asterisk" and "Reboot" (the same helper op as the Updates tab), all admin only, each with a confirmation. The page shows "Restarting…" or "Rebooting… waiting for the node" and reloads once the server answers again.
- `GET /api/updates` gains an `os` object. Demo mode and `web/mock` simulate OS updates (a security package, an asterisk package, a reboot-required toggle and a job log).
- Tests: the apt output parser, the daily scheduler (persisted, no re-check after a restart within 24 hours, manual check), concurrency (`409`), admin-only access for all new endpoints (`403` for operator and viewer), the audit records, and the SSE counts not reaching non-admins.

### Changed
- The "Update available" badge in the header is now the updates badge (ASLCP plus system packages) and links to `#/updates` instead of Settings.

## [1.1.14] - 2026-11-16

### Changed
- Favorites scan: the row marked is now the next one to be checked, not the one being checked. A request takes a fraction of a second on real hardware, so the ring around the # number only flashed. The # number of the next favorite now has a blinking underscore beneath it ("Next to check"; a static underline with reduced motion). The ring is gone.
- `GET /api/favorites/stats` and the `favstats` event carry a new `next` field: the node that will be requested next, chosen by the same ordering the poller uses (never checked first, then the oldest; nodes in their skip period, your own nodes and out-of-range numbers are passed over). It is empty when the scan is off, nobody is watching, nothing is eligible or every node is in its skip period. It is sent whenever it changes, including when favorites are added, removed or reordered. `checking` stays in the payload for compatibility.

## [1.1.13] - 2026-11-16

### Changed
- Favorites scan: requests are now evenly paced. The burst of 2 is gone, so the first requests are no longer 3 s apart and the rest 6 s: one request goes out exactly every 60/rate seconds from the very first, never closer than 3 s. The default rate is back to 20 per minute (one request every 3 s; `[favstats] rate` stays 1..25 as the ceiling and the rate still halves after an HTTP 429): the earlier 429s were most likely the offline-node errors fixed in beta.12, which the header mislabelled as rate limiting. The adaptive rate, backoff and `Retry-After` work as before. The request log line shows `gap=` instead of `tokens=`.
- The Favorites header only says "rate limited, retrying in Ns" for an HTTP 429. Other pauses say "stats service error, retrying in Ns" (HTTP 5xx) or "paused after a network error, retrying in Ns"; the payload has a new `backoffReason` (`rate-limit`, `service-error`, `network-error`).
- Favorites header: instead of "updated Ns ago" (which only moved on successful polls and so kept climbing, and never moved for offline nodes) it shows `checked 12 of 30 · next in 4s`, `rate limited, retrying in Ns`, or `no favorites to check`. The `updated` field stays in the API for compatibility.
- The `favstats` SSE event is sent when a request starts and when it ends instead of on value changes plus a 15 s heartbeat; `stats` still carries only changed nodes.

### Added
- Favorites scan progress in `GET /api/favorites/stats` and the `favstats` event: `checking` (node being requested), `nextAt`, `checked`, `total` (eligible favorites; `checked` counts those answered in the current pass and restarts when a pass completes) and `checkedAt` (last attempt per node, including offline ones).
- The favorite being checked right now has a ring around its # number ("Checking now"; static with reduced motion).
- Rx% tooltips show how long ago the node was checked ("checked 12s ago", "not checked yet"); offline nodes show "—" with "No data (offline)"; EchoLink and private numbers say "Not scanned (EchoLink or private node)".
- Demo mode and `web/mock` simulate the progress, the indicator and offline nodes.

## [1.1.12] - 2026-11-16

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Favorites scan: one bad node no longer pauses the whole scan. On hardware the journal showed only `favstats: request failed (error), pausing 15s`. Responses are now decoded tolerantly (every field may be a string, number, bool or null, `links` an array of strings or numbers, `node` an object, null, array or missing; a field of the wrong type is left zero) and large unneeded parts such as `linkedNodes` are skipped token by token without being buffered. The size cap is 4 MB (was 512 KB) and an oversized response is reported as such instead of as a decode error.
- Offline and unknown nodes (the service answers 200 with `"stats":null`, an empty body, `null`, `{}`, an error object or an HTML page) no longer pause the scan or log an error: the node shows no values, is asked again after 10 minutes and goes to the back of the queue. Only the per-request line (`[favstats] log = true`, `status=200-nodata`) mentions it.
- Favorites scan: a node counts as Active (green) only while the service has live data for it. The registration record says "Active" even for offline nodes; `stats.data.time` older than 15 minutes also means offline, and so does WebTransceiver.
- Errors are classified. Network errors, timeouts, HTTP 429 and 5xx still pause the scan (backoff, adaptive rate). Invalid JSON, too-large responses and other HTTP 4xx (such as 404) only skip that node for 10 minutes and leave the success streak alone. The journal names the node and a short sanitized reason, for example `favstats: node 50001: response too large (>4 MB), skipping for 10m0s` or `favstats: request failed (timeout), pausing 15s`; the request log shows `status=too-large`, `decode-error` and `200-nodata`. Read timeouts while receiving the body are reported as `timeout`.
- Favorites scan: an open dashboard now clears a favorite's Rx%, LCnt and colour when the node goes offline, returns no data or 404. The `favstats` SSE event carries the cleared nodes in a new `removed` list instead of leaving the old values until the next full snapshot.

## [1.1.11] - 2026-11-16

### Changed
- Favorites scan: EchoLink favorites (7-digit numbers 3000000..3999999) are no longer queried and show no activity values. Only node numbers 2000..999999 are scanned.
- Favorites scan stays under the stats service's real rate limit: the default is now 10 requests per minute (`[favstats] rate`, 1..25, now a ceiling), burst 2, at least 3 s between requests. After an HTTP 429 the rate in use is halved (not below 3 per minute) in addition to the backoff, and it rises by 1 per minute after every 20 successful requests. `Retry-After` is honoured.
- Favorites scan polls nodes never queried first, then the one queried longest ago, so a backoff no longer starves the end of the list.
- The Favorites "Scan" checkbox is now a prominent "Start scan" / "Scanning" toggle button (viewers see a "Scan on" / "Scan off" badge). During a backoff the header shows "rate limited, retrying in Ns".
- `[favstats] log = true` (default false) writes one journal line per stats request (node, status, duration, rate, tokens, Retry-After) and each rate change, to diagnose rate limiting. No response data is logged.

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Favorites scan: a 429 answer no longer counts as a sample; the last values and the "updated" time stay.

## [1.1.10] - 2026-11-16

### Added
- Favorites activity scan (as in AllScan). A "Scan" checkbox in the Favorites header (operators and admins; off by default) shows two new sortable columns, Rx% (lifetime TX time as a percentage of uptime) and LCnt (number of links), and colours each row's # cell: red while the node is keyed, green when it is Active, brighter green with WebTransceiver. The Node cell is highlighted when the favorite is linked to one of your nodes. The header shows when the values were last updated. One server-side poller queries `stats.allstarlink.org` for the favorite nodes (not EchoLink or your own), one at a time, at most 20 requests per minute and only while someone is logged in with the panel open, with backoff after rate limiting. New `GET /api/favorites/stats`, `PUT /api/favorites/scan` and SSE event `favstats` (see `docs/API.md`); never on the public page. `[favstats]` in `aslcp.conf` overrides the URL and the request rate (`docs/FAVSCAN.md`).

## [1.1.9] - 2026-11-16

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- The dashboard picks up nodes added to or removed from `rpt.conf` without restarting aslcp. The local node list is re-read when the Asterisk connection comes back, shortly after a reload or restart the panel triggers (config apply or restore with reload, Asterisk restart), and every 5 minutes. A removed node's row disappears from the dashboard and the public view; an added node appears and is polled at once.
- A stale node shows the actual reason (for example the node poll error) instead of always "AMI disconnected".

## [1.1.8] - 2026-11-16

### Removed
- Audio Tuning: the TX test tone. `susb tune flash` gave inconsistent tones on real hardware (see `docs/SIMPLEUSB_TUNE.md`). Removed the "Send test tone" button and its confirmation dialog, the `POST /api/radio/{dev}/tone` endpoint and its `radio.tone` audit entry, the tone rate limits and error codes (`node_keyed`, `channel_not_ready`, `tone_busy`, `rate_limited`), the channel-up pre-flight check, and the demo and mock tone code. The page now points to `simpleusb-tune-menu` in a terminal for a steady test tone.
- The separate AMI connection for radio tuning; it existed only so the blocking tone would not stall the shared connection. Tuning commands now use the shared one.

## [1.1.7] - 2026-11-16

### Fixed
- First-run restore: the "Log in" button after a restore opens the login form instead of the public page.
- Audio Tuning: the TX test tone was always refused with "the channel SimpleUSB/<dev> is not up". The pre-flight check expected a `-<suffix>` on the channel name, but chan_simpleusb names its channel `SimpleUSB/<dev>` with none.

## [1.1.6] - 2026-11-16

SimpleUSB audio tuning (admin only). See `docs/SIMPLEUSB_TUNE.md`.

- New "Audio Tuning" tab (`#/tune`), shown to admins when a node has a SimpleUSB device: live receive meter, RX/TX level sliders, toggles, carrier/CTCSS source, PTT mode, RX-on/TX-off delays in milliseconds, echo mode (auto-off, banner), TX test tone with confirmation, Save and Revert.
- Link guard: shows linked nodes, warns before echo and tone, names the nodes in the tone confirmation, one-click "Disconnect all links".
- Save records `simpleusb.conf` in the config history (new helper op `config-record`). Unsaved changes are lost when the module reloads or Asterisk restarts.
- New admin-only API `/api/radio` (documented in `docs/API.md`), demo mode and mock support.
- Needs hardware testing: tone and echo over AMI, save formatting. See the unverified list in `docs/SIMPLEUSB_TUNE.md`.

## [1.1.5] - 2026-11-16

Fixes restore on a real node.

- Fix: restore on a real node rejected every backup as "not a gzip-compressed backup file" (the request's trailing newline was read as part of the archive).
- Restore errors now show the server's reason.

## [1.1.4] - 2026-11-16

Debian 13 "Trixie" support, alongside Debian 12 "Bookworm" (ASL3 is supported on both).

- The installer and the documentation now cover Debian 12 (bookworm) and Debian 13 (trixie) and Raspberry Pi OS; the installer only warns on other releases.
- Backups record the Debian release of the node (`osId`, `osCodename`, `osName` in the manifest; format unchanged, older backups still restore). The restore preview shows it next to the ASL3 and Asterisk versions.
- Restoring a backup made on another Debian release (for example bookworm onto trixie) shows a warning: your files replace the new defaults, the new defaults are kept as `.dpkg-dist`, and if Asterisk or app_rpt does not start you should compare them.
- The health check after a restore now checks that the `app_rpt` module is loaded, and a node only counts as answering when app_rpt really responds.

## [1.1.3] - 2026-11-15

Backup and full restore. Download one file from a working node, re-image the SD card, install ASLCP, upload the file, and get the same node back. Try a restore on a spare SD card first.

- Settings > Backup (admins only): tick the optional parts and Download. The file is `aslcp-backup-<hostname>-<date>.tar.gz`.
- Always included: everything in `/etc/asterisk` (also `allmon.ini.php`, `custom/`, ...) with owners and permissions, AstDB, the ASLCP database (users, favorites, settings, public page, audit log) and `/etc/aslcp/aslcp.conf`, the update channel, and the SA818 radio settings in `/etc/sa818.conf` when present. Optional: config history, Allmon3 configuration and custom sounds.
- **The backup is not encrypted.** It contains every secret of the node (node, IAX, AMI and EchoLink passwords, ASLCP password hashes). Store it like a password.
- Restore from Settings > Backup (password re-entry) or on the first-run page ("Restore from a backup", with the one-time setup code, instead of creating an administrator). The file is checked completely first, then a preview shows what will happen to every file, the users that will exist, the SA818 settings, packages that are missing here (with the `apt install` command) and what to set up separately (network, hostname, timezone, SSH keys, other software). Nothing changes until you confirm.
- Restoring onto a newer ASL3 is safe: your changed and local files win, files that are still stock keep the new default, and when ASL3 changed the default of a file you edited, the new one is kept beside yours as `<file>.dpkg-dist` for review. Files only the new image has are kept, nothing is deleted unless you tick it.
- Before anything is written the current `/etc/asterisk` is saved to the config history, so a restore can be undone from History. If a step fails the changes are undone.
- Asterisk is stopped and started, ASLCP restarts itself and the page shows the health check and the "still to do" list. Everyone has to log in again with their old account.
- SA818 modules keep their frequency and tone settings in their own memory, and these survive a re-image. The restore result offers `sudo sa818-menu --apply` to program a different module from the restored file; it is not run automatically.
- A backup made by a newer ASLCP is refused ("update ASLCP first"); a different ASL3 or architecture only gives a warning.

## [1.1.2] - 2026-11-15

- Config editor: lines starting with `;---` (separator lines, as in `usbradio.conf`) no longer turn the rest of the file grey. Like Asterisk, only `;--` followed by something other than a dash opens a block comment.

## [1.1.1] - 2026-11-15

First 2.0 test version (Beta channel). Configuration editing, part 1. Start with harmless edits (comments) and keep an eye on the result page.

- New Config page (admins only): the Asterisk configuration files in `/etc/asterisk`, grouped (Core, Radio, Linking, Platform, Custom), with a code editor.
- Saving shows a review first: syntax check, the exact changes, and which reload will run. Apply takes a snapshot, writes the files keeping their owner and permissions, reloads only what changed (for example `module reload app_rpt.so` for `rpt.conf`, `iax2 reload` for `iax.conf`), and checks that Asterisk and every node still answer. If the check fails, one click rolls back.
- A file changed on disk while you were editing (by `nano`, `asl3-menu`, ...) is never overwritten: the page says so and lets you load the new version while keeping your text to copy from.
- History: every change is kept in a local git history (`/var/lib/aslcp-config-history`, root only, never sent anywhere), with diffs (passwords and secrets hidden unless you reveal them) and Restore.
- Restart Asterisk button on the Config page.
- Config changes, restores, secret reveals and Asterisk restarts are recorded in an audit log.
- The package now depends on `git`.

## [1.0.0] - 2026-11-15

First stable release. 1.0 is the stable line of everything since 0.1: live dashboard, connect and monitor, favorites, node list and lookup, public status page, users and roles, system and diagnostics, and one-click updates. It continues to receive fixes (1.0.x) while the next major version is tested as 2.0 betas.

- Update channels: Settings > Updates has a new "Update channel" switch, Release or Beta. Release (the default) only gets finished versions. Beta gets 2.0 test versions early and may be less stable. The channel is the suite in `/etc/apt/sources.list.d/aslcp.list`, so `apt upgrade` on the command line follows the same choice. Switching back from Beta to Release does not downgrade: the node stays on the installed beta until a release newer than it comes out. The install script accepts `ASLCP_CHANNEL=beta` and keeps the channel of an existing install when run again.

## [0.2.5] - 2026-11-15

- Readable text on keyed and receiving rows: node descriptions and locations on red (transmitting/keyed) and green (local receive) rows are now near-white in both themes, on the dashboard, the public page and favorites. They were grey and hard to read, especially in light mode. The light-theme receive green is slightly darker.
- Settings page sections are in a fixed order: Updates and About, then Connecting and Node list, then Public page, then Change password and Users (one column on phones).
- Updates no longer log "Ignoring unknown escape sequences" for the upgrade job in the system journal.

## [0.2.4] - 2026-11-15

- The Updates page no longer fills the system journal with "Failed to open /run/systemd/transient/aslcp-upgrade.service: No such file or directory" messages every few seconds. The helper now checks whether the upgrade job exists before asking systemd about it.
- This is the first update that is installed by 0.2.3's non-blocking updater, so it should finish without the "systemd-run failed" or "unexpected end of JSON input" message and without the pause near the end.

## [0.2.3] - 2026-11-16

- Fixed the real cause of "helper: bad response: unexpected end of JSON input" when updating: the helper waited for the whole upgrade before answering the Update request, and the package script, which restarts the helper, then waited for the helper. The helper now starts the upgrade and answers at once, which also removes a pause of about 20 seconds near the end of every upgrade. (This takes effect from the update after 0.2.3, because the updater that runs is the one already installed.)
- Instant update checks: "Check now" and the background check (1 minute after start, then every 6 hours) no longer run apt. They fetch the small `latest.json` from the update URL and compare versions themselves, so the check takes a moment instead of many seconds on a Raspberry Pi 3. Manual checks bypass the CDN cache.
- Cheaper status polling: the update page and its polling no longer load apt's package cache; they ask only for the installed version and the state of the upgrade job (the journal is read only while an upgrade runs). The page shows the last check time ("just now", "5 min ago") and keeps the check working, with only the Update button disabled, when the helper is not running.
- Faster upgrade: one step (refresh the ASLCP apt source and install), no changelog reader, no recommended packages, and the package script skips the firewalld reload when the rule is already open and restarts ASLCP last so the web interface returns sooner. If the package index lags behind the release information the page says "Already up to date" instead of showing an empty success.

## [0.2.2] - 2026-11-16

- The helper finishes requests in flight when it is restarted during an update; the update page no longer shows "unexpected end of JSON".
- The update page rides out the helper restart.

## [0.2.1] - 2026-11-16

- Settings > Updates no longer stays on "An update is running…" after an update. The page reloads into the new version while the upgrade is still finishing; it now keeps checking until the job ends instead of showing the first answer forever.

## [0.2.0] - 2026-11-16

- Public status page (optional, off by default; Settings > Public page): a modern, read-only replacement for public Allmon3/Supermon pages. When enabled, visitors who are not logged in see a live status view at the panel's address (keying, connected links, optional linked-network list and recent activity) with a "Log in" button; logged-in users still get the dashboard and can preview it ("Public view"). It never shows IP addresses, system information, favorites or users; you choose the nodes and whether callsigns, network list and activity are visible.
- It runs on the same server and port (public data at `GET /api/public/status` and `/api/public/events`, 404 while disabled; the rest of `/api` still requires a login). Visitors are rate-limited (429), with per-address and global caps on live connections; a "trust proxy headers" setting makes the limits use the real address behind a tunnel or reverse proxy.
- Optional embedding: `/embed` serves a chrome-less view that can be put in an iframe when "allow embedding" is on. Every other page, the login page included, keeps `frame-ancestors 'none'`.
- Recent activity: the server now records transmissions (link keyups and local receiver keyups, 7 days, at most 10,000) in a new `activity` table; `GET /api/activity` lists them for logged-in users.

## [0.1.2] - 2026-11-15

- New public home at https://github.com/artw1/aslcp (the apt repository is now https://artw1.github.io/aslcp). Existing installs are migrated automatically on upgrade (apt source and an unmodified `[updates] url` line); the packaged `aslcp.conf` no longer sets URLs, so the built-in defaults apply.
- Settings > About: versions, platform, and links to report a bug (prefilled issue form), request a feature and release notes; administrators can download a diagnostics file with private data (IP addresses, secrets, emails, other stations' callsigns) removed. A small "Report a bug" link was added to the footer.
- The package now ships a copyright file with the licence.

## [0.1.1] - 2026-11-15

- The install script now asks for the admin username and password right in the terminal (no echo, confirmed twice). Without a terminal (for example a non-interactive install) it prints a one-time setup link instead.
- New `aslcp admin` commands on the server: `create`, `reset-password USER` (also signs that user out everywhere) and `list`, for creating the first account or recovering a lost password. They work on the database directly and are safe while the service runs.
- The one-time setup link (`/#/setup?code=...`) is logged at startup, so `journalctl -u aslcp` shows a clickable link; the setup page fills in the code from the link.
- The service no longer blocks resource syscalls the Go runtime uses at startup (systemd SystemCallFilter fix).
- Disconnect-before-connect: commands to a node are now paced with a minimum gap, so a connect that replaces a link no longer races the disconnect before it.

## [0.1.0] - 2026-11-15

First release (milestone 1).

- Live dashboard for local AllStarLink nodes over AMI: links, keyed state, node statistics, system strip (CPU temperature, load, memory, Asterisk status).
- Connect, disconnect and disconnect-all, with "disconnect before connect" to avoid cross-linking networks; monitor and local-monitor modes; permanent links.
- Favorites with groups and ordering, import from AllScan, node lookup by number, callsign, description or location, node list refresh.
- Users with admin, operator and viewer roles, first-run setup code, rate-limited login, CSRF protection.
- Debian packages (arm64, armhf, amd64) with a hardened systemd service, a socket-activated root helper, firewalld service definition and a signed apt repository.
- In-app updates: Settings > Updates shows the installed and available version with release notes and installs updates on request.
