export type Role = 'admin' | 'operator' | 'viewer';
export interface User { id: number; username: string; role: Role }
export interface NodeInfo { node: string; callsign: string; description: string; location: string }
export type LinkKind = 'allstar' | 'echolink' | 'phone' | 'client' | 'unknown';
export type LinkMode = 'T' | 'R' | 'L' | 'C';
export interface Link {
  node: string; kind: LinkKind; info: NodeInfo | null; ip: string;
  direction: 'IN' | 'OUT'; connected: number; state: 'ESTABLISHED' | 'CONNECTING';
  mode: LinkMode; keyed: boolean; sinceKey: number | null; sinceUnkey: number | null;
}
export interface NetworkEntry { node: string; mode: string; kind: LinkKind; info: NodeInfo | null }
export interface NodeStatus {
  node: string; info: NodeInfo | null; stale: boolean; staleReason?: string;
  rxKeyed: boolean; txKeyed: boolean; extTxKeyed: boolean; numLinks: number;
  links: Link[]; network: NetworkEntry[]; flags: Record<string, boolean>; updated: string;
}
export interface Favorite { id: number; node: string; label: string; group: string; position: number; info: NodeInfo | null }
export interface SystemInfo {
  hostname: string; cpuTemp: number | null; load: number[]; memTotal: number; memAvailable: number;
  uptime: number; asterisk: { connected: boolean; version: string; uptime: number; error: string | null }; version: string;
  updateAvailable?: boolean; updateVersion?: string;
}
export interface UpdateInfo {
  available: boolean; reason: string | null; applyReason: string | null; channel: 'stable' | 'beta'; installed: string; candidate: string; upgradable: boolean;
  job: 'idle' | 'running' | 'succeeded' | 'failed' | 'noop'; jobLog: string; notes: string | null; checked: string | null;
  /** Operating system (apt) updates; absent when the server cannot offer them. */
  os?: OSUpdates;
}
export interface OSPackage { name: string; from: string; to: string; origin?: string; security: boolean }
export interface OSUpdates {
  available: boolean; reason: string | null; checkedAt: string | null; checking: boolean; error: string | null;
  packages: OSPackage[]; keptBack: string[]; rebootRequired: boolean; rebootPkgs: string[];
  job: 'idle' | 'running' | 'succeeded' | 'failed' | 'noop'; jobLog: string;
}
/** Admin-only SSE event "updates": counts only. */
export interface UpdatesBadge { aslcp: boolean; aslcpVersion: string; os: number; security: number; reboot: boolean }
export interface About {
  version: string; asterisk: string;
  platform: { os: string; arch: string; model: string; distro: string; kernel: string; hardware: string };
}
export interface Settings { favoritesScan?: boolean }
/** Activity of one favorite from the stats scan (GET /api/favorites/stats, SSE favstats). */
export interface FavActivity { rxPct: number | null; linkCnt: number; active: boolean; webTx: boolean; keyed: boolean; txAvg: number; lastSeen: string }
export interface FavStatsUpdate { enabled: boolean; rate: number; effRate: number; backoff: boolean; retryAt: string | null; /** Why polling is paused. */ backoffReason?: 'rate-limit' | 'service-error' | 'network-error' | ''; updated: string | null; stats: Record<string, FavActivity>;
  /** Node being requested right now ('' when idle). Kept for compatibility; the UI marks `next`. */ checking?: string;
  /** Node that will be requested next ('' when none). */ next?: string;
  /** When the next request is due (null: scan off, nobody watching or nothing to check). */ nextAt?: string | null;
  /** Eligible favorites with an answer in the current pass, and how many are eligible. */ checked?: number; total?: number;
  /** Last attempt per node, offline ones included; SSE carries only the changed nodes. */ checkedAt?: Record<string, string>;
  /** SSE only: nodes whose values were cleared (offline, unknown, no longer a favorite). */ removed?: string[] }
export interface StatField { key: string; value: string }
export interface NodeStats { node: string; stats: StatField[] }
export type ConnectMode = 'transceive' | 'monitor' | 'localmonitor';
export interface NodeListStatus { count: number; updated: string | null; source: string; error: string | null }
export interface ImportPreview { found: boolean; path: string; items: { node: string; label: string }[] }

