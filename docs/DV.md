# Digital modes (DV)

Digital modes (DMR, YSF, P25, NXDN) run on the ASLCP engine, `aslcp-dv`, a small service that ships in the ASLCP package. ASLCP 2.0 replaces the previous third-party digital voice stack with it: that stack is no longer installed, configured or restarted by ASLCP.

> **You have to redo the digital-modes configuration in 2.0.** The BrandMeister, TGIF, DMR - Other, YSF, P25 and NXDN settings and your identity (callsign and DMR ID) are not carried over from the previous stack, and nothing is imported from its ini files. The previous stack is removed on upgrade, except md380-emu. See "Upgrading from 1.x" below.

Status in 2.0.0: the engine, the control socket, the vocoder link, the **Echo test** (`LOOP`, a local audio test), **BrandMeister** (`BM`, a native Rewind client), **TGIF** and **DMR - Other** (native Homebrew clients), **NXDN**, **YSF** and **P25** (native reflector clients) are all in. YSF carries voice in DN (V/D mode 2) and Voice FR (VW); P25 carries IMBE. All modes are available.

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
2. The previous stack that ASLCP installed is removed (see "Upgrading from 1.x"); this step is also the retry if the removal after the upgrade failed.
3. The engine config is rendered and `aslcp-dv` is enabled and started.

`aslcp dv status [--json]` shows the state; `aslcp dv disable` stops the engine and keeps its settings. The command and the installer variable of 1.x keep working as aliases. In the installer, `ASLCP_DV=1` runs it.

Supported: Debian bookworm and trixie on amd64, arm64 and armhf (ARMv7). **ARMv6 boards (Pi Zero, Pi 1) are not supported**; the page says so and enable is refused with `409 not_supported`.

## Modes

| Mode | Id | In 2.0.0 | Tune value | Unlink |
|---|---|---|---|---|
| Echo test | `LOOP` | available, always on | none (started from setup, not tuned) | none |
| BrandMeister | `BM` | available | talkgroup (a trailing `#` makes a private call) | `4000` |
| TGIF, DMR - Other | `TGIF`, `DMR` | available | talkgroup | `4000` |
| YSF | `YSF` | available (DN, VW) | reflector id or `host:port` | `disconnect` |
| P25 | `P25` | available | talkgroup | `9999` |
| NXDN | `NXDN` | available | talkgroup | `9999` |

Availability comes from the engine (which modes the running build implements); while the engine is down the page falls back to a fixed list (Echo test, BrandMeister, TGIF, DMR - Other, YSF, P25 and NXDN). A mode that is not available answers `409 not_available` to mode and tune; an available one that is not set up answers `409 mode_disabled`. The Echo test is described below.

## Echo test

The Echo test is a local test with no network and no account: the engine records one transmission, waits about a second and plays it back through the bridge node as a received call from the pseudo callsign "ECHO TEST" (no talkgroup, no DMR ID). It proves the audio path of the node, USRP, the engine and the md380 vocoder in both directions. Its engine mode id is `LOOP`; it is always enabled while digital modes are enabled, so it has no Enable card, and a `LOOP` entry in an older `networks.json` is ignored. Not to be confused with the **BrandMeister Parrot** (TG 9990 on BrandMeister), a service of the real network that is called from the BrandMeister mode on the dashboard.

**Using it.** Digital modes setup, *Test audio*: press *Start echo test*, key up, talk, unkey, and you hear yourself back a second later. While it runs the dashboard card says "Echo test running" (it has no mode button there) and *Now calling* and *Last heard* show the playback as "Echo test". *Stop* switches back to the mode that was in effect before you started (the page remembers it; failing that, the last mode from `GET /api/dv/resume`, then the first enabled mode), and that mode's remembered talkgroup or reflector is tuned again, as when you switch modes on the dashboard. The Echo test is never remembered as the last mode, so a restart does not come back in it. Starting and stopping use `POST /api/dv/mode` (operator or admin).

## BrandMeister

BrandMeister is a native client of the Rewind protocol: the engine logs in to a BrandMeister master with your DMR ID and a hotspot password, and carries the audio between the bridge node and the network. There is no separate bridge program and no ini file to edit.

**Settings** (Digital modes tab, admin; docs/API.md "Digital modes networks and identity"):

