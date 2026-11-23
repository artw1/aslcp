# Configuration editing (2.0)

Configuration editing of `docs/DESIGN.md` (sections 4-6), released in 2.0.

| Slice | Content |
|---|---|
| **beta.1: foundation** | File browser, raw editor, validate + diff review, snapshot history (git), apply through the helper, targeted reload, health check, rollback, history view with restore, Asterisk restart. |
| beta.4: forms | Schema (`internal/schema`) and typed forms for `rpt.conf` node settings, `simpleusb`, `iax` `[general]`, `echolink`, registrations, `manager`. Secrets masked. Provenance from `astconf.Effective`. |
| beta.5: guided | Node renumber, radio interface change, setup wizard (DESIGN 4.3, 8). |

Everything below is beta.1.

## Principles

- Admin only, CSRF-protected, every apply/restore/reveal audited (no secret values in audit payloads).
- Lossless: the raw editor saves exactly the bytes the user typed; `astconf.Parse` must succeed (syntax check) but never rewrites the text.
- Optimistic concurrency: every write carries the sha256 of the version it was based on; the helper refuses (conflict) if the file changed on disk meanwhile (e.g. `asl3-menu`, manual `nano`).
- The server never writes `/etc/asterisk` and never reads the history repository; both go through the root helper. The helper validates every path itself and never trusts the server.

## Helper protocol extension

Until now requests carried no arguments. Config ops add one optional, strictly typed field; the other ops must reject a request that carries it.

```go
type Request struct {
    Op     string         `json:"op"`
    Config *ConfigRequest `json:"config,omitempty"` // config-* ops only
}
type ConfigRequest struct {
    Path    string       `json:"path,omitempty"`    // config-read
    Files   []ConfigFile `json:"files,omitempty"`   // config-apply
    Message string       `json:"message,omitempty"` // commit message (<= 200 chars, one line)
    Author  string       `json:"author,omitempty"`  // ASLCP username (<= 64, [A-Za-z0-9._-])
    Commit  string       `json:"commit,omitempty"`  // config-show / config-restore: 7-40 hex
    Limit   int          `json:"limit,omitempty"`   // config-log (1-200)
    Offset  int          `json:"offset,omitempty"`
}
type ConfigFile struct {
    Path    string `json:"path"`
    Content string `json:"content"`
    Base    string `json:"base"` // sha256 hex of the version edited; "" = new file
}
```

Request size limit for config ops: 4 MiB (others stay small).

**Path rules (enforced in the helper):** relative to `/etc/asterisk`; matches `^[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*$`; ends in `.conf`; at most 4 components; after `filepath.Join` + `EvalSymlinks` of the parent directory the result stays inside `/etc/asterisk` (realpath of the root); the target itself, if it exists, must be a regular file (not a symlink, not a device). New files only under `custom/` (directory must already exist). Per file <= 1 MiB, <= 50 files per apply, content must be valid UTF-8 without NUL.

