import { test } from 'node:test';
import assert from 'node:assert/strict';
import { movedPath, MOVED, isSettingsPath, sectionFromPath, settingsSection, clampLines, auditSummary } from './sysnav.ts';

test('movedPath redirects every old route', () => {
  assert.equal(movedPath('/updates'), '/settings/updates');
  assert.equal(movedPath('/config'), '/settings/asterisk');
  assert.equal(movedPath('/tune'), '/settings/audio-levels');
  assert.equal(movedPath('/digital'), '/settings/dv-engine');
  assert.equal(movedPath('/backup'), '/settings/backup');
  assert.equal(movedPath('/system'), '/settings/overview');
  assert.equal(movedPath('/system/'), '/settings/overview');
});

test('movedPath maps each old System section to the same id under Settings', () => {
  for (const id of ['overview', 'updates', 'network', 'wifi', 'asl-services', 'web-port', 'terminal', 'logs', 'diagnostics', 'audit']) {
    assert.equal(movedPath('/system/' + id), '/settings/' + id);
  }
  assert.equal(movedPath('/system/nope'), '/settings/overview');
});

test('movedPath leaves current routes alone', () => {
  for (const p of ['/', '/settings', '/settings/updates', '/dv', '/allstar', '/schedules', '/systematic']) assert.equal(movedPath(p), null);
});

test('every redirect target is a settings route', () => {
  for (const to of Object.values(MOVED)) assert.equal(isSettingsPath(to), true);
});

test('isSettingsPath', () => {
  assert.equal(isSettingsPath('/settings'), true);
  assert.equal(isSettingsPath('/settings/logs'), true);
  assert.equal(isSettingsPath('/settingsx'), false);
  assert.equal(isSettingsPath('/system'), false);
});

test('sectionFromPath falls back for unknown sections', () => {
  const ids = ['overview', 'updates', 'logs'];
  assert.equal(sectionFromPath('/settings/updates', ids, 'overview'), 'updates');
  assert.equal(sectionFromPath('/settings', ids, 'overview'), 'overview');
  assert.equal(sectionFromPath('/settings/nope', ids, 'overview'), 'overview');
  assert.equal(sectionFromPath('/settings/updates/x', ids, 'overview'), 'overview');
});

test('settingsSection: an admin lands on the fallback, a non-admin on Dashboard for unknown pages', () => {
  const admin = ['overview', 'updates', 'account', 'about'];
  const other = ['account', 'about'];
  assert.equal(settingsSection('/settings', admin, 'overview', true), 'overview');
  assert.equal(settingsSection('/settings/updates', admin, 'overview', true), 'updates');
  assert.equal(settingsSection('/settings/nope', admin, 'overview', true), 'overview');
  assert.equal(settingsSection('/settings', other, 'account', false), 'account');
  assert.equal(settingsSection('/settings/about', other, 'account', false), 'about');
  assert.equal(settingsSection('/settings/updates', other, 'account', false), null);
  assert.equal(settingsSection('/settings/nope', other, 'account', false), null);
  assert.equal(settingsSection('/dv', admin, 'overview', true), null);
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
