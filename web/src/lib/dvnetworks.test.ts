// Run with `npm test`. The mirror of the helper's DMR network registry (internal/helper/dvswitch_networks.go).
import test from 'node:test';
import assert from 'node:assert/strict';
import { claimedBy, DV_NETWORKS, isDVNetwork, netView, networkLabel, otherOptions, PANEL_MODES } from './dvnetworks.ts';
import type { DVListItem, DVNetworkView } from './types';

const row = (name: string, address: string, port = 62031): DVListItem => ({ value: address, name, address, port });
// The shape of the real DMR_Hosts.txt, with placeholder addresses: a comment line naming TGIF must never match.
const hosts: DVListItem[] = [
  row('#                               TGIF Network Hosts Below', '203.0.113.9'),
  row('BM_2001_Example', '192.0.2.20'),
  row('Example_Repeater', '3102.repeater.net'),
  row('TGIF_Backup', '203.0.113.5'),
  row('tgif_network', 'tgif.network'),
  row('Other_Example', '192.0.2.21'),
];

test('the registry: BM, TGIF, DMR in that order, labels and transport', () => {
  assert.deepEqual(DV_NETWORKS.map((n) => [n.id, n.label, n.via, n.master]), [
    ['BM', 'BrandMeister', 'stfu', ''],
    ['TGIF', 'TGIF', 'mmdvm', 'fixed'],
    ['DMR', 'DMR - Other', 'mmdvm', 'user'],
  ]);
  assert.equal(DV_NETWORKS.filter((n) => n.catchAll).length, 1);
  assert.deepEqual(PANEL_MODES, ['BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
  assert.ok(isDVNetwork('TGIF') && isDVNetwork('DMR') && isDVNetwork('BM') && !isDVNetwork('YSF') && !isDVNetwork('tgif'));
  assert.equal(networkLabel('DMR'), 'DMR - Other');
  assert.equal(networkLabel('NXDN'), 'NXDN');
});

test('claims: BM_ rows and BrandMeister domains to BM, TGIF rows to TGIF, comment rows to nobody', () => {
  assert.deepEqual(hosts.map((h) => claimedBy(h)?.id ?? ''), ['', 'BM', 'BM', 'TGIF', 'TGIF', '']);
});

test('the DMR - Other picker leaves out the rows another network claims', () => {
  assert.deepEqual(otherOptions(hosts).map((h) => h.name), ['#                               TGIF Network Hosts Below', 'Other_Example']);
});

test('netView finds a network card', () => {
  const v: DVNetworkView = { id: 'TGIF', label: 'TGIF', via: 'mmdvm', master: 'fixed', address: 'tgif.network', port: 62031, source: 'default', passwordSet: false, enabled: true, configured: true, live: false };
  assert.equal(netView({ networks: [v] }, 'TGIF'), v);
  assert.equal(netView({ networks: [v] }, 'DMR'), undefined);
  assert.equal(netView(null, 'TGIF'), undefined);
});
