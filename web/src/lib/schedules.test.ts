import test from 'node:test';
import assert from 'node:assert/strict';
import { validateSchedule, findConflicts, conflictMessage, evLabel, emptyInput, fmtIn } from './schedules.ts';
import type { Schedule, ScheduleInput } from './types';

const modes = ['allstar', 'bm', 'ysf'];
const inp = (o: Partial<ScheduleInput> = {}): ScheduleInput => ({ ...emptyInput('bm'), target: '91', days: [2], start: '19:00', end: '20:30', ...o });
const sch = (id: number, o: Partial<ScheduleInput> = {}): Schedule => ({ ...inp(o), id, lastRun: null, lastResult: '' });

test('validation', () => {
  assert.equal(validateSchedule(inp(), modes), '');
  assert.notEqual(validateSchedule(inp({ mode: 'nope' }), modes), '');
  assert.notEqual(validateSchedule(inp({ target: ' ' }), modes), '');
  assert.notEqual(validateSchedule(inp({ days: [] }), modes), '');
  assert.notEqual(validateSchedule(inp({ end: '19:00' }), modes), '');
  assert.equal(validateSchedule(inp({ action: 'disconnect', target: '', end: '' }), modes), '');
  assert.equal(validateSchedule(inp({ days: [], date: '2026-12-01' }), modes), '');
});

test('conflicts ignore the mode', () => {
  const other = sch(1, { mode: 'ysf', target: 'X', days: [2], start: '20:00', end: '21:00' });
  assert.deepEqual(findConflicts(inp(), [other]).map((c) => c.id), [1]);
  assert.deepEqual(findConflicts(inp({ start: '20:30', end: '21:30' }), [other]).map((c) => c.id), [1]);
  assert.deepEqual(findConflicts(inp({ start: '21:00', end: '22:00' }), [other]), []);
});

test('conflicts skip itself, disabled, disconnects and other days', () => {
  assert.deepEqual(findConflicts(inp(), [sch(1)], 1), []);
  assert.deepEqual(findConflicts(inp(), [sch(1, { enabled: false })]), []);
  assert.deepEqual(findConflicts(inp(), [sch(1, { action: 'disconnect', end: '' })]), []);
  assert.deepEqual(findConflicts(inp(), [sch(1, { days: [3] })]), []);
  assert.deepEqual(findConflicts(inp({ enabled: false }), [sch(1)]), []);
});

test('windows across midnight and one-off dates', () => {
  const late = sch(1, { days: [2], start: '23:00', end: '01:00' });
  assert.equal(findConflicts(inp({ days: [3], start: '00:30', end: '02:00' }), [late]).length, 1);
  // 2026-12-01 is a Tuesday
  assert.equal(findConflicts(inp({ days: [], date: '2026-12-01' }), [sch(2)]).length, 1);
  assert.equal(findConflicts(inp({ days: [], date: '2026-12-02' }), [sch(2)]).length, 0);
});

test('messages and labels', () => {
  assert.equal(conflictMessage([sch(1, { name: 'Tuesday net' })]), 'overlaps "Tuesday net" (Tue 19:00–20:30)');
  assert.equal(conflictMessage([sch(1), sch(2, { name: 'B' })]), 'overlaps #1 (Tue 19:00–20:30) and "B" (Tue 19:00–20:30)');
  assert.equal(evLabel({ action: 'connect', mode: 'bm', target: '91' }), 'Connect BrandMeister 91');
  assert.equal(evLabel({ action: 'disconnect', mode: 'echolink', target: '' }), 'Disconnect EchoLink');
  assert.equal(fmtIn(3 * 3600 + 120), 'in 3 h 2 min');
});
