# ASLCP Design Document

ASLCP is a modern web control panel for AllStarLink 3 (ASL3) nodes. ASL3 is Asterisk (22 as of ASL3 3.10; earlier releases used 20 LTS) plus `app_rpt`, the amateur-radio linking system. This document describes goals, architecture, the configuration engine, safety model, packaging, and the v1 scope.

Items marked **To verify** are assumptions that need confirmation on real hardware or against upstream sources.

## 1. Goals and requirements

| Requirement | Meaning |
|---|---|
| No PHP | Single Go binary, no interpreter runtime on the node. |
| Full configurability | Every ASL3 file is reachable: forms for known settings, a raw editor for everything. |
| Modern look and feel | Responsive, mobile-first, dark and light themes. |
| Easy install and update | One-line install, signed apt repo, updates through `apt upgrade`. |
| Works with any configuration | Real nodes vary widely. The panel adapts to what is on disk; migration is optional. |

**Targets.** Raspberry Pi 3, 4, 5 and Zero 2 W on Debian 12 (bookworm) or 13 (trixie) and Raspberry Pi OS, arm64, plus amd64. armhf (32-bit Pi OS) is built but best-effort: not tested in CI on hardware until a tester with a 32-bit node is available, and dropped if ASL3 itself does not ship armhf packages.

