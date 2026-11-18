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
  node: string; kind: LinkKind; info?: PubInfo; direction: 'IN' | 'OUT'; mode: LinkMode;
  connected: number; keyed: boolean; sinceKey: number | null; sinceUnkey: number | null;
}
export interface PubNetNode { node: string; callsign?: string }
export interface PubNode {
  node: string; info: PubInfo | null; stale: boolean; txKeyed: boolean; rxKeyed: boolean; numLinks: number;
  links: PubLink[]; network?: PubNetNode[];
}
export interface PubActivity { node: string; link: string; callsign?: string; at: number; duration: number; kerchunk: boolean }
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
export type BackupPartId = 'history' | 'allmon3' | 'sounds' | 'dvswitch';
export interface BackupInfo { parts: { id: BackupPartId; present: boolean; size: number }[]; sa818: boolean }
export interface BackupSelection { history: boolean; allmon3: boolean; sounds: boolean; dvswitch: boolean }
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
  parts: { history: boolean; allmon3: boolean; sounds: boolean; sa818: boolean; dvswitch?: boolean };
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

/** GET /api/dvswitch (admin only; docs/API.md "DVSwitch"). */
export interface DVSwitchJob { job: 'idle' | 'running' | 'succeeded' | 'failed'; log: string; at?: number }
export interface DVSwitchStatus {
  installed: boolean;
  /** "aslcp": ASLCP installed it (marker); "external": installed outside ASLCP; "": not installed. */
  installedBy: '' | 'aslcp' | 'external';
  version: string; arch: string; osCodename: string; supported: boolean; unsupportedReason?: string;
  repoPresent: boolean; keyPresent: boolean; keyOk: boolean; keyFingerprint?: string;
  packages: { name: string; installed: boolean; version?: string }[];
  units: { name: string; loaded: boolean; active: string; enabled: string }[];
  hostFiles: { name: string; present: boolean; size: number }[]; hostFilesOk: boolean;
  marker: { version: number; installedAt: string; wgetPreinstalled: boolean; packages: string[] } | null;
  canInstall: boolean; canUninstall: boolean;
  uninstallPackages: string[]; uninstallPaths: string[]; uninstallUnits: string[];
  install: DVSwitchJob; uninstall: DVSwitchJob;
}
export interface DVSwitchJobs { install: DVSwitchJob; uninstall: DVSwitchJob }

