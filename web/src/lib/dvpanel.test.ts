// Run with `npm test` (node's own test runner; no extra dependency). svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { CONFIRM_MS, allOk, bridgeLinked, controlsLocked, defaultFavName, favoriteBlock, favoritesFor, pendingOutcome, serviceDots, shownMode, stateLine, validFavName, validTune } from './dvpanel.ts';
import type { DVFavorite, DVSnapshot } from './types';

const bridge = { mode: 'YSF', tune: '192.0.2.10:42000', targetName: 'Example room', slot: 2, muted: false, source: 'abinfo' as const, updated: '' };
const snap = (over: Partial<DVSnapshot> = {}): DVSnapshot => ({
  installed: true, running: true, configured: true, bridge, modes: [],
  services: [{ unit: 'analog_bridge', active: true }, { unit: 'mmdvm_bridge', active: true }], bridgeNode: { present: true, node: '1999' }, link: { mode: 'ondemand', localNode: '', linked: false, paused: false }, ...over,
});

test('state line', () => {
  assert.equal(stateLine(bridge), 'Mode YSF, tuned to Example room (192.0.2.10:42000)');
  assert.equal(stateLine({ ...bridge, targetName: undefined }), 'Mode YSF, tuned to 192.0.2.10:42000');
  assert.equal(stateLine({ ...bridge, tune: '', targetName: undefined }), 'Mode YSF');
  assert.equal(stateLine({ ...bridge, source: 'none', mode: '' }), 'Mode unknown');
  assert.equal(stateLine({ ...bridge, mode: '' }), 'Mode unknown');
  assert.equal(stateLine(null), 'Mode unknown');
});

test('service dots are amber for a down unit or an unknown state', () => {
  assert.ok(allOk(snap()));
  const down = snap({ services: [{ unit: 'analog_bridge', active: true }, { unit: 'ysfgateway', active: false }] });
  assert.deepEqual(serviceDots(down).map((d) => d.ok), [true, false]);
  assert.ok(!allOk(down));
  const unknown = snap({ bridge: { ...bridge, source: 'none', mode: '' } });
  assert.ok(!allOk(unknown));
  assert.deepEqual(serviceDots(unknown).map((d) => d.ok), [false, true]);
  assert.deepEqual(serviceDots(null), []);
  assert.ok(!allOk(null));
});

test('favourite names', () => {
  assert.equal(validFavName('  Example room '), 'Example room');
  assert.equal(validFavName(''), null);
  assert.equal(validFavName('   '), null);
  assert.equal(validFavName('a'.repeat(40)), 'a'.repeat(40));
  assert.equal(validFavName('a'.repeat(41)), null);
  assert.equal(validFavName('é'.repeat(40)), 'é'.repeat(40));
  assert.equal(validFavName('a\nb'), null);
  assert.equal(validFavName('a\u0007b'), null);
});

test('tune values follow the server rules', () => {
  assert.ok(validTune('DMR', '91') && validTune('DMR', '12345678'));
  assert.ok(!validTune('DMR', '123456789') && !validTune('DMR', 'abc') && !validTune('DMR', ''));
  assert.ok(validTune('P25', '10100') && !validTune('P25', '101000'));
  assert.ok(validTune('NXDN', '65') && !validTune('NXDN', '-1'));
  assert.ok(validTune('YSF', '00001') && validTune('YSF', 'US-Example') && validTune('YSF', '192.0.2.10:42000') && validTune('YSF', 'disconnect'));
  assert.ok(!validTune('YSF', '192.0.2.10:70000') && !validTune('YSF', 'a=b') && !validTune('YSF', '-x') && !validTune('YSF', 'a;b') && !validTune('YSF', 'x'.repeat(33)));
  assert.ok(!validTune('DSTAR', '1'));
  assert.ok(validTune('DMR', ' 91 '));
});

const fav = (id: number, mode: DVFavorite['mode'], value: string, sort = id): DVFavorite => ({ id, mode, value, name: `f${id}`, sort });

