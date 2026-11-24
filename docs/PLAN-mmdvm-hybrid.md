# Plan: hscp hotspot panel and AllStar hybrid (MMDVM hat, one repo, two products)

Status: design note, no code yet. Branch try/mmdvm-hybrid. Decisions taken by the owner on 2026-11-24 are folded in below (product split, routing model, all modes on RF, owner hardware, optional AllStar, all Pi-Star extras wanted). What is still open is in section J. Nothing here is built.

The idea: take a spare Raspberry Pi with an MMDVM hotspot hat (the kind that used to run a hotspot image) and let the engine (`aslcp-dv` today) drive the hat. Two products come out of this repo:

- **ASLCP** stays the AllStar panel.
- **hscp** (working name, name pending) is a hotspot panel: a one-package Pi-Star replacement with a nicer UI.

The big use case is AllStar for people who only have a DMR (or YSF, P25, NXDN) radio: the hotspot can act as an AllStar node, so a digital radio reaches AllStar through it. The hotspot can also be a plain Pi-Star style hotspot to an internet network. It is one or the other at a time (section E).

Things marked "to verify" are claims about external software (MMDVMHost, the modem firmware, the serial protocol, Pi-Star) that this repo cannot confirm. Check them against the upstream sources before building on them. Everything else cites a file in this repo.

---

## A. Goal and scope

### A.1 Goal

- One Pi runs the hat, the engine and a panel. With ASL3 installed it can also be an AllStar node; without ASL3 it is a plain hotspot.
- A licensed operator keys a DMR, YSF, P25, NXDN (or M17, if the firmware supports it) radio on the hotspot frequency. All of these modes work on RF.
- The hotspot runs in exactly one of two modes (section E):
  - **AllStar mode:** RF to AllStar and back. No internet DV network.
  - **Hotspot mode:** RF to one internet DV network and back, like Pi-Star. Nothing from AllStar on RF.
- In AllStar mode the radio can control AllStar (link and unlink nodes) from the keypad or by calling a number (E.3).
- Set-up, status and control are in the panel and the CLI. No hand-edited ini files.
- Everything a Pi-Star user expects is on the roadmap (section C and the phased plan in section I), not "maybe".

### A.2 Hardware assumed

Owner hardware (the first target):

- **A duplex MMDVM_HS hat**: two ADF7021 chips, so it receives and transmits on separate frequencies at the same time. Firmware **1.6.1** (the owner says "I think": confirm with the modem version query, D and F.3).
- **Raspberry Pi 3B+.**
- Duplex means: DMR can use both timeslots at once, RX and TX frequencies are separate, and the modem can receive while it transmits. Duplex builds of the firmware differ from simplex ones (to verify).

Also supported (same driver, fewer settings):

- Simplex hats: one frequency, one radio chip, DMR on one timeslot. A "duplex" switch in the RF settings (G.1) chooses the behaviour.
- Low power (milliwatts) is the norm for these boards.

OS:

- 64-bit Raspberry Pi OS Lite (Debian bookworm or trixie). ASLCP refuses armhf (README.md "Requirements"; docs/DV.md "Enable and disable", status reason `armhf_unsupported`). A Pi that ran a 32-bit hotspot image must be reflashed. Back up what you need first; nothing carries over except a Pi-Star backup import (C.3).
- The Pi 3B+ has a 64-bit CPU and runs 64-bit Raspberry Pi OS (to verify for the exact image).

### A.3 Non-goals

- **No analog FM on the hat.** The ADF7021 cannot transmit analog FM here. The RF side is digital only. Analog RF users need a separate USB sound interface and an FM radio as an ordinary AllStar port. That is a normal ASL3 setup and is out of scope here.
- **No repeater boards with external amplifiers or cavity duplexers.** A duplex hotspot hat is a target; a full repeater is not. The design should not make it impossible later.
- **No Wires-X.** The YSF client does not implement it (internal/dv/network/ysf/ysf.go header comment). Nothing changes.
- No DMR data, no GPS/APRS beacons, no text messaging. Voice only (plus the control calls in E.3 and paging in section I).
- No firmware flashing from the panel in v1. The hat arrives with working firmware. A "how to flash" note may come later.
- No hosting of a public Homebrew master or reflector. Any listener is on loopback only.

---

## B. Two products, one repo

### B.1 Decision

- **ASLCP** stays the AllStar panel: nodes, the AllStar parts of the Digital modes page, everything it does today.
- **hscp** (working name, name pending) is the hotspot panel: one package, one panel, one config, a nicer UI than Pi-Star. It works without ASL3.
- Both are built from this repo: separate binaries and packages (nfpm `.deb`), separate branding, shared code in `internal/`.

Why one repo, not two:

- Go `internal/` packages cannot be imported from another repo. Splitting would force a public, versioned engine library, and every engine change would be two repos, two PRs and two releases.
- The engine still changes fast, for both products.
- The AllStar mode couples the two: the engine's USRP side, the bridge node and the router are used by both.
- A split stays possible later, once the engine interface settles.

### B.2 What each product is

| | ASLCP | hscp (working name) |
|---|---|---|
| Purpose | AllStar panel | Hotspot panel, Pi-Star replacement |
| Needs ASL3 | Yes | No. AllStar mode appears only when ASL3 is installed (or added later). |
| RF modes of the hotspot | AllStar mode and hotspot mode, when a hat is configured | Hotspot mode always; AllStar mode only with ASL3 |
| UI | Existing panel, plus the RF card | Hotspot-focused pages (dashboard, RF, networks, Wi-Fi, system) |

### B.3 Package and binary layout (proposal, details open)

- `cmd/aslcp` and the existing binaries stay as they are (to check against the repo layout).
- New `cmd/hscp`: the hotspot panel server and its CLI (name pending).
- The engine (`aslcp-dv` today) is shared. It runs as a service under both products. See B.5 for naming.
- Packaging: the existing `packaging/nfpm.yaml` stays. A second definition (for example `packaging/nfpm-hscp.yaml`) builds the hscp package. A third, shared engine package is likely (B.6). All for arm64 and amd64, `CGO_ENABLED=0` as today (Makefile).
- Web UI: **one Svelte codebase with a product build flag** is the proposal. The flag selects the product's pages, branding (name, logo, colours) and which API areas exist. Each binary embeds its own build. Alternative: two app shells over a shared component library. Which one: open question (J).
- systemd units, helper ops and the sandbox profile follow the existing pattern (docs/PLAN-2.0-M1.md A.1). Unit names carry the product or engine name.

### B.4 Shared and product-specific

Shared (`internal/`):

