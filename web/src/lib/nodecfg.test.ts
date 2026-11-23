import test from 'node:test';
import assert from 'node:assert/strict';
import { advancedLink, parseConfigQuery, provenance, valueLabel, formatMs, validateValue, initialDraft, draftChanges, duplexRequest, fieldActionRequest, errorParts, radioSummary, registrationSummary, echolinkSummary, presetPreview, changeLine, draftInvalid, validCallsign, customIdRecording, identityRequest, registrationProblem, registrationRequest, echolinkRequest, type NodeModel, type Field, type SchemaKey, type Preset, type SchemaDoc } from './nodecfg.ts';

const key = (o: Partial<SchemaKey>): SchemaKey => ({ id: 'rpt.conf:node:x', file: 'rpt.conf', scope: 'node', name: 'x', label: 'X', form: 'timers', type: 'string', desc: '', reload: 'assumed', ...o });
const field = (o: Partial<Field>): Field => ({ id: 'rpt.conf:node:x', key: 'x', value: '', set: false, source: { kind: 'unset' }, canSet: true, canReset: false, canTemplate: false, ...o });

test('advancedLink and parseConfigQuery round trip', () => {
  assert.equal(advancedLink('rpt.conf', 12), '/config?tab=advanced&file=rpt.conf&line=12');
  assert.equal(advancedLink(), '/config?tab=advanced');
  assert.deepEqual(parseConfigQuery('tab=advanced&file=custom/rpt/macros.conf&line=3'), { tab: 'advanced', file: 'custom/rpt/macros.conf', line: 3, history: false });
  assert.deepEqual(parseConfigQuery(''), { tab: 'overview', file: '', line: 0, history: false });
  assert.equal(parseConfigQuery('tab=advanced&file=../etc/passwd').file, '');
  assert.equal(parseConfigQuery('tab=bogus').tab, 'overview');
  assert.equal(parseConfigQuery('tab=advanced&view=history').history, true);
});

test('provenance wording', () => {
  assert.equal(provenance(field({ source: { kind: 'node' } })).label, 'set on this node');
  assert.equal(provenance(field({ source: { kind: 'template', section: 'node-main' } })).label, 'from template node-main');
  assert.equal(provenance(field({ source: { kind: 'section', file: 'echolink.conf' } })).label, 'set in echolink.conf');
  assert.equal(provenance(field({ source: { kind: 'default' } })).label, 'default');
  assert.equal(provenance(field({})).label, 'not set');
});

test('value labels and durations', () => {
  const duplex = key({ type: 'enum', values: ['0', '1'], labels: ['Half, no tones', 'Half with tones'] });
  assert.equal(valueLabel(duplex, '1'), 'Half with tones');
  assert.equal(valueLabel(key({ type: 'yesno' }), 'yes'), 'Yes');
  assert.equal(valueLabel(key({ type: 'flag' }), '0'), 'No');
  assert.equal(valueLabel(key({ type: 'int', unit: 'ms' }), '5000'), '5 s');
  assert.equal(formatMs(300000), '5 min');
  assert.equal(formatMs(1500), '1500 ms');
  assert.equal(formatMs(0), '0 s');
});

test('validateValue', () => {
  const n = key({ type: 'int', min: 0, max: 100, default: '5' });
  assert.equal(validateValue(n, '50'), '');
  assert.match(validateValue(n, '101'), /At most/);
  assert.match(validateValue(n, '-1'), /At least/);
  assert.match(validateValue(n, 'x'), /whole number/);
  assert.equal(validateValue(n, ''), '');
  assert.equal(validateValue(key({ type: 'string' }), ''), '');
  assert.match(validateValue(key({ type: 'string' }), '', true), /Required/);
  assert.equal(validateValue(key({ type: 'string', optional: true }), '', true), '');
  assert.equal(validateValue(key({ type: 'string', secret: true }), '', true), '');
  assert.match(validateValue(key({ type: 'string' }), 'a;b'), /semicolons/);
  assert.equal(validateValue(key({ type: 'enum', values: ['0', '1'] }), '2') !== '', true);
  assert.equal(validateValue(key({ type: 'flag' }), '1'), '');
  assert.equal(validateValue(key({ type: 'node' }), '2001'), '');
  assert.notEqual(validateValue(key({ type: 'node' }), '0'), '');
});

