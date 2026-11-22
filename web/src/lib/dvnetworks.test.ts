// Run with `npm test`. The read-only registry of the DMR networks.
import test from 'node:test';
import assert from 'node:assert/strict';
import { DV_NETWORKS, isDVNetwork, NETWORK_MODES, networkLabel, PANEL_MODES } from './dvnetworks.ts';

test('the registry: BM, TGIF, DMR in that order, with labels', () => {
  assert.deepEqual(DV_NETWORKS.map((n) => [n.id, n.label]), [['BM', 'BrandMeister'], ['TGIF', 'TGIF'], ['DMR', 'DMR - Other']]);
  assert.deepEqual(PANEL_MODES, ['LOOP', 'BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
  assert.ok(isDVNetwork('TGIF') && isDVNetwork('DMR') && isDVNetwork('BM') && !isDVNetwork('YSF') && !isDVNetwork('LOOP') && !isDVNetwork('tgif'));
  assert.equal(networkLabel('DMR'), 'DMR - Other');
  assert.equal(networkLabel('NXDN'), 'NXDN');
  assert.equal(networkLabel('LOOP'), 'Echo test');
  assert.deepEqual(NETWORK_MODES, ['BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
});
