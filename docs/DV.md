# Digital modes (DV)

Digital modes (DMR, YSF, P25, NXDN) run on the ASLCP engine, `aslcp-dv`, a small service that ships in the ASLCP package. ASLCP 2.0 replaces the previous third-party digital voice stack with it: that stack is no longer installed, configured or restarted by ASLCP.

Status in 2.0 milestone 5: the engine, the control socket, the vocoder link, the **Parrot** test mode (`LOOP`), **BrandMeister** (`BM`, a native Rewind client), **TGIF** and **DMR - Other** (native Homebrew clients) **NXDN**, **YSF** and **P25** (native reflector clients) are in. YSF carries voice in DN (V/D mode 2) and Voice FR (VW); P25 carries IMBE. All modes are available; settings and favourites from 1.x are kept and not lost on upgrade.

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

| Mode | Id | In 2.0 M5 | Tune value | Unlink |
|---|---|---|---|---|
| Parrot | `LOOP` | available | parrot id (default 9990, private call) | `disconnect` |
| BrandMeister | `BM` | available | talkgroup (a trailing `#` makes a private call) | `4000` |
| TGIF, DMR - Other | `TGIF`, `DMR` | available | talkgroup | `4000` |
| YSF | `YSF` | available (DN, VW) | reflector id or `host:port` | `disconnect` |
| P25 | `P25` | available | talkgroup | `9999` |
| NXDN | `NXDN` | available | talkgroup | `9999` |

Availability comes from the engine (which modes the running build implements); while the engine is down the page falls back to a fixed list (Parrot, BrandMeister, TGIF, DMR - Other, YSF, P25 and NXDN). A mode that is not available answers `409 not_available` to mode and tune; an available one that is not set up answers `409 mode_disabled`. Parrot echoes what you transmit back through the bridge node, which tests the whole path (node, USRP, engine, vocoder) without a network account.

## BrandMeister

BrandMeister is a native client of the Rewind protocol: the engine logs in to a BrandMeister master with your DMR ID and a hotspot password, and carries the audio between the bridge node and the network. There is no separate bridge program and no ini file to edit.

**Settings** (Digital modes tab, admin; docs/API.md "Digital modes networks and identity"):

