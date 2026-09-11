import test from 'node:test';
import assert from 'node:assert/strict';
import {createTimingObserver, predictFiller, quantile, summarizeTimings, validTimingSample} from './fillerTiming.js';
export const sample = (ready = 5000) => ({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100,
  tts_chunk_ms: ready - 30, answer_ready_ms: ready, llm_ttft_ms: 870, tts_latency_ms: ready - 1140});
test('timing projection excludes payloads and distinguishes native and delivery clocks', () => {
  let time = 50;
  const o = createTimingObserver(() => time);
  for (const [name, local, native] of [['endpoint_commit', 50, 0], ['llm_requested', 70, 10], ['llm_first_token', 950, 880],
    ['tts_requested', 1150, 1100], ['tts_first_chunk', 5020, 4960]]) {
    time = local; o.trace({name, monotonic_ms: native, text: 'DO_NOT_RETAIN'});
  }
  time = 5050;
  const result = o.ready();
  assert.equal(result.answer_ready_ms, 5000);
  assert.equal(result.llm_ttft_ms, 870);
  assert.equal(result.tts_latency_ms, 3860);
  assert.equal(JSON.stringify(result).includes('DO_NOT_RETAIN'), false);
  assert.equal(o.ready(), null);
  assert.equal(validTimingSample({...result, text: 'x'}), false);
});
test('missing events are not substituted with zero or fabricated measurements', () => {
  const o = createTimingObserver(() => 100);
  assert.equal(o.ready(), null);
  o.trace({name: 'endpoint_commit'});
  assert.equal(o.ready(), null);
  assert.equal(summarizeTimings([]).llm_ttft_ms.p95, null);
  assert.equal(quantile([1, 9, 2], .95), 9);
});
test('paired timing calibration suppresses fast replies，small samples and wide tails', () => {
  const samples = Array.from({length: 100}, () => sample());
  const marks = {llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100};
  assert.deepEqual(predictFiller(samples, 100, {}, 700), {action: 'wait', delay_ms: 4050});
  assert.equal(predictFiller(samples, 4150, marks, 700).action, 'fire');
  assert.equal(predictFiller(samples.slice(0, 29), 4150, marks, 700).action, 'disable');
  assert.equal(predictFiller(samples, 5000, marks, 700).action, 'expire');
  const wide = [...samples.slice(0, 50), ...samples.slice(0, 50).map(() => sample(7000))];
  assert.equal(predictFiller(wide, 4150, marks, 700).action, 'wait');
  assert.notEqual(predictFiller(samples, 4150, {llm_request_ms: 20}, 700).action, 'fire');
});