// Public status view (GET /api/public/*): never contains IPs or system info.
export interface PubInfo { callsign: string; description: string; location: string }
export interface PubLink {
  node: string; label?: string; kind: LinkKind; info?: PubInfo; direction: 'IN' | 'OUT'; mode: LinkMode;
  connected: number; keyed: boolean; sinceKey: number | null; sinceUnkey: number | null;
}
export interface PubNetNode { node: string; label?: string; callsign?: string }
export interface PubNode {
  node: string; info: PubInfo | null; stale: boolean; txKeyed: boolean; rxKeyed: boolean; numLinks: number;
  links: PubLink[]; network?: PubNetNode[];
}
export interface PubActivity { node: string; link: string; label?: string; callsign?: string; at: number; duration: number; kerchunk: boolean }
export interface PubSnapshot {
  title: string; now: number; callsigns: boolean; showNetwork: boolean; showActivity: boolean;
  nodes: PubNode[]; activity?: PubActivity[];
}
export interface PublicInfo { enabled: boolean; title: string; loggedIn: boolean }
/** GET/PUT /api/public (admin). */
export interface PublicSettings {
  enabled: boolean; title: string; nodes: string[]; showCallsigns: boolean; showNetwork: boolean; showActivity: boolean;
  allowEmbedding: boolean; trustProxy: boolean; defaultTitle: string;
  localNodes: { node: string; info: NodeInfo | null }[]; clients: number;
}

// Configuration editing (docs/CONFIG.md, /api/config/*). Admin only.
export interface ConfigFileInfo { path: string; size: number; mtime: string; sha256: string; known: boolean; group: string }
export interface ConfigFile { path: string; content: string; sha256: string }
export interface ConfigDraft { path: string; content: string; base: string }
export interface ConfigPreviewFile { path: string; ok: boolean; error?: { line: number; message: string }; diff: string; changed: boolean; conflict?: boolean }
export interface ConfigPreview { files: ConfigPreviewFile[]; reload: { command: string; reason: string }[]; restart: boolean }
export interface ConfigHealthCheck { name: string; ok: boolean; detail: string }
export interface ConfigApplyResult {
  commit: string; before: string;
  /** Files written, and whether any of them only takes effect after an Asterisk restart. */
  files?: string[]; restart?: boolean;
  reload?: { command: string; ok: boolean; output: string }[] | null;
  health?: { ok: boolean; checks: ConfigHealthCheck[] } | null;
  /** Restore only (helper `skipped`): files that did not exist in the restored commit. */
  skipped?: string[];
}
export interface ConfigCommit { commit: string; date: string; author: string; message: string; files: string[] }
export interface ConfigCommitDetail extends Omit<ConfigCommit, 'files'> { files: { path: string; status: 'added' | 'modified' | 'deleted'; diff: string }[] }

// Backup and full restore (docs/BACKUP.md, /api/backup/*, /api/restore/*). Admin only (restore upload also with a setup code).
export type BackupPartId = 'history' | 'allmon3' | 'sounds' | 'dv';
export interface BackupInfo { parts: { id: BackupPartId; present: boolean; size: number }[]; sa818: boolean }
export interface BackupSelection { history: boolean; allmon3: boolean; sounds: boolean; dv: boolean }
export type RestoreAction = 'restore' | 'restore_dist' | 'keep_stock' | 'keep_new' | 'delete_optional' | 'skip';
export interface RestoreRadio {
  band: string; freqRx: string; freqTx: string; tone: string; ctcssRx: string; ctcssTx: string; dcsRx: string; dcsTx: string; port: string; portPresent: boolean;
}
export interface RestorePreview {
  backup: { hostname: string; created: string; createdBy: string; aslcpVersion: string; asl3Version: string; asteriskVersion: string; arch: string; osId?: string; osCodename?: string; osName?: string; nodes: string[]; channel: string };
  node: { asl3Version: string; asteriskVersion: string; arch: string; osId?: string; osCodename?: string; osName?: string };
  warnings: { code: string; message: string }[];
  files: { path: string; action: RestoreAction; note?: string }[];
  users: { username: string; role: string }[];
  favorites: number;
  schedules: number;
  missingPackages: string[];
  installCommand: string;
  checklist: { item: string; value: string }[];
  radio: RestoreRadio | null;
  parts: { history: boolean; allmon3: boolean; sounds: boolean; sa818: boolean; dv?: boolean };
}
export interface RestoreUpload { id: string; preview: RestorePreview }
export interface RestoreStep { name: string; ok: boolean; detail: string }
export interface RestoreApplyResult { ok: boolean; steps: RestoreStep[]; restarting: boolean }
export interface RestoreResult {
  at: string; backupCreated: string; steps: RestoreStep[];
  health: { ok: boolean; pending?: boolean; checks: ConfigHealthCheck[] }; checklist: { item: string; value: string }[];
}

