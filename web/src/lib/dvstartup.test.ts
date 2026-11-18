// Run with `npm test`.
import test from 'node:test';
import assert from 'node:assert/strict';
import { ambeModeFor, cardOrder, cardPatch, lastUsed, startupModeOf, timeAgo, type StartupForm } from './dvstartup.ts';

const snap: StartupForm = { ambeMode: 'DMR', txTg: '91', txTs: '2', colorCode: '1', ysf: '00001', p25: '10100', nxdn: '10' };
const now = Date.parse('2026-11-17T12:00:00Z');

test('startup mode maps the ini values', () => {
  assert.equal(startupModeOf('YSFN'), 'YSF');
  assert.equal(startupModeOf('YSFW'), 'YSF');
  assert.equal(startupModeOf('P25'), 'P25');
  assert.equal(startupModeOf('NXDN'), 'NXDN');
  assert.equal(startupModeOf(''), 'DMR');
  assert.equal(ambeModeFor('YSF', 'YSFW'), 'YSFW');
  assert.equal(ambeModeFor('YSF', 'DMR'), 'YSFN');
  assert.equal(ambeModeFor('P25', 'YSFW'), 'P25');
});

test('the selected card comes first', () => {
  assert.deepEqual(cardOrder('DMR'), ['DMR', 'YSF', 'P25', 'NXDN']);
  assert.deepEqual(cardOrder('P25'), ['P25', 'DMR', 'YSF', 'NXDN']);
});

test('a card sends only its own changed fields', () => {
  assert.equal(cardPatch('DMR', snap, snap), null);
  assert.deepEqual(cardPatch('DMR', snap, { ...snap, txTg: '9', p25: '1' }), { txTg: '9' });
  assert.deepEqual(cardPatch('DMR', snap, { ...snap, txTs: '1', colorCode: '3' }), { txTs: 1, colorCode: 3 });
  assert.deepEqual(cardPatch('P25', snap, { ...snap, p25: '10200', txTg: '9' }), { p25: '10200' });
  assert.equal(cardPatch('NXDN', snap, { ...snap, p25: '10200' }), null);
  assert.deepEqual(cardPatch('YSF', snap, { ...snap, ysf: '00002' }), { ysf: '00002' });
});

test('time ago', () => {
  assert.equal(timeAgo('2026-11-17T11:59:40Z', now), 'just now');
  assert.equal(timeAgo('2026-11-17T11:55:00Z', now), '5 min ago');
  assert.equal(timeAgo('2026-11-17T09:00:00Z', now), '3 h ago');
  assert.equal(timeAgo('2026-11-15T12:00:00Z', now), '2 d ago');
  assert.equal(timeAgo('bad', now), '');
});

test('last used label', () => {
  const r = { resume: false, perMode: { DMR: { tune: '91', ts: '2', at: '2026-11-17T11:55:00Z' }, P25: { tune: '10100', at: '2026-11-17T09:00:00Z' } } };
  assert.equal(lastUsed(r, 'DMR', now), 'Last used: TG 91 TS 2 (5 min ago)');
  assert.equal(lastUsed(r, 'P25', now), 'Last used: 10100 (3 h ago)');
  assert.equal(lastUsed(r, 'YSF', now), '');
  assert.equal(lastUsed(null, 'DMR', now), '');
});

test('useCurrent is gone from the API client', async () => {
  const { readFileSync } = await import('node:fs');
  const src = readFileSync(new URL('./api.ts', import.meta.url), 'utf8') + readFileSync(new URL('../components/DvswitchSetup.svelte', import.meta.url), 'utf8');
  assert.ok(!/useCurrent|Use current/.test(src));
});
