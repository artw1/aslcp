// Run with `npm test` (node's own test runner; no extra dependency). svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { aslPinActive, buildAlerts, buildPins, bridgeNodeNumber, dvLastCaller, dvPinActive, dvPinPlan, pinActive, pinLocalNode, tileLinks, tileNodes, tileTalker } from './pins.ts';
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
  // BrandMeister outside its mode (idle, "Not in use") is not a failed login
  const idle = [{ id: 'BM' as const, enabled: true, available: true, unlink: '4000', login: 'idle' as const }];
  assert.deepEqual(buildAlerts([node('2001')], true, snap({ modes: idle })), []);
  // YSF, P25, NXDN and M17 have no login: no login alert, whatever the engine says
  const refl = ['YSF', 'P25', 'NXDN', 'M17'].flatMap((id) => (['pending', 'failed'] as const).map((login) => ({ id: id as 'YSF', enabled: true, available: true, unlink: 'disconnect', login })));
  assert.deepEqual(buildAlerts([node('2001')], true, snap({ modes: refl })), []);
  // A bridge node that is not linked (on demand, or a paused permanent link) raises no alert on the overview
  assert.deepEqual(buildAlerts([node('2001')], true, snap({ link: { mode: 'ondemand', localNode: '2001', linked: false, paused: false, exclusive: true } })), []);
  assert.deepEqual(buildAlerts([node('2001')], true, snap({ link: { mode: 'permanent', localNode: '2001', linked: false, paused: true, exclusive: true } })), []);
  // A permanent link that should be up but is down does alert
  const down = buildAlerts([node('2001')], true, snap({ link: { mode: 'permanent', localNode: '2001', linked: false, paused: false, exclusive: true } }));
  assert.deepEqual(down.map((a) => [a.key, a.text]), [['bridge', 'The bridge node link is down.']]);
  assert.equal(buildAlerts([node('2001')], true, snap({ running: false }))[0].key, 'dvdown');
  assert.equal(buildAlerts([node('2001', { stale: true })], true, null)[0].key, 'stale:2001');
});

const link = (n: string, over: Partial<NodeStatus['links'][number]> = {}): NodeStatus['links'][number] => ({ node: n, kind: 'allstar', info: null, ip: '192.0.2.1', direction: 'OUT', connected: 10, state: 'ESTABLISHED', mode: 'T', keyed: false, sinceKey: null, sinceUnkey: null, ...over });

test('tile links: three at most, keyed first, names, and the rest counted', () => {
  const n = node('2001', { links: [link('1998', { connected: 50 }), link('1999', { connected: 40 }), link('1997', { connected: 5, info: { callsign: 'N1CALL' } as never }), link('2002', { connected: 70, keyed: true })] });
  const r = tileLinks(n, snap());
  assert.deepEqual(r.shown.map((l) => l.node), ['2002', '1997', '1999']);
  assert.equal(r.shown[1].label, 'N1CALL');
  assert.equal(r.shown[2].label, 'Digital modes linked node');
  assert.equal(r.more, 1);
  assert.deepEqual(tileLinks(node('2001'), null), { shown: [], more: 0 });
});

test('tile talker: AllStar by local node, digital by the keyed bridge link, none when idle', () => {
  const call = (over: Partial<NowCall>): NowCall => ({ id: '1', kind: 'allstar', call: 'N1CALL', start: '', active: true, ...over });
  const keyed = node('2001', { rxKeyed: true, links: [link('1999', { keyed: true })] });
  assert.equal(tileTalker(keyed, call({ local: '2001' }), snap()), 'N1CALL');
  assert.equal(tileTalker(keyed, call({ local: '2002' }), snap()), '');
  assert.equal(tileTalker(keyed, call({ kind: 'dmr', call: 'N0CALL' }), snap()), 'N0CALL');
  assert.equal(tileTalker(node('2001', { rxKeyed: true, links: [link('1999')] }), call({ kind: 'dmr' }), snap()), '');
  assert.equal(tileTalker(node('2001', { links: [link('1999', { keyed: true })] }), call({ kind: 'dmr' }), snap()), '');
  assert.equal(tileTalker(keyed, null, snap()), '');
});

test('DV last caller', () => {
  assert.equal(dvLastCaller(snap()), null);
  const c = dvLastCaller(snap({ heard: [{ call: 'N0CALL', at: '2026-11-25 10:00:00' }, { call: 'N1CALL' }] }));
  assert.equal(c?.who, 'N0CALL');
  assert.ok((c?.at ?? 0) > 0);
});

test('AllStar pin is active when the target is linked to any local node', () => {
  const nodes = [node('2001', { links: [link('1998')] }), node('2002', { links: [link('1997')] })];
  assert.equal(aslPinActive('1997', nodes), true);
  assert.equal(aslPinActive('1998', nodes), true);
  assert.equal(aslPinActive('1996', nodes), false);
  assert.equal(aslPinActive('', nodes), false);
  assert.equal(aslPinActive('1998', []), false);
});

test('DV pin is active when mode and target match what is tuned', () => {
  assert.equal(dvPinActive(snap(), dvFav(1, 'YSF', '00001', 'x')), true);
  assert.equal(dvPinActive(snap(), dvFav(1, 'YSF', '00002', 'x')), false);
  assert.equal(dvPinActive(snap(), dvFav(1, 'M17', '00001', 'x')), false);
  assert.equal(dvPinActive(snap({ bridge: { ...snap().bridge, source: 'none' } }), dvFav(1, 'YSF', '00001', 'x')), false);
  assert.equal(dvPinActive(snap({ bridge: { ...snap().bridge, mode: 'BM', tune: '91', private: true } }), dvFav(1, 'BM', '91#', 'x')), true);
  assert.equal(dvPinActive(snap({ bridge: { ...snap().bridge, mode: 'BM', tune: '91', private: true } }), dvFav(1, 'BM', '91', 'x')), false);
  assert.equal(dvPinActive(null, dvFav(1, 'YSF', '00001', 'x')), false);
  assert.equal(pinActive({ key: 'a', kind: 'asl', label: '', sub: '', node: '1998' }, [node('2001', { links: [link('1998')] })], null), true);
  assert.equal(pinActive({ key: 'd', kind: 'dv', label: '', sub: '', fav: dvFav(1, 'YSF', '00001', 'x') }, [], snap()), true);
});