- *Identity*: callsign, DMR ID and an optional repeater ID. The DMR networks log in with it. Clearing it disables them.
- *Master address* and *port* (default 54005). There is no default address: pick the master from your BrandMeister account (the closest one) and enter it.
- *Password*: the hotspot password of your BrandMeister account. It is write-only; the page shows only "Password set" and a Change button, and the password is never logged or returned.
- *Options*: application ID (`client_id`), repeater ID, time slot, colour code, super header, and under "advanced" the client version and the keep-alive, timeout and back-off timers. A blank option uses the engine default.
- *Enable* needs the address, the password and the identity; the page says which one is missing. Saving renders the engine config and reloads the engine, so a login attempt starts at once; the card shows the login state (pending, logged in, or refused with the master's reason).

**Application ID.** BrandMeister requires a registered application ID for this client; until one is set in Options, logins may be refused. A refusal shows on the card as "Login failed".

**Using it.** On the dashboard, choose BrandMeister, type a talkgroup and press Tune, or pick a favourite. The *Private call* switch (or a trailing `#`) calls the number as a private call. **Parrot** calls TG 9990 as a private call: key up, release, and the master plays your transmission back. That tests the whole path over the real network without disturbing a talkgroup.

**Upgrade from 1.x.** The BrandMeister enable state in the 1.x `networks.json` is kept. The master address, password and application ID of the old bridge program's ini are not read from it: enter them once on the BrandMeister card.

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

Writes are audited as `dv.enable`, `dv.disable`, `dv.mode`, `dv.tune`, `dv.unlink`, `dv.favorite.*`, `dv.network`, `dv.identity`, `dv.private.*`, `dv.node_name`, `dv.link`, `dv.restore_discard`, `dv.resume` and `dv.lists.refresh`. Audit lines never contain a password, key or token.

REST paths, the SSE event name and the settings keys keep their 1.x names for now; see docs/API.md.

## TGIF

TGIF is a native Homebrew (MMDVM) client: the engine logs in to a TGIF master with your DMR ID and the hotspot security password of your TGIF profile.

- *Master address* and *port*: optional. Left empty, the public master `tgif.network` port 62031 is used (the address field shows it as a placeholder).
- *Password*: write-only, like the BrandMeister password; the page shows only "Password set" and a Change button.
- *Options*: the master options string (for example static talkgroups, sent after login), the time slot (default 2), and under "advanced" the repeater ID, colour code, link by short key-up, unlink talkgroup, frequencies, location, description, URL and the keep-alive, timeout and back-off timers. A blank option uses the engine default.
- *Enable* needs the password and the identity (callsign and DMR ID). The card shows the login state.

On the dashboard, choose TGIF, type a talkgroup and press Tune, or pick a favourite. With *link by short key-up* on (the default), a short key-up on a talkgroup is enough for the master to link it.

## DMR - Other (Homebrew master)

DMR - Other connects to any Homebrew (MMDVM) master, for example a FreeDMR, DMR+ or hblink3 server. It has the same settings as TGIF, but there is no default: enter the master address, port (default 62031) and the password the operator gave you. *Enable* needs the address, the password and the identity. TGIF and DMR - Other can be enabled at the same time; each has its own login.

## NXDN

NXDN is a native reflector client. It has no password and no login: the engine registers with the reflector by sending polls, using the callsign of the identity. Set your NXDN unit ID (1-65535) in Options to transmit. Under "advanced" are the RAN, the local listen address and the poll and back-off timers. *Enable* needs the identity. On the dashboard, tune a reflector talkgroup; unlink is `9999`.

## YSF (System Fusion)

YSF is a native reflector client for System Fusion reflectors (YSFReflector style). It has no password and no login: the engine polls the reflector to keep the link up and uses the callsign of the identity as the node callsign, so *Enable* needs the identity.

- *Reflector address* and *port*: optional. Normally leave them empty and tune a reflector on the dashboard, picked from the YSF list (the port defaults to 42000). A reflector set here is linked when the engine starts.
- Under "advanced": the poll period, the link timeout and the call end timeout.
- Voice is received in **DN (V/D mode 2)** and in **Voice FR (VW)**; a call may use either. Your transmissions are always sent in DN. Wires-X is not supported.
- VW is IMBE-coded and decoded by the built-in pure-Go IMBE codec (see "Voice codecs"); DN uses the md380-emu vocoder.

On the dashboard, choose YSF, pick a reflector from the list (or type a 5-digit id or `host:port`) and press Tune; unlink is `disconnect`. The card shows "retrying" while the reflector does not answer.

## P25

P25 is a native reflector client for P25Reflector style reflectors (UDP, the link P25Gateway and MMDVMHost speak). It has no password and no login: the engine polls the reflector to keep the link up, and the link is "ok" once the reflector answers. The callsign of the identity is the node callsign, so *Enable* needs the identity.

- *Reflector address* and *port*: optional. Normally leave them empty and tune a talkgroup on the dashboard, picked from the P25 list, which names the reflector (the port defaults to 41000). A reflector set here is linked when the engine starts.
- *P25 unit ID* (Options, 1-16777215): the source ID sent with your transmissions. Default: the DMR ID of the identity.
- Under "advanced": the poll period, the link timeout and the call end timeout.
- Voice is IMBE (full-rate Phase 1), carried in both directions. The source callsign is not sent on P25, so a received call shows the unit ID.

On the dashboard, choose P25, pick a talkgroup from the list (or type up to five digits) and press Tune; unlink is `9999`. The card shows "retrying" while the reflector does not answer.

## Voice codecs

DMR, NXDN and YSF DN use AMBE+2 through the md380-emu vocoder; P25 and YSF Voice FR use IMBE, which the engine encodes and decodes itself in pure Go (no extra service). Every frame a network hands the bridge carries its codec, so one YSF call may be DN and the next VW. The bridge encodes your transmissions with the codec of the active network (P25: IMBE; everything else AMBE). A frame of the wrong length for its codec is dropped. IMBE is computed on the CPU of the node: a 20 ms frame (encode plus decode) takes well under a millisecond on a current x86 core, and the engine runs at most one IMBE encoder and one IMBE decoder at a time.
