// Run with `npm test`. The audio settings and the level meter of the digital modes card.
import test from 'node:test';
import assert from 'node:assert/strict';
import { agcLine, audioError, dbText, dirShown, meterColor, meterPct, summaryLine, verdictLabel, verdictTone } from './dvaudio.ts';
import type { DVAudioSummary } from './types';

const sum = (o: Partial<DVAudioSummary> = {}): DVAudioSummary => ({ speechDb: -28, outSpeechDb: -22, peakDb: -4, outPeakDb: -3, clipped: 0, durationMs: 4000, verdict: 'low', agc: false, at: '', ...o });

test('meter bar', () => {
  assert.equal(meterPct(-90), 0);
  assert.equal(meterPct(0), 100);
  assert.equal(meterPct(-45), 50);
  assert.equal(meterPct(-200), 0);
  assert.equal(meterColor(-20), 'bg-ok');
  assert.equal(meterColor(-5), 'bg-warn');
  assert.equal(meterColor(-0.2), 'bg-danger');
  assert.equal(meterColor(-20, true), 'bg-danger');
});

test('verdict', () => {
  assert.equal(verdictLabel(sum()), 'Low');
  assert.equal(verdictTone(sum()), 'warn');
  assert.equal(verdictLabel(sum({ verdict: 'ok' })), 'OK');
  assert.equal(verdictTone(sum({ verdict: 'ok' })), 'ok');
  assert.equal(verdictTone(sum({ verdict: 'high' })), 'warn');
  assert.equal(verdictLabel(sum({ verdict: 'high', clipped: 2 })), 'Clipping');
  assert.equal(verdictTone(sum({ verdict: 'high', clipped: 2 })), 'danger');
});

test('summary lines', () => {
  assert.equal(summaryLine(sum()), 'Last over: -28 dBFS speech, peak -4');
  assert.equal(summaryLine(sum({ clipped: 1 }), 'call'), 'Last call: -28 dBFS speech, peak -4, 1 clipped frame');
  assert.equal(summaryLine(sum({ speechDb: -90, peakDb: -90 })), 'Last over: silent speech, peak silent');
  assert.equal(agcLine(sum()), '');
  assert.equal(agcLine(sum({ agc: true })), '→ -22 after AGC');
  assert.equal(dbText(-27.6), '-28');
});

test('direction shown', () => {
  assert.equal(dirShown(undefined), false);
  const d = { agc: false, active: false, peakDb: -90, rmsDb: -90, outPeakDb: -90, outRmsDb: -90 };
  assert.equal(dirShown(d), false);
  assert.equal(dirShown({ ...d, active: true }), true);
  assert.equal(dirShown({ ...d, last: sum() }), true);
});

test('settings form', () => {
  const ok = { agcTargetDb: '-22', agcMaxGainDb: '18', gainRxDb: '0', gainTxDb: '-20' };
  assert.equal(audioError(ok), '');
  assert.match(audioError({ ...ok, agcTargetDb: '-5' }), /AGC target level/);
  assert.match(audioError({ ...ok, agcTargetDb: '-41' }), /-40 to -6/);
  assert.match(audioError({ ...ok, agcMaxGainDb: '31' }), /AGC maximum gain/);
  assert.match(audioError({ ...ok, agcMaxGainDb: '' }), /AGC maximum gain/);
  assert.match(audioError({ ...ok, gainRxDb: '21' }), /Node audio gain/);
  assert.match(audioError({ ...ok, gainTxDb: '1.5' }), /Network audio gain/);
});