test('secrets are never pre-filled and empty means keep', () => {
  const pw = key({ id: 'echolink.conf:el0:pwd', name: 'pwd', secret: true });
  const fields = [field({ id: pw.id, key: 'pwd', value: '********', set: true, masked: true })];
  assert.equal(initialDraft(fields, [pw])[pw.id], '');
  assert.deepEqual(draftChanges(fields, [pw], { [pw.id]: '' }), []);
  assert.deepEqual(draftChanges(fields, [pw], { [pw.id]: 'example-pass' }), [{ id: pw.id, value: 'example-pass' }]);
});

test('draftChanges only sends differences', () => {
  const a = key({ id: 'a', type: 'int', default: '5000' }), b = key({ id: 'b', type: 'int', optional: true }), c = key({ id: 'c', type: 'int', default: '1' });
  const fields = [field({ id: 'a', value: '5000', source: { kind: 'default' } }), field({ id: 'b' }), field({ id: 'c', value: '7', set: true, source: { kind: 'node' } }), ];
  const d = initialDraft(fields, [a, b, c]);
  assert.deepEqual(d, { a: '5000', b: '', c: '7' });
  assert.deepEqual(draftChanges(fields, [a, b, c], d), []);
  assert.deepEqual(draftChanges(fields, [a, b, c], { ...d, a: '2000', b: '9' }), [{ id: 'a', value: '2000' }, { id: 'b', value: '9' }]);
  // clearing a set optional value removes it; clearing an unset one is nothing
  assert.deepEqual(draftChanges(fields, [a, b, c], { ...d, c: '' }), [{ id: 'c', value: '' }]);
  // keys the form cannot set are skipped
  assert.deepEqual(draftChanges([field({ id: 'a', canSet: false })], [a], { a: '1' }), []);
});

test('duplexRequest leaves untouched preset values to the preset', () => {
  const ks = [key({ id: 'duplex', type: 'enum', values: ['0', '1', '2'], default: '1' }), key({ id: 'hangtime', type: 'int', default: '5000' })];
  const fields = [field({ id: 'duplex', value: '1', source: { kind: 'default' } }), field({ id: 'hangtime', value: '5000', source: { kind: 'default' } })];
  const preset: Preset = { name: 'repeater', label: 'Repeater', desc: '', values: { duplex: '2', hangtime: '2000' } };
  const r1 = duplexRequest('2001', fields, ks, { duplex: '2', hangtime: '2000' }, preset);
  assert.deepEqual(r1, { node: '2001', mode: 'override', changes: [], preset: 'repeater' });
  const r2 = duplexRequest('2001', fields, ks, { duplex: '2', hangtime: '3000' }, preset);
  assert.deepEqual(r2.changes, [{ id: 'hangtime', value: '3000' }]);
  const r3 = duplexRequest('2001', fields, ks, { duplex: '2', hangtime: '5000' }, null);
  assert.deepEqual(r3, { node: '2001', mode: 'override', changes: [{ id: 'duplex', value: '2' }] });
});

test('fieldActionRequest', () => {
  assert.deepEqual(fieldActionRequest('1999', 'reset', 'id', 'zzz'), { node: '1999', mode: 'reset', changes: [{ id: 'id', value: '' }] });
  assert.deepEqual(fieldActionRequest('1999', 'template', 'id', ' 7 '), { node: '1999', mode: 'template', changes: [{ id: 'id', value: '7' }] });
});

test('errorParts', () => {
  assert.deepEqual(errorParts(undefined), {});
  assert.equal(errorParts({ refusal: { code: 'radio_unsupported', message: 'm', file: 'usbradio.conf' } }).refusal?.file, 'usbradio.conf');
  assert.equal(errorParts({ plan: { files: [], errors: [] } }).plan?.files.length, 0);
  assert.deepEqual(errorParts({ files: ['rpt.conf', 3] }).files, ['rpt.conf']);
  assert.deepEqual(errorParts({ refusal: 'x' }), {});
});

test('summaries', () => {
  assert.equal(radioSummary({ type: 'simpleusb', channel: 'SimpleUSB/1999', deviceStanza: true, supported: true }), 'SimpleUSB interface (SimpleUSB/1999)');
  assert.equal(radioSummary({ type: 'hub', channel: 'Local/pseudo', deviceStanza: false, supported: true }), 'No radio (hub)');
  assert.match(registrationSummary({ method: 'http', host: 'register.example.net', status: { state: 'registered' } }), /Web registration at register.example.net, registered/);
  assert.match(registrationSummary({ method: 'none' }), /Not registered/);
  assert.match(registrationSummary({ method: 'both' }), /use one/);
  assert.equal(echolinkSummary({ present: true, moduleState: 'explicit', linked: true, configured: true, enabled: true }), 'On');
  assert.match(echolinkSummary({ present: true, moduleState: 'noload', linked: true, configured: false, enabled: false }), /no EchoLink callsign/);
  assert.equal(echolinkSummary({ present: false, moduleState: 'unknown', linked: false, configured: false, enabled: false }), 'No echolink.conf');
});

