// Run with `npm test`.
import test from 'node:test';
import assert from 'node:assert/strict';
import { cardEnabled, lastUsed, MODE_NAME, modesBody, RESUME_NOTICE, resumeHint, resumeToast, RESTART_WARNING, timeAgo } from './dvmodes.ts';
import { masterSource } from './dvnetworks.ts';
import type { DVNetworkView, DVSwitchSetup } from './types';

const now = Date.parse('2026-11-17T12:00:00Z');

test('the notice and the texts no longer speak of startup values', () => {
  assert.match(RESUME_NOTICE, /no startup modes to set/);
  for (const s of [RESUME_NOTICE, RESTART_WARNING, resumeHint(true), resumeHint(false), resumeToast(true), resumeToast(false)]) assert.ok(!/startup values/i.test(s), s);
  assert.match(resumeHint(false), /^Off:/);
  assert.match(resumeHint(true), /^On:/);
});

test('time ago', () => {
  assert.equal(timeAgo('2026-11-17T11:59:40Z', now), 'just now');
  assert.equal(timeAgo('2026-11-17T11:55:00Z', now), '5 min ago');
  assert.equal(timeAgo('2026-11-17T09:00:00Z', now), '3 h ago');
  assert.equal(timeAgo('2026-11-15T12:00:00Z', now), '2 d ago');
  assert.equal(timeAgo('bad', now), '');
});

test('last used: the last mode and its tune', () => {
  const perMode = { BM: { tune: '91', ts: '2', at: '2026-11-17T11:55:00Z' }, P25: { tune: '10100', at: '2026-11-17T09:00:00Z' } };
  assert.equal(lastUsed({ resume: true, lastMode: 'BM', perMode }, now), 'Last used: BrandMeister TG 91 TS 2 (5 min ago)');
  assert.equal(lastUsed({ resume: false, lastMode: 'P25', perMode }, now), 'Last used: P25 10100 (3 h ago)');
  assert.equal(lastUsed({ resume: false, lastMode: 'YSF', perMode }, now), 'Last used: System Fusion (YSF)');
  assert.equal(lastUsed({ resume: false, perMode }, now), '');
  assert.equal(lastUsed({ resume: false, lastMode: 'bogus', perMode }, now), '');
  assert.equal(lastUsed(null, now), '');
});

test('last used names the actual DMR network', () => {
  const perMode = { TGIF: { tune: '31665', ts: '2', at: '2026-11-17T11:55:00Z' }, DMR: { tune: '1', at: '2026-11-17T09:00:00Z' } };
  assert.equal(lastUsed({ resume: true, lastMode: 'TGIF', perMode }, now), 'Last used: TGIF TG 31665 TS 2 (5 min ago)');
  assert.equal(lastUsed({ resume: true, lastMode: 'DMR', perMode }, now), 'Last used: DMR - Other TG 1 (3 h ago)');
  assert.equal(lastUsed({ resume: true, lastMode: 'BM', perMode }, now), 'Last used: BrandMeister');
  assert.deepEqual(Object.keys(MODE_NAME), ['BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
  assert.equal(lastUsed({ resume: true, lastMode: 'toString', perMode }, now), '');
});

test('fixed master source', () => {
  assert.equal(masterSource({ source: 'hosts', name: 'TGIF_Network', label: 'TGIF' }), 'From DMR_Hosts.txt (TGIF_Network).');
  assert.match(masterSource({ source: 'default', label: 'TGIF' }), /^TGIF is not in the host list, using the default/);
});

const nv = (id: DVNetworkView['id'], enabled: boolean): DVNetworkView => ({ id, label: id, via: id === 'BM' ? 'stfu' : 'mmdvm', address: '', port: 0, passwordSet: false, enabled, configured: true, live: false });

test('cards: each DMR network has its own Enable, no exclusivity', () => {
  const v: Pick<DVSwitchSetup, 'modes' | 'networks'> = { modes: { dmr: true, ysf: false, p25: true, nxdn: false }, networks: [nv('BM', false), nv('TGIF', true), nv('DMR', true)] };
  assert.equal(cardEnabled(v, 'DMR'), true);
  assert.equal(cardEnabled(v, 'TGIF'), true);
  assert.equal(cardEnabled(v, 'BM'), false);
  assert.equal(cardEnabled(v, 'P25'), true);
  assert.equal(cardEnabled(v, 'YSF'), false);
  const off = { ...v, networks: [nv('BM', true), nv('TGIF', false), nv('DMR', true)] };
  assert.equal(cardEnabled(off, 'TGIF'), false);
  assert.equal(cardEnabled(off, 'DMR'), true);
  assert.equal(cardEnabled(off, 'BM'), true);
});

test('modes body of a gateway card button', () => {
  assert.deepEqual(modesBody('YSF', true), { ysf: true });
  assert.deepEqual(modesBody('NXDN', false), { nxdn: false });
  assert.deepEqual(modesBody('P25', true), { p25: true });
});

test('the DMR profile and the exclusivity are gone from the client', async () => {
  const { readFileSync } = await import('node:fs');
  const src = ['./api.ts', './dvmodes.ts', './types.ts', '../components/DvswitchSetup.svelte', '../components/DvswitchCard.svelte'].map((f) => readFileSync(new URL(f, import.meta.url), 'utf8')).join('\n');
  assert.ok(!/dmrProfile|DVDMRProfile|turns DMR - Other off|turns TGIF off|\/dvswitch\/tgif/.test(src));
  assert.match(src, /\/dvswitch\/networks\//);
});

test('startup modes are gone from the API client and the page', async () => {
  const { readFileSync } = await import('node:fs');
  const src = readFileSync(new URL('./api.ts', import.meta.url), 'utf8') + readFileSync(new URL('../components/DvswitchSetup.svelte', import.meta.url), 'utf8');
  assert.ok(!/useCurrent|Use current|dvswitchStartup|\/dvswitch\/startup|startup values/i.test(src));
});