// SimpleUSB audio tuning (GET /api/radio/*, docs/API.md "Radio tuning").
export interface TuneDevice { name: string; node?: string; usbPath?: string; card: number; links: string[] }
export interface TuneList { available: boolean; reason?: string; devices: TuneDevice[] }
export type SignalFrom = 'no' | 'usb' | 'usbinvert' | 'pp' | 'ppinvert';
export interface TuneParams {
  rxVoice: number; txA: number; txB: number; echo: boolean; rxBoost: boolean; preEmphasis: boolean; deEmphasis: boolean;
  plFilter: boolean; pttInvert: boolean; carrierFrom: SignalFrom | string; ctcssFrom: SignalFrom | string;
  rxOnDelayMs: number; txOffDelayMs: number; micPlayMax: number; spkrMax: number; micMax: number;
  carrierOther?: boolean; ctcssOther?: boolean;
}
export type TunePatch = Partial<Pick<TuneParams, 'rxVoice' | 'txA' | 'txB' | 'rxBoost' | 'preEmphasis' | 'deEmphasis' | 'plFilter' | 'pttInvert' | 'carrierFrom' | 'ctcssFrom' | 'rxOnDelayMs' | 'txOffDelayMs'>>;
export interface TuneEcho { on: boolean; expiresAt?: string }
export interface TuneState {
  device: string; node?: string; params: TuneParams; saved: TuneParams; dirty: string[];
  identity: { device: string; usbString?: string; serial?: string; card?: number }; echoState: TuneEcho;
}
export interface TuneApplyResult { applied: { field: string; reply: string }[]; errors: { field: string; message: string; reply?: string }[]; params: TuneParams; dirty: string[] }
export interface TuneMeter { pk: number; avg: number; min: number; max: number; clip: number; at: string; stale?: boolean }
export interface TuneSaveResult { saved: boolean; params: TuneParams; changed: string[]; commit?: string; warning?: string }

export interface Schedule {
  id: number; name: string; enabled: boolean; node: string; target: string;
  action: 'connect' | 'disconnect'; mode: ConnectMode; permanent: boolean; exclusive: boolean;
  /** Weekly: days of week, 0 = Sunday .. 6 = Saturday. Empty for a one-off. */
  days: number[];
  /** One-off: YYYY-MM-DD, else "". */
  date: string;
  start: string; end: string;
  lastRun: string | null; lastResult: string;
}
/** Body of POST/PUT /api/schedules: a Schedule without the server-side fields. */
export type ScheduleInput = Omit<Schedule, 'id' | 'lastRun' | 'lastResult'>;
export interface ScheduleEvent {
  at: string; id: number; name: string; node: string; target: string; action: 'connect' | 'disconnect'; mode: ConnectMode;
}
export type ManualAction = 'pause' | 'pause_today' | 'cancel';
/** Cancel mode: windows cancelled by a manual action, while they would still be running. */
export interface CancelNotice { reason: string; at: string; until: string; ids: number[]; names: string[] }
export interface SchedulesSnapshot {
  enabled: boolean; pausedReason: string; pausedAt: string | null; resumeAt: string | null;
  manualAction: ManualAction; lastCancel: CancelNotice | null; timezone: string; now: string;
  schedules: Schedule[]; active: { id: number; start: string; end: string }[]; upcoming: ScheduleEvent[];
}

