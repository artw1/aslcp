// Run with `npm test` (node's own test runner; no extra dependency). svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { findByValue, indexList, optionLabel, searchList } from './dvlist.ts';
import type { DVList, DVListItem } from './types';

const items: DVListItem[] = [
  { value: 'example-master.invalid', name: 'BM_0000_Example', detail: 'ID 2001', address: 'example-master.invalid', port: 62031 },
  { value: '192.0.2.11', name: 'XLX_000', address: '192.0.2.11', port: 62030 },
  { value: '00001', name: 'US-Example', detail: 'Example reflector', address: '198.51.100.21', port: 42000 },
  { value: '00002', name: 'XX-Europe', detail: 'Link', address: '203.0.113.22', port: 42500 },
];
const index = indexList(items);

test('search matches name, id, description and address', () => {
  assert.equal(searchList(index, 'europe')[0].value, '00002');
  assert.equal(searchList(index, '00001')[0].name, 'US-Example');
  assert.equal(searchList(index, 'reflector')[0].value, '00001');
  assert.equal(searchList(index, '203.0.113')[0].value, '00002');
  assert.equal(searchList(index, '198.51.100.21:42000').length, 1);
  assert.equal(searchList(index, 'example master').length, 1); // every word must match
  assert.equal(searchList(index, 'nothing here').length, 0);
});

test('an empty query lists the first entries and the limit holds', () => {
  assert.equal(searchList(index, '  ').length, 4);
  assert.equal(searchList(index, '', 2).length, 2);
});

test('an exact value ranks first', () => {
  const x = indexList([{ value: '10', name: 'P25 TG 10', address: '192.0.2.1', port: 1 }, { value: '1', name: 'P25 TG 1', address: '192.0.2.1', port: 1 }]);
  assert.equal(searchList(x, '1')[0].value, '1');
});

test('search stays fast with a few thousand entries', () => {
  const big: DVListItem[] = Array.from({ length: 5000 }, (_, i) => ({ value: String(10000 + i), name: `Reflector ${i}`, address: `192.0.2.${i % 250}`, port: 41000 }));
  const idx = indexList(big);
  const t = performance.now();
  for (let i = 0; i < 100; i++) searchList(idx, 'reflector 4');
  assert.ok(performance.now() - t < 1000);
});

test('labels read name, id, address:port', () => {
  assert.equal(optionLabel('ysf', items[2]), 'US-Example — 00001 — 198.51.100.21:42000');
  assert.equal(optionLabel('dmr', items[1]), 'XLX_000 — 192.0.2.11:62030');
});

test('M17 labels read the designator and address:port', () => {
  assert.equal(optionLabel('m17', { value: 'M17-XXX', name: 'M17-XXX', address: 'm17-xxx.example.net', port: 17000 }), 'M17-XXX — m17-xxx.example.net:17000');
});

test('findByValue only matches a listed value', () => {
  assert.equal(findByValue(items, ' 00002 ')?.name, 'XX-Europe');
  assert.equal(findByValue(items, 'my own value'), undefined);
  assert.equal(findByValue(items, ''), undefined);
});

test('the list reply type', () => {
  const l: DVList = { mode: 'dmr', items, etag: 'x' };
  assert.equal(l.items[0].port, 62031);
});
