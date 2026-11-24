# Plan: MMDVM hotspot hybrid (ASL3 + ASLCP + an MMDVM hotspot hat on one Pi)

Status: design note, no code yet. Branch try/mmdvm-hybrid. This note is for the owner to decide on; nothing here is built.

The idea: take a spare Raspberry Pi with an MMDVM hotspot hat (the kind that used to run a hotspot image), put 64-bit Raspberry Pi OS, ASL3 and ASLCP on it, and let `aslcp-dv` drive the hat. Digital radios on RF then talk to AllStar through the bridge node, and to the internet digital voice networks through the engine's existing clients.

Things marked "to verify" are claims about external software (MMDVMHost, the modem firmware, the serial protocol) that this repo cannot confirm. Check them against the upstream sources before building on them. Everything else cites a file in this repo.

---

## A. Goal and scope

### A.1 Goal

- One Pi runs ASL3, ASLCP, `aslcp-dv` and the hotspot hat.
- A licensed operator keys a DMR, YSF, P25 or NXDN radio on the hotspot frequency. The call goes to the tuned internet network and to the AllStar bridge node.
- A call from the network or from AllStar is sent out on RF.
- Set-up, status and control are in the ASLCP panel (the Digital modes page, the dashboard) and the `aslcp` CLI. No hand-edited ini files.

### A.2 Hardware assumed

- A Raspberry Pi with a hotspot hat: ADF7021 radio chip, MMDVM_HS style firmware, wired to the Pi UART (an MMDVM_HS_Hat, ZUMspot or JumboSPOT style board or a clone). It is simplex, one frequency, low power (milliwatts).
- 64-bit Raspberry Pi OS Lite (Debian bookworm or trixie). ASLCP refuses armhf (README.md "Requirements"; docs/DV.md "Enable and disable", status reason `armhf_unsupported`). A Pi that ran a 32-bit hotspot image must be reflashed. Back up what you need first; nothing carries over.

### A.3 Non-goals

- **No analog FM on the hat.** The ADF7021 cannot transmit analog FM here. The RF side is digital only. Analog RF users need a separate USB sound interface and an FM radio as an ordinary AllStar port. That is a normal ASL3 setup and is out of scope here.
- **No full repeater boards in v1.** Duplex boards, Pi-hat repeater boards with external amplifiers and cavity duplexers are not targets. The design should not make them impossible later (see section G).
- **No Wires-X.** The YSF client does not implement it (internal/dv/network/ysf/ysf.go header comment). Nothing changes.
- No DMR cross-slot tricks, no DMR data, no GPS/APRS beacons, no text messaging. Voice only.
- No firmware flashing from the panel in v1. The hat arrives with working firmware. A "how to flash" note may come later.
- No hosting of a public Homebrew master or reflector. Any listener is on loopback only.

---

## A2. Direction: a Pi-Star replacement

The owner's longer-term view: ASLCP as a one-package replacement for the hotspot image the Pi used to run, with AllStar connectivity on top. That changes how the integration choice (section B) should be weighed. A product that is "one package, one panel, one config" does not fit well with a second, C++ program that has its own ini file and its own update path.

### A2.1 Gap table

"Pi-Star" here means the usual hotspot image and its tools (MMDVMHost, DMRGateway, YSFGateway and the like). What each does is from general knowledge and is to verify against the upstream projects. The "ASLCP today" column is checked against this repo.

