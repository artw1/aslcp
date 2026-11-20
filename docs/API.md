# ASLCP HTTP API (milestone 1)

Contract between the Go server (`cmd/aslcp`) and the Svelte UI (`web/`). JSON everywhere, UTF-8. All paths are under `/api`. The server also serves the embedded UI at `/` (SPA: unknown non-`/api` paths return `index.html`).

## Conventions

- **Auth:** session cookie `aslcp_session` (HttpOnly, SameSite=Lax, Secure when served over HTTPS). Unauthenticated requests to protected endpoints get `401`.
- **CSRF:** every non-GET request must send header `X-ASLCP: 1`; the server also rejects a non-GET whose `Origin` header doesn't match the host. Missing header returns `403`.
- **Errors:** non-2xx responses have body `{"error": "human readable message", "code": "machine_code"}`.
- **Roles:** `admin` (everything), `operator` (connect/disconnect, favorites), `viewer` (read only). Forbidden returns `403`.
- **Live updates:** Server-Sent Events (not WebSocket) at `GET /api/events`. Commands go over REST.
- Durations are integer **seconds**; timestamps are RFC 3339 strings; "never" is `null`.

## Setup and session

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/setup` | | `{"needed": bool, "hotspot": bool}`. `needed` is `true` when no users exist. `hotspot` is `true` when the request arrived through the setup hotspot (local address `10.42.0.1`), so the page can warn that the network is open. Public. |
| POST | `/api/setup` | `{"code", "username", "password"}` | `204`, and logs in. `code` is the one-time setup code the server writes to `<datadir>/setup-code`; at startup it also logs a ready-made link `http://<addr>:<port>/#/setup?code=<code>` (and prints the code in `--dev`). Only allowed while no users exist: afterwards `409 {"code":"setup_done"}`; a wrong code is `403 {"code":"bad_setup_code"}`. |
| POST | `/api/login` | `{"username", "password"}` | `200` `User`. `401` `{"code":"bad_credentials"}`. Rate limited (`429`). |
| POST | `/api/logout` | | `204` |
| GET | `/api/session` | | `200` `User` or `401` |
| POST | `/api/password` | `{"current", "new"}` | `204` |

`aslcp admin setup-code` prints the current setup code (exit 1 once an account exists; it creates the file if missing, and the server always checks against the file). Accounts can also be created out of band with `aslcp admin create` (see `aslcp admin --help`), which removes `<datadir>/setup-code`, after which `GET /api/setup` reports `needed: false`.

`User` = `{"id": int, "username": str, "role": "admin"|"operator"|"viewer"}`

### Users (admin)

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/users` | | `[User]` |
| POST | `/api/users` | `{"username", "password", "role"}` | `201` `User` |
| PUT | `/api/users/{id}` | `{"role"?, "password"?}` | `200` `User` |
| DELETE | `/api/users/{id}` | | `204` (cannot delete the last admin or yourself) |

## Nodes and links

`GET /api/nodes` returns `[NodeStatus]` for all local nodes (same objects as the SSE `node` event).

```json
NodeStatus = {
  "node": "2001",
  "info": NodeInfo | null,          // lookup for the local node itself
  "stale": false,                   // true while AMI is disconnected or polls fail
  "rxKeyed": false,                 // signal on the node's receiver (COS)
  "txKeyed": false,                 // node transmitter keyed
  "extTxKeyed": false,
  "numLinks": 159,                  // whole-network count as reported (may include duplicates)
  "links": [Link],                  // directly connected links
  "network": [{"node": "1999", "mode": "T", "kind": "allstar", "info": NodeInfo|null}],
  "flags": {"txDisabled": false, "linkFunsDisabled": false, "parrot": false, "...": "..."},
  "updated": "2026-11-15T12:00:00Z"
}
Link = {
  "node": "50241",
  "kind": "allstar" | "echolink" | "phone" | "client" | "unknown",
  "info": NodeInfo | null,
  "ip": "192.0.2.58" | "",          // only sent to admin/operator; "" for viewer
  "direction": "IN" | "OUT",
  "connected": 29,                  // seconds
  "state": "ESTABLISHED" | "CONNECTING",
  "mode": "T" | "R" | "L" | "C",    // transceive, monitor (RX only), local monitor, connecting
  "keyed": false,
  "sinceKey": 12 | null,            // seconds since this link last keyed
  "sinceUnkey": 3 | null
}
NodeInfo = {"node": "50241", "callsign": "N0CALL", "description": "Hub name", "location": "City, ST"}
```

### Link control (operator)

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/api/nodes/{node}/connect` | `{"target": "2000", "mode": "transceive"\|"monitor"\|"localmonitor", "permanent": bool, "exclusive"?: bool}` | `202` (accepted by app_rpt; the link appears via SSE) |
| POST | `/api/nodes/{node}/disconnect` | `{"target": "2000", "permanent": bool}` | `202` |
| POST | `/api/nodes/{node}/disconnect-all` | | `202` |

**`exclusive`** ("disconnect before connect", prevents cross-linking networks) is optional and defaults to `false` when absent. When `true` and `mode` is `transceive` or `monitor`, the server first sends a normal (non-permanent) disconnect (`rpt cmd <node> ilink 1 <link>`) for every current direct link of that local node except `target`, in the order of the latest monitored status (not a fresh poll), and then sends the connect. Permanent links survive because app_rpt ignores a non-permanent disconnect of a permanent link. If `target` is already directly linked it is not disconnected, but the connect is still sent (so a mode change works). Links whose name is not a valid disconnect target are skipped. If any disconnect command fails, that error is returned and the connect is not sent. `localmonitor` ignores `exclusive` (local monitor audio is not passed to other links, so it cannot cross-link).

`400 {"code":"bad_target"}` for invalid input, `404` unknown local node, `503 {"code":"ami_down"}` when AMI is disconnected.

**Manual commands and the schedules.** After a successful connect, disconnect or disconnect-all (any user), the server applies the schedules setting `manualAction` (since 1.1.24): by default it cancels the schedule occurrences running now and switches all schedules off (reason, for example `manual connect 2000 on 1997 by op`); it can instead pause only until midnight, or cancel only the running windows on that node. See Schedules below. The scheduler's own commands do not.

### Node stats (all roles)

`GET /api/nodes/{node}/stats` runs `Action: RptStatus`, `Command: NodeStat` for a local node and returns the reply's `Key: Value` lines in wire order (`Response`/`ActionID` and any `Node`/`Message` headers are dropped):

```json
{"node": "2001", "stats": [{"key": "IsRemoteBase", "value": "NO"}, {"key": "KeyupsToday", "value": "2"}, {"key": "TxTimeToday", "value": "00:00:33:172"}]}
```

`404 {"code":"unknown_node"}` for a node that is not local, `503 {"code":"ami_down"}` when AMI is disconnected.

## Settings (read: all roles; write: admin)

`Settings = {"favoritesScan": bool}` (`favoritesScan` is read-only here; it is changed with `PUT /api/favorites/scan`). The "Disconnect before Connect" (default on) and "Disconnect before Monitor" (default off) checkboxes are not server settings: every page load starts from those fixed defaults; a change in the connect bar lasts only until the page is reloaded (nothing is saved in the browser). The connect endpoint itself is unaffected; the UI sends `exclusive` accordingly (Connect uses the first, Monitor the second, Local Mon never). The former `exclusiveDefault` / `monitorExclusiveDefault` fields and `PUT /api/settings` were removed in 1.1.17; stored `connect.exclusive_default` / `monitor.exclusive_default` values are ignored.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/settings` | | `200` `Settings` |

## Favorites (read: all roles; write: operator)

`Favorite = {"id": int, "node": "2000", "label": "My hub", "group": "Hubs" | "", "position": int, "info": NodeInfo|null}`

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/favorites` | | `[Favorite]` ordered by group then position |
| POST | `/api/favorites` | `{"node", "label"?, "group"?}` | `201` `Favorite` (label defaults to the node-list callsign/description) |
| PUT | `/api/favorites/{id}` | `{"node"?, "label"?, "group"?}` | `200` `Favorite` |
| DELETE | `/api/favorites/{id}` | | `204` |
| GET | `/api/favorites/stats` | | `200` `FavStats` snapshot (login required, all roles; never part of the public page) |
| PUT | `/api/favorites/scan` | `{"enabled": bool}` | `200` `FavStats` (operator or admin). `400` without `enabled`. Stored as setting `favstats.enabled`, default off |
| POST | `/api/favorites/reorder` | `{"ids": [int, ...]}` | `204` (positions follow array order) |
| GET | `/api/favorites/import/allscan` | | `{"found": bool, "path": str, "items": [{"node","label"}]}` preview from an existing AllScan install |
| POST | `/api/favorites/import/allscan` | | `{"imported": int, "skipped": int}` (skips nodes already in favorites) |

## Schedules (read: all roles; write: operator)

Scheduled connects and disconnects (`docs/SCHEDULES.md`). All endpoints answer `503 {"code":"schedules_unavailable"}` when the feature is not wired.

`Schedule = {"id": int, "name": str, "enabled": bool, "node": "1997", "target": "41234", "action": "connect"|"disconnect", "mode": "transceive"|"monitor"|"localmonitor"|"", "permanent": bool, "exclusive": bool, "days": [int], "date": "YYYY-MM-DD"|"", "start": "HH:MM", "end": "HH:MM"|"", "lastRun": RFC3339|null, "lastResult": str}`: `days` are 0 = Sunday to 6 = Saturday (weekly), or `date` is set (once); exactly one of the two. Times are node local time. `end` only for `connect`; `end` before `start` crosses midnight. `lastResult` is `""`, `ok`, `skipped: <reason>` or `error: <reason>`.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/schedules` | | `200` `Snapshot` |
| POST | `/api/schedules` | `Schedule` fields without `id`, `lastRun`, `lastResult` (`enabled` defaults to `true`) | `201` `Schedule`. `400 bad_request` with a message (bad target, target equals node, both or neither of `days` / `date`, bad `date`, `HH:MM` or day, `end` on a disconnect, `end` equals `start`, unknown `mode` or `action`, name over 60 characters); `400 unknown_node` when `node` is not a current local node; `409 schedule_conflict` (see below) |
| PUT | `/api/schedules/{id}` | same as POST (full replace; also how a schedule is enabled or disabled) | `200` `Schedule`; `404 not_found`; `409 schedule_conflict` (see below) |
| DELETE | `/api/schedules/{id}` | | `204`; `404 not_found` |
| PUT | `/api/schedules/enabled` | `{"enabled": bool}` | `200` `Snapshot` (the global switch; switching on clears the pause reason). `400` without `enabled` |
| PUT | `/api/schedules/settings` | `{"manualAction": "pause"\|"pause_today"\|"cancel"}` | `200` `Snapshot`. `400 bad_request` for a missing or unknown value. Operator and admin |

**Overlap check** (1.1.24): POST and PUT answer `409 {"code":"schedule_conflict","error":"overlaps \"Tuesday net\" (Tue 19:00–20:30) on node 2006"}` when the schedule being saved is a connect, enabled, on the same node as another enabled connect schedule, at least one of the two has `exclusive: true` and their time spans overlap on some date (midnight crossing and the week wrap included; a connect without `end` is an instant that conflicts only when inside the other's window). The message names the clashing schedules (`#id` when unnamed). Disabled schedules and disconnects never conflict. Details in `docs/SCHEDULES.md`.