/** GET /api/dv/status (admin; plan C.1 DVStatus). `job` is the state of the enable job unit; its log is GET /api/dv/job. */
export type DVJobState = '' | 'idle' | 'running' | 'failed' | 'done' | 'succeeded';
export interface DVStatus {
  /** The platform can run the engine (bookworm/trixie; amd64/arm64/armhf with ARMv7 or newer). `reason` says why not ("armv6_unsupported"). */
  supported: boolean; reason?: string;
  md380Installed: boolean;
  /** aslcp-dv.service is enabled / running; md380-emu.service is running. */
  enabled: boolean; active: boolean; md380Active: boolean;
  job?: DVJobState;
  /** Units of the previous stack still loaded on the node (stopped by Enable; the upgrade removes the ones ASLCP installed). */
  legacyUnits?: string[];
}
/** GET /api/dv/job: the enable job's state and the last lines of its log (`log` text, or `lines`). */
export interface DVJob { job: DVJobState; log?: string; lines?: string[]; at?: number }

/** The private-node part of GET /api/dv/setup. `files[].sha256` is the version a private-node write is checked against (`base`). */
export interface DVFileView { id: string; present: boolean; writable: boolean; sha256: string; history: number }
/** The "Bridge node" part of GET /api/dv/setup. `suggested` is the first free node of 1900-1997. */
export interface DVBridgeSetup {
  node: string; owned: boolean; /** Found but not added by ASLCP, and it looks exactly like the node ASLCP adds: "Manage this bridge node with ASLCP" works. */ adoptable?: boolean; suggested: string; used: string[]; localNodes: string[];
  rxPort: number; txPort: number; link: { mode: DVLinkMode; localNode: string; bridge: string; exclusive: boolean }; paused: boolean;
  /** Display name of the bridge node (the default when none was saved). */
  name?: string;
}
/** PUT /api/dv/private-node (docs/API.md "Digital modes private node"). */
export interface DVPrivateReq { node: string; rxPort: number; txPort: number; linkMode: DVLinkMode; localNode: string; /** "Disconnect other links first"; absent keeps the saved value. */ linkExclusive?: boolean; dryRun?: boolean; restart?: boolean; base?: Record<string, string>; renumber?: boolean }
export type DVPlanKind = 'set' | 'renumber' | 'remove';
/** `kind` says what the plan does: save the node, renumber it (`oldNode` goes away) or remove it. `unlinks` are the local nodes disconnected first; `keepRecord` says the removal leaves ASLCP's record because something could not be reverted. */
export interface DVPlan {
  kind: DVPlanKind; oldNode: string; unlinks: string[]; keepRecord: boolean;
  node: string; rxPort: number; txPort: number; layout: string;
  files: { path: string; diff: string; changed: boolean }[];
  restartAsterisk: boolean; restarts: string[]; warnings: string[]; base: Record<string, string>;
  link: { mode: DVLinkMode; localNode: string; bridge: string; exclusive: boolean };
}
export interface DVStep { name: string; ok: boolean; detail?: string }
export interface DVRollbackInfo { available: boolean; before?: boolean; ini?: boolean }
export interface DVApplyResult { applied: boolean; kind: DVPlanKind; node: string; steps: DVStep[]; warnings?: string[]; keepRecord?: boolean; health?: unknown; rollback: DVRollbackInfo }
export interface DVRollbackResult { rolledBack: boolean; problems: string[]; steps: DVStep[] }
/** GET /api/dv/setup (admin): only the private-node view and the file versions are used. */
export interface DVSetup { bridgeNode: DVBridgeSetup; files?: DVFileView[] }

/** GET /api/dv/lists?mode=... (operator and admin; docs/API.md "Digital modes host lists"). 404 `no_list` when the node has no file for the mode. */
export type DVListMode = 'dmr' | 'ysf' | 'p25' | 'nxdn' | 'm17' | 'bm' | 'dmr-other';
/** `value` is what gets written: the master address (dmr), the 5-digit reflector ID (ysf) or the TG (p25, nxdn) or the reflector designator `M17-XXX` (m17; the module is picked apart from the list). `port` is the master port for dmr. The DMR list never carries a password. */
export interface DVListItem { value: string; name: string; detail?: string; address: string; port: number; password?: string }
export interface DVList { mode: DVListMode; items: DVListItem[]; truncated?: boolean; etag: string }

/** GET and PUT /api/dv/resume (docs/API.md "Digital modes tune and resume"): the last tune of each mode and the toggle. */
/** LOOP is the echo test (the local test network) of the native engine; BM, TGIF, DMR, YSF, P25, NXDN and M17 are carried but marked unavailable in M1. */
export type DVTuneMode = 'LOOP' | 'BM' | 'TGIF' | 'DMR' | 'YSF' | 'P25' | 'NXDN' | 'M17';
export interface DVRemembered { tune: string; ts?: string; at: string }
export interface DVResume { resume: boolean; lastMode?: string; perMode: Partial<Record<DVTuneMode, DVRemembered>> }