| Pi-Star feature | ASLCP today | Needed |
|---|---|---|
| MMDVM modem control, RF calibration (frequency offset, levels, BER check) | Nothing. No serial code in the repo. | Modem driver (section B), offset and level settings, a receive-only BER/level check page. |
| Multi-mode hotspot with mode hang (one RF mode at a time, hang time after a call) | One active mode at a time, a mode switch is refused while a call is in progress (internal/dv/bridge/router.go `switchMode`). No RF. | RF endpoint and hang time (C.4, C.5). Mode scan later. |
| Internet networks: BM, TGIF, Homebrew masters, YSF, P25, NXDN, M17 | All native clients, with tests (internal/dv/network/*). | Nothing new for networks. |
| DMRGateway-style rewrite rules, several DMR networks at once | One active network (`active()` in router.go). BM, TGIF and DMR - Other can all be logged in at once, but only the active one carries calls. | A routing layer with rules (talkgroup/slot maps, per-network prefixes). Large; not v1. |
| Cross-mode (YSF2DMR, DMR2YSF, YSF2NXDN, ...) | None. | AMBE+2 repack for DMR, YSF DN, NXDN (C.3); a PCM transcode for P25 and Voice FR. Not v1. |
| Host list updates | Daily download of the DMR, YSF, P25, NXDN and M17 lists (docs/DV.md "Host lists"). | Nothing. |
| Last heard, live dashboard | Now calling, Last heard, SSE event `dv` (docs/DV.md "Parts"). | "via RF" source tag, RF state card (E.3). |
| Wi-Fi setup and a setup hotspot | Scan, add, reorder, and a setup network `ASLCP-<hostname>` when no network is found (README.md "WiFi"). | Nothing. |
| Config backup and restore | Present (docs/BACKUP.md). | RF settings with `transmit` forced off on restore (E.6). Import from a Pi-Star backup (A2.3). |
| Updates | Update page and apt source (README.md). | Nothing. |
| POCSAG / DAPNET paging | None. | Out of scope for v1. A pager network client would be new, and the modem protocol has pager frames (to verify). Ask whether it is wanted. |
| Nextion / OLED displays | None. | Out of scope for v1. A serial display driver is a separate project; the dashboard is the display. |
| Remote command, firewall, SSH access | A browser terminal, firewall service control and SSH are part of the System page (README.md). | Nothing for parity; the panel is already ahead here. |
| BrandMeister manager (static talkgroups, dynamic TG list) | Tune and favourites on the dashboard (docs/DV.md "BrandMeister"). No static talkgroup management through the BrandMeister API. | A static-talkgroup editor needs the BrandMeister API and an API key (to verify). Not v1. |
| "Expert editors" (edit every ini file by hand) | None, by design: settings are typed fields and the config is rendered. | Probably a read-only "show the rendered config" view, plus an `advanced` key/value passthrough per network. Decide how far to go; every raw editor weakens validation and the safety rules in section F. |

What this says:
- ASLCP already covers the network side, the host lists, the dashboard, Wi-Fi, backup and updates. The missing core is the modem and RF handling, the rest is optional features.
- The big functional gaps (DMRGateway rules, cross-mode, several networks at once) are routing features. They sit in the router, not in the modem driver, so they can come after the RF basics.

### A2.2 Should AllStar be optional?

Today ASLCP requires ASL3: the installer works on an ASL3 system and the bridge node is a node in `rpt.conf` (docs/DV.md "Private bridge node and USRP"). A hotspot-only install, without ASL3, would be possible only if the engine's USRP side became optional. This is an open question; no decision here.

For an optional AllStar:
- Smaller install. ASL3 is a large package set (Asterisk and friends, to verify sizes).
- No Asterisk process running for no reason; less to patch and to attack.
- A simpler first run for people who only want a hotspot. No node number, no AllStar account.
- The Pi-Star replacement story is clearer: "install ASLCP, get a hotspot".

Against:
- A second product shape to test and support: with and without ASL3. The panel, installer, backup and health checks all assume a node today.
- The router is built around USRP as one endpoint (router.go). A no-USRP mode means the engine must run with a missing peer, and the RF to network path must not depend on it. The e2e tests would need that mode.
- "ASL3 on the same Pi" is the stated goal of this note. Optional AllStar might dilute the project's identity.
- Packaging: ASLCP is an ASL3 add-on today, with a dependency on it (packaging/nfpm.yaml, to check the exact dependency lines). A hotspot-only package could be a different package name or a "recommends" change.

If it is made optional, the clean split is: `aslcp-dv` runs with the USRP endpoint disabled, and the panel hides the AllStar parts when no node is found. Section C already treats USRP as one of three endpoints, so the routing model holds either way.

### A2.3 Migration from a Pi-Star backup

The owner reflashes anyway (32-bit to 64-bit, section A.2), so migration is an import from a backup file taken before the reflash, not an in-place upgrade. The panel's restore flow already accepts an uploaded backup (docs/BACKUP.md). A Pi-Star import would be a second, read-only importer for a zip.

What could be imported (file names and keys are from memory, all to verify against a real backup):
- The backup zip's config files: `MMDVM.ini` (callsign, DMR ID, frequencies, colour code, offsets, modes enabled, modem port), `DMRGateway.ini` (network entries; passwords), `ysfgateway`, `p25gateway`, `nxdngateway` ini files (reflector settings), and a Pi-Star `config` file (to verify the name and format; it may hold the same data).
- A small set of values maps onto ASLCP settings: callsign, DMR ID and ESSID (Identity), RX/TX frequency, frequency offset, colour code, RAN, and the BrandMeister, TGIF or other master address and password.

Rules for an importer:
- Show a preview of what will be set, and let the owner untick items. Nothing applies without a click.
- Passwords are read once, stored in the same write-only way as today, and never shown or logged.
- Transmit stays off after an import (F). Frequencies are validated like typed ones.
- Whatever is not understood is listed, not dropped silently. Unknown keys are ignored, not carried.
- Talkgroup lists, static talkgroups, DMRGateway rewrite rules and display settings are not imported in v1 (they have no ASLCP equivalent yet, see A2.1).
- The importer is a parser with test fixtures made from synthetic files (N0CALL, DMR ID 1234567). It does not need a Pi. Real backups are used by the owner for the on-hardware check only.

### A2.4 What this means for the plan

- The one-package goal pushes toward option (b), the native Go modem driver (section B.4).
- It also argues for doing the routing features (rules, several networks) in the router, so a later Go driver does not need to be touched for them.
- Optional AllStar (A2.2) and the importer (A2.3) are new milestones (section G).

---

## B. Integration choice

Two ways to drive the hat. Both end in the same engine; the question is who speaks serial to the modem.

### B.1 What the engine already has

- Network clients that carry voice as codec frames, not RF bursts: Homebrew (DMR), YSF, P25, NXDN, M17, BrandMeister (internal/dv/network/*). Each implements `network.Network` (internal/dv/network/network.go).
- Air-interface coding, already byte-checked against MMDVMHost vectors:
  - DMR: bursts, BPTC, Golay, RS(12,9), embedded LC, slot type, SYNC, a TX superframe builder and an RX parser (internal/dv/dmr/doc.go).
  - YSF: sync, FICH, VCH and DCH coding, CSD (internal/dv/ysf/ysf.go).
  - NXDN: LICH, SACCH, FACCH1, convolutional code, scrambler, Layer 3 (internal/dv/nxdn/doc.go). The 72-bit AMBE+2 FEC is deliberately not implemented.
  - P25: no air-interface package. internal/dv/network/p25/p25.go builds the reflector records only. There is IMBE FEC code in internal/dv/imbe/fec.go, but the LDU framing, NID and trellis are not in the repo.
  - M17: LSF, stream and Codec2 framing (internal/dv/m17).
- Vocoders: md380-emu for AMBE+2, pure-Go IMBE, pure-Go Codec2 (docs/DV.md "Voice codecs").
- The router (internal/dv/bridge/router.go) has one active network at a time (`active()`, `switchMode`). A mode switch with a call in progress is refused with `busy` unless forced.

### B.2 Option (a): MMDVMHost as the modem driver, aslcp-dv as its network peer

MMDVMHost (G4KLX) owns the serial port, the modem protocol, RF framing and RF timing. aslcp-dv listens on loopback and MMDVMHost connects to it as if it were its usual gateway or master:

```
RF <-> hat <-UART-> MMDVMHost <-UDP loopback-> aslcp-dv <-USRP-> Asterisk
                       |                          |
                  MMDVM.ini                  config.json
                                                  ^-- internet networks
```

Peer endpoints (to verify against upstream; the repo only describes the wire formats it already speaks):

- DMR: aslcp-dv acts as a Homebrew *master* that MMDVMHost logs in to. The engine has only the client side (internal/dv/network/homebrew/doc.go). A master side means: answer RPTL with a salt, check RPTK, accept RPTC, answer pings, send and receive DMRD. About 300 lines, same packet formats.
- YSF: aslcp-dv acts as a gateway endpoint on a loopback UDP port; MMDVMHost sends YSFD frames to it. The "YSFD" format is already parsed by internal/dv/network/ysf/ysf.go.
- P25 and NXDN: the same, using the reflector style packets the engine already parses (p25.go, nxdn/network.go). The NXDN payload is the 33-byte frame with FEC already stripped by MMDVMHost (internal/dv/nxdn/doc.go).

Pros:
- The hard, hardware-specific part (serial protocol, modem config, per-mode RF framing, DMR slot timing, calibration quirks) is done by software that thousands of hotspots already use.
- The engine needs no RF coding for P25 (which it does not have) and nothing new for NXDN FEC.
- Fastest route to a working hotspot. Hardware-specific bugs are mostly already fixed upstream.
- Upstream tracks new firmware and mode changes.

Cons:
- MMDVMHost is C++ with no Debian package in the Debian archive (to verify). ASLCP ships arm64 and amd64 `.deb` files built with nfpm, Go with `CGO_ENABLED=0` (Makefile). Options: build MMDVMHost in CI for both arches and bundle it, or ask the owner to install it from another source. Bundling adds a C++ toolchain and cross-build to the release workflow.
- License: ASLCP is GPL-3.0 (LICENSE). MMDVMHost is GPL-2.0, with "or later" to verify per file. The repo already credits MMDVMHost-derived code as GPL-2.0-or-later in comments (internal/dv/ysf/ysf.go, internal/dv/nxdn/doc.go). Shipping the binary as a separate program in the same package is fine, as long as source and notices are provided (packaging/THIRD-PARTY-NOTICES). If MMDVMHost is GPL-2.0-only, it still runs as a separate process; do not link it.
- Two config files: MMDVM.ini and the engine config. ASLCP would render MMDVM.ini from its own settings and own it (users should not hand-edit it). Every MMDVMHost option the panel does not expose is lost, unless an "advanced" passthrough is added.
- Another process to supervise: unit ordering (MMDVMHost needs the engine's loopback ports up, or retries), restarts, logs, a second hardening profile. It also needs the serial device, so it cannot share the engine's `PrivateDevices=yes` sandbox (packaging/systemd/aslcp-dv.service).
- MMDVMHost has its own idea of "one mode at a time, timeouts, hang time". The engine's router rules (section C) sit behind it. Two sets of timers can disagree. To verify: how MMDVMHost behaves when the network end goes quiet mid-call.
- Per-mode "gateway" semantics differ: MMDVMHost YSF mode expects a YSF gateway program for link management, not a reflector (to verify). The engine would have to play the gateway, including the reflector-side polls. The engine already does the reflector side.
- Debugging is two layers deep.

### B.3 Option (b): aslcp-dv speaks the modem protocol over the UART itself

A new Go package, say `internal/dv/modem`, opens the serial device, speaks the MMDVM serial protocol, and a new network-like endpoint ("RF") feeds the router.

```
RF <-> hat <-UART-> aslcp-dv (modem + RF framing + router) <-USRP-> Asterisk
                       |
                  config.json only
```

Pros:
- One binary, one config, one process, Go, `CGO_ENABLED=0`. Packaging stays as is. No GPL-2.0 binary to ship (we would still credit protocol knowledge, as the repo does now).
- Same-mode RF to network passes frames with no extra hop and no second state machine. Direct control of timers, hang time, contention.
- The RF layer reuses the DMR, YSF, NXDN and M17 coding already in the repo.
- Status is richer: modem state, RSSI and BER can go straight to the dashboard (if the firmware reports them, to verify).

Cons:
- The modem protocol is ours to get right (to verify against the firmware sources). Scope, from memory and all to verify:
  - Frame format: start byte, length, command, data. Commands for version/capabilities, status (modem state, TX/RX flags, buffer space per mode), set config, set mode, set frequency (ADF7021 hotspots have their own set-frequency command), calibration data, and per-mode data frames with a "lost frame" marker.
  - Buffer flow control: the firmware reports free FIFO space per mode; the host must not overrun it. DMR needs frames timed to slots (even for a simplex hotspot).
  - ADF7021-specific: RF frequency, frequency offset in ppm, per-mode deviation levels, RX/TX level. These matter for audio quality and for a clean signal.
  - Modes the firmware supports. M17: to verify (it depends on the firmware fork and version). Do not promise it.
- RF-side framing is missing for P25 (no air-interface package, see B.1). That is real work: LDU1/LDU2, NID, Golay/Hamming/RS, trellis for data, link control. P25 would come last.
- DMR on RF needs more than the existing burst coder: slot timing, the host-side sync and "data sync / voice sync" handling the modem expects (to verify). Cross-checking against real radios is only possible on the owner's hardware.
- No other implementation to lean on. A bug here can put a bad signal on the air (see section F).
- Calibration and level tuning, normally done in Pi-Star or MMDVMCal, must be redone (see B.5).

### B.4 Recommendation

**Build option (b), the native Go modem driver, as the product. Do not ship MMDVMHost.** Use an existing MMDVMHost only as a reference and as a comparison tool on the owner's bench.

Reasons, from the code and the one-package goal (section A2):

- **Product fit.** The aim is a one-package Pi-Star replacement. (a) means a bundled C++ build in a Go-only, `CGO_ENABLED=0` release pipeline (Makefile), a second GPL-2.0 binary and notice, a second config (MMDVM.ini) the panel must render and own, and a second process with its own sandbox. Those costs would stay for good, and a "temporary" bundled binary tends to stay.
- **Much of the RF work exists.** DMR burst coding, YSF FICH/VCH/DCH, NXDN layers and M17 framing are in the repo and were checked against MMDVMHost vectors (B.1). The missing pieces are the serial protocol and the P25 air framing, not everything.
- **The network side is not a reason for (a).** Under (a) the engine needs new loopback peers (a Homebrew *master*, YSF/P25/NXDN gateway endpoints). Under (b) it needs none: RF frames go straight into the router.
- **Behaviour.** One router owns timers, hang time and contention (section C). Under (a) MMDVMHost has its own and the two can disagree.
- **Risk.** The real risk of (b) is a bad signal on the air from a driver bug. That is handled by process, not by a second program: transmit off by default (F), receive-only first, low power and a dummy load for the first TX, and a side-by-side check against an MMDVMHost the owner already has (it can be the old hotspot image on another card).

So the order is:

1. **RF endpoint and router changes with a fake modem** (M1). No hardware, and the same for any driver.
2. **The Go modem driver, receive only, then TX, YSF first** (DN framing is complete in internal/dv/ysf), then DMR, then NXDN. P25 needs new air framing and M17 depends on the firmware (to verify); both come last, and only if wanted.
3. Panel, CLI, API (M4) and the migration importer land alongside.

What would change my mind: if the owner needs P25 on RF soon, (a) gets it for free because MMDVMHost already has it. In that case ship (a) as an *optional, user-installed* add-on for P25 only, not bundled. Otherwise do not take (a) on.

Whichever driver is used, the router sees RF as one more endpoint with frames in and frames out (section C). The code boundary is the `Endpoint` interface of M1, so a different driver can sit behind it.

### B.5 Calibration

- Under (a), calibration is MMDVMHost's and the firmware's; ASLCP only renders the level and offset settings into MMDVM.ini. The owner calibrates the frequency offset with a known-good receiver, as for any hotspot.
- Under (b), which is recommended, the engine needs the modem calibration commands (to verify) and a simple "send a test tone / carrier for N seconds" tool. TX must stay off by default (section F).
- A generic "frequency offset (ppm)" and "per-mode TX/RX level" setting exists in both cases (section E).

---

## C. Routing model

### C.1 Three endpoints

```
                +----------------+
   RF  <------> |                | <------> USRP bridge node <---> ASL3 (analog and linked nodes)
 (the hat)      |  aslcp-dv      |           (voice PCM, internal/dv/usrp)
                |  router        |
                |                | <------> tuned internet network (BM, TGIF, YSF, P25, NXDN, M17)
                +----------------+
                        |
                   vocoder (md380-emu AMBE+2, pure-Go IMBE, pure-Go Codec2)
```

Today the router joins two endpoints: USRP and the single active network (internal/dv/bridge/router.go). The hybrid adds RF as a third.

### C.2 What passes how

| From | To | Path |
|---|---|---|
| RF, mode X | network, mode X | frames pass unchanged (same codec, same frame size). No vocoder. |
| network, mode X | RF, mode X | same, unchanged. |
| RF, mode X | AllStar (USRP) | decode to PCM with the vocoder of X, send as USRP voice. |
| network, mode X | AllStar | already done today (decode, USRP). |
| AllStar | network, mode X | already done today (encode, send). |
| AllStar | RF, mode X | encode with the codec of X, frame for RF. |

Rules:
- The RF mode must equal the tuned network's mode for the no-transcode path. v1 has exactly one active mode, which is both the network mode and the RF mode. That follows how the router already works (`r.mode`, one `active()` network).
- A voice frame already carries its codec (`network.VoiceFrame.Codec`, internal/dv/network/network.go). The pass-through case is a check that codecs match and the length is right (`codecLen` in router.go).
- A sample cost: one decode plus one encode per AllStar leg. The IMBE and Codec2 costs on a Pi 3B+ are in docs/DV.md "Voice codecs". md380-emu runs under emulation and is the expensive one; it is single-threaded (docs/PLAN-2.0-M1.md A.2).

### C.3 Cross-mode (not in v1)

Example: the radio is DMR on RF while the engine is tuned to YSF.

- **Repack, no transcode.** DMR, NXDN and YSF DN all carry the same 49-bit AMBE+2 frame (internal/dv/nxdn/doc.go: "the same 49-bit frame as the md380 vocoder"; internal/dv/ysf/ysf.go; internal/dv/dmr/doc.go). Only the FEC and framing differ. A DMR to YSF DN or NXDN bridge can copy the 7-byte frames and rebuild framing. This is cheap and lossless in the vocoder sense. It needs a call model that maps header data (source callsign, DMR ID, talkgroup) to the other side's fields. YSF Voice FR and P25 are IMBE, which is a different codec; AMBE+2 to IMBE needs a transcode.
- **Full transcode through PCM.** Works for any pair. Costs a decode and an encode per frame, plus a quality loss (a second lossy vocoder pass).
- **v1 does neither.** The RF mode follows the tuned mode. Switching mode on the dashboard (`aslcp dv mode`) switches the RF mode too. Repack for the AMBE+2 trio is a later milestone and an option (a "cross-mode bridge" switch, default off). Anything needing PCM goes through the AllStar leg, which already works.

### C.4 One RF mode at a time

A simplex hotspot listens on one frequency. The firmware (to verify) can scan modes by watching for each mode's sync, but this costs sensitivity and the mode-detect handling lives in the modem host. v1 is a fixed single mode: the one that is tuned. Mode scan ("any enabled mode on RF") is deferred. If added, it is a list of enabled RF modes, with the engine switching the network side to match the first mode that sees a call. That conflicts with the router's rule that a mode switch cuts a call (`switchMode`), so it needs its own design.

### C.5 Contention rules

Who holds the channel: one talker at a time, first come first served.

- RF talker active: network and AllStar audio for that call is dropped on the RF leg (nothing to transmit anyway; the hat is receiving). It still flows between the network and AllStar if they are linked, as today. The router currently lets a node transmission win over a received call (`startTX` calls `cutRX`). With RF in, the same rule would let an AllStar user cut a network call. Decide per source, see below.
- RF is transmitting a network or AllStar call: RF keying (a radio user pressing PTT) is ignored by the hotspot's own half-duplex nature. The RF user's call is lost until the TX ends. Hang time keeps the channel from flipping back and forth.
- Proposed priority for v1, highest first: local RF talker, AllStar talker, network talker. This matches the current "node wins over a received call" rule. To be confirmed by the owner (section H).
- **Hang time.** After a call ends on RF, hold the channel for a short time (example: 2 s) before sending the next queued call, so a reply on RF is not cut by an incoming network call. Configurable. Default value is an open question.
- **Timeouts.**
  - RF call: ends on terminator, or after the existing RX silence timeout (`DefaultRxSilence`, router.go), or when the modem reports lost sync.
  - TX timeout on RF: a hard cap on continuous transmit (example: 3 minutes), then stop and drop. The router already has `DefaultTxTimeout` for the node side; RF needs its own, and the RF one must be enforced even if the network side stalls.
  - A call that stalls mid-stream is ended by silence timeout, and TX is then dropped to idle. Never leave the carrier up.
- **What RF transmits when AllStar analog users talk.** The audio is encoded with the active mode's codec (AMBE+2 for DMR/YSF DN/NXDN, IMBE for P25). The call has no DMR ID, no DMR source. v1 sends the owner's identity (callsign and DMR ID from the ASLCP identity) as source and "ALL" or the tuned talkgroup as destination, as the network TX path does today (see `startTX` in router.go). Whether this is allowed is a licence question for the operator, see section F. A setting "send AllStar audio to RF" (default off) lets the owner keep the hotspot as RF-to-network only.
- **Loop prevention.**
  - An RF-originated call, once passed to the network or AllStar, must never come back out on RF. Each call carries a source tag (`rf`, `net`, `allstar`) in the router's call record (`call` struct, router.go); the RF endpoint refuses frames tagged `rf`.
  - A network that echoes our own transmission (reflector loops) is already a risk for the current engine; the call-id check in the router (`netCall`) does not cover it. An RF tag plus a short "same source id within N seconds" guard should.
  - AllStar side: the bridge node is private and not linked to the same network (docs/DV.md "Private bridge node and USRP"). The existing rules there apply.

---

## D. Hardware and OS set-up on the Pi

### D.1 Serial port

- The hat is on the Pi's primary UART (`/dev/ttyAMA0` or `/dev/serial0`, to verify per model and hat). The panel stores the device path; the default is `/dev/serial0`.
- `config.txt` (to verify the current names for the OS release):
  - `enable_uart=1`.
  - Pi 3, 4, Zero 2 W: the full UART is shared with Bluetooth. Disable Bluetooth with `dtoverlay=disable-bt` (or move Bluetooth to the mini UART, which is worse for timing), and `systemctl disable hciuart`.
  - Pi 5 uses a different UART header and device names (to verify).
- The serial console on the UART must be off: remove `console=serial0,115200` from `cmdline.txt` (raspi-config can do both; the helper should edit the files itself to be non-interactive).
- A reboot is needed after these edits. The installer must say so, and the panel must show "reboot needed" until it has happened.
- Modem baud rate: the firmware default (115200 on MMDVM_HS to verify). Configurable.

### D.2 GPIO

- Some hats have a reset line and a boot-mode line (for firmware flashing). v1 does not flash. A "reset modem" button is useful: it toggles the reset GPIO. The pin number depends on the hat (to verify per board). Optional config `modem.reset_gpio`, empty by default. In v1 only a power-cycle or a serial "reset" command (to verify) is offered.
- The engine must not need `/dev/gpiomem` for normal operation. If a reset line is used, a helper op toggles it (D.4).

### D.3 Firmware detection

On start, the engine (or MMDVMHost under (a)) asks the modem for its version (to verify the command). The panel shows:

- the firmware name and version string,
- the protocol version and which modes it reports (DMR, YSF, P25, NXDN, M17),
- an error state when nothing answers ("modem not responding on /dev/serial0").

Modes the firmware does not report are greyed out in the RF card. Do not assume M17 is there (to verify).

### D.4 Installer and helper

Follows the existing split: the web user never shells out; root does the system work (docs/PLAN-2.0-M1.md A.1).

New helper ops (names follow the existing `dv-*` style):

- `dv-rf-prepare`: check the OS is 64-bit, edit `config.txt` and `cmdline.txt` (with a history copy of each, as the private node ops do), disable the serial getty and hciuart where needed. Return "reboot needed".
- `dv-rf-device-check`: open the serial device read-write, send the version query, report the result. No transmit.
- `dv-rf-config`: render the engine config (and MMDVM.ini under (a)) from the helper state dir. Same pattern as `dv-config` (docs/DV.md "Parts").
- `dv-rf-install` (option (a) only): install the MMDVMHost package/binary, create its unit.

The engine runs as `aslcp-dv` with `PrivateDevices=yes` and no capabilities (packaging/systemd/aslcp-dv.service). Reaching the serial port needs a changed unit: `PrivateDevices=no` with `DeviceAllow=/dev/ttyAMA0 rw` and `DevicePolicy=closed`, and the user in group `dialout` (to verify the device group on current Raspberry Pi OS). Prefer a drop-in written by the helper only when RF is enabled, so non-RF installs keep the tight profile.

---

## E. Configuration and UI

### E.1 Settings

A new `rf` section in the engine config (internal/dv/config/config.go, `ctl.Config`), rendered by the helper. All fields default to off or empty. Example values only:

| Setting | Meaning | Example |
|---|---|---|
| `enabled` | RF endpoint on | false |
| `transmit` | allow transmit (a separate switch, see F) | false |
| `port` | serial device | `/dev/serial0` |
| `baud` | serial speed | 115200 |
| `rx_freq`, `tx_freq` | Hz; simplex hotspots use the same value | 433000000 (an example, not a recommendation) |
| `freq_offset_ppm` | crystal correction | 0 |
| `power` | output level, as the firmware defines it | 20 |
| `modes` | which modes may use RF | `["YSF","DMR"]` |
| per mode levels | RX/TX level, deviation (to verify names) | firmware defaults |
| `colour_code` | DMR colour code | 1 |
| `ran` | NXDN RAN | 1 |
| `dmr_slot` | DMR slot used by a simplex hotspot | 2 |
| `rf_id` | use Identity.DMRID, or `DMRID + ESSID` as the repeater ID | DMR ID 1234567 |
| `hang_ms`, `tx_timeout_s` | contention timers (section C.5) | 2000, 180 |
| `allstar_to_rf` | send AllStar audio to RF | false |

ID use: the existing identity is callsign N0CALL and DMR ID 1234567, with an optional 2-digit ESSID (docs/DV.md "BrandMeister"). Over RF, a radio has its own DMR ID. The hotspot's repeater ID towards the network is `DMRID + ESSID` as today (`repeater_id` option, internal/dv/network/homebrew/doc.go). Use a different ESSID from any other hotspot of the same person on the same master, or one drops the other.

### E.2 Digital modes page

A new card, "RF / hotspot", between "Bridge node" and "Networks":

- State line: modem found / not found, firmware, "TX off" or "TX on".
- Enable switch (saves at once; refuses with the reason when something is missing: identity, port, frequency).
- Modem port, RX and TX frequency (MHz, validated, see F), offset, power.
- Mode checkboxes, limited to what the firmware reports.
- "advanced": levels, colour code, RAN, slot, hang time, TX timeout.
- A separate **Allow transmit** switch, off until the frequency is set and the operator confirms (F.1).
- "Reboot needed" banner after `dv-rf-prepare`.
- A test button "Check modem" (read-only, no TX).

### E.3 Dashboard

- A small RF card: modem state, RF mode, "TX off" badge, carrier/receive indicator.
- *Now calling* and *Last heard* show "via RF" for RF-originated calls, next to the existing slot display (TS1, TS2). This needs a `source` field on `ctl.Talk` (internal/dv/ctl/types.go): `rf`, `net` or `allstar`. It is also the loop-prevention tag (C.5).
- Switching mode on the dashboard switches the RF mode (C.4). The existing busy rule applies: with a call in progress the switch is refused unless forced.

### E.4 CLI

Follows the current `aslcp dv ...` commands (docs/DV.md "Enable and disable"):

- `aslcp dv rf status [--json]`: modem, firmware, frequency, TX state.
- `aslcp dv rf enable|disable`
- `aslcp dv rf set --port ... --rx-freq ... --tx-freq ... [--offset-ppm ...]` (admin; rejects out-of-band frequencies).
- `aslcp dv rf check`: the read-only modem check.
- `aslcp dv rf tx on|off`: the transmit switch.

### E.5 API

Additions in the same style as docs/API.md "Digital modes":

- `GET /api/dv/rf`: settings (no secrets) and live state.
- `PUT /api/dv/rf` (admin): validated settings, audited as `dv.rf`.
- `POST /api/dv/rf/check` (admin): the modem check, rate limited.
- `PUT /api/dv/rf/transmit` (admin): the transmit switch, audited as `dv.rf.tx`.
- The `dv` SSE event carries RF state and the `source` on talks.
- New ctl ops (`internal/dv/ctl/types.go`): `rf_status`, `rf_check`; the settings go through `reload` as everything else does.

### E.6 Backup and restore

The RF settings are ordinary ASLCP settings (docs/BACKUP.md). On restore:
- The settings come back, but `transmit` is forced off until the owner turns it on again on the new machine. A restored frequency on a different board or country should not key up by itself.
- The OS edits (UART, config.txt) are not part of a backup. The page shows "run RF prepare" when they are missing.
- Under (a), MMDVM.ini is a generated file and is not backed up; it is rendered again.

---

## F. Regulatory and safety

The hotspot transmits. Only a licensed operator may do that, on frequencies and modes the licence allows.

- **Licensed operator only.** The RF card says so in plain words. Enabling RF needs a callsign in the identity (it already does for the networks). The callsign is checked for shape only; ASLCP cannot check a licence.
- **Transmit off by default.** Even with the modem enabled, RF only receives until the owner turns on *Allow transmit*. The modem is started in a receive-only state. A restore, an upgrade or a reset never turns transmit on.
- **Frequency validation.** Reject frequencies outside hotspot bands. Keep a small table of the bands where simplex hotspots are used (UHF and VHF amateur allocations; the table lives in code and is reviewed, because these differ by country). Reject a TX frequency outside the table, and ask the owner to confirm a country/band choice. The table and the confirmation are an open question (section H). Example frequency in docs: 433.000 MHz, clearly marked as an example.
- **Station ID.** Digital modes carry the callsign in the stream (DMR LC, YSF CSD, NXDN, P25), so the callsign in the identity is sent with every transmission. For AllStar-originated audio on RF, the owner is the control operator; whether the licence allows it is for the owner to decide (`allstar_to_rf` is off by default). The panel must not claim compliance.
- **Timeouts.** A hard TX timeout (default 3 min, maximum bounded in code), plus the silence timeout. The carrier is dropped on any engine error, on SIGTERM (the existing signal handler already sends keyup=0 for the USRP side, docs/PLAN-2.0-M1.md A.2; RF needs the same: tell the modem to stop TX before exit), and when the modem stops answering.
- **Watchdog.** If the engine stops (crash, kill -9), the modem may keep transmitting. The firmware has its own timeouts (to verify). The engine also sends keep-alive status requests and treats an unanswered request as a fault.
- **Limit power.** Let the owner set output power. The default is a low value.
- **Privacy.** No callsigns or IDs from live traffic in logs beyond what the engine already logs for calls.
- **Audit.** Every change to RF settings and to the transmit switch is audited (E.5).

---

## G. Phased plan

Each milestone is one commit or a short series; each states what it delivers and how it is tested. Hardware checks are done by the owner on the real hat.

### M0: this note

Decide (a) versus (b), the contention rules and the band table.

### M1: RF endpoint in the router, with a fake modem (no hardware)

- `internal/dv/rf`: an `Endpoint` interface (frames in and out, calls, state) and a fake modem that replays captured frames.
- Router: three-endpoint routing, `source` tag, loop guard, contention rules, hang time, TX timeout.
- `ctl.Talk.source`, `rf_status`.
- Tests: router tests (internal/dv/bridge/harness_test.go style) for RF-to-network pass-through, RF-to-USRP, USRP-to-RF, priority, hang time, loop prevention, TX timeout with a stalled network. Table tests with captured frames (testdata).
- Nothing reaches a radio.

### M2: Go modem driver, YSF, receive only

- `internal/dv/modem`: the serial protocol (frame parse and build, version, status, config, per-mode data, flow control) in pure Go, with a serial layer that keeps `CGO_ENABLED=0` (a pure-Go termios implementation, to verify the choice).
- Helper ops `dv-rf-prepare`, `dv-rf-device-check`, `dv-rf-config` (D.4). The unit drop-in for the serial device.
- YSF receive: RF frames to the tuned reflector and to AllStar. TX is not coded yet, or is hard-disabled.
- Tests: a fake modem on a pty speaking the protocol, captured byte traces from a real modem, fuzz on the frame parser, helper ops with a fake root.
- **Owner on hardware:** the modem check, then calls from a YSF radio showing in Last heard "via RF". No transmit.

### M3: YSF transmit

- Transmit path, the transmit switch, TX timeout, watchdog, keep-alive (F).
- Tests: router and modem tests for timeouts, stall and exit paths (the carrier is dropped on every one); loop and contention tests.
- **Owner on hardware:** a dummy load or very low power first; then one YSF call to a reflector and back. Compare with the old hotspot image on the same radio.

### M4: DMR

- DMR on RF through the existing burst coder (internal/dv/dmr), simplex slot handling, colour code, the repeater ID rules (E.1).
- Tests: captured DMR frames from the existing testdata, and new captured RF traces; slot timing tests against the fake modem.
- **Owner on hardware:** a DMR call with a real radio to the tuned network and to AllStar, and back.

### M5: panel, CLI and API for RF

- The RF card, dashboard badges, `aslcp dv rf ...`, API, audit, backup and restore (E.6).
- Tests: Go handler tests, Svelte component checks, a backup round trip that forces `transmit` off.

### M6: NXDN

- NXDN on RF. The 72-bit AMBE+2 FEC is not implemented in the repo (internal/dv/nxdn/doc.go) and is needed here.
- Tests: the existing NXDN vectors plus new FEC vectors from captured frames.
- **Owner on hardware:** one call, if the owner has an NXDN radio.

### M7: Pi-Star backup importer, and optional AllStar (decision first)

- The importer (A2.3), with synthetic fixtures. A preview, and no apply without a click.
- Optional AllStar (A2.2) only if the owner decides for it; it is its own plan.

### M8 (maybe): P25 RF framing (or an optional user-installed MMDVMHost for P25, B.4), M17 if the firmware supports it, mode scan, cross-mode AMBE+2 repack, a DMRGateway-style rules layer, duplex boards, POCSAG, displays.

---

## H. Open questions for the owner

1. **Recommendation OK?** The native Go driver (b) as the product, no bundled MMDVMHost? Or do you want (a) first to get on the air sooner?
2. If P25 on RF matters soon: is an optional, user-installed MMDVMHost for P25 acceptable, or should P25 wait for native framing?
3. Which modes do you actually use on RF? If it is DMR and YSF only, P25 and NXDN can wait or never happen.
4. Which hat exactly (name, firmware version), and which Pi model? This settles the UART and Bluetooth steps and the reset pin.
5. Does the firmware on your hat report M17? Is M17 wanted?
6. Should AllStar audio go out on RF at all, or is the hotspot RF-to-network only? What is the right priority order (RF, AllStar, network)?
7. Hang time and TX timeout defaults (suggested 2 s and 3 min)?
8. Frequency check: which bands and countries should the built-in table allow, and should the owner have to confirm a country?
9. Should the hotspot also be offered as a *DMR repeater ID / ESSID* of its own, or share the main identity's ID with a fixed ESSID?
10. Mode scan on a simplex hotspot, or one mode at a time through the dashboard?
11. Do you want a "calibration" helper in the panel, or is calibration done with other tools first?
12. Is the test plan acceptable: you do the on-hardware checks, with a dummy load for first TX, and compare against your old hotspot image?
13. Should AllStar be optional, so a hotspot-only install works without ASL3 (A2.2)?
14. Which Pi-Star features from the gap table (A2.1) do you actually use: rewrite rules, several DMR networks at once, cross-mode, POCSAG, a display, static talkgroups? That sets what comes after M7.
15. Is a Pi-Star backup import (A2.3) wanted in the first release, or later?

---

## To verify (summary)

All of these come from outside the repo and must be checked upstream before use:

- MMDVMHost: license version ("GPL-2.0 or later"), Debian availability, exact network peer behaviour per mode (DMR master login, YSF/P25/NXDN gateway endpoints and ports), and its timers.
- Modem firmware and serial protocol: frame format, commands, buffer flow control, supported modes (M17), frequency/offset/deviation commands, version query, status contents (RSSI, BER), own TX timeouts.
- UART names, `config.txt` and `cmdline.txt` options, and Bluetooth overlay per Pi model and OS release; Pi 5 differences; device group name.
- Reset and boot GPIO pins of each hat.
- A pure-Go serial library choice that keeps `CGO_ENABLED=0`.
- Hotspot band allocations per country.
- Pi-Star backup file names and formats (A2.3), what each Pi-Star feature does (A2.1), the BrandMeister API for static talkgroups, and ASL3 package sizes (A2.2).
