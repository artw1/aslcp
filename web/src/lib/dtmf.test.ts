import test from 'node:test';
import assert from 'node:assert/strict';
import { codeLabel, dtmfSummary, groupHref, itemHref, itemText, type Dtmf, type DtmfGroup, type DtmfItem } from './dtmf.ts';

const it = (code: string, action: string, desc?: string, line = 10): DtmfItem => ({ code, action, desc, file: 'rpt.conf', line, section: 'functions-main' });
const group = (kind: DtmfGroup['kind'], items: DtmfItem[], over: Partial<DtmfGroup> = {}): DtmfGroup => ({ kind, title: kind, items, ...over });

test('known codes show the meaning, unknown ones the raw line', () => {
  assert.deepEqual(itemText(it('70', 'ilink,5', 'System status')), { text: 'System status', known: true });
  assert.deepEqual(itemText(it('987', 'frob,1')), { text: '987 = frob,1', known: false });
});

test('code labels', () => {
  assert.equal(codeLabel('functions', '70'), '*70');
  assert.equal(codeLabel('macro', '1'), '*5 1');
  assert.equal(codeLabel('telemetry', 'ct1'), 'ct1');
});

test('links go to the file and line in Advanced', () => {
  assert.equal(itemHref({ file: 'custom/rpt/macros.conf', line: 4 }), '#/config?tab=advanced&file=custom%2Frpt%2Fmacros.conf&line=4');
  assert.equal(groupHref(group('functions', [], { file: 'rpt.conf', line: 300 })), '#/config?tab=advanced&file=rpt.conf&line=300');
  assert.equal(groupHref(group('functions', [])), '');
});

test('summary', () => {
  assert.equal(dtmfSummary(null), 'Loading…');
  const d: Dtmf = { node: '2001', groups: [group('functions', [it('1', 'ilink,1', 'Disconnect specified link'), it('9', 'zzz')]), group('macro', [it('1', '*81')]), group('telemetry', [it('ct1', '|t(1)')])] };
  assert.equal(dtmfSummary(d), '2 commands · 1 macro · 1 not recognised');
  assert.equal(dtmfSummary({ node: '2001', groups: [group('functions', [], { note: 'x' }), group('macro', [])] }), 'no commands found · no macros');
  assert.equal(dtmfSummary({ node: '2001', groups: [] }), 'No DTMF settings found');
});
