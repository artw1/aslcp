// Run with `npm test` (node's own test runner; no extra dependency). svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { CONFIRM_MS, PANEL_MODES, allOk, bridgeLinked, bridgeNodeName, bridgeRx, bridgeRxTarget, controlsLocked, DEFAULT_NODE_NAME, defaultFavName, favoriteBlock, favoritesFor, freqToHz, heardLine, hzToMHz, isDMRFamily, isPrivateTune, modeButtonLabel, modeLabel, nodeLabel, nodeLinkClass, pendingOutcome, rxCaller, reflectorSilent, serviceDots, shownMode, stateLine, stateTitle, stateTone, validFavName, validCoord, validInfoText, validInfoUrl, validNodeName, validTune, withPrivate } from './dvpanel.ts';
import type { DVFavorite, DVSnapshot } from './types';

const bridge = { mode: 'YSF', tune: '192.0.2.10:42000', targetName: 'Example room', slot: 2, muted: false, source: 'abinfo' as const, updated: '' };
const snap = (over: Partial<DVSnapshot> = {}): DVSnapshot => ({
  installed: true, running: true, configured: true, bridge, modes: [],
  services: [{ unit: 'analog_bridge', active: true }, { unit: 'mmdvm_bridge', active: true }], bridgeNode: { present: true, node: '1999' }, link: { mode: 'ondemand', localNode: '', linked: false, paused: false, exclusive: true }, heard: [], ...over,
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
  const link = (linked: boolean) => ({ mode: 'ondemand' as const, localNode: '1998', linked, paused: false, exclusive: true });
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

test('state tone and title: warn for a down unit or unknown state, ok when tuned', () => {
  assert.equal(stateTone(snap()), snap().bridge.tune ? 'ok' : '');
  assert.equal(stateTone(snap({ bridge: { ...bridge, tune: '' } })), '');
  const down = snap({ services: [{ unit: 'analog_bridge', active: true }, { unit: 'ysfgateway', active: false }] });
  assert.equal(stateTone(down), 'warn');
  assert.equal(stateTitle(down), 'ysfgateway: not running');
  assert.equal(stateTone(snap({ bridge: { ...bridge, source: 'none', mode: '' } })), 'warn');
  assert.equal(stateTone(null), 'warn');
  assert.equal(stateTitle(snap()), 'All services running');
});

test('bridge node name and label', () => {
  assert.equal(bridgeNodeName(snap()), DEFAULT_NODE_NAME);
  assert.equal(bridgeNodeName(snap({ bridgeNode: { present: true, node: '1999', name: ' Example room ' } })), 'Example room');
  assert.equal(bridgeNodeName(null), DEFAULT_NODE_NAME);
  const named = snap({ bridgeNode: { present: true, node: '1999', name: 'Example room' } });
  assert.equal(nodeLabel(named, '1999'), 'Example room');
  assert.equal(nodeLabel(named, '2001'), '');
  assert.equal(nodeLabel(snap({ bridgeNode: { present: false } }), '1999'), '');
  assert.equal(validNodeName('  '), '');
  assert.equal(validNodeName(' Shack bridge '), 'Shack bridge');
  assert.equal(validNodeName('x'.repeat(41)), null);
  assert.equal(validNodeName('bad\u0007name'), null);
  assert.equal(nodeLinkClass('connect'), 'btn-ok');
  assert.equal(nodeLinkClass('disconnect'), 'btn-bad');
});

test('RX line shows the mode and target while the bridge node is keyed', () => {
  const link = (keyed: boolean, node = '1999') => ({ node, kind: 'allstar', info: null, ip: '', direction: 'OUT', connected: 0, state: 'ESTABLISHED', mode: 'T', keyed, sinceKey: null, sinceUnkey: null }) as never;
  const local = (links: never[], rxKeyed = false) => ({ node: '2001', rxKeyed, links });
  const dmr = snap({ bridge: { ...bridge, mode: 'DMR', tune: '3100', targetName: undefined } });
  assert.equal(bridgeRx([local([link(true)])], dmr), 'RX: DMR - Other TG 3100');
  assert.equal(bridgeRx([local([link(true)])], snap()), 'RX: YSF Example room');
  assert.equal(bridgeRx([local([link(true)])], snap({ bridge: { ...bridge, targetName: undefined } })), 'RX: YSF 192.0.2.10:42000');
  assert.equal(bridgeRx([local([link(true)])], snap({ bridge: { ...bridge, mode: 'P25', tune: '10100', targetName: undefined } })), 'RX: P25 TG 10100');
  assert.equal(bridgeRx([local([link(true)])], snap({ bridge: { ...bridge, tune: '', targetName: undefined } })), 'RX: YSF');
  // the bridge node monitored on its own
  assert.equal(bridgeRx([{ node: '1999', rxKeyed: true, links: [] }], dmr), 'RX: DMR - Other TG 3100');
  // not keyed, another node keyed, no bridge node, unknown bridge
  assert.equal(bridgeRx([local([link(false)])], dmr), null);
  assert.equal(bridgeRx([local([link(true, '2006')])], dmr), null);
  assert.equal(bridgeRx([local([link(true)])], snap({ bridgeNode: { present: false } })), null);
  assert.equal(bridgeRx([local([link(true)])], snap({ bridge: { ...bridge, source: 'none', mode: '' } })), null);
  assert.equal(bridgeRx([], dmr), null);
  assert.equal(bridgeRx([local([link(true)])], null), null);
});

test('bridge RX target', () => {
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'DMR', tune: '3100', targetName: undefined } })), 'DMR - Other TG 3100');
  assert.equal(bridgeRxTarget(snap()), 'YSF Example room');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, targetName: undefined } })), 'YSF 192.0.2.10:42000');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'P25', tune: '10100', targetName: undefined } })), 'P25 TG 10100');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'DMR', tune: '', targetName: undefined } })), 'DMR - Other');
  assert.equal(bridgeRxTarget(snap({ bridgeNode: { present: false } })), null);
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, source: 'none', mode: '' } })), null);
  assert.equal(bridgeRxTarget(null), null);
});

