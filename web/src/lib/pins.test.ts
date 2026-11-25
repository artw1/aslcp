// Run with `npm test` (node's own test runner; no extra dependency). svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { buildAlerts, buildPins, bridgeNodeNumber, dvPinPlan, mergeActivity, pinLocalNode, tileNodes } from './pins.ts';
import type { DVFavorite, DVSnapshot, Favorite, NodeStatus, NowCall } from './types';

const snap = (over: Partial<DVSnapshot> = {}): DVSnapshot => ({
  installed: true, running: true, configured: true,
  bridge: { mode: 'YSF', tune: '00001', slot: 2, muted: false, source: 'aslcp-dv', updated: '' },
  modes: [], services: [], bridgeNode: { present: true, node: '1999' },
  link: { mode: 'ondemand', localNode: '2001', linked: true, paused: false, exclusive: true }, heard: [], ...over,
});
const node = (n: string, over: Partial<NodeStatus> = {}): NodeStatus => ({ node: n, info: null, stale: false, rxKeyed: false, txKeyed: false, extTxKeyed: false, numLinks: 0, links: [], network: [], flags: {}, updated: '', ...over });
const aslFav = (id: number, n: string, label = ''): Favorite => ({ id, node: n, label, group: '', position: id, pinned: false, info: null });
const dvFav = (id: number, mode: DVFavorite['mode'], value: string, name: string): DVFavorite => ({ id, mode, value, name, sort: id, pinned: false });

test('the bridge node gets no tile', () => {
  const nodes = [node('2001'), node('1999'), node('2002')];
  assert.equal(bridgeNodeNumber(snap()), '1999');
  assert.deepEqual(tileNodes(nodes, snap()).map((n) => n.node), ['2001', '2002']);
  assert.deepEqual(tileNodes(nodes, snap({ bridgeNode: { present: false } })).length, 3);
  assert.deepEqual(tileNodes(nodes, null).length, 3);
});

test('pin local node: selected, else first', () => {
  const nodes = [node('2001'), node('2002')];
  assert.equal(pinLocalNode('2002', nodes), '2002');
  assert.equal(pinLocalNode('1999', nodes), '2001');
  assert.equal(pinLocalNode('', []), '');
});

test('buildPins lists the pinned favourites only: AllStar first, then DV', () => {
  const items = buildPins(
    [aslFav(1, '2000', 'Example hub'), { ...aslFav(2, '1000', 'Other'), pinned: true }, { ...aslFav(3, '1001', 'Third'), pinned: true }],
    [{ ...dvFav(2, 'YSF', '00001', 'US-Example'), pinned: true }, dvFav(3, 'P25', '10100', 'Not pinned')]);
  assert.deepEqual(items.map((i) => [i.key, i.label]), [['asl:2', 'Other'], ['asl:3', 'Third'], ['dv:2', 'US-Example']]);
  assert.equal(items[0].node, '1000');
  assert.equal(items[2].fav?.value, '00001');
  assert.deepEqual(buildPins([], []), []);
});

test('dvPinPlan: switch mode, tune, link the bridge', () => {
  const f = dvFav(1, 'P25', '10100', 'Example TG');
  const p = dvPinPlan(snap({ link: { mode: 'ondemand', localNode: '2001', linked: false, paused: false, exclusive: true } }), f)!;
  assert.equal(p.mode, 'P25');
  assert.equal(p.tune, '10100');
  assert.deepEqual(p.link, { local: '2001', node: '1999', exclusive: true });
  const same = dvPinPlan(snap(), dvFav(2, 'YSF', '00002', 'XX-Europe'))!;
  assert.equal(same.mode, null);
  assert.equal(same.link, null);
  assert.equal(dvPinPlan(snap({ configured: false }), f), null);
  assert.equal(dvPinPlan(null, f), null);
  // a permanent link is kept by the engine, never linked from here
  assert.equal(dvPinPlan(snap({ link: { mode: 'permanent', localNode: '2001', linked: false, paused: false, exclusive: false } }), f)!.link, null);
});

test('alerts', () => {
  assert.deepEqual(buildAlerts([node('2001')], true, snap()), []);
  assert.deepEqual(buildAlerts([], false, null), []);
  assert.equal(buildAlerts([], true, null)[0].key, 'nonodes');
  const modes = [{ id: 'BM' as const, enabled: true, available: true, unlink: '4000', login: 'failed' as const, detail: 'bad password' }];
  assert.ok(buildAlerts([node('2001')], true, snap({ modes })).some((a) => a.key === 'login:BM'));
  const down = buildAlerts([node('2001')], true, snap({ link: { mode: 'ondemand', localNode: '2001', linked: false, paused: false, exclusive: true } }));
  assert.deepEqual(down.map((a) => a.key), ['bridge']);
  assert.equal(buildAlerts([node('2001')], true, snap({ running: false }))[0].key, 'dvdown');
  assert.equal(buildAlerts([node('2001', { stale: true })], true, null)[0].key, 'stale:2001');
});

test('activity merges ASL and DV, newest first', () => {
  const t = (s: number) => new Date(Date.UTC(2026, 0, 1, 12, 0, s)).toISOString();
  const recent: NowCall[] = [
    { id: 'a', kind: 'allstar', call: 'N1CALL', node: '2000', desc: 'Example hub', start: t(0), end: t(10), active: false },
    { id: 'd', kind: 'dmr', call: 'N0CALL', start: t(30), end: t(40), active: false },
  ];
  const heard = [{ call: 'N0CALL', dst: '91', slot: 2, at: '2026-01-01T12:00:30Z'.slice(0, 19).replace('T', ' ') }];
  const out = mergeActivity(recent, heard);
  assert.equal(out.length, 2);
  assert.deepEqual(out.map((o) => o.src).sort(), ['ASL', 'DV']);
  assert.equal(out.filter((o) => o.src === 'ASL').length, 1);
  assert.equal(mergeActivity(undefined, undefined).length, 0);
  assert.equal(mergeActivity(recent, heard, 1).length, 1);
});