test('favourites per mode, ordered, and why a save is blocked', () => {
  const favs = [fav(1, 'YSF', '00001', 5), fav(2, 'DMR', '91'), fav(3, 'YSF', '00002', 1)];
  assert.deepEqual(favoritesFor(favs, 'YSF').map((f) => f.id), [3, 1]);
  assert.equal(favoriteBlock(favs, 'YSF', '00003'), null);
  assert.equal(favoriteBlock(favs, 'YSF', '00001'), 'Already a favourite');
  assert.match(favoriteBlock(favs, 'DMR', '') ?? '', /no valid target/);
  const many = Array.from({ length: 50 }, (_, i) => fav(i + 10, 'P25', String(i + 1)));
  assert.match(favoriteBlock(many, 'P25', '99') ?? '', /At most 50/);
  assert.equal(favoriteBlock(many, 'NXDN', '99'), null);
});

test('default favourite name', () => {
  assert.equal(defaultFavName({ tune: '91', targetName: 'World wide' }), 'World wide');
  assert.equal(defaultFavName({ tune: '91' }), '91');
  assert.equal(defaultFavName({ tune: '', targetName: undefined }), '');
  assert.equal(defaultFavName({ tune: '1', targetName: 'x'.repeat(60) }).length, 40);
});

test('pending mode switch: waiting, confirmed, unconfirmed after 5 s', () => {
  const p = { mode: 'P25' as const, since: 1000 };
  assert.equal(pendingOutcome(null, 'DMR', 0), 'none');
  assert.equal(pendingOutcome(p, 'DMR', 1000), 'waiting');
  assert.equal(pendingOutcome(p, 'DMR', 1000 + CONFIRM_MS - 1), 'waiting');
  assert.equal(pendingOutcome(p, 'DMR', 1000 + CONFIRM_MS), 'unconfirmed');
  assert.equal(pendingOutcome(p, 'P25', 1000 + CONFIRM_MS + 10), 'confirmed');
  assert.equal(pendingOutcome(p, undefined, 1500), 'waiting');
  assert.equal(shownMode(p, 'waiting', 'DMR'), 'P25');
  assert.equal(shownMode(p, 'unconfirmed', 'DMR'), 'DMR');
  assert.equal(shownMode(p, 'confirmed', 'P25'), 'P25');
  assert.equal(shownMode(null, 'none', undefined), '');
});

test('controls are locked while a command is pending, free on reply, confirmation or timeout', () => {
  const p = { mode: 'P25' as const, since: 1000 };
  assert.equal(controlsLocked(false, pendingOutcome(null, 'DMR', 0)), false);
  assert.equal(controlsLocked(true, 'none'), true); // request in flight
  assert.equal(controlsLocked(false, pendingOutcome(p, 'DMR', 1500)), true); // replied, bridge not showing the mode yet
  assert.equal(controlsLocked(false, pendingOutcome(p, 'P25', 1500)), false); // confirmed
  assert.equal(controlsLocked(false, pendingOutcome(p, 'DMR', 1000 + CONFIRM_MS)), false); // "Not confirmed"
  assert.equal(controlsLocked(true, 'confirmed'), true); // a later tune still in flight
});

test('the card is tinted only when the bridge node is linked to the local node', () => {
  const link = (linked: boolean) => ({ mode: 'ondemand' as const, localNode: '1998', linked, paused: false });
  assert.equal(bridgeLinked(snap({ link: link(true) })), true);
  assert.equal(bridgeLinked(snap({ link: link(false) })), false);
  // a talkgroup or reflector on its own does not count, nor does a missing tune
  assert.equal(bridgeLinked(snap({ link: link(false), bridge: { ...bridge, tune: '3100', mode: 'DMR' } })), false);
  assert.equal(bridgeLinked(snap({ link: link(true), bridge: { ...bridge, tune: '', mode: 'DMR' } })), true);
  assert.equal(bridgeLinked(snap({ link: link(true), bridgeNode: { present: false, node: '' } })), false);
  assert.equal(bridgeLinked(snap({ link: link(true), bridgeNode: { present: true, node: '' } })), false);
  assert.equal(bridgeLinked(null), false);
  assert.equal(bridgeLinked(undefined), false);
});