- *Identity*: callsign, DMR ID and an optional ESSID (2 digits, 01–99; the repeater ID is your DMR ID followed by it). The DMR networks log in with it. Clearing it disables them.
- *Master*: picked from a dropdown of the BrandMeister masters (entries named `BM_<id>_<Country>` in the DMR host list the node downloads daily, shown as "3104 – United States", sorted by id); pick the one closest to you. There is no port setting: the engine always uses 54006 (the port in the host list is the Homebrew port and is not used), and a port stored by an earlier version is ignored. If the list has not been downloaded yet, the card says so and shows an *Other address* field instead; *Other…* in the dropdown does the same for a master that is not in the list.
- *Password*: the password set in your BrandMeister SelfCare (hotspot security). It is write-only; the page shows only "Password set" and a Change button, and the password is never logged or returned.
- *Options*: all under "advanced": the client version and the keep-alive, timeout and back-off timers. A blank option uses the engine default.
- *Enable* needs the address, the password and the identity; the page says which one is missing. Saving renders the engine config and reloads the engine, so a login attempt starts at once; the card shows the login state (pending, logged in, or refused with the master's reason).

**Login.** The engine logs in to BrandMeister as an Open DMR Terminal with your DMR ID and your BrandMeister password; no application ID is needed. A refusal (wrong password) shows on the card as "Login failed".

**Using it.** On the dashboard, choose BrandMeister, type a talkgroup and press Tune, or pick a favourite. The *Private call* switch (or a trailing `#`) calls the number as a private call. The dashboard's **Parrot** button (BrandMeister's Parrot, not the Echo test) calls 9990 as a private call: key up, release, and the master plays your transmission back as a private call from 9990 to you. That tests the whole path over the real network without disturbing a talkgroup. **Unlink** tunes TG 4000, BrandMeister's disconnect talkgroup.

**Upgrade from 1.x.** Nothing of the old bridge program is imported: pick the master and enter the password once on the BrandMeister card.

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

The tracker remembers the last tune of each mode and the last mode used. A disconnect (an unlink value, or the Unlink button) erases the tune of that mode but keeps the mode: after a restart the engine comes back in that mode with nothing linked (the log says "dv resume: YSF selected, nothing linked"). This is a plain mode switch, nothing is sent to the network, and a disconnected target is never linked again. A mode that is switched off is skipped, and nothing is resumed more than a few minutes after the engine started.

A tune, a mode switch or an unlink made on the dashboard is remembered at once; changes made elsewhere are picked up by the tracker's next poll.

## Upgrading from 1.x

**You have to redo the digital-modes configuration in 2.0.** Settings of the previous stack (BrandMeister, TGIF, DMR - Other, YSF, P25 and NXDN, and the identity) are not carried over and nothing is imported from its ini files, or from the ini files in a 1.x backup. Whatever the 1.4.0 panel itself stored in its own `networks.json` (enable switches, TGIF and DMR - Other address, port and password) is still read, so cards may show it filled in: check every card.

On upgrade from a version before 2.0.0 the package starts the transient unit `aslcp-dv-purge` once the upgrade has finished (`journalctl -u aslcp-dv-purge`; `aslcp dv enable` repeats it). It acts only on what ASLCP installed, which the root-only install marker in `/var/lib/aslcp-dvstate` records, and does nothing on a node where ASLCP did not install the old stack:

1. It deletes ASLCP's own ini history copies of the old stack (`/var/lib/aslcp-dvstate-history` and the 1.x name of that directory).
2. It waits until dpkg is idle, then stops and disables the old stack's units (`analog_bridge`, `mmdvm_bridge`, `netcheck`, `ysfgateway`, `p25gateway`, `nxdngateway`, `stfu`) that exist.
3. It marks md380-emu and qemu-user-static as manually installed, simulates `apt-get purge` of the old stack's packages recorded in the marker, and refuses (removing nothing) if the simulation would remove anything else.
4. It purges those packages, checks that md380-emu is still installed, and drops the purged names from the marker.
5. The apt source and keyring stay, because md380-emu is installed from that repository and keeps getting its updates. md380-emu stays installed and working.

Nothing is removed that ASLCP did not install, and md380-emu is never touched. A backup made on 1.x still restores (its ini files are ignored, see docs/BACKUP.md).

## Audit

Writes are audited as `dv.enable`, `dv.disable`, `dv.mode`, `dv.tune`, `dv.unlink`, `dv.favorite.*`, `dv.network`, `dv.identity`, `dv.private.*`, `dv.node_name`, `dv.link`, `dv.resume` and `dv.lists.refresh`. Audit lines never contain a password, key or token.

REST paths, the SSE event name and the settings keys keep their 1.x names for now; see docs/API.md.

## TGIF

TGIF is a native Homebrew (MMDVM) client: the engine logs in to a TGIF master with your DMR ID and the hotspot security password of your TGIF profile.

- *Master address* and *port*: optional. Left empty, the public master `tgif.network` port 62031 is used (the address field shows it as a placeholder).
- *Password*: write-only, like the BrandMeister password; the page shows only "Password set" and a Change button.
- *Options*: the master options string (for example static talkgroups, sent after login), the time slot (default 2), and under "advanced" the repeater ID, colour code, link by short key-up, unlink talkgroup, frequencies, location, description, URL and the keep-alive, timeout and back-off timers. A blank option uses the engine default.
- *Enable* needs the password and the identity (callsign and DMR ID). The card shows the login state.

On the dashboard, choose TGIF, type a talkgroup and press Tune, or pick a favourite. With *link by short key-up* on (the default), a short key-up on a talkgroup is enough for the master to link it.

## DMR - Other (Homebrew master)

