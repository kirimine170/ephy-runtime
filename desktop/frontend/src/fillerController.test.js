import test from 'node:test';
import assert from 'node:assert/strict';
import {createFillerController, createFillerPlayer} from './fillerController.js';

function rig(ledger = {used: false}) {
  let time = 0, current = true, end;
  const callbacks = new Map(), trace = [], events = [];
  const samples = Array.from({length: 100}, () => ({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100,
    tts_chunk_ms: 4970, answer_ready_ms: 5000, llm_ttft_ms: 880, tts_latency_ms: 3870}));
  const controller = createFillerController({samples, durationMS: 700, ledger, now: () => time, isCurrent: () => current,
    play(done) { events.push('play'); end = done; }, stop() { events.push('stop'); }, onTrace: e => trace.push(e),
    timers: {setTimeout(fn, ms) { const id = Symbol(); callbacks.set(id, {fn, at: time + ms}); return id; }, clearTimeout(id) { callbacks.delete(id); }}});
  return {controller, ledger, callbacks, trace, events, invalidate() { current = false; }, end() { end?.(); },
    advance(t) { time = t; for (const [key, c] of [...callbacks]) if (c.at <= t) { callbacks.delete(key); c.fn(); } },
    arm() { controller.arm(0); controller.progress({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100}); }};
}
test('fast body arrival cancels timers without playing and cannot be rearmed', () => {
  const r = rig(); r.arm(); const queued = [...r.callbacks.values()];
  r.advance(1000); r.controller.answerReady(); r.advance(5000); queued.forEach(c => c.fn()); r.controller.arm(0);
  assert.deepEqual(r.events, []); assert.equal(r.controller.state, 'CLOSED');
});
test('answer preempts filler synchronously without waiting for an ACK or natural ending', () => {
  const r = rig(); r.arm(); r.advance(4150); assert.deepEqual(r.events, ['play']);
  r.controller.answerReady(); assert.deepEqual(r.events, ['play', 'stop']);
  r.end(); r.advance(6000); assert.equal(r.controller.state, 'CLOSED');
  assert.equal(r.ledger.used, true);
});
test('cancel，barge-in，old callbacks and continuation never create a second filler', () => {
  for (const reason of ['cancel', 'barge_in', 'invalidated']) {
    const r = rig(); r.arm(); r.advance(4150); r.controller.cancel(reason); r.end(); r.advance(6000);
    assert.deepEqual(r.events, ['play', 'stop']);
    const next = rig(r.ledger); next.arm(); next.advance(4150); assert.deepEqual(next.events, []);
    assert.ok(r.trace.every(t => Object.keys(t).sort().join() === 'kind,latency_ms'));
  }
  const stale = rig(); stale.arm(); stale.invalidate(); stale.advance(4150); assert.deepEqual(stale.events, []);
});
test('natural end does not repeat；gap overrun is visible and watchdog halts stuck source', () => {
  const r = rig(); r.arm(); r.advance(4150); r.advance(4850); r.end();
  r.advance(5300); r.controller.answerReady();
  assert.equal(r.trace.find(t => t.kind === 'filler_gap_exceeded').latency_ms, 450);
  assert.deepEqual(r.events, ['play']);
  const stuck = rig(); stuck.arm(); stuck.advance(4150); stuck.advance(5000);
  assert.deepEqual(stuck.events, ['play', 'stop']);
});
test('player silences gain before stopping source and disconnects failed source', () => {
  const actions = []; let source;
  const gain = {gain: {set value(v) { actions.push(`gain:${v}`); }}, connect() {}, disconnect() { actions.push('disconnectGain'); }};
  const p = createFillerPlayer({state: 'running', destination: {}, createGain: () => gain,
    createBufferSource() { source = {connect() {}, start() { actions.push('play'); }, stop() { actions.push('stop'); }, disconnect() { actions.push('disconnectSource'); }}; return source; }}, {});
  p.play(() => {}); p.stop();
  assert.deepEqual(actions, ['play', 'gain:0', 'disconnectGain', 'stop', 'disconnectSource']);
  assert.equal(source.onended, null);
});
