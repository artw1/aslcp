// Run with `npm test`. The Networks list summaries of the digital modes setup page.
import test from 'node:test';
import assert from 'node:assert/strict';
import { enableToast, NETWORK_GROUPS, netSummary } from './dvsummary.ts';
import { NETWORK_MODES } from './dvnetworks.ts';
import type { DVNetView, DVTuneMode } from './types';

const net = (mode: DVTuneMode, p: Partial<DVNetView> = {}): DVNetView => ({
  mode, label: mode, available: true, enabled: false, address: '', port: 0, passwordSet: false,
  fields: { address: false, port: false, password: false }, needsIdentity: false, options: [], values: {}, ...p,
});

test('groups keep the NETWORK_MODES order', () => {
  assert.equal(NETWORK_GROUPS[0].title, 'DMR');
  assert.deepEqual(NETWORK_GROUPS.flatMap((g) => g.ids), NETWORK_MODES);
});

test('summaries come from the saved values', () => {
  assert.equal(netSummary(net('BM'), true, false), 'Not set up');
  assert.equal(netSummary(net('BM', { address: 'master.example.org' }), true, true), 'Master: master.example.org');
  assert.equal(netSummary(net('TGIF'), true, false), 'Not set up');
  assert.equal(netSummary(net('TGIF', { passwordSet: true, defaultAddress: 'tgif.example.org', values: { slot: '2' } }), true, true), 'Master: tgif.example.org, TS 2');
  assert.equal(netSummary(net('DMR', { address: 'dmr.example.org' }), true, false), 'Master: dmr.example.org, TS 2');
  assert.equal(netSummary(net('DMR', { address: 'dmr.example.org', values: { slot: '1' } }), true, false), 'Master: dmr.example.org, TS 1');
  assert.equal(netSummary(net('DMR'), true, false), 'Not set up');
  assert.equal(netSummary(net('NXDN'), true, false), 'Not set up');
  assert.equal(netSummary(net('NXDN', { values: { nxdn_id: '1234567' } }), true, true), 'NXDN ID 1234567');
  assert.equal(netSummary(net('M17', { values: { suffix: 'H' } }), true, false), 'Suffix H');
  assert.equal(netSummary(net('M17'), true, false), 'Enable it, then tune on the dashboard');
  assert.equal(netSummary(net('YSF'), true, false), 'Enable it, then tune on the dashboard');
  assert.equal(netSummary(net('P25'), true, true), 'Tune on the dashboard');
});

test('unavailable and unreadable settings', () => {
  assert.equal(netSummary(net('YSF'), false, false), 'Not yet available in this build');
  assert.equal(netSummary(undefined, true, true), 'Enabled');
  assert.equal(netSummary(undefined, true, false), '');
});

test('the Enable toast', () => {
  assert.equal(enableToast('TGIF', true), 'TGIF enabled');
  assert.equal(enableToast('TGIF', false, 'engine busy'), 'TGIF disabled. engine busy');
});
