import { test } from 'node:test';
import assert from 'node:assert/strict';
import { allmonStillOpen, offersAllmon, firewallText, listenText, webAddress, pagePort, shouldConfirm, watchOldAddress } from './svcweb.ts';
import type { SvcScope, SvcPort, WebPortStatus } from './svcweb.ts';

const scope = (name: SvcScope['scope'], active: boolean, installed = true, fw: string[] = []): SvcScope => ({
  scope: name, label: name, installed, active, enabled: active, recorded: false, units: [], firewall: fw.map((service) => ({ service, open: true })),
});

test('allmonStillOpen: Apache off while Allmon3 runs', () => {
  assert.equal(allmonStillOpen([scope('cockpit', true), scope('apache', false), scope('allmon3', true)]), true);
  assert.equal(allmonStillOpen([scope('apache', true), scope('allmon3', true)]), false);
  assert.equal(allmonStillOpen([scope('apache', false), scope('allmon3', false)]), false);
  assert.equal(allmonStillOpen([scope('apache', false, false), scope('allmon3', true)]), false);
});

test('offersAllmon only for Apache off with Allmon3 running', () => {
  const s = [scope('apache', true), scope('allmon3', true)];
  assert.equal(offersAllmon(s, 'apache', 'disable'), true);
  assert.equal(offersAllmon(s, 'apache', 'enable'), false);
  assert.equal(offersAllmon(s, 'cockpit', 'disable'), false);
  assert.equal(offersAllmon([scope('apache', true), scope('allmon3', false)], 'apache', 'disable'), false);
});

test('firewallText', () => {
  assert.equal(firewallText(scope('apache', true, true, ['http', 'https']), 'disable'), 'Also close http and https in the firewall');
  assert.equal(firewallText(scope('cockpit', false, true, ['cockpit']), 'enable'), 'Also reopen cockpit in the firewall');
  assert.equal(firewallText(scope('allmon3', true), 'disable'), '');
});

test('listenText', () => {
  const p = (o: Partial<SvcPort>): SvcPort => ({ scope: 'x', label: '80', listening: true, ports: [80], addrs: ['*'], ...o });
  assert.equal(listenText(p({ listening: false, ports: [], addrs: [] })), 'closed');
  assert.equal(listenText(p({})), 'listening on all addresses');
  assert.equal(listenText(p({ addrs: ['127.0.0.1'] })), 'listening on 127.0.0.1');
  assert.equal(listenText(p({ ports: [16700, 16701], addrs: ['0.0.0.0'] })), 'listening on all addresses (16700, 16701)');
});

test('webAddress builds the link from the browser host', () => {
  assert.equal(webAddress({ protocol: 'http:', hostname: 'node2001.local' }, 80), 'http://node2001.local/');
  assert.equal(webAddress({ protocol: 'http:', hostname: '192.0.2.10' }, 8080, '/#/settings/system/web-port'), 'http://192.0.2.10:8080/#/settings/system/web-port');
  assert.equal(webAddress({ protocol: 'http:', hostname: '2001:db8::1' }, 80), 'http://[2001:db8::1]/');
  assert.equal(webAddress({ protocol: 'http:', hostname: '[2001:db8::1]' }, 8080), 'http://[2001:db8::1]:8080/');
});

test('pagePort and shouldConfirm', () => {
  assert.equal(pagePort({ port: '', protocol: 'http:' }), 80);
  assert.equal(pagePort({ port: '8080', protocol: 'http:' }), 8080);
  const st = (o: Partial<WebPortStatus>): WebPortStatus => ({ available: true, listen: ':80', port: 80, on80: true, canMove: false, reason: 'already', apacheActive: false, dropIn: true, previous: ':8080', backPort: 8080, pending: { deadline: '', seconds: 40 }, ...o });
  assert.equal(shouldConfirm(st({}), { port: '', protocol: 'http:' }), true);
  assert.equal(shouldConfirm(st({}), { port: '8080', protocol: 'http:' }), false); // old address: do not confirm
  assert.equal(shouldConfirm(st({ pending: undefined }), { port: '', protocol: 'http:' }), false);
  assert.equal(shouldConfirm(st({ on80: false }), { port: '', protocol: 'http:' }), false);
});

test('watchOldAddress: down and up again means the move was undone', async () => {
  let t = 0, calls = 0;
  const probe = async () => { calls++; if (calls === 2 || calls === 3) throw new Error('refused'); return {}; };
  const r = await watchOldAddress({ probe, timeoutMs: 90000, now: () => t, sleep: async (ms) => { t += ms; } });
  assert.equal(r, 'returned');
  assert.equal(calls, 4);
});

test('watchOldAddress: staying down means the move stuck', async () => {
  let t = 0;
  const probe = async () => { throw new Error('refused'); };
  const r = await watchOldAddress({ probe, timeoutMs: 10000, intervalMs: 2000, now: () => t, sleep: async (ms) => { t += ms; } });
  assert.equal(r, 'gone');
});

test('watchOldAddress: always up never reports a return', async () => {
  let t = 0;
  const r = await watchOldAddress({ probe: async () => ({}), timeoutMs: 6000, intervalMs: 2000, now: () => t, sleep: async (ms) => { t += ms; } });
  assert.equal(r, 'gone');
});