/** GET /api/dvswitch/setup and the reply of the PUT writes (docs/API.md "DVSwitch setup"). No secret value: the DMR password is write-only. */
export interface DVFileView { id: string; present: boolean; writable: boolean; sha256: string; history: number }
export interface DVSwitchInfo { found: boolean; version: string; mode: string; tg: string; slot: string; colorCode: string; tune: string; mute: string; gatewayId: string; repeaterId: string; updated: string }
/** The "Bridge node" part of GET /api/dvswitch/setup. `suggested` is the first free node of 1900-1997. */
export interface DVBridgeSetup {
  node: string; owned: boolean; suggested: string; used: string[]; localNodes: string[];
  rxPort: number; txPort: number; link: { mode: DVLinkMode; localNode: string; bridge: string }; paused: boolean;
}
/** PUT /api/dvswitch/private-node (docs/API.md "DVSwitch private node"). */
export interface DVPrivateReq { node: string; rxPort: number; txPort: number; linkMode: DVLinkMode; localNode: string; dryRun?: boolean; restart?: boolean; base?: Record<string, string>; renumber?: boolean }
export type DVPlanKind = 'set' | 'renumber' | 'remove';
/** `kind` says what the plan does: save the node, renumber it (`oldNode` goes away) or remove it. `unlinks` are the local nodes disconnected first; `keepRecord` says the removal leaves ASLCP's record because something could not be reverted. */
export interface DVPlan {
  kind: DVPlanKind; oldNode: string; unlinks: string[]; keepRecord: boolean;
  node: string; rxPort: number; txPort: number; layout: string;
  files: { path: string; diff: string; changed: boolean }[];
  restartAsterisk: boolean; restarts: string[]; warnings: string[]; base: Record<string, string>;
  link: { mode: DVLinkMode; localNode: string; bridge: string };
}
export interface DVStep { name: string; ok: boolean; detail?: string }
export interface DVRollbackInfo { available: boolean; before?: boolean; ini?: boolean }
export interface DVApplyResult { applied: boolean; kind: DVPlanKind; node: string; steps: DVStep[]; warnings?: string[]; keepRecord?: boolean; health?: unknown; rollback: DVRollbackInfo }
export interface DVRollbackResult { rolledBack: boolean; problems: string[]; steps: DVStep[] }
export interface DVSwitchSetup {
  identity: { callsign: string; dmrId: string; essid: string; nxdnId: string; consistent: boolean };
  warnings?: string[];
  dmr: { address: string; port: number; passwordSet: boolean };
  usrp: { address: string; rxPort: number; txPort: number };
  modes: { dmr: boolean; ysf: boolean; p25: boolean; nxdn: boolean };
  startup: { ambeMode: string; txTg: string; txTs: number; colorCode: number };
  gateways: { ysf: { present: boolean; startup: string }; p25: { present: boolean; startup: string }; nxdn: { present: boolean; startup: string } };
  files: DVFileView[];
  current: DVSwitchInfo;
  bridgeNode: DVBridgeSetup;
  changed?: string[]; skipped?: string[]; restartHint?: string[];
}
/** GET /api/dvswitch/dmr-status (operator and admin; docs/API.md "DVSwitch DMR login status"). Never holds the password. */
export type DVDMRState = 'not_installed' | 'disabled' | 'not_running' | 'connecting' | 'logged_in' | 'login_failed' | 'timed_out' | 'socket_failed' | 'master_closing';
export interface DVDMRStatus { state: DVDMRState; master?: string; at?: string; since?: string; lastLine?: string }
export type DVRestartScope = 'dmr' | 'bridge' | 'gateways' | 'all';
export interface DVSwitchRestartResult { action: 'restart' | 'start'; units: string[]; skipped: string[]; states: DVSwitchStatus['units']; warning: string }

/** GET /api/dvswitch/lists?mode=... (operator and admin; docs/API.md "DVSwitch host lists"). 404 `no_list` when the node has no file for the mode. */
export type DVListMode = 'dmr' | 'ysf' | 'p25' | 'nxdn';
/** `value` is what gets written: the master address (dmr), the 5-digit reflector ID (ysf) or the TG (p25, nxdn). `port` is the master port for dmr. The DMR list never carries a password. */
export interface DVListItem { value: string; name: string; detail?: string; address: string; port: number }
export interface DVList { mode: DVListMode; items: DVListItem[]; truncated?: boolean; etag: string }

/** GET and PUT /api/dvswitch/resume (docs/API.md "DVSwitch tune and resume"): the last tune of each mode and the toggle. */
export type DVTuneMode = 'DMR' | 'YSF' | 'P25' | 'NXDN';
export interface DVRemembered { tune: string; ts?: string; at: string }
export interface DVResume { resume: boolean; lastMode?: string; perMode: Partial<Record<DVTuneMode, DVRemembered>> }

/** GET /api/dvswitch/snapshot and the SSE `dvswitch` event (all roles; docs/API.md "DVSwitch panel"). No install log, no identity, no secret. */
export interface DVBridge { mode: string; tune: string; targetName?: string; slot: number; muted: boolean; source: 'abinfo' | 'none'; updated: string }
export interface DVSnapshot {
  installed: boolean; running: boolean; configured: boolean;
  bridge: DVBridge;
  modes: { id: DVTuneMode; enabled: boolean; unlink: string }[];
  services: { unit: string; active: boolean }[];
  bridgeNode: { present: boolean; node?: string };
  /** The link of the bridge node to a local node (docs/DVSWITCH.md "Link mode"). */
  link: DVLink;
}
export type DVLinkMode = 'ondemand' | 'permanent';
export interface DVLink { mode: DVLinkMode; localNode: string; linked: boolean; paused: boolean }
/** The reply of POST /api/dvswitch/mode, /unlink and /tune. */
export interface DVCmdReply { mode: string; switched: boolean; confirmed: boolean; snapshot: DVSnapshot }
export interface DVFavorite { id: number; mode: DVTuneMode; value: string; name: string; sort: number }