| Op | Request | Result |
|---|---|---|
| `config-list` | - | `[{path, size, mtime, sha256, mode, owner, group}]` for every `*.conf` under `/etc/asterisk` (recursive, max depth 4, symlinks not followed, at most 500 entries) |
| `config-read` | `path` | `{path, content, sha256}` |
| `config-apply` | `files, message, author` | `{commit, before}`; see pipeline |
| `config-log` | `limit, offset` | `[{commit, date, author, message, files: [path]}]`, newest first |
| `config-show` | `commit` | `{commit, date, author, message, files: [{path, status: added\|modified\|deleted, diff}]}` (unified diff vs parent, 3 lines context, raw: masking is the server's job) |
| `config-restore` | `commit, author` | `{commit, before}`: writes the files as they were in `commit` (only files that differ; files that did not exist then are left alone and listed in `skipped`) |
| `asterisk-restart` | - | `{ok}`: `systemctl restart asterisk` |

**History repository:** `/var/lib/aslcp-config-history` (not under `/var/lib/aslcp`: that directory is the `aslcp` user's data dir and is writable by it, so a repository inside it could be tampered with by the unprivileged server), root:root 0700, a bare-style git dir used with `GIT_DIR=<repo> GIT_WORK_TREE=/etc/asterisk`, `core.fileMode=true`, local identity `aslcp <aslcp@localhost>` with `--author` from the request. Initialized on first use with an "Initial snapshot" commit of the current tree. `git` is called with a fixed argv (never through a shell), `-c` options to disable hooks/pager/gpg (`core.hooksPath=/dev/null`, `commit.gpgSign=false`), env `HOME=/nonexistent`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`. The package gains `Depends: git`.

**config-apply pipeline** (one at a time, under a lock shared with restore and update-apply):

1. Validate all paths and sizes; read every target; compare sha256 with `Base` (missing file and `Base==""` is ok). Any mismatch: error `conflict` naming the file(s); nothing written.
2. Snapshot: `git add -A` + commit "Before: <message>" if the tree differs from HEAD (captures outside edits). `before` = HEAD after this step.
3. Write each file atomically: temp file in the same directory (name not ending in `.conf`), copy uid/gid/mode of the existing file (new file: `asterisk:asterisk 0640` if that user/group exists, else root 0644), fsync, rename.
4. On any write error: rewrite the already-written files from `before` the same way, return the error.
5. Commit "<message>" with the author; return its id.

Restore is the same pipeline with the file contents taken from `git show <commit>:<path>`, message "Restore <short id>: <original message>".

## Server

All under `/api/config`, admin only.

| Method / path | Body | Response |
|---|---|---|
| `GET /api/config/files` | | `[{path, size, mtime, sha256, known: bool, group: "Core"\|"Radio"\|...}]` (group/known from the DESIGN 4.4 table; unknown files under "Other") |
| `GET /api/config/file?path=` | | `{path, content, sha256}` |
| `POST /api/config/preview` | `{files: [{path, content, base}]}` | `{files: [{path, ok, error?: {line, message}, diff, changed, conflict?}], reload: [{command, reason}], restart: bool}`; `conflict` is set when the file changed on disk since `base`; `reload` is `[]`, never null |
| `POST /api/config/apply` | `{files: [{path, content, base}], message, reload: bool}` | `{commit, before, files: [path], restart: bool, reload: [{command, ok, output}] \| null, health: {ok, checks: [{name, ok, detail}]} \| null}` (`reload`/`health` are null when `reload` was false); 409 `{code: "conflict", files: [path]}`; 422 `{code: "syntax_error", issues: [{path, line, message}]}` |
| `GET /api/config/history?limit=&offset=` | | helper `config-log` |
| `GET /api/config/history/{commit}?reveal=1` | | helper `config-show`; values of sensitive keys masked unless `reveal=1` (audited) |
| `POST /api/config/history/{commit}/restore` | `{reload: bool}` | like apply, plus `skipped: [path]` (files that did not exist in that commit) |
| `POST /api/system/asterisk/restart` | | `{ok}`; shares its implementation (AMI `core restart gracefully` when connected, else the helper's `systemctl restart`) with the older `POST /api/system/restart-asterisk` |

**Preview** parses each file with `astconf.Parse` (error with line on failure), computes a unified diff against the current content (server-side, Go), and derives the reload plan from the changed files.

**Reload plan** (AMI `Command`, fixed strings only; union over changed files, de-duplicated):

| File | Command |
|---|---|
| `rpt.conf`, `custom/rpt/*` | `module reload app_rpt.so` (verify on hardware) |
| `iax.conf`, `custom/iax/*` | `iax2 reload` |
| `extensions.conf`, `custom/extensions/*` | `dialplan reload` |
| `manager.conf` | `manager reload` (then the server reconnects AMI if its own credentials changed) |
| `simpleusb.conf` | `module reload chan_simpleusb.so` |
| `usbradio.conf` | `module reload chan_usbradio.so` |
| `echolink.conf` | `module reload chan_echolink.so` |
| `rpt_http_registrations.conf` | `module reload res_rpt_http_registrations.so` |
| `logger.conf` | `logger reload` |
| anything else | `restart: true` (the UI offers "Restart Asterisk" instead of reloading) |

The table lives in one Go map so it can be corrected after testing on the node (DESIGN 13, "Reload semantics").

**Health check** (after reload or restart, wait up to 15 s): AMI connected and answering; every local node that existed before the apply answers `rpt` status. Result per check. If not ok, the UI offers one-click rollback = restore of `before` + the same reload + health check.

**Masking:** keys `secret`, `password`, `passwd`, `pass`, `md5secret`, `dbsecret`, and `register =>` / `register =` values (the `user:secret@` part) are replaced with `********` in history diffs unless revealed. The raw editor shows real content (admin only); reading a file is audited as `config.read`.

## Forms

Forms and wizards are clients of one planner (`internal/nodecfg`) that sits on top of the file editing above. It reads the files back on every request, so edits made in the raw editor show up at once. All routes are admin only; request and response shapes are in [API.md](API.md), "Node forms". Design: DESIGN 4.3 and 8.

**Schema.** `internal/schema` is a hand-curated list of known keys. A key id is `file:scope:name`; the scope is `node` (the node's stanza in `rpt.conf`, with template inheritance), `device` (the node's device section in the radio file) or a literal section name. Each key has a type (`string`, `int`, `yesno`, `flag`, `enum`, `node`), range or values, default, description and a reload class (`verified`, `assumed` or `restart`; no key is `verified` until tested on hardware). The same list is served by `GET /api/config/schema` and committed as `web/mock/schema.json` for the mock (a Go test fails if they differ; `go test ./internal/nodecfg -update` rewrites it). Keys marked secret are never returned or logged: the overview shows `********`, plans and diffs mask them.

**Fields per form** (all in the node's stanza of `rpt.conf` unless a file is named):

| Form | Fields |
|---|---|
| identity | `idrecording` (callsign, written as `\|i<CALL>`), `idtalkover`, `idtime`, `politeid`, `callerid` |
| radio | `rxchannel` (restart); `simpleusb.conf` device: `carrierfrom`, `ctcssfrom`, `invertptt`, `rxboost`, `preemphasis`, `deemphasis`, `plfilter` |
| duplex | `duplex` (restart), `linktolink`, `telemdefault`, `telemdynamic`, `holdofftelem`, `nounkeyct`, `unlinkedct`, `remotect`, `linkunkeyct` |
| timers | `hangtime`, `althangtime`, `totime`, `time_out_reset_unkey_interval`, `tailmessagetime`, `parrot` |
| linking | `linkmongain`, `erxgain`, `etxgain`, `context`, `startup_macro`; `iax.conf` `[general]` `bindport` (restart) |
| status | `statpost_url`, `statpost_time` |
| general | `rpt.conf` `[general]` `node_lookup_method` |
| ami | `manager.conf` `[general]` `enabled`, `bindaddr`, `port` (restart); `[admin]` `secret` (secret) |
| echolink | `echolink.conf` `[el0]`: `call`, `pwd` (secret), `name`, `qth`, `email`, `astnode` (restart), `maxstns`, `freq`, `tone`, `power`, `height`, `gain`, `dir` |

Registration is not a schema key: it is the line `register => <node>:<password>@<host>` in `[general]` of `rpt_http_registrations.conf` (method `http`) or `iax.conf` (method `iax`). Keys not in the schema are returned as "other" settings and are preserved.

**Wizard steps** (Identity, Duty, Radio interface, Registration, EchoLink, Review) and the presets (simplex, halfduplexlink, repeater, hub) are listed in DESIGN 8. A wizard step is one `POST /api/config/form` call with the matching fields (`callsign`, `preset`, `radio`, `registration`, `link`, `modules`, `changes`); every call is a complete, valid plan.

**Phases.** 2.0.3 ships the planner and API, the mock, the node overview and the Identity, Duplex and timers, Registration and EchoLink forms. The setup wizard (renumber included) is described below; add-node comes next.

**Save semantics.** `mode` is `override` (default: write the node's own stanza; a value already in effect is a no-op), `reset` (remove the node's own key) or `template` (change it in the template it is inherited from, the "change for all nodes" option). `dryRun: true` returns the plan with per-file diffs, applied changes, warnings, errors and base shas and writes nothing. A real call re-plans, refuses on conflict (`base` differs from disk), and applies through `configsvc` (snapshot, write, reload, health), the same pipeline as the raw editor. The server never writes `/etc/asterisk` itself.

**Cross-field checks** (run on the after-state; new errors block, old ones are warnings): `driver_not_loaded` and `driver_noload` (rxchannel driver versus `modules.conf`), `no_device_stanza` (device section missing in the radio file), `registered_both`, `private_registered` (node 1 to 1999 with a register line), `public_not_registered` (node above 1999 with none), `register_node_mismatch`, `callsign_format`, `echolink_module` (EchoLink set up but `chan_echolink.so` not loaded), `node_not_in_nodes`.

**Refusals.** `radio_unsupported` (a radio change or the hub preset on a node whose radio is USBRadio, Voter, USRP or another channel; only SimpleUSB, hub and no radio nodes may switch), `no_file`, `no_stanza`, `multiple_stanzas`, `is_template`, `split_stanza`, `broken_inheritance`: the stanza cannot be edited with confidence, so the answer is `422 refused` with the file and line and the UI links to the raw editor.

**USBRadio.** Not supported by forms or wizards: `rxchannel = Radio/...` nodes show a note and a link to Advanced (`usbradio.conf`). The same for Voter, USRP and other channels.

**Reload.** The plan's `reload` is the union of the reload map (table above) for the changed files. `restart` is true, with `restartReasons`, when a changed file is not in the map or a key marked `restart` changed. Then the client offers "Restart Asterisk" (`restart: true` in the apply call); the server never restarts on its own. All reload commands and classes are **to verify on hardware**.

## Set up this node

A guided path for a fresh install, on the planner of "Forms". It is offered on the overview when the node is the stock placeholder (`placeholder` in `GET /api/config/nodes`), with a **Skip for now** that is stored on the server (store setting `setup_wizard_skipped`, so it holds on every browser) and can be undone: the overview always has "Run the setup wizard". Skipping changes no file.

| Step | Asks | Notes |
|---|---|---|
| 1 Node | Node number and password | 1 to 1999 is private (not registered), 2000 and up is public. The wizard renumbers the sample node. |
| 2 Identity | Callsign and ID | `idrecording = \|i<CALL>` |
| 3 Radio | SimpleUSB or hub | USBRadio, Voter and USRP are not offered. |
| 4 Duplex | A preset | The hub preset goes with a hub only. |
| 5 Registration | HTTP (default) or IAX | Public nodes only; the registration password is the node password. |
| 6 EchoLink | Optional: call (-L link or -R repeater), password, e-mail, name, QTH | Links `astnode` and loads `chan_echolink.so`. |
| 7 Review | The combined multi-file diff, warnings and the restart notice | Secrets are masked. Apply is one action. |

After Apply the server runs the fixed order of API "Node setup": files, `savenode.conf`, ASLCP's AMI login, Asterisk restart, Allmon3, health check; then the page polls the registration status (public nodes, up to a minute) and offers a one-click **Roll back**.

Files touched (only those that change, all through the history-recorded pipeline, never written by the web server):

| File | Change |
|---|---|
| `rpt.conf` | Node stanza header, `[nodes]` key and `/old,` value, `idrecording`, `idtalkover`, `rxchannel`, preset values |
| `simpleusb.conf` | The device section when it is named after the node |
| `extensions.conf` | `[globals]` `NODE` when it equals the old node |
| `modules.conf` | `load` for the radio driver and, with EchoLink, `chan_echolink.so` |
| `rpt_http_registrations.conf` / `iax.conf` | The old registration line is replaced by the new one (public) or removed |
| `echolink.conf` | `[el0]` call, password, name, QTH, e-mail and `astnode` |
| `manager.conf` | `[admin] secret`: 32 hex characters generated by the server, never shown. Refused when there is no `[admin]` section. |
| `savenode.conf` | `NODE`, `PASSWORD`, `ENABLE=1` through the helper op; skipped when the file does not exist; recorded in history |
| `/etc/allmon3/allmon3.ini` | Node section name and `pass`, through the helper op; skipped when absent |

The AMI secret change makes ASLCP's own client log in again; when `aslcp.conf` sets the AMI secret by hand the step says so and that value must be updated there. Rollback restores the snapshot taken before the apply (it contains `savenode.conf` too), reverses Allmon3 and the AMI login, and restarts Asterisk if the apply did. `allmon.ini.php` and `voter.conf` are not renumbered (Allmon2/Supermon and Voter setups are edited in Advanced).

## UI

The Config page (admin only, hidden for others) has two tabs. The route is `#/config` (Overview, the default) or `#/config?tab=advanced`. Advanced links open a file, and a line, directly: `#/config?tab=advanced&file=rpt.conf&line=12`; `&view=history` opens the History tab. Cards, refusals and "Edit in Advanced" buttons use these links.

**Overview.** One page per local node (a row of node buttons appears when there are several; the choice is remembered in the browser). Cards in plain language: Identity, Radio, Duplex and timers, Registration (with the last registration status from Asterisk when it answers), EchoLink, Linking and telemetry, Manager interface, Access lists, Status reporting, DTMF commands and macros. Identity, Duplex and timers, Registration and EchoLink have an Edit form; the others show a read-only summary with "Edit in Advanced" (Access lists has no file yet). Settings the schema does not know are listed read-only under "Other settings on this node". Warnings from the checks and any refusal for the node are shown above the cards with a link to the file.

**USBRadio.** The Radio card of a USBRadio, Voter, USRP or other node is read-only and points to Advanced (`usbradio.conf` and so on). Duplex presets that need a hub radio are disabled for such nodes.

**Forms.** Each field shows where its value comes from: "set on this node", "from template node-main", "default" or "set in echolink.conf". Save writes an override on the node's own stanza. Two separate buttons per field: "Reset to template" (removes the node's line; "Remove from this node" when no template sets it) and "Change for all nodes" (writes the value into the template). Duplex and timers starts with an optional preset; choosing one fills the inputs and lists what it sets, and values you change afterwards win. Secret fields (EchoLink password, registration password) are never pre-filled and are empty when you open the form; an empty field keeps the saved value.

**Every save is a dry run first.** "Review changes" sends the request with `dryRun`, then shows what changes, new problems (these block Apply), warnings, the per-file diff, and the reload plan or "needs an Asterisk restart" with a checkbox to restart right after saving. Apply sends the same request with the plan's `base` (a changed file gives a conflict message and "Plan again"), then shows the result of the config pipeline: reload, health check, history snapshot and, on a failed health check, "Roll back". A refusal explains why forms do not edit this and links to the file in Advanced. When a restart is still needed after saving, the result offers "Restart Asterisk".

**Advanced tab.** The raw editor and History, as before: file list grouped (Core, Radio, Linking, Platform, Custom, Other) with a filter, CodeMirror editor, Revert and Save with a review dialog (diff, syntax errors, reload plan, commit message, rollback on failed health), 409 conflict handling with a side buffer, History with Restore. Unsaved drafts survive switching to Overview and back. The "Restart Asterisk" button (with confirmation) is in the page header.

Demo mode and the mock API (`npm run dev:mock`) provide an in-memory `/etc/asterisk` with a few anonymized files, including a second node on USBRadio.

## Test plan

- Helper: path validation table tests (traversal, absolute, symlink escape, non-.conf, depth, NUL, size), apply/conflict/rollback-on-write-error, ownership and mode preserved, restore, log/show, hooks disabled. Use a temp dir as root and a temp history repo.
- Server: preview diff and reload plan, masking, authorization (viewer/operator get 403), audit entries without secrets.
- Forms: `internal/nodecfg` tests on the asl3-dist corpus (overview and provenance, each mode, refusals, cross-field checks, no-op saves, secrets never echoed) and API tests in `internal/api`.
- On real hardware before beta.1: check `/etc/asterisk` ownership and modes, the reload commands in the table, and the health check after each.