test('DMR private call: a trailing # is accepted for DMR only', () => {
  assert.ok(validTune('DMR', '9990#'));
  assert.ok(validTune('DMR', '91'));
  assert.ok(!validTune('DMR', '#'));
  assert.ok(!validTune('DMR', '9990##'));
  assert.ok(!validTune('DMR', '#9990'));
  assert.ok(!validTune('P25', '9990#'));
  assert.ok(!validTune('YSF', '9990#'));
  assert.ok(isPrivateTune('DMR', '9990#'));
  assert.ok(!isPrivateTune('DMR', '9990'));
  assert.ok(!isPrivateTune('P25', '9990#'));
  assert.equal(withPrivate('DMR', ' 9990 ', true), '9990#');
  assert.equal(withPrivate('DMR', '9990#', true), '9990#');
  assert.equal(withPrivate('DMR', '9990#', false), '9990');
  assert.equal(withPrivate('P25', '10100', true), '10100');
});

test('a private call is a favourite with its #, apart from the plain talkgroup', () => {
  const favs: DVFavorite[] = [{ id: 1, mode: 'DMR', value: '9990', name: 'x', sort: 0 }];
  assert.equal(favoriteBlock(favs, 'DMR', '9990#'), null);
  assert.equal(favoriteBlock(favs, 'DMR', '9990'), 'Already a favourite');
});

test('bridge RX target of a private call, and the caller', () => {
  const priv = snap({ bridge: { ...bridge, mode: 'DMR', tune: '9990', targetName: undefined, private: true } });
  assert.equal(bridgeRxTarget(priv), 'DMR - Other private call');
  assert.equal(rxCaller(snap()), null);
  assert.equal(rxCaller(snap({ caller: { call: 'N0CALL', dmrId: '1234567', dst: '91', slot: 2 } })), 'N0CALL');
  assert.equal(rxCaller(snap({ caller: { call: '  ' } })), null);
  assert.equal(rxCaller(null), null);
  // no callsign known: the DMR ID; the Parrot's reply is a private call
  assert.equal(rxCaller(snap({ caller: { dmrId: '1234567' } })), '1234567');
  assert.equal(rxCaller(snap({ caller: { dmrId: '9990', private: true } })), 'Parrot (private call)');
  assert.equal(rxCaller(snap({ caller: { dmrId: '1234567', private: true } })), 'Private call');
});

test('last heard line', () => {
  assert.equal(heardLine({ call: 'N0CALL', dst: '91', at: '2026-11-18 10:00:01' }), 'N0CALL · TG 91 · 10:00:01');
  assert.equal(heardLine({ call: 'N0CALL' }), 'N0CALL');
  assert.equal(heardLine({ dmrId: '1234567', dst: '3100' }), '1234567 · TG 3100');
  assert.equal(heardLine({ dmrId: '9990', private: true, dst: '1234567' }), 'Parrot (private call) · TG 1234567');
});

test('[Info] frequency: MHz or Hz to Hz', () => {
  assert.equal(freqToHz('434'), 434000000);
  assert.equal(freqToHz('434.0125'), 434012500);
  assert.equal(freqToHz('434,0125'), 434012500);
  assert.equal(freqToHz('434000000'), 434000000);
  assert.equal(freqToHz(' 222.34 '), 222340000);
  assert.equal(freqToHz('29.9'), null);
  assert.equal(freqToHz('3001'), null);
  assert.equal(freqToHz('4000'), null);
  assert.equal(freqToHz('abc'), null);
  assert.equal(freqToHz(''), null);
  assert.equal(freqToHz('1e3'), null);
  assert.equal(freqToHz('-434'), null);
  assert.equal(hzToMHz(434000000), '434');
  assert.equal(hzToMHz(434012500), '434.0125');
  assert.equal(hzToMHz(0), '');
});

