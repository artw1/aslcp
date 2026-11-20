import { test } from 'node:test';
import assert from 'node:assert/strict';
import { movedPath, isSystemPath, sectionFromPath, clampLines, auditSummary } from './sysnav.ts';

test('movedPath redirects the old Updates route only', () => {
  assert.equal(movedPath('/updates'), '/system/updates');
  assert.equal(movedPath('/system/updates'), null);
  assert.equal(movedPath('/'), null);
});

test('isSystemPath', () => {
  assert.equal(isSystemPath('/system'), true);
  assert.equal(isSystemPath('/system/logs'), true);
  assert.equal(isSystemPath('/systematic'), false);
});

test('sectionFromPath falls back for unknown sections', () => {
  const ids = ['overview', 'updates', 'logs'];
  assert.equal(sectionFromPath('/system/updates', ids, 'overview'), 'updates');
  assert.equal(sectionFromPath('/system', ids, 'overview'), 'overview');
  assert.equal(sectionFromPath('/system/nope', ids, 'overview'), 'overview');
  assert.equal(sectionFromPath('/system/updates/x', ids, 'overview'), 'overview');
});

test('clampLines', () => {
  assert.equal(clampLines(5), 50);
  assert.equal(clampLines(99999), 1000);
  assert.equal(clampLines(200.7), 200);
  assert.equal(clampLines(NaN), 200);
});

test('auditSummary', () => {
  assert.equal(auditSummary(''), '');
  assert.equal(auditSummary('{"from":"node2001","to":"node2002"}'), 'from: node2001, to: node2002');
  assert.equal(auditSummary('{}'), '');
  assert.equal(auditSummary('plain'), 'plain');
});
