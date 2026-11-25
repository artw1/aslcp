import test from 'node:test';
import assert from 'node:assert/strict';
import { nodeTile, pubBanner, pubTile, tileBorder, tileState } from './tiles.ts';
import type { NodeStatus, PubLink, PubNode } from './types';

const link = (node: string, over: Partial<PubLink> = {}): PubLink => ({ node, kind: 'allstar', direction: 'OUT', mode: 'T', connected: 10, keyed: false, sinceKey: null, sinceUnkey: null, ...over });
const pnode = (over: Partial<PubNode> = {}): PubNode => ({ node: '1997', info: { callsign: 'N0CALL', description: 'Test node', location: '' }, stale: false, txKeyed: false, rxKeyed: false, numLinks: 0, links: [], ...over });

test('state text and border', () => {
  assert.equal(tileState({ stale: false, txKeyed: true, rxKeyed: false }), 'TX keyed');
  assert.equal(tileState({ stale: true, txKeyed: true, rxKeyed: false }), 'Stale');
  assert.equal(tileBorder({ stale: false, txKeyed: false, rxKeyed: true }), 'border-rx');
  assert.equal(tileBorder({ stale: false, txKeyed: false, rxKeyed: false }), 'border-line');
});

test('public tile: links sorted keyed first, at most 3, bridge label and hidden callsigns', () => {
  const n = pnode({
    links: [link('2000', { connected: 50, info: { callsign: 'N1CALL', description: 'Hub', location: '' } }), link('1999', { label: 'Digital modes linked node', connected: 5 }), link('2001', { keyed: true, connected: 90 }), link('2002', { kind: 'echolink' })],
  });
  const t = pubTile(n, null);
  assert.deepEqual(t.shown.map((l) => l.node), ['2001', '1999', '2002']);
  assert.equal(t.more, 1);
  assert.equal(t.shown[1].label, 'Digital modes linked node');
  assert.equal(t.shown[2].label, 'EchoLink');
  assert.equal(t.name, 'N0CALL');
  assert.equal(t.linkCount, 4);
});

test('public tile talker: ASL link callsign, node number when hidden, DV call over the bridge link', () => {
  const asl = pnode({ rxKeyed: true, links: [link('2000', { keyed: true, info: { callsign: 'N1CALL', description: '', location: '' } })] });
  assert.equal(pubTile(asl, null).talker, 'N1CALL');
  assert.equal(pubTile(pnode({ rxKeyed: true, links: [link('2000', { keyed: true })] }), null).talker, 'Node 2000');
  const dv = pnode({ rxKeyed: true, links: [link('1999', { keyed: true, label: 'Digital modes linked node' })] });
  assert.equal(pubTile(dv, { mode: 'DMR', callsign: 'N0CALL', at: 1 }).talker, 'N0CALL');
  assert.equal(pubTile(dv, null).talker, '');
  assert.equal(pubTile(pnode({ links: [link('2000', { keyed: true })] }), null).talker, ''); // node idle
});

test('dashboard tile', () => {
  const n = { node: '2001', info: { node: '2001', callsign: 'N0CALL', description: '', location: '' }, stale: false, rxKeyed: false, txKeyed: false, extTxKeyed: false, numLinks: 1, links: [{ node: '2000', kind: 'allstar', keyed: false, connected: 1, info: null }], network: [], flags: {}, updated: '' } as unknown as NodeStatus;
  const t = nodeTile(n, null, null);
  assert.deepEqual([t.node, t.name, t.linkCount, t.shown[0].node, t.more], ['2001', 'N0CALL', 1, '2000', 0]);
});

test('banner: DV call first, else a keyed ASL link, nothing otherwise', () => {
  const nodes = [pnode({ txKeyed: true, links: [link('2000', { keyed: true, sinceKey: 4, info: { callsign: 'N1CALL', description: 'Hub', location: '' } })] })];
  const asl = pubBanner({ nodes, showDV: false }, 0, { '1997': 100000 });
  assert.deepEqual([asl?.source, asl?.main, asl?.startMs, asl?.qrz], ['ASL', 'N1CALL', 96000, 'https://www.qrz.com/db/N1CALL']);
  const dv = pubBanner({ nodes, showDV: true, dv: { now: { mode: 'YSF', callsign: 'N0CALL', target: 'YSF-TEST', at: 50, name: 'Pat Example' }, heard: [] } }, 1000, {});
  assert.deepEqual([dv?.source, dv?.main, dv?.sub, dv?.startMs], ['YSF', 'N0CALL', 'Pat Example · YSF-TEST', 49000]);
  assert.equal(pubBanner({ nodes: [pnode()], showDV: true, dv: { now: null, heard: [] } }, 0, {}), null);
  assert.equal(pubBanner(null, 0, {}), null);
  // DV off: the DV call is ignored even if present
  assert.equal(pubBanner({ nodes: [pnode()], showDV: false, dv: { now: { mode: 'DMR', at: 1 }, heard: [] } }, 0, {}), null);
});