/** GET /api/dv/snapshot and the SSE `dv` event (all roles; docs/API.md "Digital modes panel"). No install log, no identity, no secret. */
export interface DVBridge { mode: string; tune: string; targetName?: string; slot: number; muted: boolean; /** The last tune was a DMR private call (the BrandMeister Parrot): `tune` is empty then. */ private?: boolean; source: 'aslcp-dv' | 'abinfo' | 'none'; updated: string }
/** One talker from the Analog_Bridge log. `at` is the log's own time, no zone. */
export interface DVCaller { call?: string; private?: boolean; dmrId?: string; dst?: string; slot?: number; at?: string }
export interface DVSnapshot {
  installed: boolean; running: boolean; configured: boolean;
  bridge: DVBridge;
  /** `label`: the button's name when it is not the id (a DMR network's registry label: "BrandMeister", "TGIF", "DMR - Other"). `available`: the engine implements the mode (LOOP and BM in this build). */
  modes: { id: DVTuneMode; enabled: boolean; available: boolean; unlink: string; label?: string; /** The engine's login state of an enabled network (`off`, `pending`, `ok`, `failed`) and its detail. */ login?: DVLogin; detail?: string; /** A tuned YSF, P25, NXDN or M17 reflector sends nothing back (the engine's lost state). */ silent?: boolean; /** DMR - Other: when the tuned talkgroup last had traffic (RFC 3339); absent during a call or when none is tuned. */ quietSince?: string; /** BrandMeister and DMR - Other: the master the mode is set to (its name in the host list, else the address as saved). */ master?: string }[];
  /** M1: aslcp-dv and md380-emu. */
  services: { unit: string; active: boolean }[];
  /** md380 vocoder health (`ok`; `timeouts` counts the calls that timed out) and the engine's version and uptime in seconds; absent while the engine is not reachable. */
  vocoder?: { ok: boolean; timeouts: number };
  engine?: { version: string; uptime: number };
  /** `name` is the display name of the bridge node (the default "Digital modes linked node" when none was saved); present with `present`. */
  bridgeNode: { present: boolean; node?: string; name?: string };
  /** The link of the bridge node to a local node (docs/DV.md "Link mode"). */
  link: DVLink;
  /** Who is talking now (the log's last call that has not ended); only meaningful while RX is keyed. */
  caller?: DVCaller;
  /** The last few distinct callers, newest first. */
  heard: DVCaller[];
}
export type DVLogin = 'off' | 'pending' | 'ok' | 'failed';
/** One option of a network (GET /api/dv/networks): `kind` text, int or bool; `default` is what the engine uses when it is unset. */
export interface DVOptSpec { key: string; label: string; kind: 'text' | 'int' | 'bool'; default: string; help?: string; min?: number; max?: number; maxLen?: number; advanced?: boolean }
/** One network of GET /api/dv/networks and the reply of PUT /api/dv/networks/{mode}. The password is write-only: only `passwordSet` comes back. */
export interface DVNetView {
  mode: DVTuneMode; label: string; available: boolean; enabled: boolean; address: string; port: number; defaultPort?: number; defaultAddress?: string; defaultPassword?: string; passwordSet: boolean;
  fields: { address: boolean; port: boolean; password: boolean }; needsIdentity: boolean; help?: string;
  options: DVOptSpec[]; values: Record<string, string>; login?: DVLogin; detail?: string;
  /** PUT only: the engine took the new settings; otherwise `applyError` says why not. */
  applied?: boolean; applyError?: string;
}
export interface DVIdentity { callsign: string; dmrId: number; repeaterId?: number; applied?: boolean; applyError?: string }
export interface DVNetworks { identity: DVIdentity; modes: DVNetView[] }
/** PUT /api/dv/networks/{mode}: only the fields that change. `password` sets, `clearPassword` removes. */
export interface DVNetPut { enabled?: boolean; address?: string; port?: number; password?: string; clearPassword?: boolean; options?: Record<string, string> }
export type DVLinkMode = 'ondemand' | 'permanent';
/** `exclusive` is the saved "Disconnect other links first" (dv.link_exclusive). */
export interface DVLink { mode: DVLinkMode; localNode: string; linked: boolean; paused: boolean; exclusive: boolean }
/** The reply of POST /api/dv/mode, /unlink and /tune. */
/** `left`: the mode a switch unlinked before leaving it (it reconnects when switched back). */
export interface DVCmdReply { mode: string; switched: boolean; confirmed: boolean; left?: string; snapshot: DVSnapshot }
export interface DVFavorite { id: number; mode: DVTuneMode; value: string; name: string; sort: number }