- The DV engine, the router, codecs (md380-emu AMBE+2, pure-Go IMBE, pure-Go Codec2), the network clients (internal/dv/network/*), host lists, radioid lookups.
- The new modem driver and RF endpoint (section D, E).
- Now calling and Last heard, and the `dv` SSE event.
- The web UI framework, components, auth, sessions, audit.
- Backup and restore, updates and the apt source, the Wi-Fi scan and the Wi-Fi setup hotspot (README.md "WiFi").

Product-specific:

- ASLCP: AllStar node management, the `rpt.conf` and node pages, the bridge node set-up (docs/DV.md "Private bridge node and USRP"), ASL3 dependency lines.
- hscp: the hotspot-first UI and first-run flow, the Pi-Star importer front end, branding, the setup-hotspot name (ASLCP uses `ASLCP-<hostname>` today; hscp needs its own, name open), package metadata with no ASL3 dependency.

### B.5 The engine's name

The engine is called `aslcp-dv` and lives under `internal/dv`. For hscp the name is off. A product-neutral name is likely needed, for the binary, the service, the helper ops (`dv-*` already sounds neutral) and the paths. Renaming has a cost for existing ASLCP installs (service name, config path, upgrade handling). Open question (J).

### B.6 Can both products run on one Pi?

Technically yes, if the Pi has exactly one engine process that owns the serial port. A proposal:

- The engine is its own package, depended on by both products. One engine process, one config, one modem owner.
- Both panels talk to the engine over the same control socket (`ctl`). Neither panel opens the serial port.
- The RF settings and the mode switch live in the engine config, so either panel shows and changes the same state.
- Two panels means two web ports and two user databases unless auth is shared. Whether to allow that, or to make the two packages conflict (so a Pi runs one panel), is open (J). A simpler line: on a Pi with ASL3, install ASLCP, which then shows the RF card; hscp is for Pis without ASL3, and also works with ASL3 for AllStar mode.
- Without the engine package split, installing both would ship two copies of the engine fighting over the port. The package split avoids that.

### B.7 CI and release impact (details open)

- The release workflow builds two sets of `.deb` files, plus the shared engine package, per architecture.
- Tags and notes: today a `vX.Y.Z` tag with a `## [X.Y.Z]` CHANGELOG entry drives a release (CLAUDE.md "Release numbers"). With two products: one shared version number for everything, or product-prefixed tags? One CHANGELOG with product headings or two?
- apt repository: one repo with two components, or two suites/channels (stable and testing per product)? Does hscp reuse the existing update page and apt source mechanism with its own source entry?
- Tests: the shared engine tests run once; each product adds its own build and UI checks. An hscp install test without ASL3 is needed (no node, no Asterisk).
- All of the above are questions for the owner (J).

---

## C. Pi-Star parity and migration

### C.1 Gap table

"Pi-Star" here means the usual hotspot image and its tools (MMDVMHost, DMRGateway, YSFGateway and the like). What each does is from general knowledge and is to verify against the upstream projects. The "Today" column is checked against this repo. **All of these are wanted.** The last column says where each lands in the plan.

| Pi-Star feature | Today | Needed | Milestone |
|---|---|---|---|
| MMDVM modem control, RF calibration (frequency offset, levels, BER check) | Nothing. No serial code in the repo. | Modem driver (section D), offset and level settings, a receive-only BER/level check page. | M3 to M6 |
| Multi-mode hotspot with mode hang (one RF mode at a time, hang time after a call) | One active mode at a time, a mode switch is refused while a call is in progress (internal/dv/bridge/router.go `switchMode`). No RF. | RF endpoint, hang time (E.6), mode scan (E.8). | M1, M6 |
| Internet networks: BM, TGIF, Homebrew masters, YSF, P25, NXDN, M17 | All native clients, with tests (internal/dv/network/*). | Nothing new for networks. | none |
| DMRGateway-style rewrite rules | One active network (`active()` in router.go). | A rules layer: talkgroup and slot maps, per-network prefixes. | M11 |
| Several DMR networks at once | BM, TGIF and DMR - Other can all be logged in at once, but only the active one carries calls. | Several networks carry calls at once, routed by the rules. Duplex helps: one network per timeslot. | M11 |
| Cross-mode (YSF2DMR, DMR2YSF, YSF2NXDN, ...) | None. | AMBE+2 repack for DMR, YSF DN, NXDN (E.9); a PCM transcode for P25 and Voice FR. | M12 |
| Host list updates | Daily download of the DMR, YSF, P25, NXDN and M17 lists (docs/DV.md "Host lists"). | Nothing. | none |
| Last heard, live dashboard | Now calling, Last heard, SSE event `dv` (docs/DV.md "Parts"). | "via RF" source tag, RF state card (G.3). | M1, M6 |
| Wi-Fi setup and a setup hotspot | Scan, add, reorder, and a setup network `ASLCP-<hostname>` when no network is found (README.md "WiFi"). | Nothing for the code; hscp needs its own setup network name (B.4). | M2 |
| Config backup and restore | Present (docs/BACKUP.md). | RF settings with `transmit` forced off on restore (G.6). Import from a Pi-Star backup (C.3). | M6, M10 |
| Updates | Update page and apt source (README.md). | hscp channel (B.7). | M2 |
| POCSAG / DAPNET paging | None. | A DAPNET client and the modem's pager frames (to verify). | M13 |
| Nextion / OLED displays | None. | Serial and I2C display drivers fed from the engine state. | M14 |
| Remote command, firewall, SSH access | A browser terminal, firewall service control and SSH are part of the System page (README.md). | Nothing for parity; the panel is already ahead here. | none |
| BrandMeister manager (static talkgroups, dynamic TG list) | Tune and favourites on the dashboard (docs/DV.md "BrandMeister"). No static talkgroup management through the BrandMeister API. | A static-talkgroup editor through the BrandMeister API and an API key (to verify). | M10 |
| "Expert editors" (edit every ini file by hand) | None, by design: settings are typed fields and the config is rendered. | A read-only "show the rendered config" view, plus an `advanced` key/value passthrough per network. Every raw editor weakens validation and the safety rules in section H, so go no further than that. | M6 |

What this says:

- The engine already covers the network side, the host lists, the dashboard, Wi-Fi, backup and updates. The missing core is the modem and RF handling.
- The big functional gaps (rules, several networks, cross-mode) are routing features. They sit in the router, not in the modem driver, so they come after the RF basics.

### C.2 Optional AllStar

Decided: **AllStar is optional.** An hscp install works without ASL3. AllStar mode appears only when ASL3 is installed, or when it is added later.

What this means for the code:

- The engine runs with the USRP endpoint disabled. Hotspot mode must not depend on it (router.go today is built around USRP as one endpoint; a no-USRP mode needs its own tests).
- The panel hides AllStar parts when no node is found and shows them when ASL3 appears (a re-check on the system page and at start).
- The installer, backup and health checks of hscp assume no node. ASLCP's still do.
- No node number, no AllStar account, no Asterisk process on a hotspot-only Pi: smaller install, less to patch.
- Packaging: hscp has no ASL3 dependency; ASL3 is at most a "suggests". The exact dependency lines of the ASLCP package are in packaging/nfpm.yaml (to check).

### C.3 Migration from a Pi-Star backup

The owner reflashes anyway (32-bit to 64-bit, section A.2), so migration is an import from a backup file taken before the reflash, not an in-place upgrade. The panel's restore flow already accepts an uploaded backup (docs/BACKUP.md). A Pi-Star import is a second, read-only importer for a zip. It is wanted (milestone M10).

What could be imported (file names and keys are from memory, all to verify against a real backup):

- The backup zip's config files: `MMDVM.ini` (callsign, DMR ID, frequencies, colour code, offsets, modes enabled, modem port), `DMRGateway.ini` (network entries; passwords), `ysfgateway`, `p25gateway`, `nxdngateway` ini files (reflector settings), and a Pi-Star `config` file (to verify the name and format; it may hold the same data).
- A small set of values maps onto the engine settings: callsign, DMR ID and ESSID (Identity), RX/TX frequency, frequency offset, colour code, RAN, and the BrandMeister, TGIF or other master address and password.
- Once the matching features exist (M10, M11): static talkgroups and DMRGateway rewrite rules.

Rules for an importer:

- Show a preview of what will be set, and let the owner untick items. Nothing applies without a click.
- Passwords are read once, stored in the same write-only way as today, and never shown or logged.
- Transmit stays off after an import (H). Frequencies are validated like typed ones.
- Whatever is not understood is listed, not dropped silently. Unknown keys are ignored, not carried.
- The importer is a parser with test fixtures made from synthetic files (N0CALL, DMR ID 1234567). It does not need a Pi. Real backups are used by the owner for the on-hardware check only.

---

## D. Integration choice

Two ways to drive the hat. Both end in the same engine; the question is who speaks serial to the modem.

### D.1 What the engine already has

- Network clients that carry voice as codec frames, not RF bursts: Homebrew (DMR), YSF, P25, NXDN, M17, BrandMeister (internal/dv/network/*). Each implements `network.Network` (internal/dv/network/network.go).
- Air-interface coding, already byte-checked against MMDVMHost vectors:
  - DMR: bursts, BPTC, Golay, RS(12,9), embedded LC, slot type, SYNC, a TX superframe builder and an RX parser (internal/dv/dmr/doc.go).
  - YSF: sync, FICH, VCH and DCH coding, CSD (internal/dv/ysf/ysf.go).
  - NXDN: LICH, SACCH, FACCH1, convolutional code, scrambler, Layer 3 (internal/dv/nxdn/doc.go). The 72-bit AMBE+2 FEC is deliberately not implemented.
  - P25: no air-interface package. internal/dv/network/p25/p25.go builds the reflector records only. There is IMBE FEC code in internal/dv/imbe/fec.go, but the LDU framing, NID and trellis are not in the repo.
  - M17: LSF, stream and Codec2 framing (internal/dv/m17).
- Vocoders: md380-emu for AMBE+2, pure-Go IMBE, pure-Go Codec2 (docs/DV.md "Voice codecs").
- The router (internal/dv/bridge/router.go) joins the USRP bridge node and one active network (`active()`, `switchMode`). The engine links the bridge node to one network target. A mode switch with a call in progress is refused with `busy` unless forced.

### D.2 RF frames and network payloads

The hat does the modulation. The modem and the host exchange air-interface frames, and for most modes these are close to the network payloads (to verify each against the upstream sources):

| Mode | Over the modem | Network side | Gap |
|---|---|---|---|
| DMR | 33-byte bursts | Homebrew DMRD payload is the same 33-byte burst | Small: header fields (slot, call type, stream id) and slot timing. |
| YSF | 120-byte frames with FICH | YSF network frame (YSFD) carries the same 120 bytes | Small: framing around the payload. |
| NXDN | Frames close to the network frame | NXDN network frame (33 bytes, FEC stripped, internal/dv/nxdn/doc.go) | Medium: the 72-bit AMBE+2 FEC and the LICH/SACCH handling. |
| P25 | Over-the-air LDU frames (voice, NID, FEC) | The P25 network protocol carries IMBE voice pieces, not over-the-air frames | **Large**: the engine must rebuild the RF framing (LDU1/LDU2, NID, FEC, link control). |
| M17 | LSF and stream frames, if the firmware supports M17 | M17 reflector frames | Small to medium; firmware support to verify. |

This is why same-mode pass-through in hotspot mode (E.4) is cheap, and why the milestones are ordered by effort with P25 last (section I).

### D.3 Option (a): MMDVMHost as the modem driver, aslcp-dv as its network peer

MMDVMHost (G4KLX) owns the serial port, the modem protocol, RF framing and RF timing. The engine listens on loopback and MMDVMHost connects to it as if it were its usual gateway or master:

```
RF <-> hat <-UART-> MMDVMHost <-UDP loopback-> engine <-USRP-> Asterisk
                       |                         |
                  MMDVM.ini                 config.json
                                                 ^-- internet networks
```

Peer endpoints (to verify against upstream; the repo only describes the wire formats it already speaks):

- DMR: the engine acts as a Homebrew *master* that MMDVMHost logs in to. The engine has only the client side (internal/dv/network/homebrew/doc.go). A master side means: answer RPTL with a salt, check RPTK, accept RPTC, answer pings, send and receive DMRD. About 300 lines, same packet formats.
- YSF: the engine acts as a gateway endpoint on a loopback UDP port; MMDVMHost sends YSFD frames to it. The "YSFD" format is already parsed by internal/dv/network/ysf/ysf.go.
- P25 and NXDN: the same, using the reflector style packets the engine already parses (p25.go, nxdn/network.go). The NXDN payload is the 33-byte frame with FEC already stripped by MMDVMHost (internal/dv/nxdn/doc.go).

Pros:

- The hard, hardware-specific part (serial protocol, modem config, per-mode RF framing, DMR slot timing, calibration quirks) is done by software that thousands of hotspots already use.
- The engine needs no RF coding for P25 (which it does not have) and nothing new for NXDN FEC.
- Fastest route to a working hotspot. Hardware-specific bugs are mostly already fixed upstream.
- Upstream tracks new firmware and mode changes.

Cons:

- MMDVMHost is C++ with no Debian package in the Debian archive (to verify). The project ships arm64 and amd64 `.deb` files built with nfpm, Go with `CGO_ENABLED=0` (Makefile). Options: build MMDVMHost in CI for both arches and bundle it, or ask the owner to install it from another source. Bundling adds a C++ toolchain and cross-build to the release workflow, now for two products.
- License: ASLCP is GPL-3.0 (LICENSE). MMDVMHost is GPL-2.0, with "or later" to verify per file. The repo already credits MMDVMHost-derived code as GPL-2.0-or-later in comments (internal/dv/ysf/ysf.go, internal/dv/nxdn/doc.go). Shipping the binary as a separate program in the same package is fine, as long as source and notices are provided (packaging/THIRD-PARTY-NOTICES). If MMDVMHost is GPL-2.0-only, it still runs as a separate process; do not link it.
- Two config files: MMDVM.ini and the engine config. The product would render MMDVM.ini from its own settings and own it (users should not hand-edit it). Every MMDVMHost option the panel does not expose is lost, unless an "advanced" passthrough is added.
- Another process to supervise: unit ordering (MMDVMHost needs the engine's loopback ports up, or retries), restarts, logs, a second hardening profile. It also needs the serial device, so it cannot share the engine's `PrivateDevices=yes` sandbox (packaging/systemd/aslcp-dv.service).
- MMDVMHost has its own idea of "one mode at a time, timeouts, hang time". The engine's router rules (section E) sit behind it. Two sets of timers can disagree. To verify: how MMDVMHost behaves when the network end goes quiet mid-call.
- Per-mode "gateway" semantics differ: MMDVMHost YSF mode expects a YSF gateway program for link management, not a reflector (to verify). The engine would have to play the gateway, including the reflector-side polls. The engine already does the reflector side.
- AllStar mode and the radio control (E.3) would need the engine to read call metadata out of MMDVMHost's network stream. Doable, but one more thing that depends on a second program.
- Debugging is two layers deep.

### D.4 Option (b): the engine speaks the modem protocol over the UART itself

A new Go package, say `internal/dv/modem`, opens the serial device, speaks the MMDVM serial protocol, and a new network-like endpoint ("RF") feeds the router.

```
RF <-> hat <-UART-> engine (modem + RF framing + router) <-USRP-> Asterisk
                       |
                  config.json only
```

Pros:

- One binary, one config, one process, Go, `CGO_ENABLED=0`. Packaging stays as is. No GPL-2.0 binary to ship (we would still credit protocol knowledge, as the repo does now).
- Same-mode RF to network passes frames with no extra hop and no second state machine. Direct control of timers, hang time, contention.
- The RF layer reuses the DMR, YSF, NXDN and M17 coding already in the repo.
- Status is richer: modem state, RSSI and BER can go straight to the dashboard (if the firmware reports them, to verify).
- Radio control of AllStar (E.3) is just a router rule on frames the engine already parses.

Cons:

- The modem protocol is ours to get right (to verify against the firmware sources). Scope, from memory and all to verify:
  - Frame format: start byte, length, command, data. Commands for version/capabilities, status (modem state, TX/RX flags, buffer space per mode), set config, set mode, set frequency (ADF7021 hotspots have their own set-frequency command), calibration data, and per-mode data frames with a "lost frame" marker.
  - Buffer flow control: the firmware reports free FIFO space per mode; the host must not overrun it. DMR needs frames timed to slots, and on a duplex hat both slots run at once.
  - ADF7021-specific: RF frequency, frequency offset in ppm, per-mode deviation levels, RX/TX level. These matter for audio quality and for a clean signal.
  - Duplex specifics: separate RX and TX frequencies, and the duplex timing the firmware expects (to verify).
  - Modes the firmware supports. M17 in firmware 1.6.1: to verify (it depends on the firmware fork and version).
- RF-side framing is missing for P25 (no air-interface package, see D.1 and D.2). That is real work: LDU1/LDU2, NID, Golay/Hamming/RS, trellis for data, link control. P25 comes last.
- DMR on RF needs more than the existing burst coder: slot timing, the host-side sync and "data sync / voice sync" handling the modem expects (to verify). Cross-checking against real radios is only possible on the owner's hardware.
- No other implementation to lean on. A bug here can put a bad signal on the air (see section H).
- Calibration and level tuning, normally done in Pi-Star or MMDVMCal, must be redone (see D.6).

### D.5 Recommendation

**Build option (b), the native Go modem driver, as the product. Do not ship MMDVMHost.** Use an existing MMDVMHost only as a reference and as a comparison tool on the owner's bench.

Reasons, from the code and the one-package goal:

- **Product fit.** hscp is meant to be a one-package Pi-Star replacement. (a) means a bundled C++ build in a Go-only, `CGO_ENABLED=0` release pipeline (Makefile), a second GPL-2.0 binary and notice, a second config (MMDVM.ini) the panel must render and own, and a second process with its own sandbox. Those costs would stay for good, and a "temporary" bundled binary tends to stay. With two products they are paid twice.
- **Much of the RF work exists.** DMR burst coding, YSF FICH/VCH/DCH, NXDN layers and M17 framing are in the repo and were checked against MMDVMHost vectors (D.1). The missing pieces are the serial protocol and the P25 air framing, not everything.
- **The network side is not a reason for (a).** Under (a) the engine needs new loopback peers (a Homebrew *master*, YSF/P25/NXDN gateway endpoints). Under (b) it needs none: RF frames go straight into the router.
- **Behaviour.** One router owns timers, hang time, contention and the AllStar radio control (section E). Under (a) MMDVMHost has its own and the two can disagree.
- **Risk.** The real risk of (b) is a bad signal on the air from a driver bug. That is handled by process, not by a second program: transmit off by default (H), receive-only first, low power and a dummy load for the first TX, and a side-by-side check against an MMDVMHost the owner already has (it can be the old hotspot image on another card).

The order of work is in section I. In short: the RF endpoint and router changes with a fake modem first (no hardware), then the Go modem driver, receive only, then TX, YSF first (DN framing is complete in internal/dv/ysf), then DMR, then NXDN, M17 and P25 last.

What would change my mind: if the owner needs P25 on RF soon, (a) gets it for free because MMDVMHost already has it. In that case ship (a) as an *optional, user-installed* add-on for P25 only, not bundled. Otherwise do not take (a) on. (J, question 2)

Whichever driver is used, the router sees RF as one more endpoint with frames in and frames out (section E). The code boundary is the `Endpoint` interface of M1, so a different driver can sit behind it.

### D.6 Calibration

- Under (a), calibration is MMDVMHost's and the firmware's; the product only renders the level and offset settings into MMDVM.ini. The owner calibrates the frequency offset with a known-good receiver, as for any hotspot.
- Under (b), which is recommended, the engine needs the modem calibration commands (to verify) and a simple "send a test tone / carrier for N seconds" tool. TX must stay off by default (section H).
- A generic "frequency offset (ppm)" and "per-mode TX/RX level" setting exists in both cases (section G).

---

## E. Routing model

### E.1 Two modes, never both

The core idea: **someone with only a DMR (or YSF, P25, NXDN) radio can get into AllStar through the hotspot.**

The hotspot's RF side runs in exactly one of two modes:

```
AllStar mode:
   RF <-----> engine (vocoder) <-----USRP-----> bridge node <---> ASL3 and linked nodes

Hotspot mode:
   RF <-----> engine (frame pass-through) <-----> one internet DV network or target
              (BrandMeister, TGIF, YSF reflector, ...)
```

- **AllStar mode:** RF to AllStar. The hotspot acts like an AllStar node, the way the DV bridge node (a private USRP node) does today. Traffic from the linked AllStar nodes goes out on RF. The radio on RF is heard on AllStar. **No internet DV network in this mode.**
- **Hotspot mode:** RF to one internet DV network or target, exactly like Pi-Star. **Nothing from AllStar goes out on RF.**
- Without ASL3 installed (hscp only), only hotspot mode exists (C.2).

There is no mode where RF, an internet network and AllStar are all joined. AllStar audio never mixes with network traffic on RF.

### E.2 AllStar mode

Paths (RF mode X is the air interface of the active RF mode):

| From | To | Path |
|---|---|---|
| RF, mode X | AllStar (USRP) | decode to PCM with the vocoder of X, send as USRP voice. |
| AllStar | RF, mode X | encode with the codec of X, frame for RF. |

What changes in the engine:

- Today the router links the bridge node to one network target and carries frames between USRP and that network (router.go). AllStar mode needs an RF endpoint instead of the network: RF frames go through the vocoder to USRP, and USRP audio goes through the encoder to RF frames.
- The vocoders exist (docs/DV.md "Voice codecs"). Cost per call: one decode plus one encode. The IMBE and Codec2 costs on a Pi 3B+ are in docs/DV.md "Voice codecs". md380-emu runs under emulation and is the expensive one; it is single-threaded (docs/PLAN-2.0-M1.md A.2).
- Because audio goes through PCM, a DMR radio and a YSF radio on the same hotspot can both be heard on AllStar. They are not on air at once (E.8).
- Outgoing identity: the source is the owner's identity (callsign and DMR ID from the identity), and the destination is a fixed value such as "ALL" or the configured talkgroup, as the network TX path does today (see `startTX` in router.go). Whether the operator may send AllStar audio on RF is a licence question for the owner (H).
- In AllStar mode the existing network clients are logged out or idle. The panel shows no network target.
- Duplex: DMR has two timeslots, but AllStar mode uses one RF channel for one conversation. Which slot carries it, and whether the other slot is free or refused, is open (J).

### E.3 Radio control of AllStar (AllStar mode)

An analog node is controlled with DTMF function codes. A digital radio has no keypad for that, so the hotspot reads the call setup instead. Proposal (to design):

- **DMR:**
  - A private call to an AllStar node number **links the hotspot's node to it**. Example placeholders: nodes 1997 to 2002. The engine takes the destination ID of a private call, sees it is not a talkgroup, and runs the link command for that node. No voice is passed for that call.
  - A private call to a reserved number **unlinks**. Example: a number such as 9999 (reserved numbers are a setting).
  - A private call to another reserved number could ask for **status** (the engine answers with a short voice or text announcement, to design) or reach a **parrot** (echo test).
  - Group calls are voice, bridged to AllStar. Whether any talkgroup counts or a chosen one does is open (J).
- **YSF, P25, NXDN, M17:** equivalents are **to design**. Ideas: the YSF DG-ID selects an entry from a configured table (DG-ID 10 means node 1997, a reserved DG-ID means unlink); P25 and NXDN talkgroup digits can map to node numbers or to table entries; M17 uses the destination field. Each mode's header fields limit what can be encoded (to verify), so a table is likely needed.
- The link and unlink actions use the same control path the panel uses for the bridge node today (docs/DV.md "Private bridge node and USRP"; to check), so the AllStar side sees one kind of request.
- Safety: only calls from the owner's allowed IDs may control (a list, default: the owner's DMR ID only). A control call changes the AllStar link state and is audited (G.5). Rate limited.
- Feedback to the radio (did the link work?) is wanted but needs a way to send a short reply on RF. To design.

### E.4 Hotspot mode

Paths:

| From | To | Path |
|---|---|---|
| RF, mode X | network, mode X | frames pass unchanged (same codec, same frame size). No vocoder. |
| network, mode X | RF, mode X | same, unchanged. |

- Pure pass-through: frames are re-framed, not decoded. The RF mode must equal the tuned network's mode (a BrandMeister target means DMR on RF, a YSF reflector means YSF on RF).
- A voice frame already carries its codec (`network.VoiceFrame.Codec`, internal/dv/network/network.go). The pass-through case is a check that codecs match and the length is right (`codecLen` in router.go).
- Contention here is **only RF versus the network.** No AllStar leg exists in this mode.
- Duplex: both DMR timeslots can carry calls at once, for example one network or talkgroup per slot. Several networks at once come with the rules layer (M11).
- Hotspot mode is what Pi-Star does. It is the only mode without ASL3.

### E.5 Switching between the modes

The two modes are mutually exclusive, the same rule as the existing Digital modes card: for personal use you should not be connected to BrandMeister **and** linked to AllStar at the same time.

- Switching to hotspot mode drops the AllStar side: the bridge node is unlinked from AllStar nodes (or the USRP leg is detached; to design which) and no AllStar audio goes to RF.
- Switching to AllStar mode drops the network side: the network client logs out and stops carrying calls.
- The existing busy rule applies: with a call in progress, the switch is refused unless forced (router.go `switchMode`).
- The switch is one control in the panel and one CLI command (G.4), saved at once. The current mode and the reason for a refusal are shown.
- What mode is active after a restart or a restore: the saved mode, except that a restore never turns transmit on (G.6, H).
- Whether a radio can also switch the mode, not only the panel, is open (J).

### E.6 Contention, hang time and timeouts

Who holds the channel: one talker at a time, first come first served, per RF channel (on a duplex hat, per timeslot for DMR).

- RF talker active: audio for the other leg is dropped on the RF side (nothing to transmit anyway; the hat is receiving). In AllStar mode the other leg is AllStar; in hotspot mode it is the network.
- RF is transmitting a call from the other leg: RF keying by a radio user is ignored while the hotspot transmits (on a simplex hat; a duplex hat can receive while it transmits, to verify what the firmware does with it). The RF user's call is lost until the TX ends. Hang time keeps the channel from flipping back and forth.
- Proposed priority, highest first: local RF talker, then the other leg's talker. This matches the router's current rule that a node transmission wins over a received call (`startTX` calls `cutRX`). With only two legs in each mode there is no three-way question left. To be confirmed (J).
- **Hang time.** After a call ends on RF, hold the channel for a short time (example: 2 s) before sending the next queued call, so a reply on RF is not cut by an incoming call. Configurable. Default value is an open question.
- **Timeouts.**
  - RF call: ends on terminator, or after the existing RX silence timeout (`DefaultRxSilence`, router.go), or when the modem reports lost sync.
  - TX timeout on RF: a hard cap on continuous transmit (example: 3 minutes), then stop and drop. The router already has `DefaultTxTimeout` for the node side; RF needs its own, and the RF one must be enforced even if the other leg stalls.
  - A call that stalls mid-stream is ended by silence timeout, and TX is then dropped to idle. Never leave the carrier up.

### E.7 Loop prevention

- A call that came from RF must never come back out on RF. Each call carries a source tag (`rf`, `net`, `allstar`) in the router's call record (`call` struct, router.go); the RF endpoint refuses frames tagged `rf`.
- A network that echoes our own transmission (reflector loops) is already a risk for the current engine; the call-id check in the router (`netCall`) does not cover it. An RF tag plus a short "same source id within N seconds" guard should.
- AllStar side: the bridge node is private and not linked to the same network (docs/DV.md "Private bridge node and USRP"). The existing rules there apply. Because the two modes are exclusive, AllStar and an internet network are never both live on the same RF call.

### E.8 One RF mode at a time (mode scan)

The hat listens on one air interface at a time. All modes work on RF (D.2), but the firmware (to verify) can scan modes by watching for each mode's sync, at some cost in sensitivity. Pi-Star does this with a mode hang. Two choices:

- **Fixed mode:** the RF mode is the one chosen (in hotspot mode, the one matching the network target; in AllStar mode, the one chosen in the panel).
- **Scan:** a list of enabled RF modes, and the first mode that sees a call wins, with a mode hang afterwards. In hotspot mode that conflicts with the router's rule that a mode switch cuts a call (`switchMode`) and needs its own design, since the network target is tied to one mode.

v1 is a fixed single mode per mode of operation. Scan comes with M6 or later. Question (J).

### E.9 Cross-mode (later, M12)

Example: the radio is DMR on RF while the hotspot mode target is a YSF reflector.

- **Repack, no transcode.** DMR, NXDN and YSF DN all carry the same 49-bit AMBE+2 frame (internal/dv/nxdn/doc.go: "the same 49-bit frame as the md380 vocoder"; internal/dv/ysf/ysf.go; internal/dv/dmr/doc.go). Only the FEC and framing differ. A DMR to YSF DN or NXDN bridge can copy the 7-byte frames and rebuild framing. This is cheap and lossless in the vocoder sense. It needs a call model that maps header data (source callsign, DMR ID, talkgroup) to the other side's fields. YSF Voice FR and P25 are IMBE, which is a different codec; AMBE+2 to IMBE needs a transcode.
- **Full transcode through PCM.** Works for any pair. Costs a decode and an encode per frame, plus a quality loss (a second lossy vocoder pass).
- Before M12, the RF mode follows the tuned target mode in hotspot mode. In AllStar mode cross-mode between radios already works through PCM (E.2).

---

## F. Hardware and OS set-up on the Pi

### F.1 Serial port

- The hat is on the Pi's primary UART (`/dev/ttyAMA0` or `/dev/serial0`, to verify per model and hat). The panel stores the device path; the default is `/dev/serial0`.
- `config.txt` (to verify the current names for the OS release):
  - `enable_uart=1`.
  - **Pi 3B+ (the owner's board):** Bluetooth uses the PL011 UART, the good one. Disable Bluetooth with a device-tree overlay that frees it for the hat (`dtoverlay=disable-bt`; **exact overlay name to verify**), or move Bluetooth to the mini UART (`miniuart-bt`, name to verify; worse for timing, not recommended). Also `systemctl disable hciuart`.
  - Pi 4 and Zero 2 W behave the same way (to verify); Pi 5 uses a different UART header and device names (to verify).
- The serial console on the UART must be off: remove `console=serial0,115200` from `cmdline.txt` (raspi-config can do both; the helper should edit the files itself to be non-interactive).
- A reboot is needed after these edits. The installer must say so, and the panel must show "reboot needed" until it has happened.
- Modem baud rate: the firmware default (115200 on MMDVM_HS to verify). Configurable.

### F.2 GPIO

- Some hats have a reset line and a boot-mode line (for firmware flashing). v1 does not flash. A "reset modem" button is useful: it toggles the reset GPIO. The pin number depends on the hat (to verify per board). Optional config `modem.reset_gpio`, empty by default. In v1 only a power-cycle or a serial "reset" command (to verify) is offered.
- The engine must not need `/dev/gpiomem` for normal operation. If a reset line is used, a helper op toggles it (F.4).

### F.3 Firmware detection

On start, the engine (or MMDVMHost under (a)) asks the modem for its version (to verify the command). The panel shows:

- the firmware name and version string (the owner believes it is 1.6.1; the check settles that),
- whether the build is duplex or simplex (to verify how it reports it),
- the protocol version and which modes it reports (DMR, YSF, P25, NXDN, M17),
- an error state when nothing answers ("modem not responding on /dev/serial0").

Modes the firmware does not report are greyed out in the RF card. M17 in firmware 1.6.1: to verify; do not assume it is there.

### F.4 Installer and helper

Follows the existing split: the web user never shells out; root does the system work (docs/PLAN-2.0-M1.md A.1).

New helper ops (names follow the existing `dv-*` style; the final names follow the engine's name, B.5):

- `dv-rf-prepare`: check the OS is 64-bit, edit `config.txt` and `cmdline.txt` (with a history copy of each, as the private node ops do), disable the serial getty and hciuart where needed. Return "reboot needed".
- `dv-rf-device-check`: open the serial device read-write, send the version query, report the result. No transmit.
- `dv-rf-config`: render the engine config (and MMDVM.ini under (a)) from the helper state dir. Same pattern as `dv-config` (docs/DV.md "Parts").
- `dv-rf-install` (option (a) only): install the MMDVMHost package/binary, create its unit.

The engine runs as `aslcp-dv` with `PrivateDevices=yes` and no capabilities (packaging/systemd/aslcp-dv.service). Reaching the serial port needs a changed unit: `PrivateDevices=no` with `DeviceAllow=/dev/ttyAMA0 rw` and `DevicePolicy=closed`, and the user in group `dialout` (to verify the device group on current Raspberry Pi OS). Prefer a drop-in written by the helper only when RF is enabled, so non-RF installs keep the tight profile.

---

## G. Configuration and UI

### G.1 Settings

A new `rf` section in the engine config (internal/dv/config/config.go, `ctl.Config`), rendered by the helper. All fields default to off or empty. Example values only:

| Setting | Meaning | Example |
|---|---|---|
| `enabled` | RF endpoint on | false |
| `route` | `hotspot` or `allstar` (E.1). Default `hotspot`. `allstar` is offered only when ASL3 is found. | `hotspot` |
| `transmit` | allow transmit (a separate switch, see H) | false |
| `port` | serial device | `/dev/serial0` |
| `baud` | serial speed | 115200 |
| `duplex` | duplex hat (two radio chips) or simplex | true |
| `rx_freq`, `tx_freq` | Hz; duplex uses different values, simplex the same value | 433000000 (an example, not a recommendation) |
| `freq_offset_ppm` | crystal correction | 0 |
| `power` | output level, as the firmware defines it | 20 |
| `modes` | which modes may use RF | `["DMR","YSF","NXDN","P25"]` |
| per mode levels | RX/TX level, deviation (to verify names) | firmware defaults |
| `colour_code` | DMR colour code | 1 |
| `ran` | NXDN RAN | 1 |
| `dmr_slots` | DMR timeslots in use (both on a duplex hat) | `[1,2]` |
| `rf_id` | use Identity.DMRID, or `DMRID + ESSID` as the repeater ID | DMR ID 1234567 |
| `hang_ms`, `tx_timeout_s` | contention timers (E.6) | 2000, 180 |
| `control.allowed_ids` | who may control AllStar by radio (E.3) | `[1234567]` |
| `control.unlink`, `control.status` | reserved numbers for unlink, status | 9999, 9998 (examples) |

ID use: the existing identity is callsign N0CALL and DMR ID 1234567, with an optional 2-digit ESSID (docs/DV.md "BrandMeister"). Over RF, a radio has its own DMR ID. The hotspot's repeater ID towards the network is `DMRID + ESSID` as today (`repeater_id` option, internal/dv/network/homebrew/doc.go). Use a different ESSID from any other hotspot of the same person on the same master, or one drops the other.

### G.2 RF card and pages

In ASLCP: a new card, "RF / hotspot", on the Digital modes page between "Bridge node" and "Networks". In hscp: the same content as its own RF page. Same fields either way:

- State line: modem found / not found, firmware, duplex or simplex, "TX off" or "TX on".
- **Mode switch: AllStar mode or Hotspot mode** (E.5), with the busy-refusal reason. AllStar mode is greyed out when ASL3 is not found.
- Enable switch (saves at once; refuses with the reason when something is missing: identity, port, frequency).
- Modem port, RX and TX frequency (MHz, validated, see H), offset, power.
- Mode checkboxes, limited to what the firmware reports.
- "advanced": levels, colour code, RAN, slots, hang time, TX timeout, the control numbers (E.3).
- A separate **Allow transmit** switch, off until the frequency is set and the operator confirms (H).
- "Reboot needed" banner after `dv-rf-prepare`.
- A test button "Check modem" (read-only, no TX).

### G.3 Dashboard

- A small RF card: modem state, RF mode, the route (AllStar or Hotspot), "TX off" badge, carrier/receive indicator.
- *Now calling* and *Last heard* show "via RF" for RF-originated calls, next to the existing slot display (TS1, TS2). This needs a `source` field on `ctl.Talk` (internal/dv/ctl/types.go): `rf`, `net` or `allstar`. It is also the loop-prevention tag (E.7).
- Control calls (E.3) appear as events ("linked to node 1997 by radio"), not as voice calls.
- Switching mode on the dashboard follows E.5.

### G.4 CLI

Follows the current `aslcp dv ...` commands (docs/DV.md "Enable and disable"); hscp gets the same verbs under its own binary name:

- `dv rf status [--json]`: modem, firmware, frequency, route, TX state.
- `dv rf mode allstar|hotspot`: the mode switch (E.5).
- `dv rf enable|disable`
- `dv rf set --port ... --rx-freq ... --tx-freq ... [--offset-ppm ...]` (admin; rejects out-of-band frequencies).
- `dv rf check`: the read-only modem check.
- `dv rf tx on|off`: the transmit switch.

### G.5 API

Additions in the same style as docs/API.md "Digital modes":

- `GET /api/dv/rf`: settings (no secrets) and live state.
- `PUT /api/dv/rf` (admin): validated settings, audited as `dv.rf`.
- `PUT /api/dv/rf/mode` (admin): the mode switch, audited as `dv.rf.mode`.
- `POST /api/dv/rf/check` (admin): the modem check, rate limited.
- `PUT /api/dv/rf/transmit` (admin): the transmit switch, audited as `dv.rf.tx`.
- The `dv` SSE event carries RF state and the `source` on talks.
- New ctl ops (`internal/dv/ctl/types.go`): `rf_status`, `rf_check`, `rf_mode`; the settings go through `reload` as everything else does.
- A radio control call that links or unlinks a node is audited as `dv.rf.control`.

### G.6 Backup and restore

The RF settings are ordinary settings (docs/BACKUP.md). On restore:

- The settings come back, but `transmit` is forced off until the owner turns it on again on the new machine. A restored frequency on a different board or country should not key up by itself.
- The saved mode (route) is restored. A restore on a machine without ASL3 falls back to hotspot mode and says so.
- The OS edits (UART, config.txt) are not part of a backup. The page shows "run RF prepare" when they are missing.
- Under (a), MMDVM.ini is a generated file and is not backed up; it is rendered again.

---

## H. Regulatory and safety

The hotspot transmits. Only a licensed operator may do that, on frequencies and modes the licence allows.

- **Licensed operator only.** The RF card says so in plain words. Enabling RF needs a callsign in the identity (it already does for the networks). The callsign is checked for shape only; the product cannot check a licence.
- **Transmit off by default.** Even with the modem enabled, RF only receives until the owner turns on *Allow transmit*. The modem is started in a receive-only state. A restore, an upgrade or a reset never turns transmit on.
- **Hotspot mode is the default.** AllStar mode is a deliberate choice, with a confirmation that the owner is the control operator of what AllStar sends out on RF. That audio comes from other people and other nodes.
- **Frequency validation.** Reject frequencies outside hotspot bands. Keep a small table of the bands where hotspots are used (UHF and VHF amateur allocations; the table lives in code and is reviewed, because these differ by country). A duplex hat needs RX and TX both checked. Reject a TX frequency outside the table, and ask the owner to confirm a country/band choice. The table and the confirmation are an open question (J). Example frequency in docs: 433.000 MHz, clearly marked as an example.
- **Station ID.** Digital modes carry the callsign in the stream (DMR LC, YSF CSD, NXDN, P25), so the callsign in the identity is sent with every transmission. For AllStar audio on RF, the owner is the control operator; whether the licence allows it is for the owner to decide. The panel must not claim compliance.
- **Radio control is not open to everyone.** Only the allowed IDs (E.3) may link or unlink nodes. Everything else is ignored and logged.
- **Timeouts.** A hard TX timeout (default 3 min, maximum bounded in code), plus the silence timeout. The carrier is dropped on any engine error, on SIGTERM (the existing signal handler already sends keyup=0 for the USRP side, docs/PLAN-2.0-M1.md A.2; RF needs the same: tell the modem to stop TX before exit), and when the modem stops answering.
- **Watchdog.** If the engine stops (crash, kill -9), the modem may keep transmitting. The firmware has its own timeouts (to verify). The engine also sends keep-alive status requests and treats an unanswered request as a fault.
- **Limit power.** Let the owner set output power. The default is a low value.
- **Privacy.** No callsigns or IDs from live traffic in logs beyond what the engine already logs for calls.
- **Audit.** Every change to RF settings, the mode switch, the transmit switch and radio control actions is audited (G.5).

---

## I. Phased plan

Each milestone is one commit or a short series; each states what it delivers and how it is tested. Hardware checks are done by the owner on the real hat (duplex MMDVM_HS hat, Pi 3B+). All modes come to RF. The mode milestones are ordered by effort: YSF, DMR, NXDN, M17, P25 last. The Pi-Star extras follow the core.

### M0: this note

Decided so far (2026-11-24): two products in one repo, the two-mode routing model, all modes on RF, the owner hardware, optional AllStar, all Pi-Star extras wanted. Still to decide: section J.

### M1: RF endpoint in the router, with a fake modem (no hardware)

- `internal/dv/rf`: an `Endpoint` interface (frames in and out, calls, state) and a fake modem that replays captured frames.
- Router: the two routes (E.1), the `route` switch with the busy rule (E.5), the `source` tag, loop guard, contention rules, hang time, TX timeout. A hotspot mode that works with no USRP endpoint (C.2).
- `ctl.Talk.source`, `rf_status`, `rf_mode`.
- Tests: router tests (internal/dv/bridge/harness_test.go style) for RF-to-network pass-through, RF-to-USRP, USRP-to-RF, priority, hang time, loop prevention, TX timeout with a stalled network, mode switch exclusivity. Table tests with captured frames (testdata).
- Nothing reaches a radio.

### M2: hscp product skeleton

- `cmd/hscp`, the hscp nfpm definition, branding, the product build flag for the web UI (B.3), the setup-hotspot name, the update source for the hscp channel, CI building both sets of packages (B.7). No ASL3 dependency.
- The engine and the packages are split as decided in B.5 and B.6.
- Tests: package build and install checks without ASL3, UI build for both products, the shared engine tests run once.
- Pages are mostly empty here; the point is that both products build and install, and each can run alone.

### M3: Go modem driver, YSF, receive only

- `internal/dv/modem`: the serial protocol (frame parse and build, version, status, config, per-mode data, flow control, duplex handling) in pure Go, with a serial layer that keeps `CGO_ENABLED=0` (a pure-Go termios implementation, to verify the choice).
- Helper ops `dv-rf-prepare`, `dv-rf-device-check`, `dv-rf-config` (F.4). The unit drop-in for the serial device. The Pi 3B+ UART and Bluetooth steps (F.1).
- YSF receive: RF frames to the tuned reflector (hotspot mode) and through the vocoder to AllStar (AllStar mode). TX is not coded yet, or is hard-disabled.
- Tests: a fake modem on a pty speaking the protocol, captured byte traces from a real modem, fuzz on the frame parser, helper ops with a fake root.
- **Owner on hardware:** the modem check (and the firmware version), then calls from a YSF radio showing in Last heard "via RF". No transmit.

### M4: YSF transmit

- Transmit path, the transmit switch, TX timeout, watchdog, keep-alive (H).
- Tests: router and modem tests for timeouts, stall and exit paths (the carrier is dropped on every one); loop and contention tests.
- **Owner on hardware:** a dummy load or very low power first; then one YSF call to a reflector and back. Compare with the old hotspot image on the same radio.

### M5: DMR (both timeslots on the duplex hat)

- DMR on RF through the existing burst coder (internal/dv/dmr): duplex slot handling, colour code, the repeater ID rules (G.1), DMRD pass-through in hotspot mode.
- Tests: captured DMR frames from the existing testdata, and new captured RF traces; slot timing tests against the fake modem.
- **Owner on hardware:** a DMR call with a real radio to the tuned network and back (hotspot mode); then AllStar mode with a call to AllStar and back.

### M6: panel, CLI, API for RF, and the AllStar radio control

- The RF card or page, the mode switch, dashboard badges, `dv rf ...`, API, audit, backup and restore (G.6). The rendered-config view and the `advanced` passthrough (C.1).
- Radio control of AllStar in AllStar mode: DMR private call to a node number links, a reserved number unlinks, a status number (E.3). YSF, P25, NXDN and M17 equivalents once designed. Mode scan if the design allows (E.8).
- Tests: Go handler tests, Svelte component checks, a backup round trip that forces `transmit` off, control call tests (allowed and refused IDs).

### M7: NXDN

- NXDN on RF. The 72-bit AMBE+2 FEC is not implemented in the repo (internal/dv/nxdn/doc.go) and is needed here.
- Tests: the existing NXDN vectors plus new FEC vectors from captured frames.
- **Owner on hardware:** one call, if the owner has an NXDN radio.

### M8: M17 (if the firmware supports it)

- M17 on RF using internal/dv/m17. Gated on the firmware reporting M17 (F.3; firmware 1.6.1 to verify). If the firmware does not, the milestone waits for a firmware change.
- Tests: the existing M17 vectors plus captured modem traces.

### M9: P25 RF framing

- The air framing the engine does not have: LDU1/LDU2, NID, FEC, link control (D.2), on top of the IMBE FEC code in internal/dv/imbe/fec.go. Last of the modes because it is the largest gap.
- Tests: synthetic and captured vectors; a self-check that framing and parsing round-trip.
- Fallback if it slips and the owner needs P25 sooner: an optional, user-installed MMDVMHost for P25 only (D.5, question 2).

### M10: Pi-Star backup importer and BrandMeister static talkgroups

- The importer (C.3) with synthetic fixtures. A preview, and no apply without a click.
- A static-talkgroup editor through the BrandMeister API (to verify the API and the key handling).

### M11: DMRGateway-style rules and several DMR networks at once

- A rules layer in the router: talkgroup and slot maps, per-network prefixes, one network per timeslot on a duplex hat.
- Importer support for DMRGateway rewrite rules.
- Tests: table tests for the rule engine; router tests with several fake networks.

### M12: Cross-mode

- AMBE+2 repack for DMR, YSF DN and NXDN (E.9), then PCM transcode for P25 and Voice FR. A "cross-mode" switch, default off.

### M13: POCSAG / DAPNET paging

- A DAPNET client and the modem's pager frames (to verify). Panel settings and a send test.

### M14: Nextion and OLED displays

- Display drivers fed from the engine state. Serial for Nextion, I2C for OLED (to verify). Needs the UART or GPIO access to be sorted in the unit profile (F.4).

---

## J. Open questions for the owner

1. **Product name.** `hscp` is a working name. What should it be called, and what should the setup Wi-Fi hotspot, the unit names and the package be called?
2. **Recommendation OK?** The native Go driver (b) as the product, no bundled MMDVMHost (D.5)? If P25 on RF turns out to matter earlier than M9, is an optional, user-installed MMDVMHost for P25 acceptable?
3. **Engine name.** Rename the engine to a product-neutral name (B.5), and accept the upgrade work for existing ASLCP installs? Or keep `aslcp-dv` for now?
4. **Web UI shape.** One Svelte codebase with a product build flag, or two app shells over a shared component library (B.3)?
5. **Both products on one Pi.** Allow it, with a shared engine package and two panels (two ports, shared or separate users)? Or make them conflict, with ASLCP on a Pi with ASL3 and hscp otherwise (B.6)?
6. **Release shape.** One shared version and tag, or product-prefixed tags? One CHANGELOG or two? One apt repo with two components, or separate suites or channels (B.7)?
7. **Hat and firmware.** Confirm the exact hat model and firmware version (the modem check will report it), and the reset pin if you want a reset button (F.2).
8. **Radio control details.** Which reserved numbers for unlink, status and parrot? Does any talkgroup count as voice for AllStar, or one chosen talkgroup? What are the YSF, P25, NXDN and M17 equivalents (E.3)? Who may control (only your own ID)?
9. **Duplex and AllStar mode.** Does AllStar mode use one DMR timeslot (and which), and is the other slot free or refused (E.2)?
10. **Mode switch by radio.** Should a radio also be able to switch between AllStar mode and hotspot mode, or only the panel and CLI (E.5)?
11. **Priority and timers.** Is "RF talker first" right in both modes (E.6)? Hang time and TX timeout defaults (suggested 2 s and 3 min)?
12. **Frequency check.** Which bands and countries should the built-in table allow, and should the owner have to confirm a country (H)?
13. **Repeater ID.** Should the hotspot be a *DMR repeater ID / ESSID* of its own, or share the main identity's ID with a fixed ESSID (G.1)?
14. **Mode scan.** Scan several RF modes at once with a mode hang, or one RF mode at a time through the dashboard (E.8)?
15. **Calibration.** Do you want a "calibration" helper in the panel, or is calibration done with other tools first?
16. **Test plan.** Is it acceptable that you do the on-hardware checks, with a dummy load for first TX, and compare against your old hotspot image?

---

## K. To verify (summary)

All of these come from outside the repo and must be checked upstream before use:

- MMDVMHost: license version ("GPL-2.0 or later"), Debian availability, exact network peer behaviour per mode (DMR master login, YSF/P25/NXDN gateway endpoints and ports), and its timers.
- Modem firmware and serial protocol: frame format, commands, buffer flow control, supported modes, **M17 support in firmware 1.6.1**, whether firmware 1.6.1 is the version on the owner's hat (the owner says "I think"), duplex build behaviour and timing, frequency/offset/deviation commands, version query, status contents (RSSI, BER), own TX timeouts, pager frames.
- Air-interface frame sizes versus network payloads per mode (D.2): DMR 33-byte bursts and DMRD, YSF 120-byte frames with FICH and YSFD, NXDN, and P25's difference.
- UART names, `config.txt` and `cmdline.txt` options, and Bluetooth overlay per Pi model and OS release: **the exact overlay name on the Pi 3B+** (`disable-bt` and `miniuart-bt` are from memory); Pi 5 differences; device group name; 64-bit OS image for the Pi 3B+.
- Reset and boot GPIO pins of each hat.
- A pure-Go serial library choice that keeps `CGO_ENABLED=0`.
- Hotspot band allocations per country.
- Pi-Star backup file names and formats (C.3), what each Pi-Star feature does (C.1), the BrandMeister API for static talkgroups, and ASL3 package sizes (C.2).
- Header fields per mode (YSF DG-ID, P25 and NXDN talkgroup range, M17 destination) for the radio control mapping (E.3).
- Display interfaces (Nextion serial, OLED I2C) and the DAPNET protocol.
