# Digital modes (DV)

Digital modes (DMR, YSF, P25, NXDN) run on the ASLCP engine, `aslcp-dv`, a small service that ships in the ASLCP package. ASLCP 2.0 replaces the previous third-party digital voice stack with it: that stack is no longer installed, configured or restarted by ASLCP.

Status in 2.0 milestone 1: the engine, the control socket, the vocoder link and the **Parrot** test mode (`LOOP`) are in. BrandMeister, TGIF, DMR - Other, YSF, P25 and NXDN show "Not yet available in this build"; their settings and favourites are kept and they are not lost on upgrade.

## Parts

```
browser  <-- REST + SSE -->  aslcp (user aslcp)
                               |-- ctl socket /run/aslcp-dv/ctl.sock --> aslcp-dv (user aslcp-dv)
                               |-- helper socket /run/aslcp/helper.sock --> aslcp-helper (root)
aslcp-dv <-- USRP UDP 34001/32001 --> Asterisk chan_usrp (the bridge node)
aslcp-dv <-- UDP 127.0.0.1:2470 --> md380-emu (the vocoder)
```

- `aslcp-dv` reads `/var/lib/aslcp-dv/config.json`. The root helper renders it (`dv-config`); ASLCP never writes it itself.
- The control contract (newline-delimited JSON, ops `status`, `reload`, `mode`, `tune`, `unlink`, `subscribe`) is in `internal/dv/ctl/types.go`.
- The server keeps one subscription to the engine's event stream, only while digital modes are enabled, with a reconnect backoff of 1 s doubling to 15 s. It builds the snapshot (`GET /api/dv`), the `dv` SSE event and the Now Calling strip from it.

## Enable and disable

`aslcp dv enable` (as root) or the Enable button of the digital modes page runs one job, `aslcp-dv-enable`:

1. apt installs the md380-emu vocoder, plus qemu-user-static on 64-bit and x86 systems (the vocoder is an ARM binary). Nothing else is installed.
2. The units of the previous stack that exist are stopped and disabled. Nothing is uninstalled or deleted; purging that stack is a later milestone.
3. The engine config is rendered and `aslcp-dv` is enabled and started.

`aslcp dv status [--json]` shows the state; `aslcp dv disable` stops the engine and keeps its settings. The command and the installer variable of 1.x keep working as aliases. In the installer, `ASLCP_DV=1` runs it.

Supported: Debian bookworm and trixie on amd64, arm64 and armhf (ARMv7). **ARMv6 boards (Pi Zero, Pi 1) are not supported**; the page says so and enable is refused with `409 not_supported`.

## Modes

| Mode | Id | In 2.0 M1 | Tune value | Unlink |
|---|---|---|---|---|
| Parrot | `LOOP` | available | parrot id (default 9990, private call) | `disconnect` |
| BrandMeister, TGIF, DMR - Other | `BM`, `TGIF`, `DMR` | not available | talkgroup | `4000` |
| YSF | `YSF` | not available | reflector id or `host:port` | `disconnect` |
| P25, NXDN | `P25`, `NXDN` | not available | talkgroup | `9999` |

A mode that is not available answers `409 not_available` to mode and tune. Parrot echoes what you transmit back through the bridge node, which tests the whole path (node, USRP, engine, vocoder) without a network account.

## Private bridge node and USRP

The engine talks to Asterisk through a **bridge node**: a private node with a USRP channel in `rpt.conf` (chan_usrp loaded in `modules.conf`). The page sets it up (`PUT /api/dv/private-node`, admin) as a plan that can be previewed with `dryRun`, then applied in this order:

1. `disconnect`: drop the node's links.
2. `asterisk_config`: write the node stanza, the `[nodes]` line and chan_usrp, with a history copy of each file.
3. `engine_config`: render the engine config with the node's USRP ports (defaults 34001 receive, 32001 transmit).
4. `asterisk_restart`: only when Asterisk must be restarted for chan_usrp.
5. `engine_reload`: `reload` on the control socket; if it does not answer, the helper restarts `aslcp-dv`.
6. `health`: the node is registered and the engine reports the USRP peer.

ASLCP keeps a record of what it changed (`privatenode.json`, root only), so Remove and Rollback undo exactly that; a node that already existed can be adopted without being owned. The default display name of the bridge node is "Digital modes linked node". The link between a local node and the bridge node is on demand (connected when you tune) or permanent, with an optional "disconnect other links first". There is no setup write for identity, networks or credentials in 2.0.

`GET /api/dv/setup` is read-only: `bridgeNode` and the files `rpt_conf` and `modules_conf` with `present`, `writable`, `sha256` and `history` (the number of config history commits that touch the file).

## Host lists

ASLCP downloads the DMR, YSF, P25 and NXDN host lists itself (the lists the pickers show), once a day with a random jitter, only while digital modes are enabled, with conditional requests. They are kept in `<datadir>/hostlists`. `GET /api/dv/lists?mode=ysf` returns them with an `ETag`; `POST /api/dv/lists/refresh` forces a check (at most once a minute). A YSF, P25 or NXDN tune is resolved against these lists on the server; a value that is not in the list is `422 unknown_target`.

The DMR user list (`DMRIds.dat`) is **not** downloaded: ASLCP shows names and callsigns from the radioid.net list it already keeps for Now Calling.

## Resume after restart

With "Resume after restart" on, a tracker re-sends the last mode and tune after the engine restarts. It reads the engine over the control socket, not from a file. The "boot mode" hook of 1.x is gone: there is no file to write, the engine keeps its own state.

## Migration from 1.x

Enabling digital modes stops the previous stack's units but leaves its files, packages and state directories in place (nothing is read from them except what a backup restore saved). A backup taken on 1.x restores its digital modes part as a pending set when the previous stack is not installed; it can be discarded with `DELETE /api/dv/restore-pending`. See docs/BACKUP.md.

## Audit

Writes are audited as `dv.enable`, `dv.disable`, `dv.mode`, `dv.tune`, `dv.unlink`, `dv.favorite.*`, `dv.private.*`, `dv.node_name`, `dv.link`, `dv.restore_discard`, `dv.resume` and `dv.lists.refresh`. Audit lines never contain a password, key or token.

REST paths, the SSE event name and the settings keys keep their 1.x names for now; see docs/API.md.
