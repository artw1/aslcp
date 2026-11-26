import test from 'node:test';
import assert from 'node:assert/strict';
import { barPercent, targetBand, isHot, dbText, warningText, durationText, reportSummary } from './levels.ts';

test('barPercent maps -60..0 dBFS to 0..100', () => {
  assert.equal(barPercent(0), 100);
  assert.equal(barPercent(-30), 50);
  assert.equal(barPercent(-60), 0);
  assert.equal(barPercent(-90), 0);
  assert.equal(barPercent(6), 100);
  assert.equal(barPercent(NaN), 0);
  assert.equal(barPercent(-Infinity), 0);
});

test('targetBand covers -20 to -6', () => {
  const b = targetBand();
  assert.ok(Math.abs(b.left - (40 / 60) * 100) < 1e-9);
  assert.ok(Math.abs(b.width - (14 / 60) * 100) < 1e-9);
});

test('isHot is near full scale only', () => {
  assert.equal(isHot(-1), true);
  assert.equal(isHot(-3), true);
  assert.equal(isHot(-10), false);
  assert.equal(isHot(NaN), false);
});

test('dbText and durationText', () => {
  assert.equal(dbText(-12.34), '-12.3 dBFS');
  assert.equal(dbText(-90), '–');
  assert.equal(dbText(undefined), '–');
  assert.equal(durationText(3400), '3.4 s');
  assert.equal(durationText(-5), '0.0 s');
});

test('warningText explains each warning', () => {
  assert.match(warningText('clipping'), /Lower the receive volume/);
  assert.match(warningText('too quiet'), /Raise the receive volume/);
  assert.equal(warningText('other'), 'other');
});

test('reportSummary', () => {
  assert.equal(reportSummary(undefined), '');
  const ok = { at: '2026-01-01T00:00:00Z', frames: 100, durationMs: 2000, peakDb: -10, rmsDb: -20, voicedFrames: 90, clipped: 0 };
  assert.equal(reportSummary(ok), 'Peak -10.0 dBFS, RMS -20.0 dBFS, 2.0 s. Levels look good.');
  assert.match(reportSummary({ ...ok, warnings: ['clipping'] }), /clipped/);
});