test('presetPreview lists values and the hub radio change', () => {
  const doc: SchemaDoc = { keys: [key({ id: 'duplex', label: 'Duplex mode', type: 'enum', values: ['0', '1'], labels: ['Half, no tones', 'Half with tones'] })], forms: [], presets: [] };
  const rows = presetPreview({ name: 'hub', label: 'Hub', desc: '', values: { duplex: '0' }, hub: true }, doc, [field({ id: 'duplex', value: '1' })]);
  assert.deepEqual(rows.map((r) => [r.id, r.from, r.to, r.same]), [['duplex', 'Half with tones', 'Half, no tones', false], ['radio', '', 'No radio (hub)', false]]);
});

test('changeLine', () => {
  assert.equal(changeLine({ key: 'hangtime', file: 'rpt.conf', section: '1999', action: 'set', from: '5000', to: '2000' }), 'Change hangtime in rpt.conf [1999]: 5000 to 2000');
  assert.equal(changeLine({ key: 'register', file: 'iax.conf', section: 'general', action: 'remove', from: '1999:********@register.example.net' }), 'Remove register in iax.conf (was 1999:********@register.example.net)');
  assert.match(changeLine({ key: 'x', file: 'f', section: 's', action: 'unchanged' }), /already/);
});

const nodeOf = (o: Partial<NodeModel> = {}): NodeModel => ({ node: '2001', private: false, placeholder: false, callsign: 'N0CALL', radio: { type: 'simpleusb', channel: 'SimpleUSB/2001', deviceStanza: true, supported: true }, fields: [], other: [], registration: { method: 'none' }, echolink: { present: true, moduleState: 'noload', linked: false, configured: false, enabled: false, fields: [] }, warnings: [], refusals: [], inNodes: true, ...o });

test('validCallsign', () => {
  for (const c of ['N0CALL', 'n0call', 'N1CALL', 'N0CALL/P']) assert.equal(validCallsign(c), true, c);
  for (const c of ['', 'NOTSET', 'ABC', '12', 'N0CALL!']) assert.equal(validCallsign(c), false, c);
});

test('customIdRecording', () => {
  assert.equal(customIdRecording(field({ set: true, value: '|iN0CALL' })), false);
  assert.equal(customIdRecording(field({ set: true, value: 'myid' })), true);
  assert.equal(customIdRecording(field({ set: false, value: '|iNOTSET' })), false);
  assert.equal(customIdRecording(undefined), false);
});

test('identityRequest sends the callsign only when it changed', () => {
  const n = nodeOf();
  assert.deepEqual(identityRequest(n, [], {}, 'n0call'), { node: '2001', mode: 'override', changes: [] });
  assert.equal(identityRequest(n, [], {}, 'n0test').callsign, 'N0TEST');
  assert.equal(identityRequest(n, [], {}, ' ').callsign, undefined);
});

test('registration problems and requests', () => {
  const none = { method: 'none' as const };
  assert.match(registrationProblem(none, 'http', '', 'example-pass'), /server name/);
  assert.match(registrationProblem(none, 'http', 'register.example.net', ''), /password/);
  assert.equal(registrationProblem(none, 'http', 'register.example.net', 'example-pass'), '');
  assert.match(registrationProblem(none, 'iax', 'register.example.net', 'a b'), /Up to 64/);
  const http = { method: 'http' as const, host: 'register.example.net', http: { file: 'f', line: 1, node: '2001', host: 'register.example.net', hasPassword: true } };
  assert.equal(registrationProblem(http, 'http', 'register.example.net', ''), '');
  assert.equal(registrationProblem(http, 'none', '', ''), '');
  const n = nodeOf({ registration: http });
  assert.equal(registrationRequest(n, 'http', 'register.example.net', ''), null);
  assert.deepEqual(registrationRequest(n, 'iax', 'register.example.net', ''), { node: '2001', mode: 'override', registration: { method: 'iax', host: 'register.example.net' } });
  assert.deepEqual(registrationRequest(n, 'none', '', ''), { node: '2001', mode: 'override', registration: { method: 'none' } });
  assert.equal((registrationRequest(n, 'http', 'register.example.net', 'example-pass') as any).registration.password, 'example-pass');
  assert.equal(registrationRequest(nodeOf(), 'none', '', ''), null);
});