**Manual action** (1.1.24): what a manual connect, disconnect or disconnect-all (`/api/nodes/{node}/...`) does to the schedules is set by `manualAction`: `pause` (default: cancel running windows and pause all schedules until switched on), `pause_today` (same, resumes at the next local midnight; `resumeAt` shows when) or `cancel` (cancel only the running windows on that node, schedules stay on; `lastCancel` carries the notice).

`mode` is normalised to `""` and `exclusive` to `false` for a disconnect, and `exclusive` to `false` for `localmonitor`. `permanent` on a disconnect means a permanent disconnect (`ilink 11`); on a window it also applies to the closing disconnect.

```
Snapshot = {
  "enabled": bool,            // global switch
  "pausedReason": str,        // "" when paused from the switch, else e.g. "manual connect 41234 on 2006 by op"
  "pausedAt": RFC3339 | null,
  "resumeAt": RFC3339 | null, // set while paused "for today" (pause_today): schedules switch on again then
  "manualAction": "pause" | "pause_today" | "cancel",
  "lastCancel": {"reason": str, "at": RFC3339, "until": RFC3339, "ids": [int], "names": [str]} | null,  // cancel mode: windows cancelled by a manual action, while they would still be running
  "timezone": "Europe/Berlin",  // IANA name when found, else the abbreviation
  "now": RFC3339,             // node time, with offset
  "schedules": [Schedule],    // sorted by node, start, id
  "active": [{"id": int, "start": RFC3339, "end": RFC3339}],  // windows running now (none while paused)
  "upcoming": [{"at": RFC3339, "id": int, "name": str, "node": str, "target": str, "action": "connect"|"disconnect", "mode": str}]
}
```

`upcoming` lists the next 10 events within 8 days (a window gives a `connect` and a `disconnect` event; `mode` is `""` for a disconnect), also while paused; disabled schedules and cancelled occurrences are left out. Timestamps carry the node's UTC offset. The persisted state lives in the `schedules` table and the settings `schedules.enabled`, `schedules.paused_reason`, `schedules.paused_at`, `schedules.state`, `schedules.manual_action`, `schedules.resume_at` and `schedules.last_cancel`.

## Node lookup (all roles)

| Method | Path | Response |
|---|---|---|
| GET | `/api/lookup/{node}` | `NodeInfo` or `404` |
| GET | `/api/lookup?q=text&limit=50` | `[NodeInfo]` matching node number prefix, callsign, description or location (case-insensitive) |
| GET | `/api/nodelist` | `{"count": int, "updated": RFC3339 \| null, "source": str, "error": str \| null}` |
| POST | `/api/nodelist/refresh` | `202` (admin) |

## System (all roles)

`GET /api/system` returns `System`; also pushed via SSE every 10 s.

```json
System = {
  "hostname": "node2001",
  "cpuTemp": 48.2 | null,          // °C
  "load": [0.12, 0.10, 0.05],
  "memTotal": 1024000000, "memAvailable": 512000000,   // bytes
  "uptime": 120660,                // host, seconds
  "asterisk": {"connected": true, "version": "22.10.1+asl3-3.10.8-1.deb12", "uptime": 120660, "error": null},
  "version": "0.1.0",              // ASLCP version
  "updateAvailable": false,        // a newer package is known (cached; set by the background/manual check)
  "updateVersion": ""              // that version, "" when none
}
```

### Restart Asterisk (admin)

`POST /api/system/restart-asterisk` sends the AMI command `core restart gracefully` (Asterisk restarts once active calls end; all links drop and AMI reconnects afterwards). Response `202`. `403` for operator/viewer, `503 {"code":"ami_down"}` when AMI is disconnected. When AMI is down and configuration editing is available, the restart goes through the root helper (`systemctl restart asterisk`) instead.

`POST /api/system/asterisk/restart` does the same and answers `200 {"ok":true}`. Both are audited (`asterisk.restart`).

## Configuration editing (admin)

All routes are admin only (`403` otherwise), non-GET requests need the CSRF header, and `503 {"code":"config_unavailable"}` is returned when the helper is not reachable. Request bodies are limited to 2 MiB. Paths are relative to `/etc/asterisk`, end in `.conf`, are at most 4 levels deep, and new files are only allowed under an existing `custom/` directory. `base` is the `sha256` of the version being edited (`""` for a new file).

| Route | Body | Response |
|---|---|---|
| `GET /api/config/files` | | `[{path,size,mtime,sha256,known,group}]` (`mtime` RFC3339) |
| `GET /api/config/file?path=` | | `{path,content,sha256}` (audited as `config.read`, path only) |
| `POST /api/config/preview` | `{files:[{path,content,base}]}` | `{files:[{path,ok,error?:{line,message},diff,changed,conflict?}],reload:[{command,reason}],restart}` |
| `POST /api/config/apply` | `{files,message,reload}` | `{commit,before,files,reload:[{command,ok,output}]\|null,health:{ok,checks:[{name,ok,detail}]}\|null,restart}` |
| `GET /api/config/history?limit=&offset=` | | `[{commit,date,author,message,files}]`, newest first (`limit` 1-200, default 50) |
| `GET /api/config/history/{commit}?reveal=1` | | `{commit,date,author,message,files:[{path,status,diff}]}`; values of `secret`, `password`, `passwd`, `pass`, `md5secret`, `dbsecret` and the credentials of `register =>` are masked unless `reveal=1` (audited as `config.reveal`) |
| `POST /api/config/history/{commit}/restore` | `{reload}` | like apply, plus `skipped:[path]` |
| `POST /api/system/asterisk/restart` | | `{ok:true}` |

`reload` and `health` are `null` when `reload` was false (or nothing changed). Errors: `409 {"code":"conflict","files":[...]}` (a file changed on disk since it was loaded), `422 {"code":"syntax_error","issues":[{path,line,message}]}`, `400 bad_request`, `404 not_found`, `502 helper_error`. Audit entries (`config.apply`, `config.restore`, `config.read`, `config.reveal`, `asterisk.restart`) contain paths and commit ids only, never file contents, secret values or commit messages.

## Radio tuning (admin)

SimpleUSB audio tuning (see [SIMPLEUSB_TUNE.md](SIMPLEUSB_TUNE.md)). Every route is admin only (`403` otherwise); writes need the CSRF header. The commands run through the Asterisk CLI over the AMI connection. `503 {"code":"radio_unavailable"}` is returned when the service is not wired (it never is in a normal install). `{dev}` is the SimpleUSB device name from `simpleusb.conf` (letters, digits, `_` and `-`, at most 32); an unknown one is `404 unknown_device`. Node numbers below are placeholders.