DMR - Other connects to any Homebrew (MMDVM) master, for example a FreeDMR, DMR+ or hblink3 server. It has the same settings as TGIF, but there is no default master. Pick one from the *Master* list (the DMR host list, without the BrandMeister and TGIF entries, which have their own cards); the address and port are filled in, and so is the list's password when the password field is empty and none is stored. Choose *Other...* to type the address and port (default 62031) yourself. The dashboard card names the master in its status line ("Logged in to <name>", or "Master <name>" before the login succeeds): the host list name when the saved address is on the list, else the address as typed; BrandMeister shows its master the same way, TGIF has a single master and shows none. On Homebrew masters the caller's DMR ID is not a callsign: *Now calling* and *Last heard* take the callsign from the radioid.net list and show the DMR ID when the list has no entry. A master address you saved earlier selects its list entry, or *Other...* when it is not in the list. When no password is set, `passw0rd` is used to log in (the field shows it as a hint); a password you type always wins. *Enable* needs the address and the identity. TGIF and DMR - Other can be enabled at the same time; each has its own login.

**DMR+ masters** (`DMR+_IPSC2-...` entries on port 55555 in the host list): one dynamic talkgroup is tuned at a time, by the short key-up that the tune sends; there are no static talkgroups. The master moves the talkgroup to its own timeslot (TG 759 sent on slot 2 can come back on slot 1), so DMR - Other receives calls on both slots (option `rx_any_slot`, default on for DMR - Other, off for TGIF); while a call is in progress, packets of the other slot are ignored. Your own transmissions stay on the card's slot. Now calling and Last heard show the slot (TS1, TS2). The master drops a key-up talkgroup after about 15 minutes without traffic (yours included); after 15 minutes without a call on it the dashboard card says so and offers a *Retune* button, which sends the same tune again. It never retunes by itself.

## NXDN

NXDN is a native reflector client. It has no password and no login: the engine registers with the reflector by sending polls, using the callsign of the identity. Set your *NXDN ID* (1-65535) in Options; it is required to enable NXDN. Under "advanced" are the RAN, the local listen address and the poll and back-off timers. *Enable* needs the identity and the NXDN ID. Clearing a field and saving clears it (an empty option is dropped and its default applies). On the dashboard, tune a reflector talkgroup; unlink is `9999`.

## YSF (System Fusion)

YSF is a native reflector client for System Fusion reflectors (YSFReflector style). It has no password and no login: the engine polls the reflector to keep the link up and uses the callsign of the identity as the node callsign, so *Enable* needs the identity.

- The card is enable/disable only: there is no address or port. Enable it, then tune a reflector on the dashboard, picked from the YSF list. Nothing is linked when the engine starts. An address or port stored by an earlier 2.0 build is ignored and dropped.
- Under "advanced": the poll period, the link timeout and the call end timeout.
- Voice is received in **DN (V/D mode 2)** and in **Voice FR (VW)**; a call may use either. Your transmissions are always sent in DN. Wires-X is not supported.
- VW is IMBE-coded and decoded by the built-in pure-Go IMBE codec (see "Voice codecs"); DN uses the md380-emu vocoder.

On the dashboard, choose YSF, pick a reflector from the list (or type a 5-digit id or `host:port`) and press Tune; unlink is `disconnect`. The card shows "Reflector not answering" (a warning) when the reflector has sent nothing back, not even a poll reply, for the link timeout (15 s by default), and clears it as soon as anything arrives.

## P25

P25 is a native reflector client for P25Reflector style reflectors (UDP, the link P25Gateway and MMDVMHost speak). It has no password and no login: the engine polls the reflector to keep the link up, and the link is "ok" once the reflector answers. The callsign of the identity is the node callsign, so *Enable* needs the identity.

- The card is enable/disable only: there is no address or port. Enable it, then tune a talkgroup on the dashboard, picked from the P25 list, which names the reflector. Nothing is linked when the engine starts. An address, port or P25 unit ID stored by an earlier 2.0 build is ignored and dropped.
- The DMR ID of the identity is always the P25 unit ID sent with your transmissions.
- Under "advanced": the poll period, the link timeout and the call end timeout.
- Voice is IMBE (full-rate Phase 1), carried in both directions. The source callsign is not sent on P25, so a received call shows the unit ID.

On the dashboard, choose P25, pick a talkgroup from the list (or type up to five digits) and press Tune; unlink is `9999`. The card shows "Reflector not answering" in the same way.

## Voice codecs

DMR, NXDN and YSF DN use AMBE+2 through the md380-emu vocoder; P25 and YSF Voice FR use IMBE, which the engine encodes and decodes itself in pure Go (no extra service). Every frame a network hands the bridge carries its codec, so one YSF call may be DN and the next VW. The bridge encodes your transmissions with the codec of the active network (P25: IMBE; everything else AMBE). A frame of the wrong length for its codec is dropped. IMBE is computed on the CPU of the node: a 20 ms frame (encode plus decode) takes well under a millisecond on a current x86 core. Measured on a Raspberry Pi 3B+ (arm64, Go build): encode plus decode about 2.4 ms per 20 ms frame (about 12% of one core), encode alone about 3.0 ms, decode alone about 0.85 ms, and barely changed (+3%) while also receiving DMR. The engine runs at most one IMBE encoder and one IMBE decoder at a time.