test('[Info] text, URL and coordinates', () => {
  assert.ok(validInfoText('Example Town', 20));
  assert.ok(!validInfoText('', 20));
  assert.ok(!validInfoText('x'.repeat(20), 19));
  assert.ok(!validInfoText(' lead', 20));
  assert.ok(!validInfoText('trail ', 20));
  assert.ok(!validInfoText('a;b', 20));
  assert.ok(!validInfoText('a#b', 20));
  assert.ok(!validInfoText('a=b', 20));
  assert.ok(!validInfoText('a"b', 20));
  assert.ok(!validInfoText('caf\u00e9', 20));
  assert.ok(validInfoUrl(''));
  assert.ok(validInfoUrl('https://example.org/a?b=c'));
  assert.ok(!validInfoUrl('ftp://example.org'));
  assert.ok(!validInfoUrl('javascript:alert(1)'));
  assert.ok(!validInfoUrl('https://example.org/a b'));
  assert.ok(!validInfoUrl('https://example.org/#x'));
  assert.ok(validCoord('51.5', 90));
  assert.ok(validCoord('-0.12', 180));
  assert.ok(!validCoord('91', 90));
  assert.ok(!validCoord('1.1234567', 90));
  assert.ok(!validCoord('', 90));
  assert.ok(!validCoord('1e2', 90));
});

test('the DMR networks: all DMR on the air, each its own mode with its own label', () => {
  assert.ok(isDMRFamily('BM') && isDMRFamily('TGIF') && isDMRFamily('DMR') && !isDMRFamily('YSF') && !isDMRFamily(''));
  assert.deepEqual(PANEL_MODES, ['LOOP', 'BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN']);
  assert.equal(modeLabel('BM'), 'BrandMeister');
  assert.equal(modeLabel('TGIF'), 'TGIF');
  assert.equal(modeLabel('DMR'), 'DMR - Other');
  assert.equal(modeLabel('P25'), 'P25');
  assert.equal(modeButtonLabel({ id: 'TGIF', label: 'TGIF' }), 'TGIF');
  assert.equal(modeButtonLabel({ id: 'DMR' }), 'DMR - Other');
  assert.equal(modeButtonLabel({ id: 'BM' }), 'BrandMeister');
  assert.ok(validTune('BM', '91') && !validTune('BM', '123456789'));
  assert.ok(validTune('TGIF', '31665') && validTune('TGIF', '9990#') && !validTune('TGIF', 'abc'));
  assert.equal(withPrivate('BM', '1234567', true), '1234567#');
  assert.equal(withPrivate('TGIF', '9990', true), '9990#');
  assert.ok(isPrivateTune('BM', '1234567#'));
  assert.equal(stateLine({ mode: 'BM', tune: '91', source: 'abinfo' }), 'Mode BrandMeister, tuned to 91');
  assert.equal(stateLine({ mode: 'TGIF', tune: '31665', source: 'abinfo' }), 'Mode TGIF, tuned to 31665');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'BM', tune: '91', targetName: undefined } })), 'BrandMeister TG 91');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'TGIF', tune: '31665', targetName: undefined } })), 'TGIF TG 31665');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'DMR', tune: '1', targetName: undefined } })), 'DMR - Other TG 1');
  assert.equal(bridgeRxTarget(snap({ bridge: { ...bridge, mode: 'BM', tune: '1234567', targetName: undefined, private: true } })), 'BrandMeister private call');
});

test('reflector not answering: warning state line, tone and title; nothing tuned never warns', () => {
  const mode = (silent?: boolean) => [{ id: 'YSF' as const, enabled: true, available: true, unlink: 'disconnect', login: 'pending' as const, silent }];
  const quiet = snap({ modes: mode(true) });
  assert.equal(reflectorSilent(quiet), true);
  assert.equal(stateLine(bridge, true), 'Mode YSF, tuned to Example room (192.0.2.10:42000), Reflector not answering');
  assert.equal(stateLine(bridge), 'Mode YSF, tuned to Example room (192.0.2.10:42000)');
  assert.equal(stateTone(quiet), 'warn');
  assert.equal(stateTitle(quiet), 'The reflector has sent nothing back for a while');
  // answering again: back to ok
  assert.equal(reflectorSilent(snap({ modes: mode(false) })), false);
  assert.equal(stateTone(snap({ modes: mode(undefined) })), 'ok');
  // nothing tuned, no snapshot, another mode: no warning
  assert.equal(reflectorSilent(snap({ modes: mode(true), bridge: { ...bridge, tune: '' } })), false);
  assert.equal(stateLine({ ...bridge, tune: '' }, reflectorSilent(snap({ modes: mode(true), bridge: { ...bridge, tune: '' } }))), 'Mode YSF');
  assert.equal(stateTone(snap({ modes: mode(true), bridge: { ...bridge, tune: '' } })), '');
  assert.equal(reflectorSilent(snap({ modes: mode(true), bridge: { ...bridge, mode: 'P25', tune: '1997' } })), false);
  assert.equal(reflectorSilent(null), false);
});
