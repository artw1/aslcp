// Pure logic of the "Set up this node" wizard (docs/CONFIG.md, "Set up this node"; docs/API.md, "Node setup").
import type { Plan, Preset } from './nodecfg';
import type { ConfigApplyResult } from './types';

export type SetupRadio = 'simpleusb' | 'hub';
export type RegMethod = 'http' | 'iax';

export interface SetupDraft {
  node: string; password: string; callsign: string;
  radio: SetupRadio; preset: string;
  regMethod: RegMethod; regHost: string;
  echolink: boolean; elCall: string; elPassword: string; elEmail: string; elName: string; elQth: string;
}

export interface SetupPlanBody {
  oldNode: string; node: string; password: string; callsign: string;
  radio: SetupRadio; preset: string; ami: boolean;
  registration?: { method: 'http' | 'iax' | 'none'; host?: string };
  echolink?: { call: string; password: string; email: string; name: string; qth: string };
}
export interface SetupPlan extends Plan { oldNode: string; public: boolean; renumber: boolean; manual?: string[]; steps: { id: string; label: string; skipped?: string }[] }
export interface SetupStepResult { id: string; status: 'done' | 'skipped' | 'failed'; detail?: string }
export interface SetupRollback { commit: string; node: string; oldNode: string; ami: boolean; restart: boolean }
export interface SetupApplyResult { plan: SetupPlan; result?: ConfigApplyResult; steps: SetupStepResult[]; restarted: boolean; rollback?: SetupRollback }

/** Wizard pages in order; the review page shows the combined diff, the rest runs after Apply. */
export const WIZARD_STEPS = ['node', 'identity', 'radio', 'duplex', 'registration', 'echolink', 'review'] as const;
export type WizardStep = (typeof WIZARD_STEPS)[number];
export const STEP_TITLES: Record<WizardStep, string> = {
  node: 'Node number and password', identity: 'Callsign', radio: 'Radio interface', duplex: 'Duplex and timers',
  registration: 'Registration', echolink: 'EchoLink', review: 'Review',
};
/** Progress lines shown while and after applying. */
export const RUN_LABELS: Record<string, string> = {
  files: 'Write the configuration files', savenode: 'Update savenode.conf', ami: 'Switch ASLCP to the new AMI secret',
  restart: 'Restart Asterisk', allmon3: 'Update Allmon3', health: 'Health check',
};

const reNode = /^[1-9]\d{0,6}$/;
const reSecret = /^[A-Za-z0-9._+=,:@%-]{1,64}$/;
const reElCall = /^[A-Z0-9]{3,10}-[LR]$/;
const reHost = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:\d{1,5})?$/;
const reEmail = /^[^\s@;,]+@[^\s@;,]+\.[^\s@;,]+$/;
const reText = /^[^\n\r;]{1,64}$/;

export const validNode = (s: string) => reNode.test(s.trim());
/** Private nodes are 1 to 1999: they are not registered with AllStarLink. Public nodes start at 2000. */
export const nodeKind = (s: string): 'private' | 'public' | '' => (!validNode(s) ? '' : +s.trim() <= 1999 ? 'private' : 'public');
export const validPassword = (s: string) => reSecret.test(s);
export const validCall = (s: string) => /^[A-Za-z0-9]{3,12}$/.test(s.trim());

export function emptyDraft(old = ''): SetupDraft {
  return { node: old, password: '', callsign: '', radio: 'simpleusb', preset: 'simplex', regMethod: 'http', regHost: '', echolink: false, elCall: '', elPassword: '', elEmail: '', elName: '', elQth: '' };
}

/** Presets that fit the radio choice: the hub preset only with a hub. */
export const presetsFor = (presets: Preset[], radio: SetupRadio): Preset[] => presets.filter((p) => (radio === 'hub') === !!p.hub);

/** The first problem on a page, or '' when it can be left. */
export function stepError(step: WizardStep, d: SetupDraft, presets: Preset[] = []): string {
  switch (step) {
    case 'node':
      if (!validNode(d.node)) return 'A node number is 1 to 7 digits, for example 1999 (private) or 2001 (public).';
      if (!d.password) return 'A node password is required.';
      if (!validPassword(d.password)) return 'The password may use letters, digits and . _ + = , : @ % - (up to 64).';
      return '';
    case 'identity':
      return validCall(d.callsign) ? '' : 'Letters and digits, for example N0CALL.';
    case 'radio': return '';
    case 'duplex':
      return presets.length && !presetsFor(presets, d.radio).some((p) => p.name === d.preset) ? 'Choose a preset.' : '';
    case 'registration':
      if (nodeKind(d.node) !== 'public') return '';
      return d.regHost.trim() && !reHost.test(d.regHost.trim()) ? 'A host name such as register.example.net.' : '';
    case 'echolink':
      if (!d.echolink) return '';
      if (!reElCall.test(d.elCall.trim().toUpperCase())) return 'The EchoLink call ends in -L (link) or -R (repeater), for example N0CALL-L.';
      if (!d.elPassword || /[\s;\n\r]/.test(d.elPassword) || d.elPassword.length > 64) return 'An EchoLink password without spaces or semicolons.';
      if (!reEmail.test(d.elEmail.trim())) return 'A valid e-mail address.';
      if (!reText.test(d.elName.trim()) || !reText.test(d.elQth.trim())) return 'Name and location are required (no semicolons).';
      return '';
    default: return '';
  }
}
export const firstStepError = (d: SetupDraft, presets: Preset[]): { step: WizardStep; message: string } | null => {
  for (const s of WIZARD_STEPS) { const message = stepError(s, d, presets); if (message) return { step: s, message }; }
  return null;
};

/** The request of POST /config/setup/plan and /apply. The AMI secret is generated on the server. */
export function setupBody(oldNode: string, d: SetupDraft): SetupPlanBody {
  const b: SetupPlanBody = {
    oldNode, node: d.node.trim(), password: d.password, callsign: d.callsign.trim().toUpperCase(),
    radio: d.radio, preset: d.preset, ami: true,
  };
  b.registration = nodeKind(d.node) === 'public' ? { method: d.regMethod, ...(d.regHost.trim() ? { host: d.regHost.trim() } : {}) } : { method: 'none' };
  if (d.echolink) b.echolink = { call: d.elCall.trim().toUpperCase(), password: d.elPassword, email: d.elEmail.trim(), name: d.elName.trim(), qth: d.elQth.trim() };
  return b;
}

/** Whether a polled registration state ends the wait. */
export function registrationDone(state: string | undefined): boolean { return state === 'registered'; }