| Route | Body | Response |
|---|---|---|
| `GET /api/radio` | | `{available,reason?,devices:[{name,node?,usbPath?,card,links:[node]}]}`. `node` comes from `rxchannel = SimpleUSB/<dev>` in rpt.conf. `available` is false (and `devices` empty) when no device exists or Asterisk cannot be asked; this never fails |
| `GET /api/radio/{dev}` | | `{device,node?,params,saved,dirty:[field],identity:{device,usbString?,serial?,card?},echoState:{on,expiresAt?}}`. `params` is the running state read with `menu-support 0+4` (exactly 16 fields, otherwise `501 unsupported_version`); `saved` is the baseline (the values of `simpleusb.conf` laid over the running ones when the device was first opened or last saved) and `dirty` the fields that differ from it |
| `PUT /api/radio/{dev}/params` | any of `{rxVoice,txA,txB,rxBoost,preEmphasis,deEmphasis,plFilter,pttInvert,carrierFrom,ctcssFrom,rxOnDelayMs,txOffDelayMs}` | `{applied:[{field,reply}],errors:[{field,message,reply?}],params,dirty}`. Levels are integers 0-999; `carrierFrom`/`ctcssFrom` are `no`, `usb`, `usbinvert`, `pp` or `ppinvert` (the driver's numeric codes are never accepted); delays are milliseconds 0-60000 and are converted to 20 ms frames. `pttInvert` true means the PTT line is "Open". Validation of the whole body happens before any command is sent (`400 invalid`). Each setting is verified against the driver's reply; a refused one is reported in `errors` |
| `GET /api/radio/{dev}/meter` | | `{pk,avg,min,max,clip,at,stale?}`: the last second of receive audio (`menu-support Y`), dBFS. At most one read per 700 ms; faster requests, and requests while another operation holds the interface, get the last reading (`stale` true). Polling it also keeps echo mode on |
| `POST /api/radio/{dev}/echo` | `{on}` | `{on,expiresAt?}`. Echo mode retransmits what the receiver hears. It switches itself off after 10 minutes, 30 seconds after the last meter or read request for the device, before a save, and when ASLCP stops. Audited with the linked nodes |
| `POST /api/radio/{dev}/save` | | runs `susb tune save` (echo mode is switched off first), re-reads the device, and records `simpleusb.conf` in the config history through the helper (`config-record`): `{saved,params,changed:[field],commit?,warning?}`. `warning` explains a partial success (the history record failed, or the file does not show a running value) |
| `POST /api/radio/{dev}/revert` | | sets every tuning value back to the baseline (not `susb tune load`, which reads the interface EEPROM): like `PUT params` |

Other errors: `502 radio_unexpected_output` (a reply that is not the expected one, including a device other than the one addressed), `502 device_not_found` (the interface is not plugged in), `502 radio_command_failed`, `503 no_chan_simpleusb` (the module is not loaded), `503 radio_unavailable` (the interface is busy or the radio service is not wired), `503 ami_down`, `504 radio_unavailable` (no answer in time). Writes are audited as `radio.params`, `radio.echo`, `radio.save` and `radio.revert` with the device, the field names and the linked nodes, never reply text. Unsaved changes are lost when `chan_simpleusb` is reloaded or Asterisk restarts.

## Backup and restore (admin)

See [BACKUP.md](BACKUP.md) for what a backup holds and how a restore works.

| Endpoint | Auth | Body | Result |
|---|---|---|---|
| `GET /api/backup/info` | admin | | `{parts:[{id:"history"\|"allmon3"\|"sounds"\|"dvswitch",present,size}],sa818}` |
| `POST /api/backup` | admin, CSRF | `{history,allmon3,sounds,dvswitch}` (booleans; `dvswitch` includes the DVSwitch ini files and `dvsm.macro` with their passwords, unredacted) | the archive, `application/gzip`, `Content-Disposition: attachment; filename="aslcp-backup-<host>-<YYYYMMDD-HHMM>.tar.gz"` |
| `POST /api/restore/upload` | admin session + field `password`, or (only while setup is needed, i.e. no user exists) field `code` = the setup code | multipart, field `file` (at most 256 MiB) | `{id, preview}` |
| `POST /api/restore/{id}/apply` | admin session, or `code` in the body (same rule) | `{deleteMissing:[path], code?}` | `{ok, steps:[{name,ok,detail}], restarting, error?}` |
| `DELETE /api/restore/{id}` | as apply (`?code=` for the setup-code path) | | `204` |
| `GET /api/restore/result` | none while the result is under 30 minutes old, otherwise admin | | `{at, backupCreated, steps, health:{ok,pending,checks:[{name,ok,detail}]}, checklist:[{item,value}]}` or `404` |

`preview` is `{backup:{hostname,created,createdBy,aslcpVersion,asl3Version,asteriskVersion,arch,osId?,osCodename?,osName?,nodes,channel}, node:{asl3Version,asteriskVersion,arch,osId?,osCodename?,osName?}, warnings:[{code,message}], files:[{path,action,note?}], users:[{username,role}], favorites, missingPackages, installCommand, checklist:[{item,value}], radio:null|{band,freqRx,freqTx,tone,ctcssRx,ctcssTx,dcsRx,dcsTx,port,portPresent}, parts:{history,allmon3,sounds,sa818,dvswitch}}`. Arrays (and `files`) are never `null`. `files[].action` is one of `restore`, `restore_dist`, `keep_stock`, `keep_new`, `delete_optional`, `skip`. `node` describes this node for comparison; the `os*` fields come from the manifest (or, for `osCodename`, from the `.debNN` version suffix) and are omitted when unknown. Warning codes: `asl3_version`, `asterisk_version`, `os_release` (backup and node run different Debian releases), `arch`, `sa818_port`, `dist_files`, `skipped_files`, `dvswitch_not_installed` (the backup has the DVSwitch part but `/opt/Analog_Bridge` does not exist here: the settings are kept and applied when DVSwitch is installed; DVSwitch packages are not in `missingPackages` or `installCommand`). `deleteMissing` may only list `delete_optional` paths of the preview.

Errors: `403 bad_password` / `bad_setup_code`, `400 bad_backup`, `409 backup_too_new` (the backup was made by a newer ASLCP or has a newer format/schema), `413 too_large`, `404 not_found` (unknown or expired upload id; uploads expire after 1 hour, at most 3 are kept), `409 busy`, `429 rate_limited`, `401`/`403` for sessions, `503 helper_unavailable` / `backup_unavailable`, `502 backup_failed`.

An upload is checked completely (archive hashes, paths, the database) and the helper makes a dry run, but nothing on the node changes until `apply`. A successful `apply` answers first, then ASLCP exits with status 75 and systemd starts it again (`restarting:true`); the restored database is swapped in at that start (the old one is kept as `aslcp.db.pre-restore`) and the page reads `GET /api/restore/result`; `health.pending` stays `true` until the post-restart checks have run (additive field). A failed `apply` answers `ok:false` with the steps and an `error`; the node keeps running with the old ASLCP database. Audit entries: `backup.create` (parts, size) and `backup.restore`; never passwords, codes or file contents. In demo mode backup and restore run against temporary directories and nothing restarts.

## About (all roles)

`GET /api/about` returns the data of Settings > About (Asterisk version is `""` while AMI is down):

```json
About = {
  "version": "0.1.1",
  "asterisk": "22.10.1+asl3-3.10.8-1.deb12",
  "platform": {
    "os": "linux", "arch": "arm64",                       // GOOS / GOARCH
    "model": "Raspberry Pi 4 Model B Rev 1.5",            // /proc/device-tree/model, "" if absent
    "distro": "Debian GNU/Linux 12 (bookworm)",           // PRETTY_NAME of /etc/os-release
    "kernel": "6.1.0-rpi8-rpi-v8",
    "hardware": "Raspberry Pi 4"                          // value of the bug form's hardware dropdown
  }
}
```

The UI builds the bug report link from it: `https://github.com/artw1/aslcp/issues/new?template=bug_report.yml&version=..&asl=..&hardware=..&os=..` (the query keys are the field ids of `.github/ISSUE_TEMPLATE/bug_report.yml`).

## Diagnostics (admin)

`GET /api/diagnostics` returns a `text/plain` attachment `aslcp-diagnostics-<YYYY-MM-DD>.txt` (`403` for operator/viewer). Sections: versions (ASLCP, Asterisk, OS, kernel, hardware); uptime, load, memory, CPU temperature; AMI connection state; local nodes with link counts, link modes and states (no peer IPs) and the monitor's stale state; node list status; update/helper status; firewall status from the helper (when reachable); the configuration (the active lines of `aslcp.conf` and the effective values; values of `secret`, `password`, `token`, `key` entries are masked); and the last 200 lines of the server's own log (an in-memory ring of 500 lines).

The whole text is redacted before it is sent (`internal/diag/redact.go`):

- values of secret-like keys (`secret`, `password`, `token`, `key`, `authorization`, `cookie`, ...) in `k=v`, `k: v` and JSON forms, Bearer tokens, `user:pass@` in URLs, setup links (`?code=`), long random-looking tokens, and the resolved AMI secret wherever it occurs, even in free text, become `<redacted>`;
- email addresses become `<email>`;
- IPv4 and IPv6 addresses become `<ipv4>` / `<ipv6>`, except loopback (`127.x.x.x`, `::1`) and the unspecified address; MAC addresses become `<mac>`;
- callsign-like tokens (`[A-Z]{1,2}[0-9][A-Z]{1,4}`) become `<call>`, except the callsigns of the local nodes' own node-list entries. Node numbers are kept.

The hostname is not included. Redaction is best effort; users are asked to read the file before attaching it.

## Updates (admin)

Checking for updates needs no package manager. The server fetches `<[updates] url>/latest.json` (release channel) or `<[updates] url>/latest-beta.json` (beta channel) (HTTPS, 10 s timeout, 64 KB cap) and compares its `version` with the installed version using the Debian version algorithm (epoch, upstream, revision, `~` sorts first). The installed version is the running binary's version (the package version); a development build (`dev`, `v0.1.0-4-g...`, `...~git...`) asks the helper (`dpkg-query`) instead and otherwise reports "not installed from a package". A background check runs 1 minute after start and then every 6 hours (no cache buster); the result is cached. Installing is performed by the privileged helper (`aslcp-helper`, reached over a unix socket), never by the web process, and always needs an explicit `apply`. All endpoints are admin only (`403` otherwise).

```json
Updates = {
  "available": true,             // false: nothing works (see reason)
  "reason": null,                // when available is false: "not installed from a package" | "helper not running" | "no permission to use the helper"
  "applyReason": null,           // non-null: checks work but the Update button cannot (e.g. "helper not running")
  "channel": "stable" | "beta",   // update channel: the suite of /etc/apt/sources.list.d/aslcp.list as reported by the helper; "stable" when unknown
  "installed": "0.2.0", "candidate": "0.3.0", "upgradable": true,   // candidate = version in the channel's latest json ("" before the first check and after a channel change until the next check)
  "job": "idle" | "running" | "succeeded" | "failed" | "noop",       // the detached upgrade; noop: it ran but apt had nothing to upgrade
  "jobLog": "log of the latest upgrade run (at most 50 lines)",
  "notes": "release notes (plain text, - lists, **bold**) for candidate" | null,
  "checked": "2026-11-15T12:00:00Z" | null,              // last successful check since the server started
  "os": OSUpdates                                         // system (apt) packages; omitted when the server has no helper support
}

OSUpdates = {
  "available": true, "reason": null,       // false: not usable (reason as for Updates)
  "checkedAt": "2026-11-16T04:10:00Z" | null,   // last finished check, persisted across restarts
  "checking": false,                        // a check is running (apt-get update can take minutes)
  "error": null,                            // text of the last failed check, e.g. "apt is busy, try again later"
  "packages": [{"name": "libssl3", "from": "3.0.11-1", "to": "3.0.13-1", "security": true}],   // upgradable; security when the apt origin contains "security"
  "keptBack": ["raspi-firmware"],           // not installed by a plain upgrade
  "rebootRequired": false, "rebootPkgs": [],   // /var/run/reboot-required and .pkgs
  "job": "idle" | "running" | "succeeded" | "failed" | "noop", "jobLog": "..."   // the upgrade unit
}
```

| Method | Path | Response |
|---|---|---|
| GET | `/api/updates` | `200` `Updates`. Fast: cached state plus the helper's cheap job status (`dpkg-query` and `systemctl show`; no apt, no network). |
| POST | `/api/updates/check` | `200` `Updates`. Fetches `latest.json` now, with a `?t=<unix>` cache buster (the CDN caches it for ~10 minutes). `502 {"code":"check_failed"}` when it cannot be fetched or parsed. |
| PUT | `/api/updates/channel` | Body `{"channel":"stable"}` or `{"channel":"beta"}` (exactly these two values; anything else is `400 {"code":"bad_request"}`). Switches the update channel by asking the helper to rewrite the suite word (`stable`/`beta`) of the `aslcp` line in `/etc/apt/sources.list.d/aslcp.list`, so `apt upgrade` on the command line follows the same channel. The cached check is dropped and a fresh (cache-busted) check of the new channel's json runs. `200` `Updates`. `409 {"code":"update_running"}` while an upgrade runs, `503 {"code":"updates_unavailable"}` without helper, `502 {"code":"helper_error"}` when the file is missing or has no `aslcp` line. Switching from beta to stable never downgrades: a node with a beta installed stays on it until a release newer than it is published. |
| POST | `/api/updates/apply` | `202` `Updates` (`job: "running"`). Starts one transient systemd unit that refreshes only the ASLCP apt source and runs `apt-get install --only-upgrade aslcp` (no changelog reader, no recommends); it survives the restart of ASLCP. `409 {"code":"update_running"}` if one is already running. |

| POST | `/api/updates/os/check` | `202` `Updates`. Starts a check in the background (`apt-get update`, then lists upgradable packages; 5 minute timeout); poll `GET /api/updates` until `os.checking` is false. A check already running is not restarted. `409 {"code":"update_running"}` while an upgrade runs, `409 {"code":"apt_busy"}` when another apt process holds the lock ("apt is busy, try again later"), `503 {"code":"updates_unavailable"}`. |
| POST | `/api/updates/os/apply` | `202` `Updates` (`os.job: "running"`). Starts the transient unit `aslcp-osupgrade`: `apt-get update` then plain `apt-get upgrade -y` with `--force-confdef --force-confold` (never `full-upgrade`; kept-back packages stay). Survives the restart of Asterisk or ASLCP if those are upgraded. Re-checks automatically afterwards. `409 {"code":"update_running"}` if any apt job (this or the ASLCP update) runs. Audited as `updates.os.apply` (package names, count, security count). |
| POST | `/api/system/reboot` | `202 {"ok":true}`. The helper runs `systemctl --no-block reboot`. `409 update_running` during an upgrade, `503 updates_unavailable` without helper. Audited as `system.reboot`. |
| POST | `/api/system/restart-aslcp` | `202 {"ok":true}`. The helper runs `systemctl --no-block restart aslcp.service`; the panel is unavailable for a few seconds. Audited as `aslcp.restart`. |

All four are admin only (`403` for operator and viewer). The server also checks the OS packages once a day (the first check about 5 minutes after start when the last one is older than 24 hours); the result and its time are stored, so a restart does not trigger a new check.

Channels: the apt repository has two suites, `stable` (releases `X.Y.Z`) and `beta` (prereleases `X.Y.Z~beta.N`, plus every release). `latest.json` is the newest release; `latest-beta.json` is the newest version overall (a release newer than the newest beta is what beta nodes are offered).

`job: "noop"` means the upgrade ran but apt reported `0 upgraded`: latest.json is newer than the apt index (CDN lag). The UI says "Already up to date (the package index may lag a few minutes; try again later)". The job log only contains the latest run.

`503 {"code":"updates_unavailable"}` when updates cannot be used (see `reason` of `GET`) or the helper cannot be used for `apply`, `502 {"code":"helper_error"}` when the helper reports a failure. While the upgrade runs the server restarts, so the SSE stream drops; the UI reconnects and reloads when `hello.version` changes.

## System tab (admin)

The web UI shows these under **System** (`#/system/<section>`: `overview`, `updates`, `network`, `wifi`, `logs`, `audit`). `#/updates` redirects to `#/system/updates`. Every endpoint here is admin only: `403 {"code":"forbidden"}` for operator and viewer, and the System tab is hidden from their navigation. `503 {"code":"system_unavailable"}` without a helper, `409 {"code":"update_running"}` while an apt job runs, `502 {"code":"helper_error"}` when a helper tool fails. A missing tool (hostnamectl, timedatectl, journalctl, systemctl, ip) is not an error: the reply sets the `*Available`/`available` flag to false. The privileged work is done by fixed helper ops (see Helper protocol); the web process never builds a command line.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/system/host` | `200 HostStatus`: `hostname`, `prettyHostname`, `os`, `hostnameAvailable`, `timezone`, `ntp` (NTP service enabled), `ntpSynchronized`, `canNtp`, `localRtc`, `timeAvailable`, `now` (RFC 3339 UTC), `clock`. |
| PUT | `/api/system/host` | Body with at least one of `{"hostname":"node2001","timezone":"America/Chicago","ntp":true}`. Hostname: one RFC 1123 label (letters, digits, hyphens; 1 to 63 characters; no leading or trailing hyphen), stored lower case. Timezone: must be in `timedatectl list-timezones`. Only values that differ are applied. `200 HostStatus` after the change, `400 {"code":"bad_request"}` for an invalid value or an empty body. Audited as `system.hostname`, `system.timezone` and `system.ntp` (`{"from":..,"to":..}`). |
| GET | `/api/system/timezones` | `200 {"timezones":[...]}`. |
| POST | `/api/system/poweroff` | `202 {"ok":true}`. The helper runs `systemctl --no-block poweroff`. The node stays off until powered on by hand. Audited as `system.poweroff`. Reboot is `POST /api/system/reboot` (above), now in the System tab. |
| GET | `/api/system/logs` | Without `unit`: `200 {"units":[...]}`, the fixed allowlist (asterisk, aslcp, aslcp-helper, NetworkManager, systemd-networkd and the DVSwitch units). With `?unit=&lines=&since=`: `200 {"unit","lines","since","text","truncated","available"}`. `lines` is clamped to 50..1000 (default 200); `since` is empty, `1h`, `24h` or `boot`; anything else, or a unit not on the list, is `400 bad_request`. Output is capped at 128 KiB (newest part kept; `truncated`). |
| GET | `/api/system/services` | `200 {"available","services":[{"unit","description","active","sub","enabled","since"}]}` for the fixed unit list. |
| GET | `/api/system/network` | `200 {"available","interfaces":[{"name","state","kind","mac","mtu","addrs":[{"family","addr","prefix","scope","dynamic"}]}],"gateways":[{"family","gateway","dev","metric"}],"dns":[...],"dnsSource"}`. Read only. |
| GET | `/api/system/wifi` | `200` WiFi view: `available`, `hasWifi`, `radio`, `devices:[{name,type,state,connection,addrs}]`, `active`, `ethernet`, `hotspot:{enabled,delaySeconds,active,ssid,address,firewall}`, `networks:[{id,name,ssid,priority,autoconnect,active,hidden,secured}]` (highest priority first), `viaWifi` (this request arrived on a WiFi address), `viaHotspot` (arrived through the setup hotspot). `available:false` without NetworkManager or a WiFi device; the page then shows "not available". |
| POST | `/api/system/wifi/scan` | `200 {"networks":[{ssid,signal,channel,security,secured,supported,inUse,saved}]}`. Rescans. Enterprise (802.1X) networks are listed with `supported:false`. |
| POST | `/api/system/wifi/networks` | `{"ssid","password","hidden"}`. SSID 1 to 32 bytes; password empty (open network), 8 to 63 ASCII characters or 64 hexadecimal digits. Saves the network and connects now; on failure the change is rolled back. `200 {"result":{"ssid","updated"},"wifi":WifiView}`, `400 bad_request`, `502 helper_error` (for example a wrong password). The password goes to the helper in the request body only, never in argv, logs or the audit log. Audited as `system.wifi.add` (SSID only). |
| DELETE | `/api/system/wifi/networks/{id}` | Removes a saved WiFi network (`id` is the connection UUID from the list). `200 WifiView`. Audited as `system.wifi.forget`. |
| PUT | `/api/system/wifi/order` | `{"order":[id,...]}`, every saved network exactly once, first is tried first (writes descending `autoconnect-priority`). `200 WifiView`, `400 bad_request`. Audited as `system.wifi.order`. |
| PUT | `/api/system/wifi/hotspot` | `{"enabled":bool,"delaySeconds":180}`; `delaySeconds` is optional, 30 to 3600. Persisted by the helper. Off stops and removes the hotspot profile. `200 WifiView`. Audited as `system.wifi.hotspot`. |
| GET | `/api/audit` | `200 {"entries":[{"id","time","user","action","detail"}],"next":id\|null}`, newest first. `?limit=` 1..200 (default 50), `?before=<id>` returns entries older than that id (pass the previous `next`), `?user=` exact, `?action=` prefix. `detail` is the stored JSON text. `400 bad_request` for a bad `limit` or `before`. |

WiFi endpoints are admin only. Every mutating WiFi call is `503 {"code":"system_unavailable"}` without a helper, without NetworkManager or without a WiFi device. The setup hotspot is open (no WiFi password), has no uplink and no forwarding; the web page warns that anyone nearby can see the login.

## DVSwitch install and uninstall (admin)

Design and checklist: docs/DVSWITCH.md (M3). All endpoints are admin only; operator and viewer get `403 forbidden`. `503 dvswitch_unavailable` when the helper cannot be reached. Every POST needs `X-ASLCP: 1`, takes no body, is audited (`dvswitch.install`, `dvswitch.uninstall`, and `.refused` with the error) and answers `202` with the status while the job runs. The package list, the uninstall paths and the units are constants in the helper; nothing in a request reaches a command line.

| Method | Path | Response / errors |
|---|---|---|
| GET | `/api/dvswitch` | `200` DVSwitchStatus (below) |
| GET | `/api/dvswitch/job` | `200` `{"install": Job, "uninstall": Job}`, cheap, polled every 2 s while a job runs |
| POST | `/api/dvswitch/install` | `202` DVSwitchStatus with `install.job = "running"`. Errors: `409 job_running` (an install, uninstall or system update runs), `409 apt_busy`, `409 already_installed` (also when a hand install exists), `409 unsupported_platform`, `409 clock_unset`, `502 key_mismatch` (the downloaded keyring does not hold exactly the pinned fingerprint; nothing was written), `502 helper_error` |
| POST | `/api/dvswitch/uninstall` | `202` DVSwitchStatus with `uninstall.job = "running"`. Errors: `409 not_aslcp_installed` (no ASLCP marker: DVSwitch was installed outside ASLCP), `409 job_running`, `409 apt_busy`, `502 helper_error` |
| DELETE | `/api/dvswitch/restore-pending` | `200` DVSwitchStatus: deletes the DVSwitch settings a backup restore saved for the install (`restorePending.pending` becomes false). Needs `X-ASLCP: 1`, audited as `dvswitch.restore_discard` (`.refused` with the error). Errors: `409 job_running`, `502 helper_error` |
| POST | `/api/dvswitch/stfu/install` | `202` DVSwitchStatus with `stfu.job.job = "running"`: installs the `stfu` package on a DVSwitch install that predates it (new installs include it; BrandMeister through STFU, see "BrandMeister and DMR - Other" below) and leaves its unit disabled. Errors: `409 job_running`, `409 not_installed` (DVSwitch is not installed), `409 stfu_installed`, `409 unsupported_platform`, `502 helper_error`. Audited as `dvswitch.stfu.install` |

```json
Job = { "job": "idle" | "running" | "succeeded" | "failed", "log": "last 50 lines of the unit's journal" }
DVSwitchStatus = {
  "installed": true,
  "installedBy": "aslcp" | "external" | "",   // aslcp: the marker exists; external: installed without it; "": not installed
  "version": "1.7.0", "arch": "arm64", "osCodename": "bookworm",
  "supported": true, "unsupportedReason": "...",   // bookworm/trixie on amd64/arm64/armhf
  "repoPresent": true, "keyPresent": true, "keyOk": true, "keyFingerprint": "6DDF...D4C3",
  "packages": [{"name": "dvswitch-base", "installed": true, "version": "1.7.0"}],
  "units": [{"name": "analog_bridge", "loaded": true, "active": "active", "enabled": "enabled"}],
  "hostFiles": [{"name": "YSFHosts.txt", "present": true, "size": 4096}], "hostFilesOk": true,
  "marker": {"version": 1, "installedAt": "...", "wgetPreinstalled": false, "packages": ["qemu-user-static", "wget"]} | null,
  "canInstall": false, "canUninstall": true,
  "uninstallPackages": ["..."], "uninstallPaths": ["/var/lib/mmdvm", "..."], "uninstallUnits": ["analog_bridge", "..."],  // for the confirm dialog
  "install": Job, "uninstall": Job,
  "stfu": {"installed": false, "version": "...", "canInstall": true, "job": Job},   // the optional STFU package; poll GET /api/dvswitch while stfu.job runs
  "restorePending": {"pending": true, "backupDate": "2026-11-20 08:15", "files": ["/opt/Analog_Bridge/Analog_Bridge.ini"]}   // settings saved by a backup restore, applied at the end of the install; backupDate is UTC, names only, no content
}
```

The install job ends in `unexpected_deps` (the simulation would pull in Apache, PHP, mosquitto or Node.js; nothing is installed and a repository added by this run is removed again). The uninstall job ends in `unexpected_removals` (the purge would remove something outside the recorded list; nothing is changed). Both are visible in the job log. No secret is ever in a reply.


## DVSwitch setup (admin)

Design: docs/DVSWITCH.md (M4). All endpoints are admin only (`403 forbidden` otherwise), need `X-ASLCP: 1` when not GET, and answer `503 dvswitch_unavailable` when the helper cannot be reached. Every write is audited (`dvswitch.identity`, `.credentials`, `.modes`, `.startup`, `.rollback`, `.restart`/`.start`, and `.refused`) with the request key names and the changed file ids, never a value. **The DMR password is write-only**: it is not in any response, log line or audit entry; the setup view only has `passwordSet`.

| Method | Path | Body | Response / errors |
|---|---|---|---|
| GET | `/api/dvswitch/setup` | | `200` Setup |
| PUT | `/api/dvswitch/identity` | `{"callsign","dmrId","essid","nxdnId"?,"info"?,"base"?}` (`nxdnId`: digits, 1-65519, no leading zeros; omitted or empty leaves the NXDN ID alone). `info`: `{"location","description","url"?,"frequencyHz","latitude"?,"longitude"?}`, the `[Info]` section of `MMDVM_Bridge.ini`: `location` 1-20 and `description` 1-19 printable ASCII characters (letter or digit first, no `; # = "`), `url` empty or `http(s)://` up to 124, `frequencyHz` 30000000-3000000000 written to both `RXFrequency` and `TXFrequency`, `latitude` -90..90 and `longitude` -180..180 (both or neither). Omitted: `[Info]` is not touched | `200` Setup, where `identity.info` is `{location,description,url,frequencyHz,rxHz,txHz,latitude,longitude,stock}` (`frequencyHz` 0 when RX and TX differ; `stock` true while the stock values are in place, which also adds a setup warning). `400 invalid_field` names `identity/info/<field>`. The audit entry lists the key `info`, never a value |
| PUT | `/api/dvswitch/credentials` | `{"dmrAddress"?,"dmrPort"?,"dmrPassword"?,"clearPassword"?,"base"?}` | `200` Setup. `"clearPassword": true` resets the DMR password to the stock `passw0rd` (for masters without a password); `passwordSet` becomes false. With `dmrPassword` too: `400 invalid_field`. `false` is the same as absent |
| PUT | `/api/dvswitch/modes` | `{"dmr"?,"ysf"?,"p25"?,"nxdn"?,"base"?}` (booleans) | `200` Setup |
| PUT | `/api/dvswitch/startup` | `{"ambeMode"?,"txTg"?,"txTs"?,"colorCode"?,"ysf"?,"p25"?,"nxdn"?,"base"?}` | `200` Setup |
| POST | `/api/dvswitch/rollback` | `{"file":"analog_bridge","base"?}` (file ids: `analog_bridge`, `mmdvm_bridge`, `ysfgateway`, `p25gateway`, `nxdngateway`, `dvswitch`) | `200` Setup |
| PUT | `/api/dvswitch/brandmeister` | `{"server"?,"password"?,"talkerAlias"?,"startTg"?,"base"?}` | `200` Setup. See "BrandMeister and DMR - Other" below |
| DELETE | `/api/dvswitch/brandmeister` | | `200` Setup |
| DELETE | `/api/dvswitch/dmr-connection` | | `200` Setup |
| POST | `/api/dvswitch/restart` | `{"scope":"dmr\|bridge\|gateways\|all","action"?:"restart\|start"}` (`dmr` is `mmdvm_bridge`, plus `stfu` while it is enabled; `all` adds `stfu` then too) | `200` `{"action","units":[],"skipped":[],"states":[{name,loaded,active,enabled}],"warning"}` |

`PUT /api/dvswitch/startup` writes only the fields that are present; the startup mode (`ambeMode`: `DMR`, `YSFN`, `YSFW`, `P25`, `NXDN`) can be sent alone, and a gateway card sends only its own reflector. The old `useCurrent` field was removed: it is ignored, so `{"useCurrent":true}` alone is a no-op (`200`, nothing written).

### DVSwitch BrandMeister and DMR - Other (admin)

Design: docs/DVSWITCH.md "BrandMeister and DMR - Other". Two DMR connections run side by side: **BrandMeister** through STFU (Open DMR Terminal, the `[STFU]` section of `DVSwitch.ini`, ports 36100/36103) and **DMR - Other** through MMDVM_Bridge (the master under the credentials, `[DMR]` ports). The panel switches between them as the modes `BM` and `DMR`.

`PUT /api/dvswitch/brandmeister` `{"server"?:"3102.repeater.net","password"?:"example-pass","talkerAlias"?:"N0CALL","startTg"?:"4000","stayLoggedIn"?:false,"base"?}`:
- `server` is a host name (omitted or empty keeps the saved one, default `3102.repeater.net`; the port is always 54006). `password` is the hotspot security password of BrandMeister SelfCare: **write-only**, omitted keeps the saved one, required on the first save; same character rules as the DMR password. `talkerAlias` 1-27 printable ASCII without `" ' ; # =` and no outer spaces (omitted keeps the saved one, default the callsign). `startTg` 1-8 digits (default `4000`). `stayLoggedIn` (boolean, default off, omitted keeps the saved value): STFU stays enabled and logged in outside BM mode; off, **STFU logs in only while the panel is in BM mode** (see below). It is stored in the helper state directory (`/var/lib/aslcp-dvswitch/stfu-stay`, present = on), not in an ini file.
- Writes `[STFU]` (`BMAddress`, `BMPort=54006`, `BMPassword` when sent, `Address=127.0.0.1`, `txPort=36100`, `rxPort=36103`, `UserID` = the 7-digit DMR ID, `TalkerAlias`, `StartTG`), makes `STFU.log` and its rotated copies root-only, writes the `UMask=0077` drop-in, then restarts `stfu` (enabled at boot with `stayLoggedIn`, left disabled otherwise) to check the login: the reply carries `bmLogin` (`"logged_in"` when the login line was seen within 10 s, `"pending"` when not). Afterwards stfu keeps running only when the panel is in BM mode or `stayLoggedIn` is on; otherwise it is stopped (`stfuStopFailed: true` in the reply when that stop failed). `MMDVM_Bridge.ini` and `Analog_Bridge.ini` are not written (a live BrandMeister session on other `[STFU]` ports is pointed at the new ones). A failed step puts the file, the drop-in and the unit back.
- `200` Setup (plus `bmLogin`, `stfuStopFailed?`). Errors: `400 invalid_field` (`brandmeister/server`, `brandmeister/password`, `brandmeister/talkerAlias`, `brandmeister/startTg`, `brandmeister/ports` when 36100/36103 are taken by another mode, the USRP or AMBE ports or the bridge node), `409 stfu_not_installed` (no `stfu` package or no `[STFU]` section), `409 no_identity` (save the callsign and DMR ID first), `409 no_password` (no password sent and none saved), `409 not_installed`, `409 conflict`, `409 job_running`, `502 switch_failed` (a step failed and the previous state was restored), `502 helper_error`.
- Audited as `dvswitch.brandmeister` (or `.refused`) with the key names sent (`password` as a name only) and the changed files, never a value; a sent `stayLoggedIn` is also audited as `dvswitch.brandmeister.stay` with its boolean.

**STFU on demand.** STFU and other BrandMeister Open DMR Terminal clients (a phone app such as QSO One) log in with the same 7-digit DMR ID, and BrandMeister keeps one login per ID (SelfCare shows only the latest). So STFU runs only while the panel is in BM mode, unless `stayLoggedIn` is set: a mode switch to BM starts it and waits up to about 10 s for its login before Analog_Bridge is pointed at it, and a switch to DMR, YSF, P25 or NXDN stops it. The resume after an ASLCP or node restart does the same (a non-BM mode makes sure stfu is stopped). `stfu.service` is not enabled at boot in this mode. A mode switch or tune reply carries `bmLoginPending: true` when BM was entered but the login was not seen in time (the switch still happened) and `stfuStopFailed: true` when stfu could not be stopped after leaving BM (the switch still happened). `dmr-status` `bm.state` is `idle` while stfu is stopped for this reason. `DELETE /api/dvswitch/brandmeister` also turns `stayLoggedIn` off.

`DELETE /api/dvswitch/brandmeister` (no body): resets `[STFU] BMPassword` to the stock value, disables and stops `stfu`, turns `stayLoggedIn` off, removes the drop-in; a live BrandMeister session moves to the `[DMR]` ports. `200` Setup. Audited as `dvswitch.brandmeister.clear`.

`DELETE /api/dvswitch/dmr-connection` (no body): turns DMR - Other off (`[DMR]` and `[DMR Network]` `Enable=0` in `MMDVM_Bridge.ini`); the master and the password stay. `200` Setup. Audited as `dvswitch.dmr_connection.clear`.

`PUT /api/dvswitch/credentials` refuses a BrandMeister master for DMR - Other (a `BM_` entry of `DMR_Hosts.txt`, `*.brandmeister.network`, `*.repeater.net`): `400 bm_master`. A master saved earlier stays; the setup then carries a warning and `dmr.pointsAtBm: true`.

The Setup view carries `brandmeister: {installed, present, enabled, stayLoggedIn, configured, server, port, talkerAlias, startTg, txPort, rxPort, passwordSet}` (`enabled`: the unit starts at boot, only with `stayLoggedIn`; `configured`: a password saved and a 7-digit `UserID`, whether stfu runs or not; only then does the panel offer `BM`), `dmr.enabled` (`[DMR]` and `[DMR Network]` are on) and `dmr.pointsAtBm`. While BrandMeister has a password, a new DMR ID is written to `[STFU] UserID` as well.

### DVSwitch tune and resume

Design: docs/DVSWITCH.md "Tune and resume". `503 dvswitch_unavailable` when the helper or tracker is missing.

| Method | Path | Role | Body | Response / errors |
|---|---|---|---|---|
| POST | `/api/dvswitch/tune` | operator, admin | `{"mode":"BM\|DMR\|YSF\|P25\|NXDN","tune":"91"}` (`BM` is BrandMeister through STFU, `DMR` is DMR - Other through MMDVM_Bridge; each has its own remembered tune and favourites. For BM and DMR a trailing `#` makes it a private call: `"9990#"` is the BM Parrot; the other modes refuse it; a private call is never remembered or resumed) | `200` `{"mode","switched","confirmed","left"?,"bmLoginPending"?,"stfuStopFailed"?,"snapshot"}` (`left`: see mode; `bmLoginPending` and `stfuStopFailed`: see "STFU on demand"). `400 invalid_field` (value invalid for the mode), `409 no_info` (bridge not running), `409 no_ports` (no ports for the mode in `DVSwitch.ini`), `502 helper_error`. Audited as `dvswitch.tune` (or `.refused`); the value is in the audit log only when valid |
| GET | `/api/dvswitch/resume` | operator, admin | | `200` `{"resume":false,"lastMode":"P25","perMode":{"BM":{"tune":"91","ts":"2","at":"2026-11-17T11:55:00Z"},"DMR":{"tune":"1","ts":"2","at":"2026-11-17T10:00:00Z"}}}` (`at` RFC 3339 UTC; modes without a remembered tune are absent) |
| PUT | `/api/dvswitch/resume` | admin | `{"resume":true}` | `200` as GET; `400 bad_request` when `resume` is not a boolean. Audited as `dvswitch.resume` |

### DVSwitch panel (snapshot, mode, unlink, favourites)

Design: docs/DVSWITCH.md "API". `503 dvswitch_unavailable` when the helper is missing. Non-GET needs `X-ASLCP: 1`. Every write is audited (`dvswitch.mode`, `dvswitch.unlink`, `dvswitch.tune`, `dvswitch.favorite.add|update|delete`).

| Method | Path | Role | Body | Response / errors |
|---|---|---|---|---|
| GET | `/api/dvswitch/snapshot` | viewer, operator, admin | | `200` `{installed,running,configured,bridge:{mode,tune,targetName?,slot,muted,private?,source:"abinfo"\|"none",updated},modes:[{id,enabled,unlink}],services:[{unit,active}],bridgeNode:{present,node?,name?},link:{mode,localNode,linked,paused,exclusive},caller?:{call?,dmrId?,dst?,slot?,at?,private?},heard:[{call?,dmrId?,dst?,slot?,at?,private?}]}`. `bridge.private` is true (DMR only) while the last tune was a private call (also the Parrot 9990 without `#`). `call` is the log's `call=`, else the DMR ID list lookup (omitted when unknown: show `dmrId`); `private` marks a call to this node (the Parrot's reply). `caller` is who is talking now and `heard` the last 5 distinct callers, newest first (never null), both read from `/var/log/dvswitch/Analog_Bridge.log` by the helper (docs/DVSWITCH.md "Caller callsign"; `at` is the log's own time, no zone). No install logs, no node identity, no password. Callsigns and DMR IDs are for logged-in roles only and are never audited. `installed:false` is a normal `200`. `configured` = installed and analog_bridge running |
| POST | `/api/dvswitch/mode` | operator, admin | `{"mode":"BM\|DMR\|YSF\|P25\|NXDN"}` | `200` `{mode,switched,confirmed,left?,bmLoginPending?,stfuStopFailed?,snapshot}`; `400 bad_request`; `409 mode_disabled` (also `BM` while BrandMeister is not set up); `409 no_info`; `409 no_ports`; `502 helper_error`. In one sequence: sends the unlink value of the mode being left while Analog_Bridge still points at it (only when it is linked; `left` names that mode), switches, then tunes the new mode to its remembered tune, else to its unlink value. The mode left keeps its remembered tune, so switching back reconnects it. A failed unlink of the mode left is logged and the switch completes. Audited with `left` |
| POST | `/api/dvswitch/unlink` | operator, admin | `{}` | `200` as mode. Sends the unlink value of the current mode (BM and DMR `4000`, YSF `disconnect`, P25 `9999`, NXDN `9999`); `409 no_info`, `409 mode_unknown`. Also erases the node's remembered tune of that mode, so switching away and back does not reconnect it |
| POST | `/api/dvswitch/tune` | operator, admin | see above | `200` `{mode,switched,confirmed,snapshot}` |
| GET | `/api/dvswitch/favorites` | viewer and up | | `200` `[{id,mode,value,name,sort}]` |
| POST | `/api/dvswitch/favorites` | operator, admin | `{mode,value,name}` | `201` favourite; `400 bad_request` (name 1-40 printable characters, value valid for the mode); `409 duplicate`; `409 limit` (50 per mode) |
| PUT | `/api/dvswitch/favorites/{id}` | operator, admin | `{name?,sort?}` | `200` favourite; `404 not_found` |
| DELETE | `/api/dvswitch/favorites/{id}` | operator, admin | | `204`; `404 not_found` |

SSE event `dvswitch` carries the snapshot (all roles), sent only when it changed.

### DVSwitch private bridge node (admin)

Design: docs/DVSWITCH.md "Private node and USRP (M2)" (implemented, unverified on a real node). Admin only (`403 forbidden`), `X-ASLCP: 1`, `503 dvswitch_unavailable`. Audited as `dvswitch.private` (with `kind`), `dvswitch.private.remove`, `.private.failed`, `.private.refused` and `.private.rollback` (node and step names, no values).

| Method | Path | Body | Response / errors |
|---|---|---|---|
| PUT | `/api/dvswitch/link` | operator, admin | `{"exclusive": bool}` (required) | `200` `{exclusive}`. Saves "Disconnect other links first" (`dvswitch.link_exclusive`) alone: no Asterisk file, no apply, no restart. Turning it off drops a pending exclusive connect; the pause is untouched. Audit `dvswitch.link`. Used by the checkbox on the dashboard DVSwitch panel; `400 bad_request` without `exclusive` |
| PUT | `/api/dvswitch/private-node` | `{"node","rxPort","txPort","linkMode":"ondemand\|permanent","localNode","linkExclusive"?:bool,"dryRun"?,"restart"?,"base"?}` | dry run: `200` `{kind:"set\|renumber\|remove",oldNode,unlinks:[],keepRecord,node,rxPort,txPort,layout:"template\|flat\|minimal\|existing",files:[{path,diff,changed}],restartAsterisk,restarts:[],warnings:[],base:{},link:{mode,localNode,bridge,exclusive}}`. Apply: `200` `{applied,kind,node,steps:[{name,ok,detail?}],warnings,keepRecord,health,rollback:{available,before?,ini?}}`. `"renumber": true` replaces the bridge node ASLCP added by `node` (ports, link mode and local node carry over when omitted). `linkExclusive` is "Disconnect other links first" (setting `dvswitch.link_exclusive`); the UI no longer sends it (it is changed with `PUT /api/dvswitch/link`); absent keeps the saved value, which is `true` until one is saved (the analog Connect's default). It is only a setting: changing it alone writes no Asterisk file, so the plan has `restartAsterisk:false` and no `restart_required`; without it a different number is `409 node_in_use` |
| DELETE | `/api/dvswitch/private-node` | optional `{"dryRun"?,"restart"?,"base"?}` or `?dryRun=true&restart=true` | removes the bridge node ASLCP added: disconnects its link, reverts `rpt.conf` and `modules.conf` from the record, restores the `[USRP]` ports (recorded ones, else the stock 31001/31001, with a warning), restarts Asterisk once, clears the link settings and the record. Dry run: `200` plan with `kind:"remove"`. Apply: `200` `{applied,kind:"remove",node,steps,warnings,keepRecord,rollback}`; a change ASLCP could not revert (hand-edited) is a warning and `keepRecord: true`. `409 no_bridge_node` (nothing owned), `409 not_owned` (found but not added by ASLCP), `409 restart_required`, `409 conflict`, `502 apply_failed` |
| PUT | `/api/dvswitch/private-node/name` | admin | `{"name": string}` (required) | `200` `{name}` (the effective name). Saves the display name of the bridge node (`dvswitch.node_name`) alone: no Asterisk file, no apply, no restart. 1 to 40 printable characters; an empty string restores the default `DVSwitch linked node`. Audit `dvswitch.node_name`. `400 bad_request` without `name` or with an invalid one. The removal of the bridge node clears it |
| POST | `/api/dvswitch/private-node/adopt` | admin | `{}` (ignored) | `200` `{adopted,node,chanUsrpWas,usrpRestoredToStock}`. Saves ASLCP's record for a bridge node that is in `rpt.conf` without one (a backup from before 1.2.4, a hand-made node), so that Change number, Remove and the uninstall work on it. Nothing in `rpt.conf`, `modules.conf` or the ini is written and nothing restarts. Only when the node looks like the one ASLCP adds: stanza with `rxchannel USRP/127.0.0.1:<port>:<port>`, a `[nodes]` line, `chan_usrp` loaded, and it is the saved bridge node or the only USRP node. `409 already_managed`, `409 no_bridge_node`, `409 not_adoptable` (message names what differs). `GET /api/dvswitch/setup` and the snapshot's `bridgeNode` carry `adoptable` for a found node that is not owned. Audit `dvswitch.private.adopt` |
| POST | `/api/dvswitch/private-node/rollback` | `{}` | `200` `{rolledBack,problems:[],steps:[]}`; `404 no_rollback` |

Errors of the PUT: `400 invalid_node` (not a number, or 2000 or more), `400 bad_request` (bad `linkMode`, a missing or unknown `localNode`), `400 invalid_field` (bad ports: out of range or equal), `409 node_in_use` (the node exists, or another bridge node exists and the request has no `renumber`, or the bridge node is not ASLCP's), `409 no_bridge_node` (renumber with no ASLCP-owned node), `409 old_node_edited` (renumber: the old stanza or `[nodes]` line was changed by hand), `409 old_node_not_removable` (renumber: the old stanza existed before ASLCP), `409 port_conflict`, `409 conflict` (a file changed since `base`: plan again), `409 restart_required` (Asterisk must restart: send `restart: true`), `503 config_unavailable`, `422 syntax_error` (the resulting `rpt.conf` or `modules.conf` does not parse; nothing written), `502 apply_failed` (`{error,code,step,applied,steps,health,rollback}`: the restart, bridge restart or health step failed; `rollback.available` says the rollback endpoint can undo it). The order is Asterisk config, ini, Asterisk restart, bridge restart, health. A failure at the ini or record step restores the Asterisk files by itself.

`GET /api/dvswitch/setup` carries `bridgeNode: {node,owned,suggested,used,localNodes,rxPort,txPort,link:{mode,localNode,bridge,exclusive},paused,name}`. The snapshot's `bridgeNode` is `{present,node?,name?}`: `name` is the display name (the default `DVSwitch linked node` when none was saved), present with `present: true`; the dashboard shows it on the DVSwitch panel and beside the bridge node number in the connection status table. The snapshot carries `link: {mode:"ondemand\|permanent",localNode,linked,paused,exclusive}`. The Link node button sends `POST /api/nodes/{node}/connect` with `exclusive` set from `link.exclusive`, the same request and backend path as the analog Connect (audit, log, schedule and pause hooks included). In permanent mode a save with `linkExclusive: true` makes the connect it triggers exclusive (once, within 5 minutes); the reconciler's automatic relinks never are. A permanent link is kept up by the reconciler; a disconnect of it through `POST /api/nodes/{node}/disconnect` (or `disconnect-all`) pauses it until the next save or Asterisk restart through the panel.

### DVSwitch DMR login status (operator)

Design: docs/DVSWITCH.md ("DMR login status"). Answers whether `mmdvm_bridge` (DMR - Other) is logged in to its DMR master, from the log lines written since the unit started, and in `bm` whether `stfu` is logged in to BrandMeister (docs/DVSWITCH.md "BrandMeister and DMR - Other").

| Method | Path | Reply |
|---|---|---|
| GET | `/api/dvswitch/dmr-status` | `200` `{"state","via"?,"master"?,"at"?,"since"?,"lastLine"?,"rx"?,"bm"?}`. The top level is DMR - Other (`via: "mmdvm_bridge"`); `bm` is BrandMeister's login in the same shape (`via: "stfu"`, `state: "disabled"` while BrandMeister is not set up, `"idle"` while stfu is stopped because the panel is not in BM mode and `stayLoggedIn` is off), present once `stfu` is installed. `via` names the unit whose log is read (for `stfu`, `/var/log/dvswitch/STFU.log`, from which every line containing `password` is dropped before parsing). `rx` (STFU only) is the last transmission from the network: `{"active","src","dst","group","time"?,"frames"?}`. `operator` and `admin` only (`401`, `403 forbidden` for a viewer). `503 dvswitch_unavailable`. `Cache-Control: no-store`. No password and no DMR ID: digit runs of 7 or more in `lastLine` and `master` are replaced by `***` |

`state` is one of `not_installed`, `disabled` (DMR mode off; BrandMeister not set up), `idle` (BrandMeister only: stfu is stopped on purpose, not an error), `not_running`, `connecting` (running, no verdict line since the start), `logged_in`, `login_failed`, `timed_out`, `socket_failed`, `master_closing`. `master` (`host:port`, only with `logged_in`), `at` (RFC 3339 UTC time of the matched line) and `lastLine` (for example `DMR, Login to the master has failed, retrying login ...`) are present only when a verdict line matched; `since` (RFC 3339 UTC unit start) is present when the unit start is known. Example: `{"state":"logged_in","master":"3103.master.brandmeister.network:62031","at":"2026-11-18T01:23:15Z","since":"2026-11-18T01:23:00Z","lastLine":"DMR, Logged into the master successfully: 3103.master.brandmeister.network:62031"}`.

### DVSwitch host and TG lists (operator)

Design: docs/DVSWITCH.md ("Host and TG lists for the pickers").

| Method | Path | Response / errors |
|---|---|---|
| GET | `/api/dvswitch/lists?mode=dmr\|ysf\|p25\|nxdn` | `200` `{"mode","items":[{"value","name","detail"?,"address","port"}],"truncated"?,"etag"}`. `operator` and `admin` only (`401`, `403 forbidden` for a viewer). `400 bad_request` when `mode` is missing or not in the enum (checked before the helper is called). `404 no_list` when the list file of the mode is missing or unreadable: the UI then takes free text. `503 dvswitch_unavailable`. The reply carries `ETag: "<mode>-<hash of the file>"` and `Cache-Control: private, max-age=300`; `If-None-Match` with the same tag gets `304` and no body |

`value` is what a pick writes: the master **address** (dmr; `port` is the master port), the 5-digit reflector **id** (ysf) or the **TG** (p25, nxdn). `name` is the list name (`P25 TG 14` for the TG lists, which have no names), `detail` the description or `ID <network id>` for DMR, `address` and `port` where it connects. At most 5000 items (`truncated: true` beyond that; the real lists hold about 1,500 DMR, 1,700 YSF, 470 P25 and 430 NXDN entries). Lines that fail a field check are skipped. **The DMR list never carries a password**: the password column of `DMR_Hosts.txt` is not read into any value. The file for each mode is fixed in the helper (`DMR_Hosts.txt`, `YSFHosts.txt`, `P25Hosts.txt`, `NXDNHosts.txt` below `/var/lib/mmdvm`); the client sends an enum only. Not audited (read-only).

Errors of the writes: `400 invalid_field` (message names `file/section/key` or the field, never a value), `409 not_installed` (the DVSwitch ini files or units are missing), `409 conflict` (a file changed since `base`; nothing written), `409 job_running` (an install or uninstall runs), `404 no_history` (rollback), `502 helper_error`. `base` is `{file id: sha256}` from the Setup `files`; it is optional and only checked for the files the call changes.

```json
Setup = {
  "identity": { "callsign": "N0CALL", "dmrId": "1234567", "essid": "01", "nxdnId": "12346", "consistent": true },   // nxdnId "" = not set (also the stock 12345)
  "dmr": { "address": "192.0.2.10", "port": 62031, "passwordSet": true, "enabled": true, "pointsAtBm": false },   // DMR - Other
  "brandmeister": { "installed": true, "present": true, "enabled": false, "stayLoggedIn": false, "configured": true, "server": "3102.repeater.net", "port": 54006, "talkerAlias": "N0CALL", "startTg": "4000", "txPort": 36100, "rxPort": 36103, "passwordSet": true },
  "usrp": { "address": "127.0.0.1", "rxPort": 34001, "txPort": 32001 },
  "modes": { "dmr": true, "ysf": false, "p25": false, "nxdn": false },
  "startup": { "ambeMode": "DMR", "txTg": "9", "txTs": 2, "colorCode": 1 },
  "gateways": { "ysf": { "present": true, "startup": "" }, "p25": { "present": true, "startup": "" }, "nxdn": { "present": true, "startup": "" } },
  "warnings": [],   // e.g. ["NXDN needs your NXDN ID from radioid.net"] when NXDN is enabled without an ID; never blocks
  "files": [ { "id": "analog_bridge", "present": true, "writable": true, "sha256": "...", "history": 2 } ],
  "current": { "found": true, "version": "1.6.4", "mode": "DMR", "tg": "9", "slot": "2", "colorCode": "1", "tune": "", "mute": "OFF", "gatewayId": "1234567", "repeaterId": "123456701", "updated": "2026-11-17T12:00:00Z" },
  "changed": ["mmdvm_bridge"], "skipped": [], "restartHint": ["mmdvm_bridge"]   // after a write only
}
```

A restart resets the running mode and talkgroup to the startup values; `restartHint` lists the units that must restart for a change to take effect, nothing is restarted by a write.

## Public status view

Optional, off by default (admin setting). Served on the main port. While disabled the three public endpoints answer `404 {"code":"not_found"}`. They need no session and no CSRF header (GET only), set no cookies, and are rate-limited per client address (`429 {"code":"rate_limited"}` with `Retry-After`). The data comes from dedicated structs: it never contains IP addresses, system information, favorites or users.

| Method | Path | Response |
|---|---|---|
| GET | `/api/public/info` | `{"enabled": bool, "title": str, "loggedIn": bool}`. Always answers; `title` is empty while disabled; `loggedIn` says whether the request carries a valid session. |
| GET | `/api/public/status` | `PublicSnapshot` |
| GET | `/api/public/events` | SSE (below). `429` over the per-address cap (4 connections), `503` over the global cap (200). |

```json
PublicSnapshot = {
  "title": "N0AAD AllStarLink node 2001",   // setting, else "<callsign> AllStarLink node <n>", else "AllStarLink node status"
  "now": 1794725912,                          // server time, unix seconds
  "callsigns": true, "showNetwork": true, "showActivity": true,
  "nodes": [{
    "node": "2001", "info": {"callsign","description","location"} | null,
    "stale": false, "txKeyed": false, "rxKeyed": false, "numLinks": 27,
    "links": [{"node": "50241", "kind": "allstar", "info": {...} /* omitted when callsigns are hidden */,
               "direction": "OUT", "mode": "T"|"R"|"L"|"C", "connected": 64, "keyed": false,
               "sinceKey": 12 | null, "sinceUnkey": 3 | null}],
    "network": [{"node": "1021", "callsign": "N0AAA" /* omitted when hidden */}]   // omitted when disabled or empty
  }],
  "activity": [{"node": "2001", "link": "50241" | "local", "callsign"?: "N0AAF", "at": 1794725900, "duration": 7, "kerchunk": false}]   // newest first, at most 25; omitted when disabled
}
```

Ages (`connected`, `sinceKey`, `sinceUnkey`) are as of `now`; the page advances them locally. Link nodes that are not node numbers, callsigns or EchoLink numbers (an address, for example) are published as `"unknown"`.

SSE events: `snapshot` (`PublicSnapshot`; on connect, after a stale/recovered transition, and when an admin changes the settings), `node` (one node without `network`), `network` (`{"node", "network": [...]}`), `activity` (`{"now", "activity": [...]}`). A comment line (`: ping`) is sent every 20 s and a stream is closed after 6 hours (the page reconnects). If SSE is unavailable the page polls `/api/public/status` every 10 s.

### Public page settings (admin)

`PublicSettings = {"enabled", "title", "nodes": [str], "showCallsigns", "showNetwork", "showActivity", "allowEmbedding", "trustProxy", "defaultTitle", "localNodes": [{"node","info"}], "clients": int}`. Defaults: disabled, empty title (automatic), `nodes: []` (all local nodes), callsigns/network/activity on, embedding off, trust proxy off. Stored as settings `public.*`.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/public` | | `200` `PublicSettings` (admin) |
| PUT | `/api/public` | any subset of `enabled`, `title` (at most 80 characters, no control characters), `nodes` (local node numbers; all of them or `[]` means all), `showCallsigns`, `showNetwork`, `showActivity`, `allowEmbedding`, `trustProxy` | `200` `PublicSettings`; `400 {"code":"bad_request"}` for invalid input. Applies immediately to connected visitors; disabling closes their streams. |

`trustProxy`: use the last `X-Forwarded-For` entry as the client address for the rate limit and connection caps (only behind a reverse proxy or tunnel that sets it; otherwise clients could forge it).

**Embedding.** `/embed` serves the same app as a chrome-less public view. Only `/embed` gets `Content-Security-Policy: frame-ancestors *` (and no `X-Frame-Options`), and only while the page is enabled and `allowEmbedding` is on. All other paths, the login page included, send `frame-ancestors 'none'` and `X-Frame-Options: DENY`.

### Activity (all roles)

`GET /api/activity?node=&limit=` returns recorded transmissions, newest first (`limit` 1-500, default 50; `node` filters by local node): `[{"id", "node", "link": "50241"|"local", "info": NodeInfo|null, "start": RFC3339, "duration": 7.4 /* seconds */, "kerchunk": false}]`. The server records a row when a link or the local receiver unkeys (keyups under 0.5 s are ignored, under 1.5 s are flagged `kerchunk`); rows older than 7 days are deleted daily and the table is capped at 10,000 rows.

### Favorites activity scan

`FavStats = {"enabled": bool, "rate": int, "effRate": int, "backoff": bool, "retryAt": RFC3339 | null, "backoffReason": "rate-limit" | "service-error" | "network-error" | "", "updated": RFC3339 | null, "checking": "<node>" | "", "next": "<node>" | "", "nextAt": RFC3339 | null, "checked": int, "total": int, "checkedAt": {"<node>": RFC3339}, "stats": {"<node>": Activity}, "removed": ["<node>"]}` (`removed` is always `[]` in the snapshot; `checking` is the node being requested now (kept for compatibility), `next` the node that will be requested next (same ordering as the poller; `""` when off, nobody is watching, nothing is eligible or all are in their skip period), `nextAt` when the next request is due, `total` the eligible favorites, `checked` how many have an answer in the current pass, `checkedAt` each node's last attempt including offline ones; `updated` is kept for compatibility) with `Activity = {"rxPct": int | null, "linkCnt": int, "active": bool, "webTx": bool, "keyed": bool, "txAvg": int, "lastSeen": RFC3339}`. See `docs/FAVSCAN.md`.

One server-side poller queries `https://stats.allstarlink.org/api/stats/<node>` (base URL: `[favstats] url`) for each distinct favorite in 2000..999999 (regular node numbers; EchoLink numbers 3000000..3999999 and private numbers below 2000 are skipped and show no values) that is not one of this machine's nodes, round robin, one request at a time. It runs only while the scan is on and at least one logged-in SSE client is connected (public-page visitors do not count); values are kept 10 minutes after the last viewer leaves. Requests are evenly paced, one every 60/rate seconds from the first one (`[favstats] rate`, default 20 per minute = one every 3 s, at most 25; never closer than 3 s) and paused with exponential backoff (15 s up to 5 min, `Retry-After` honoured) after HTTP 429 or an error. The rate is adaptive: each 429 halves the rate in use (`effRate`, not below 3 per minute) and every 20 consecutive successful requests raise it by 1 per minute, up to `rate`. A 429 never replaces or clears a node's values. Nodes never queried go first, then the one queried longest ago. `retryAt` is when the current backoff ends (`null` otherwise). `[favstats] log = true` logs one line per request (node, status, duration, rate, gap, Retry-After) and each rate change to the journal. `rxPct` is `round(100*totaltxtime/apprptuptime)` and is `null` unless uptime is over 60 s and TX time over 10 s. `keyed` is derived from changes of `totalkeyups` / `totaltxtime` between polls (the API's own `keyed` flag is only used for the first sample); counter resets and implausible jumps count as no activity. `txAvg` is a moving average of the recent TX share. `active` / `webTx` are the node's own `Status == "Active"` and `access_webtransceiver`. `backoff` is true while polling is paused and `backoffReason` says why (`rate-limit` = HTTP 429, `service-error` = HTTP 5xx, `network-error` = timeout or connection error; `""` otherwise); `updated` is the time of the last successful poll. Only the fields above are read from the response; it is never stored, logged or forwarded.

## Server-Sent Events: `GET /api/events`

Every `/api/` response carries `X-ASLCP-Instance`, a random id generated when the server process starts (also sent as `hello.instance`); the UI compares it to detect that the panel restarted.

Requires a session. Each message has an `event:` name and a JSON `data:` line. On connect the server sends `hello`, then a full state; afterwards only changes.

| event | data |
|---|---|
| `hello` | `{"user": User, "version": str, "instance": str, "settings": Settings}` |
| `nodes` | `[NodeStatus]`: full state (sent on connect and after AMI reconnects) |
| `node` | `NodeStatus`: one node changed |
| `system` | `System` (including `updateAvailable` / `updateVersion`) |
| `updates` | Admin clients only: `{"aslcp": bool, "aslcpVersion": str, "os": n, "security": n, "reboot": bool}`: counts for the header badge, never package names. Sent on connect and whenever they change. |
| `favorites` | `[Favorite]`: favorites changed (any client) |
| `schedules` | `Snapshot` (see Schedules; all roles): sent after every change (create, edit, delete, switch, pause) and after each executed event. Not part of the initial state: fetch `GET /api/schedules` after login. |
| `favstats` | `FavStats` with only the changed nodes in `stats` and, in `removed`, the nodes whose values were just cleared (offline, no data, 404, no longer a favorite; clients delete them and show an empty row): sent when a request starts (`checking` set, `next` moved on) and when it ends, and whenever the favorites change the `next` node (`checking` cleared, the node's `checkedAt` and the progress updated; `stats` only if its values changed), and when the scan is switched on or off (then `enabled` says which; off clears all values). `checkedAt` also carries only the changed nodes. Clients merge it into the snapshot. |

The server sends a comment line (`: ping`) every 15 s. While at least one client is connected the server polls node status about once per second; otherwise it slows down.

## Configuration file

`/etc/aslcp/aslcp.conf`, Asterisk-style INI (parsed with `internal/astconf`):

```ini
[server]
; ":PORT" or "ADDR:PORT"; the default port is 8080
listen = :8080
datadir = /var/lib/aslcp

[ami]
; "auto" reads the first user from /etc/asterisk/manager.conf
host = 127.0.0.1
port = 5038
username = auto
secret = auto

[nodelist]
url = https://allmondb.allstarlink.org/
refresh = 24h

[helper]
; unix socket of the privileged helper (aslcp-helper.socket)
socket = /run/aslcp/helper.sock

[favstats]
; favorites activity scan: base URL of the stats API and requests per minute (1..25, default 20), and whether to log each request
;url = https://stats.allstarlink.org
;rate = 20
;log = false

[updates]
; base URL of the apt repository; <url>/latest.json = {"version","notes","date"}
; (empty disables release notes)
; optional override; the built-in default is https://artw1.github.io/aslcp
;url = https://artw1.github.io/aslcp
```

### Helper protocol (internal)

`aslcp-helper` listens on the socket (mode 0660, group `aslcp`) and also checks the peer uid (`SO_PEERCRED`: root or user `aslcp`). One JSON request per connection, one JSON response; operations are fixed and take no arguments: `update-status`, `update-check`, `update-apply` (`update-status` runs only `dpkg-query` and `systemctl show`, and reads the journal only while the job runs or once per finished job; `update-check` is kept for older servers and is the same as `update-status`, it no longer runs apt; `candidate` and `upgradable` are always empty/false; reply: `{"installed","candidate","upgradable","job","jobLog"}`) and `firewall-status`, `firewall-open` (reply: `{"present","running","serviceEnabled","port","portOpen","open"}`). The firewall operations take no arguments either: the helper reads the web port itself from `[server] listen` in `/etc/aslcp/aslcp.conf` (an invalid port is an error and nothing is changed). `firewall-status` queries the runtime and then the permanent configuration: `serviceEnabled` is the packaged `aslcp` firewalld service (TCP 8080, queried only when the port is 8080), `portOpen` is `<port>/tcp`, and `open` is true if either is. `firewall-open` adds the service for 8080 and `--add-port=<port>/tcp` for any other port, permanently, and reloads. Failures reply `{"error": "..."}`.

Configuration operations (`config-list`, `config-read`, `config-apply`, `config-log`, `config-show`, `config-restore`) carry the arguments in a `config` object; any other operation with a `config` field is rejected. Requests may be up to 4 MiB (others 4 KiB). The helper validates every path itself, runs `git` with a fixed argv and hardening options (history in `/var/lib/aslcp-config-history`, root only), preserves owner/mode, writes atomically and rolls back on error. `config-apply` fails with `conflict: <files>` when a `base` hash no longer matches. `asterisk-restart` runs `systemctl restart asterisk`.

System update operations (fixed apt/systemd commands, no arguments and no package names from the web process): `os-check` (`apt-get -q update`, then `apt-get -q -s upgrade`, both `LC_ALL=C`, noninteractive; reply `OSStatus`: `packages`, `keptBack`, `rebootRequired`, `rebootPkgs`, `job`, `jobLog`; a held lock answers `apt is busy`), `os-status` (no apt), `os-apply` (transient unit `aslcp-osupgrade`), `reboot` (`systemctl --no-block reboot`) and `aslcp-restart` (`systemctl --no-block restart aslcp.service`). `os-check`, `os-apply`, `reboot`, the ASLCP update and the channel switch are refused while an apt job runs.

System tab operations (typed, validated arguments in the request's `sys` object; always an argv vector, never a shell): `host-status` (`hostnamectl show`, `timedatectl show`), `host-set-hostname` (`hostnamectl set-hostname <label>`), `host-set-timezone` (checked against `timedatectl list-timezones`, then `timedatectl set-timezone <zone>`), `host-set-ntp` (`timedatectl set-ntp true|false`), `host-timezones`, `host-poweroff` (`systemctl --no-block poweroff`), `logs-read` (`journalctl -u <allowlisted unit> -n <50..1000> --no-pager -o short-iso`, optional `--since`/`-b`), `services-status` (`systemctl show` for the fixed unit list) and `net-status` (`ip -j addr`, `ip -j route`, `ip -j -6 route`, DNS from `/etc/resolv.conf` or `resolvectl status`). WiFi (1.3.2, `wifi` request object, NetworkManager through `nmcli`): `wifi-status`, `wifi-scan`, `wifi-list`, `wifi-connect` (the password travels in the request, is written to a 0600 keyfile and never appears in argv or logs), `wifi-forget`, `wifi-priority` and `wifi-hotspot-set`. The helper binary also has a root-only subcommand, `aslcp-helper wifi-watchdog`, run by `aslcp-wifi-watchdog.timer` every 30 seconds; it starts and stops the setup hotspot and needs no running web server. Each one is one Go file per feature (`internal/helper/sys_*.go`) registered with `registerSysOp`.

DVSwitch operations (no arguments; fixed scripts in transient units `aslcp-dvswitch-install` and `aslcp-dvswitch-uninstall`): `dvswitch-status`, `dvswitch-job`, `dvswitch-install`, `dvswitch-uninstall`, `dvswitch-stfu-install` (unit `aslcp-dvswitch-stfu`, step `stfu-plan`; refusal prefix `stfu_installed:`), `dvswitch-restore-discard` (deletes `/var/lib/aslcp-dvswitch/restore-pending`; refused with the busy error while a DVSwitch job runs). The units call back into the helper binary as `aslcp-helper dvswitch-step <install-plan|restore-apply|install-check|uninstall-plan>` (root only, not a socket op). Refusals come back as errors prefixed `already_installed:`, `unsupported_platform:`, `clock_unset:`, `key_mismatch:` or `not_aslcp_installed:`.

DVSwitch config operations (M4, take a `dvswitch` request object, rejected on every other op): `dvswitch-setup`, `dvswitch-info`, `dvswitch-identity`, `dvswitch-credentials`, `dvswitch-modes`, `dvswitch-startup`, `dvswitch-rollback`, `dvswitch-restart`, `dvswitch-usrp` and `dvswitch-private` (M2: the `[USRP]` keys of `Analog_Bridge.ini`; the record of the private node), `dvswitch-dmr-status` (read-only, takes no argument, so a `dvswitch` field is rejected), `dvswitch-bm` (`bm: {action: "save"|"clear", server?, password?, talkerAlias?, startTg?}`; errors also prefixed `stfu_not_installed:`, `no_identity:`, `no_password:`, `switch_failed:`; `dvswitch-credentials` also `bm_master:`), `dvswitch-lists` (`mode` enum `dmr|ysf|p25|nxdn`, maps to one fixed file). The file is an enum id, never a path. Errors are prefixed `invalid_field:`, `not_installed:`, `no_history:`, `no_info:`, `no_list:`; a base mismatch is the usual conflict error.

Backup operations: `backup-info` (plain request/response: `{parts:[{id,present,size}],sa818}`), `backup-create` and `backup-restore` (streaming). `backup-create` takes `backup:{history,allmon3,sounds,dvswitch}` and answers with one JSON line, the header (`arch`, `asl3Version`, `asteriskVersion`, `osId`, `osCodename`, `osName`, `channel`, `nodes`, `packages`, `parts`, `missingStock`, `skipped`, `entries` with per-file type, size and sha256), followed by a plain tar stream of the system files and EOF; an error before the header is an `{"error"}` line, a later failure cuts the stream short. `backup-restore` takes `restore:{dryRun,deleteMissing}` and is followed on the same connection by the gzip archive; the client half-closes after the last byte and reads one JSON result (`ok`, `files`, `steps`, node versions and Debian release, `sa818Port`/`sa818PortPresent`, `dvswitchNotInstalled`). The helper re-validates the archive as root, classifies conffiles with `dpkg-query` (never a shell), writes to fixed destinations only (including `/etc/aslcp/aslcp.conf`, since the server cannot write there), and undoes config changes if a step fails. Streaming requests may take up to 20 minutes; the helper stays alive while one runs.