test('echolinkRequest links and loads the driver when enabled', () => {
  const k = key({ id: 'echolink.conf:el0:call', name: 'call', form: 'echolink', optional: true });
  const n = nodeOf({ echolink: { present: true, moduleState: 'noload', linked: false, configured: false, enabled: false, fields: [field({ id: k.id, key: 'call', source: { kind: 'unset' } })] } });
  assert.equal(echolinkRequest(n, [k], { [k.id]: '' }, false), null);
  assert.deepEqual(echolinkRequest(n, [k], { [k.id]: 'N0CALL-L' }, true), { node: '2001', mode: 'override', changes: [{ id: k.id, value: 'N0CALL-L' }], link: true });
  const linked = nodeOf({ echolink: { ...n.echolink, linked: true } });
  assert.deepEqual(echolinkRequest(linked, [k], { [k.id]: '' }, true), { node: '2001', mode: 'override', changes: [], modules: ['chan_echolink.so'] });
  const ready = nodeOf({ echolink: { ...n.echolink, linked: true, moduleState: 'explicit' } });
  assert.equal(echolinkRequest(ready, [k], { [k.id]: '' }, true), null);
});

test('draftInvalid', () => {
  const a = key({ id: 'a', type: 'int', min: 0, max: 10 });
  const fields = [field({ id: 'a', value: '3', set: true })];
  assert.equal(draftInvalid(fields, [a], { a: '4' }), false);
  assert.equal(draftInvalid(fields, [a], { a: '40' }), true);
  assert.equal(draftInvalid(fields, [a], { a: '' }), true);
  assert.equal(draftInvalid([field({ id: 'a' })], [a], { a: '' }), false);
});

test('status reporting: state, summary and request', async () => {
  const m = await import('./nodecfg.ts');
  const f = (id: string, value: string, set: boolean, kind: 'node' | 'template' | 'unset' = 'node'): import('./nodecfg.ts').Field =>
    ({ id, key: id.split(':')[2], value, set, source: { kind }, canSet: true, canReset: set && kind === 'node', canTemplate: true });
  const off = [f(m.STATUS_IDS[0], '', false, 'unset'), f(m.STATUS_IDS[1], '60', false, 'unset')];
  assert.deepEqual(m.statusState(off), { on: false, url: m.STATPOST_DEFAULT_URL, time: '60', fromTemplate: false });
  assert.match(m.statusSummary(off), /^Off/);
  // turning on writes the stock address; the default interval is not written
  assert.deepEqual(m.statusRequest('2001', off, true, m.STATPOST_DEFAULT_URL, '60'), { node: '2001', mode: 'override', changes: [{ id: m.STATUS_IDS[0], value: m.STATPOST_DEFAULT_URL }] });
  assert.equal(m.statusRequest('2001', off, false, '', '60'), null);
  const on = [f(m.STATUS_IDS[0], m.STATPOST_DEFAULT_URL, true), f(m.STATUS_IDS[1], '120', true)];
  assert.match(m.statusSummary(on), /^On: reports to the AllStarLink statistics page every 120 s\.$/);
  assert.equal(m.statusRequest('2001', on, true, m.STATPOST_DEFAULT_URL, '120'), null);
  assert.deepEqual(m.statusRequest('2001', on, false, '', '120'), { node: '2001', mode: 'reset', changes: [{ id: m.STATUS_IDS[0], value: '' }, { id: m.STATUS_IDS[1], value: '' }] });
  // a value from a template cannot be switched off from the node
  const tpl = [f(m.STATUS_IDS[0], 'https://stats.example.net/p', true, 'template'), f(m.STATUS_IDS[1], '60', false, 'unset')];
  assert.equal(m.statusState(tpl).fromTemplate, true);
  assert.match(m.statusSummary(tpl), /set for all nodes/);
  assert.equal(m.statusRequest('2001', tpl, false, '', '60'), null);
});

test('statpost url check mirrors the server', async () => {
  const { statpostError } = await import('./nodecfg.ts');
  for (const ok of ['http://stats.allstarlink.org/uhandler', 'https://stats.example.net', 'https://stats.example.net:8080/p?x=1']) assert.equal(statpostError(ok), '', ok);
  for (const bad of ['stats.example.net', 'ftp://stats.example.net/x', 'http://', 'http://a b', 'http://user:x@stats.example.net/p', 'http://stats.example.net/$(x)']) assert.notEqual(statpostError(bad), '', bad);
});