// System tab (docs/API.md "System"); all admin-only.
export interface HostStatus { hostname: string; prettyHostname: string; os: string; hostnameAvailable: boolean; hostnamectlAvailable: boolean; hostnamectlMissing: boolean; timezone: string; ntp: boolean; ntpSynchronized: boolean; canNtp: boolean; localRtc: boolean; timeAvailable: boolean; now: string; clock: string }
export interface HostPatch { hostname?: string; timezone?: string; ntp?: boolean }
export interface SysLogs { unit: string; lines: number; since: string; text: string; truncated: boolean; available: boolean }
export interface SysService { unit: string; description: string; active: string; sub: string; enabled: string; since: string }
export interface SysServices { available: boolean; services: SysService[] }
export interface NetAddr { family: string; addr: string; prefix: number; scope: string; dynamic: boolean }
export interface NetIface { name: string; state: string; kind: string; mac: string; mtu: number; addrs: NetAddr[] }
export interface NetRoute { family: string; gateway: string; dev: string; metric: number }
export interface SysNetwork { available: boolean; interfaces: NetIface[]; gateways: NetRoute[]; dns: string[]; dnsSource: string }
export interface DiagItem { name: string; desc: string }
export interface DiagPlan { items: DiagItem[]; excluded: string[]; redacted: string[]; maxBytes: number; timeLimit: number; available: boolean; maskDefault: boolean }
export interface AuditEntry { id: number; time: string; user: string; action: string; detail: string }
export interface AuditPage { entries: AuditEntry[]; next: number | null }

// WiFi manager of the System tab (docs/API.md "WiFi").
export interface WifiDevice { name: string; type: string; state: string; connection: string; addrs: string[] }
export interface WifiHotspot { enabled: boolean; delaySeconds: number; active: boolean; ssid: string; address: string; firewall: string }
export interface WifiNetwork { id: string; name: string; ssid: string; priority: number; autoconnect: boolean; active: boolean; hidden: boolean; secured: boolean }
export interface WifiAP { ssid: string; signal: number; channel: number; security: string; secured: boolean; supported: boolean; inUse: boolean; saved: boolean }
export interface WifiState {
  available: boolean; reason?: string; hasWifi: boolean; radio: string; devices: WifiDevice[]; active: string; ethernet: boolean;
  hotspot: WifiHotspot; networks: WifiNetwork[]; viaWifi: boolean; viaHotspot: boolean;
}
export interface WifiScan { networks: WifiAP[]; note?: string }

// System > Terminal (docs/API.md "Browser terminal"); admin only, off by default.
export interface TerminalStatus { enabled: boolean; available: boolean; sessions: number; maxSessions: number; idleMinutes: number; maxHours: number }
export interface TerminalTicket { ticket: string; expiresIn: number }
// One live terminal session (attached or detached); GET /system/terminal/sessions.
export interface TerminalSessionInfo { id: string; owner: string; mine: boolean; started: number; attached: boolean; detachedSince?: number; linuxUser?: string }

/** One call shown by the Now Calling strip (GET /api/nowcalling, SSE "nowcalling"). */
export interface NowCall {
  id: string; kind: 'allstar' | 'dmr' | 'ysf' | 'm17'; call?: string; first?: string; name?: string;
  city?: string; state?: string; country?: string; location?: string; dmrId?: string;
  node?: string; desc?: string; local?: string; network?: string; tg?: string; tgName?: string;
  slot?: number; private?: boolean; room?: string; start: string; end?: string; active: boolean;
}
export interface NowCalling { current: NowCall | null; recent: NowCall[] }
/** The radioid.net user list (admin, digital modes settings). */
export interface UserListStatus { enabled: boolean; entries: number; updated?: string; checked?: string; error?: string; busy?: boolean; size?: number }