**Prior art.** AllScan (PHP, limited, outdated: connects and manages favorite nodes) and Allmon3 (ASL's monitor, widely used for public status pages). ASLCP replaces AllScan's role, coexists with Allmon3 and other AMI clients, aims to offer a better public status page (section 9), and goes much further on configuration.

## 2. Scope and non-goals

ASLCP is installed after ASL3 is present. It does not install ASL3, but it ships its own first-run setup wizard that can replace ASL's first-setup routine (`asl3-menu` node setup).

**Non-goals**

- Installing ASL3 or Asterisk itself.
- Non-Debian distributions (for now).
- Telephony features (SIP, PSTN, voicemail, queues). The stock telephony files are present on disk but not loaded; they are reachable only through the raw editor and hidden by default.

## 3. Architecture overview

```
Browser (Svelte SPA)
   |  JSON REST + Server-Sent Events
   v
aslcp (Go, user: aslcp) ---- AMI :5038 ----> Asterisk / app_rpt
   |        |
   |        +--> SQLite (/var/lib/aslcp)
   |
   +-- unix socket --> aslcp-helper (privileged, fixed allowlist)
                          |-- atomic writes under /etc/asterisk
                          |-- allowlisted `asterisk -rx` reloads
                          `-- systemctl restart asterisk / allmon3
```

### 3.1 Stack

| Layer | Choice | Notes |
|---|---|---|
| Backend | Go, single static binary | Frontend embedded via `go:embed`. Target ~15-30 MB RSS (**To verify** on a Pi Zero 2 W). Trivial cross-compilation. |
| Frontend | Svelte 5, Vite, TypeScript, Tailwind, shadcn-svelte | Mobile-first, theme switch. |
| API | JSON REST plus Server-Sent Events (SSE) for live updates | Contract in `docs/API.md`. SSE is one-way, standard-library only, and proxy-friendly; commands go over REST. |
| Live data | AMI client in Go | Events bridged to SSE. |
| Storage | SQLite via a pure-Go driver (e.g. `modernc.org/sqlite`) | Users, favorites, audit log, settings, config snapshot metadata. |

### 3.2 Live data pipeline

Formats below were taken from the app_rpt source (`rpt_manager.c`, `rpt_link.c`, `rpt_cli.c`) and Allmon3's implementation. They still need confirming against a live node.

The Go AMI client connects to `localhost:5038` and maintains one authenticated session with reconnect, backoff and `Ping` keepalive. It combines two sources:

1. **Events (instant).** app_rpt emits AMI events named after its variables: `RPT_RXKEYED`, `RPT_TXKEYED`, `RPT_ETXKEYED`, `RPT_LINKS`, `RPT_ALINKS`, `RPT_NUMLINKS`, and others, each carrying `Node` and `EventValue`. They use the `call` event class, so the AMI user needs `read` including `call` (or `all`), and the login must request events. Allmon3 logs in with events off and relies only on polling.
2. **Polling (resync).** `Action: RptStatus` with `Command: XStat` (connections table, network-wide linked-node list with modes, keying variables, node state flags) and `Command: SawStat` (per-link keyed flag and seconds since last key/unkey). Fast polling (about 1 s) only while a viewer is watching, slow otherwise.

Link control uses `rpt cmd <node> ilink <code> <target>`: 1 disconnect, 2 monitor, 3 transceive, 8 local monitor, with permanent variants. Inputs are strictly validated before being placed in a command.

Allmon3 bugs we avoid: it looks for `RPT_TXEKEYED` instead of `RPT_ETXKEYED` (so it never shows external TX), and it ignores the `L` (local monitor) link mode.

Allow/deny lists live in AstDB and are managed through AMI database actions (UI in a later release).

### 3.3 Authentication and web security

- Local users, argon2id password hashes, server-side sessions with secure, HttpOnly, SameSite cookies.
- Roles: `admin` (everything), `operator` (connect/disconnect, favorites, monitoring), `viewer` (read-only, no secrets).
- CSRF protection on all state-changing requests (custom header plus Origin check).
- Login rate limiting.
- Optional HTTPS: a self-signed certificate generated at install, or a user-supplied certificate.
- Default port 8080, configurable, so ASLCP coexists with Allmon3 and Cockpit. The optional public status page is served on the same port without login (section 9.3).

### 3.4 Privilege separation

The web process runs as the dedicated `aslcp` user, never root. A small separate binary, `aslcp-helper`, performs the only privileged operations:

| Operation | Constraint |
|---|---|
| Write a file under `/etc/asterisk` | Atomic (temp file + rename in the same directory), preserving owner and mode. Path must resolve inside the allowed tree; no symlink escapes. |
| `asterisk -rx <cmd>` | Command must match a fixed allowlist (reload variants, status queries). No shell, no free-form arguments. |
| `systemctl restart` | Only `asterisk` and detected AMI-client services (e.g. `allmon3`). |

Invocation is via a systemd socket-activated service (preferred) or a polkit/sudoers rule with the same allowlist. The helper validates every request independently of the web process. The choice between the two mechanisms is a **To verify** item (see section 13).

## 4. Configuration engine

The panel must work with whatever is on disk. The engine has three layers.

### 4.1 Layer 1: syntax (`internal/astconf`)

A lossless parser and writer for Asterisk configuration. It builds a concrete syntax tree that retains every byte.

**Must handle**

- Comments (`;` and block comments), blank lines, alignment, trailing comments.
- Both `=` and `=>` operators.
- Repeated keys (e.g. several `allow =` lines): ordered and preserved.
- Template stanzas `[name](!)`, inheritance `[node](parent)`, multi-parent `[x](!,a,b)`, and non-template parents.
- `#include` and `#tryinclude` with quoted or unquoted paths, globs (expanded alphabetically), and trailing comments.
- Values containing `,` `|` `;` `"` or `${}`: treated as opaque strings, never split or re-quoted by the engine.

**Editing model.** Edits are expressed as operations on nodes (set value, insert key, delete key, add stanza). Serialization re-emits original bytes for untouched nodes, so only changed lines differ in the diff.

**Golden test.** Parse followed by serialize of an unmodified file must be byte-identical. This runs over the whole anonymized corpus in CI.

**Resolver.** Computes effective values with provenance: for each key, which file, stanza (template or node), and line it comes from. Last parent wins; the stanza's own keys override inherited ones. The resolver follows includes in order so the UI can say "this value comes from `custom/rpt/foo.conf:12` via template `node-main`".

### 4.2 Layer 2: schema (`internal/schema`)

A knowledge base of known settings per file and stanza.

| Field | Purpose |
|---|---|
| type | string, int, bool (`yes/no`, `1/0`), enum, node number, duration, list |
| allowed values / range | Drives validation |
| default | Shown as placeholder; "effective default" display |
| description | Inline help |
| reload requirement | `none`, module reload, dialplan reload, restart (**To verify** per key on hardware) |
| sensitive | Masked in UI, excluded from logs and diagnostics |

Rules:

- Known keys render as typed forms with validation.
- Unknown keys are always shown and editable as raw key/value pairs and are never dropped.
- Every file also has a raw text editor (CodeMirror with syntax highlighting). Save runs the validation pipeline first (parse check, schema warnings, cross-file checks).
- `extensions.conf` is treated as code: raw editor, plus a form for `[globals]` only.
- `modules.conf` is handled specially: `autoload=no`, `load`/`noload`/`require`, with mixed `=` and `=>` preserved.
- `;MENU:category:type:description` machine-readable comments in `custom/` files (used by `asl3-menu`) are preserved byte-for-byte and may later be used to label custom entries in the UI.

### 4.3 Layer 3: guided actions

Wizards for common multi-file operations: add node, change node number, change radio interface, set up EchoLink, registration.

Each action:

1. Checks preconditions against the parsed tree.
2. If the config does not match expectations, refuses with a clear explanation and a link to the raw editor, rather than guessing.
3. Produces a full multi-file diff for review before anything is applied.
4. Applies through the standard safety pipeline (section 6).

**Node renumbering** is the canonical example. A node number appears in roughly eight places:

| Location | Reference |
|---|---|
| `rpt.conf` | `[nodes]` entry and the node stanza name |
| `simpleusb.conf` / `usbradio.conf` | Device stanza |
| `voter.conf` | Voter stanza |
| `echolink.conf` | `astnode` |
| `extensions.conf` | `[globals]` `NODE` |
| `allmon.ini.php` | Node section of the legacy Allmon2/Supermon-format file (also read by AllScan), if present |
| `savenode.conf` | Shell `KEY=VALUE` file |
| `rxchannel` values | Channel references |

A rename is one guided transaction covering all of them, or none.

**Radio interface changes.** Changing `rxchannel` implies matching `modules.conf` load lines and a matching device stanza; the wizard handles all three together.

**Layout migration** (old flat layout to the current templated layout with `custom/` includes) is an optional, previewed, reversible tool planned for a later version. It is never required.

### 4.4 Files in scope

| Group | Files |
|---|---|
| Core | `rpt.conf` (stanzas such as `[general]`, `[nodes]`, `[node-main](!)`, per-node stanzas, `[functions]`, `[telemetry]`, `[morse]`, `[wait-times]`, `[memory]`, `[macro]`, `[events]`, `[controlstates]`, `[schedule]`) |
| Radio | `simpleusb.conf`, `usbradio.conf`, `res_usbradio.conf`, `voter.conf` |
| Linking | `iax.conf`, `echolink.conf`, `rpt_http_registrations.conf`, `rpt_auth.conf` |
| Platform | `manager.conf`, `modules.conf`, `extensions.conf`, `logger.conf`, `http.conf`, `gps.conf`, `asterisk.conf` |
| Custom | `custom/**` includes |
| Non-Asterisk | `savenode.conf`; AMI-client configs (see 4.5) |

### 4.5 Other AMI clients

Other tools may use the same AMI login: Allmon3, AllScan, Supermon. ASLCP does not depend on any of them. It detects which are installed (packages, services, config files) and, when the AMI credentials in `manager.conf` change, either updates their configuration or shows exactly which tools will stop working until updated. Detection details are **To verify** (section 13).

## 5. Secrets handling

Secrets are spread across `manager.conf`, `iax.conf`, `echolink.conf`, `voter.conf`, `rpt_http_registrations.conf`, `savenode.conf`, `custom/iax/*`, and AMI-client configs such as `allmon.ini.php` and Allmon3's config.

- Masked in the UI by default; reveal requires `admin` and is audited.
- Never written to application logs or audit-log payloads.
- Excluded from exported diagnostics unless the user explicitly opts in.
- Config history stays local to the node (see section 6).
- Diff views mask sensitive values unless an admin reveals them.

## 6. Safety pipeline

Every apply follows the same sequence:

1. **Validate**: parse, schema and cross-file checks.
2. **Snapshot**: commit the current `/etc/asterisk` into a git repository at `/var/lib/aslcp-config-history`, with author and message. Secrets stay local; the repository is never pushed anywhere.
3. **Atomic write** through the helper.
4. **Targeted reload** using the per-key reload requirement; restart only when needed.
5. **Health check** via AMI: Asterisk responds, node is registered and up.
6. **Rollback offer**: if the health check fails, offer one-click automatic rollback to the snapshot, followed by the same reload and check.

Additional features:

- History view with per-commit diffs and one-click restore (itself a snapshot-protected apply).
- Audit log in SQLite: who, when, what changed (files and keys, secrets redacted).
- Multi-file applies are best-effort atomic: all files are staged, written in sequence, and any failure restores the snapshot. True filesystem-level atomicity across files is not available. **To verify** that this is sufficient in practice.

## 7. "Upstream changes" view

Package updates leave `*.dpkg-dist` files beside live ones, and live and dist can differ structurally. A naive text merge is not acceptable.

The view parses both files and compares per stanza and per key: added, removed, and changed keys, plus template/inheritance differences. The user adopts individual changes; each adoption goes through the normal safety pipeline. Planned for post-v1.

## 8. Setup wizard

The first-run wizard can replace the `asl3-menu` node setup.

| Step | Details |
|---|---|
| Detect | Is ASL3 present, which version, existing nodes, existing layout. |
| Node | Node number and password; registration entries. |
| Identity | Callsign, ID, basic `[general]` settings. |
| Radio interface | SimpleUSB, USBRadio, none (local/pseudo hub), USRP, Voter. |
| AMI | Create or update credentials; update or warn about detected AMI clients (section 4.5). |
| Review | Full multi-file diff, then apply through the safety pipeline. |

On an already configured node, the wizard is not forced: it detects the existing setup, offers read-only import into the panel, and sends the user to the guided actions for changes.

## 9. Feature split

### 9.1 v1 (MVP)

v1 ships in two milestones. The first is a better-built, better-looking AllScan replacement; configuration comes second.

**Milestone 1: AllScan replacement**

- Auth (local users, roles).
- Live status of local nodes: connected-links table (node, description, direction, connected time, mode, keyed), node TX/RX keying indicators, last-heard per link, whole-network linked-node list.
- Connect, disconnect, monitor and local monitor (optionally permanent) from favorites or by node number; disconnect all.
- Favorites: add, edit, group, reorder; import from an existing AllScan installation.
- Node lookup: callsign, description and location from the ASL node list, shown next to every link and searchable.
- System strip: CPU temperature, load, uptime, Asterisk uptime.
- `.deb` packages and the apt repository.

**Milestone 2: configuration**

- Config engine, with schema forms for: `rpt.conf` node settings, `simpleusb`, `iax` general, `echolink`, registrations, `manager`.
- Raw editor for all files.
- Snapshots, history, rollback.
- First-run setup wizard.
- System page: Asterisk restart, log tail, disk.

**Then:** public status page, phase 1: live, read-only status of the node's own local nodes (keying, connected links, registration). See 9.3.

### 9.2 Later

- USB radio audio tuning UI (RX/TX levels; live meters if feasible).
- DTMF functions, macros, telemetry, and scheduler editors.
- Allow/deny list manager (AstDB).
- Upstream `.dpkg-dist` merge view.
- Layout migration tool.
- Voter / RTCM.
- Plugins (e.g. M17 gateway management).
- Public status page, phase 2: activity history (last heard, busiest times, connection history, uptime), embeddable widget, optional map.
- **Want:** multi-node / multi-host status (one page showing nodes on several servers, as Allmon3 can) and fleet management.
- Backup/restore export.
- i18n.

### 9.3 Public status page

A replacement for the public Allmon3 pages, built on the same AMI live data as the dashboard.

**Status: phase 1 and recent activity are implemented.** History beyond recent activity, a widget API and a map are phase 2.

- **Single port.** Decision: no separate listener. The public view lives in the same SPA, on the admin port. When the owner enables it (Settings > Public page, default off) and the visitor has no session, `/` shows the read-only view with a "Log in" button; when disabled the login page is shown as before. Logged-in users get the dashboard plus a "Public view" preview. The app decides with the unauthenticated `GET /api/public/info` (`enabled`, `title`, `loggedIn`), which also lets visitors skip the session check (no 401).
- **Data endpoints.** `GET /api/public/status` (JSON) and `GET /api/public/events` (SSE: snapshot on connect, then only changes, ping every 20 s, closed after 6 h). Unauthenticated, 404 while disabled, no cookies. Every other `/api` route keeps requiring a session (tested).
- **Read-only and minimal.** Content is built from dedicated structs (`internal/public`), not the admin structs: node number and node-list info, keying, stale flag, links (node, kind, info, direction, mode, connected, keyed, since key/unkey), link count, optional network list and optional recent activity. Never IP addresses (a scrub also removes IP-like text from node-list fields and link names), never system information, favorites, users or settings. "Show callsigns" off hides link, network and activity identities (numbers and kind remain; the owner's own node info stays).
- **Owner-controlled.** Stored in the settings table (`public.*`): enabled, title, nodes (default all), show callsigns / network / activity (default on), allow embedding (default off), trust proxy (default off). Changes apply to connected clients immediately.
- **Abuse protection.** Per-address token bucket (2 req/s, burst 10, shared by info/status/events) answers 429; at most 4 SSE connections per address (429) and 200 in total (503). Without "trust proxy" the address is the TCP peer; with it, the last `X-Forwarded-For` entry (the one added by the trusted proxy). IPv6 clients are grouped by /64. While public SSE clients are connected the monitor polls fast (1 s), like for admin clients.
- **Framing.** All responses carry `frame-ancestors 'none'` and `X-Frame-Options: DENY`, except `/embed` (the same SPA, chrome-less) while the public page and "allow embedding" are both on: there `frame-ancestors *`. The login page can therefore never be framed (no clickjacking).
- **Security trade-off.** Because it shares the port, enabling the public page and exposing the panel to the internet also exposes the login page (login is rate-limited; strong passwords are required). The page recommends a tunnel or reverse proxy (Cloudflare Tunnel, Caddy) rather than router port forwarding. A separate listener (own port, nothing from `/api`) was the first design and was dropped in favor of one port and one firewall rule.
- **Recent activity.** The monitor updates feed `internal/activity`, which records a row `(local node, link or "local", start, duration, kerchunk)` in the `activity` table when a link or the local receiver unkeys. Keyups under 0.5 s are ignored as glitches, under 1.5 s flagged as kerchunks; local monitor links are ignored; unknown state (stale) discards open keyups. Retention 7 days and at most 10,000 rows (pruned at start, daily and every 100 inserts). The page shows the last 25 for the shown nodes; `GET /api/activity` is the admin-side listing.
- **Later.** Busiest times, connection history and uptime, a widget API, an optional map (phase 2); multi-host status is a future want.

## 10. Packaging and updates

- nfpm (pinned in the Makefile, `make deb` / `make debs`) builds `.deb` for arm64, armhf, and amd64 in GitHub Actions (`.github/workflows/`). Definitions live in `packaging/`.
- A signed apt repository hosted on GitHub Pages. A one-line install script adds the repo key and source, then runs `apt install`.
- Updates through normal `apt upgrade`; the in-app "update available" notice reads `latest.json` (see below).
- Release channels and process: versions are plain `X.Y.Z`. Every tag `vX.Y.Z` builds the debs and publishes them to the `beta` suite (and `latest-beta.json`), with a GitHub pre-release. A release is made by the `Promote` workflow (manual, input: version): the already built debs move from `pool/beta` to `pool/main`, `latest.json` is updated and the GitHub release becomes the full/latest release. No rebuild, so the tested package is the release; promotion refuses a version that is not in beta and is idempotent. The apt repo has two suites in one repository: `beta` indexes all of `pool/` (new builds in `pool/beta`, everything promoted in `pool/main`), `stable` indexes only `pool/main`, so beta nodes also get every release. Both suites are signed. Retention keeps the newest 5 per architecture in each pool directory separately (never the version just added or promoted). `latest.json` is the newest promoted version (format unchanged, existing installs read it), `latest-beta.json` the newest overall. Release notes come from the CHANGELOG section matching the version (`## [1.1.16]`). `install.sh` is refreshed only when the version being published is the newest in the pool, so a backport does not revert it. The 1.0.x line is maintained on the `release/1.0` branch (CI runs there; the release workflow runs on every `vX.Y.Z` tag and uses the tagged commit's script); a backport tag also goes to beta first and needs a promote. The `Remove from beta` workflow (dry run by default) deletes beta-pool versions by glob. Debian ordering still decides what apt offers; a node on a version higher than the suite offers is never downgraded automatically.
- The channel of a node is the suite in `/etc/apt/sources.list.d/aslcp.list`, so the web UI and `apt upgrade` agree. The helper has two fixed ops, `update-channel-stable` and `update-channel-beta`, that rewrite only the suite word of the `aslcp` line atomically (refused while an upgrade runs); its status reports the current `channel`. Updates tab has the switch (`PUT /api/updates/channel`); `install.sh` takes `ASLCP_CHANNEL=beta` and otherwise keeps the suite of an existing source file. apt never downgrades, so switching beta to release waits for a newer release.
- Package contents: systemd units (server, helper socket and service), `/etc/aslcp/aslcp.conf`, `/var/lib/aslcp`, a firewalld service definition.
- Public home: one public repo, `artw1/aslcp`. `main` is the source (README, `SECURITY.md`, `LICENSE`, issue forms in `.github/ISSUE_TEMPLATE`, screenshots in `docs/images`) and the releases are created there from the tags. The apt repository lives on the `gh-pages` branch of the same repo (GitHub Pages at `https://artw1.github.io/aslcp`), written by the workflows with the built-in token via `packaging/apt-checkout.sh` (creates an orphan branch on the first release) and `packaging/publish-apt.sh`. The .deb's `/usr/share/doc/aslcp/copyright` (DEP-5) is the static `packaging/copyright`; the licences of bundled third-party code are in `packaging/THIRD-PARTY-NOTICES`. Bug reports use the issue form; Settings > About links to it and `GET /api/diagnostics` provides a redacted support file (see API.md).
- Implemented: the public repo `artw1/aslcp` (GitHub Pages) is produced by `packaging/publish-apt.sh` from the release workflow; the in-app update flow (Updates tab) calls `aslcp-helper` (`update-status|check|apply|channel-stable|channel-beta`), which runs the upgrade as a transient systemd unit. The helper also has fixed system-update ops (`os-check|os-status|os-apply|reboot|aslcp-restart`: only fixed apt-get/systemctl commands, never a package name or any argument from the web process; Updates tab). Otherwise it implements only update, config, backup and firewalld operations; the `/etc/asterisk` write and `asterisk -rx` operations of section 3.4 are still to come.
- `postinst` creates the `aslcp` user, the data directory, the config file (without overwriting an existing one), the self-signed certificate, and the helper's permissions.
- Config files are marked as conffiles so upgrades never clobber local edits.
- Package dependencies on `asterisk`/`asl3` packages: exact names are **To verify**.

## 11. Testing

| Level | Approach |
|---|---|
| Unit | Go tests for parser, resolver, schema, auth, store. |
| Golden | Round-trip byte-identical test over the anonymized config corpus in `testdata/configs/`. Corpus covers old flat layouts, templated layouts, radioless hubs, multi-node, and custom bridge nodes (e.g. USRP to an M17 gateway). Secrets are scrubbed before inclusion. |
| Integration | A fake AMI server replays recorded event and command-output transcripts. |
| Fuzz | Go fuzzing on the parser (never panic; round-trip invariant). |
| E2E | Playwright against a built binary with a fake AMI and a temp config tree. |
| CI | GitHub Actions: lint, test, build for all three architectures, package smoke test. |
| Hardware | Manual checklist on a Pi (reload semantics, memory use, audio device handling, upgrade path). |

## 12. Repository layout

```
cmd/aslcp/          server
cmd/aslcp-helper/   privileged helper
internal/astconf/   parser, writer, resolver
internal/schema/    settings knowledge base
internal/ami/       AMI client
internal/api/       REST + SSE
internal/auth/      users, sessions, CSRF
internal/store/     SQLite
internal/system/    host metrics, logs, service control
web/                Svelte app
packaging/          nfpm, systemd units, postinst, apt repo scripts
testdata/configs/   anonymized real-world corpus
docs/
```

## 13. Open questions / to verify

| Topic | Question |
|---|---|
| Reload semantics | Which settings take effect with `module reload app_rpt.so`, `iax2 reload`, `dialplan reload`, usb channel driver reload, versus full restart? Verify on hardware; record per key in the schema. |
| `asl3-menu` and `custom/` | How does `asl3-menu` consume `custom/` and the `;MENU:` comments? Should ASLCP coexist with it, and what happens if both edit the same file? |
| AMI clients | Exact config locations and formats of Allmon3 (believed `/etc/allmon3/`), AllScan and Supermon; whether `allmon.ini.php` is Allmon2/Supermon format as believed. Update their credentials automatically or only warn? |
| Public page exposure | Recommended deployment (Cloudflare Tunnel, Caddy, port forward) and defaults for what is public. |
| Node list source | Source, format, size, and update cadence of the ASL node list for lookup; local caching and offline behavior. |
| armhf support | Does ASL3 publish armhf packages? If not, drop the armhf build. If yes, keep it best-effort (it costs only an extra build target in Go) until a 32-bit tester is available. Relevant for older 32-bit Pi OS installs and memory-constrained Zero 2 W setups. |
| Minimum ASL3 version | Which ASL3 releases (and `app_rpt` output formats) are supported. |
| Helper transport | Socket activation versus polkit/sudoers: which is simpler and more robust on bookworm and trixie. |
| Multi-file atomicity | Is staged write plus snapshot rollback sufficient? |
| Memory budget | Does the binary plus SQLite stay within ~30 MB on a Zero 2 W under live SSE load? |
| Package dependencies | Exact ASL3 package names to depend on or recommend. |
