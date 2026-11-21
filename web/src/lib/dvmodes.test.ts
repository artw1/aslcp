// Run with `npm test`.
import test from 'node:test';
import assert from 'node:assert/strict';
import { cardEnabled, lastUsed, modesBody, RESUME_NOTICE, resumeHint, resumeToast, RESTART_WARNING, tgifSource, TGIF_EXCLUSIVE, timeAgo } from './dvmodes.ts';
import type { DVSwitchSetup } from './types';

const now = Date.parse('2026-11-17T12:00:00Z');

test('the notice and the texts no longer speak of startup values', () => {
  assert.match(RESUME_NOTICE, /no startup modes to set/);
  for (const s of [RESUME_NOTICE, RESTART_WARNING, resumeHint(true), resumeHint(false), resumeToast(true), resumeToast(false)]) assert.ok(!/startup values/i.test(s), s);
  assert.match(resumeHint(false), /^Off:/);
  assert.match(resumeHint(true), /^On:/);
  assert.equal(TGIF_EXCLUSIVE, 'Enabling TGIF turns DMR - Other off.');
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

test('TGIF source', () => {
  assert.equal(tgifSource({ source: 'hosts', name: 'TGIF_Network' }), 'From DMR_Hosts.txt (TGIF_Network).');
  assert.match(tgifSource({ source: 'default' }), /not in the host list, using the default/);
});

test('cards: TGIF and DMR - Other share the DMR flags', () => {
  const tgif = { address: 'tgif.network', port: 62031, source: 'default' as const, passwordSet: true, enabled: false };
  const v = { modes: { dmr: true, ysf: false, p25: true, nxdn: false }, dmr: { address: '192.0.2.40', port: 62031, passwordSet: false }, tgif } as Pick<DVSwitchSetup, 'modes' | 'dmr' | 'tgif'>;
  assert.equal(cardEnabled(v, 'DMR'), true);
  assert.equal(cardEnabled(v, 'TGIF'), false);
  assert.equal(cardEnabled(v, 'P25'), true);
  assert.equal(cardEnabled(v, 'YSF'), false);
  const t = { ...v, tgif: { ...tgif, enabled: true } };
  assert.equal(cardEnabled(t, 'DMR'), false);
  assert.equal(cardEnabled(t, 'TGIF'), true);
});

test('modes body of a card button', () => {
  assert.deepEqual(modesBody('DMR', true), { dmr: true, dmrProfile: 'other' });
  assert.deepEqual(modesBody('DMR', false), { dmr: false });
  assert.deepEqual(modesBody('YSF', true), { ysf: true });
  assert.deepEqual(modesBody('NXDN', false), { nxdn: false });
});

test('startup modes are gone from the API client and the page', async () => {
  const { readFileSync } = await import('node:fs');
  const src = readFileSync(new URL('./api.ts', import.meta.url), 'utf8') + readFileSync(new URL('../components/DvswitchSetup.svelte', import.meta.url), 'utf8');
  assert.ok(!/useCurrent|Use current|dvswitchStartup|\/dvswitch\/startup|startup values/i.test(src));
});
