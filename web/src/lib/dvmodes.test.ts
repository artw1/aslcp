// Run with `npm test`.
import test from 'node:test';
import assert from 'node:assert/strict';
import { cardState, lastUsed, MODE_NAME, RESUME_NOTICE, resumeHint, resumeToast, timeAgo } from './dvmodes.ts';

const now = Date.parse('2026-11-17T12:00:00Z');

test('the notice and the texts no longer speak of startup values', () => {
  assert.match(RESUME_NOTICE, /no startup modes to set/);
  for (const s of [RESUME_NOTICE, resumeHint(true), resumeHint(false), resumeToast(true), resumeToast(false)]) assert.ok(!/startup values/i.test(s), s);
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
  assert.deepEqual(Object.keys(MODE_NAME), ['LOOP', 'BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
  assert.equal(MODE_NAME.LOOP, 'Parrot (test)');
  assert.equal(lastUsed({ resume: true, lastMode: 'toString', perMode }, now), '');
});

test('cards: only a mode the build carries can be on', () => {
  const modes = [
    { id: 'LOOP' as const, enabled: true, available: true, unlink: '' },
    { id: 'BM' as const, enabled: true, available: false, unlink: '4000' },
    { id: 'YSF' as const, enabled: false, available: false, unlink: 'disconnect' },
  ];
  assert.deepEqual(cardState(modes, 'LOOP'), { available: true, enabled: true });
  assert.deepEqual(cardState(modes, 'BM'), { available: false, enabled: false });
  assert.deepEqual(cardState(modes, 'P25'), { available: false, enabled: false });
  assert.deepEqual(cardState(undefined, 'LOOP'), { available: false, enabled: false });
});

test('removed setup endpoints are gone from the client', async () => {
  const { readFileSync } = await import('node:fs');
  const src = ['./api.ts', './dvmodes.ts', './types.ts', '../components/DvSetup.svelte', '../components/DvCard.svelte'].map((f) => readFileSync(new URL(f, import.meta.url), 'utf8')).join('\n');
  assert.ok(!/\/dv\/(install|uninstall|identity|credentials|modes|brandmeister|networks|stfu|restart|rollback|dmr-status)/.test(src));
});
