import test from 'node:test';
import assert from 'node:assert/strict';
import {createFillerBackchannel} from './fillerController.js';

function rig() {
  let time = 0, valid = true;
  const sources = [], trace = [], timers = new Map();
  const context = {state: 'running', destination: {}, closed: false, close() { this.closed = true; },
    createGain: () => ({gain: {value: 1}, connect() {}, disconnect() {}}),
    createBufferSource() {
      const source = {connect() {}, disconnect() {}, start() { this.started = true; }, stop() { this.stopped = true; }};
      sources.push(source); return source;
    }};
  const ack = createFillerBackchannel({context, buffer: {duration: .8}, now: () => time, isCurrent: () => valid,
    onTrace: t => trace.push(t), timers: {setTimeout(fn) { const key = Symbol(); timers.set(key, fn); return key; }, clearTimeout(key) { timers.delete(key); }}});
  return {ack, context, sources, trace, invalidate() { valid = false; },
    advance(t) { time = t; for (const [id, fn] of [...timers]) { timers.delete(id); fn(); } }};
}
test('interruption acknowledgement starts synchronously once and closes its output after natural end', () => {
  const r = rig(); r.ack.play(0); r.ack.play(0);
  assert.equal(r.sources.length, 1); assert.equal(r.sources[0].started, true);
  assert.equal(r.trace[0].latency_ms, 0);
  r.advance(800); r.sources[0].onended(); r.ack.play();
  assert.equal(r.context.closed, true); assert.equal(r.sources.length, 1);
  assert.deepEqual(r.trace.map(t => t.kind), ['backchannel_started', 'backchannel_ended']);
  assert.ok(r.trace.every(t => Object.keys(t).sort().join() === 'kind,latency_ms'));
});
test('unused acknowledgement does not close the answer context', () => {
  const r = rig(); r.ack.stop(); r.ack.play();
  assert.equal(r.context.closed, false); assert.equal(r.sources.length, 0);
});
test('explicit stop，session invalidation and watchdog silence the acknowledgement with no retry', () => {
  for (const reason of ['stop', 'session', 'watchdog']) {
    const r = rig(); r.ack.play(); const late = r.sources[0].onended;
    if (reason === 'stop') r.ack.stop();
    if (reason === 'session') { r.invalidate(); r.advance(25); }
    if (reason === 'watchdog') r.advance(901);
    assert.equal(r.sources[0].stopped, true); assert.equal(r.context.closed, true);
    late(); r.ack.play(); assert.equal(r.sources.length, 1);
    assert.equal(r.trace.at(-1).kind, reason === 'watchdog' ? 'backchannel_watchdog' : 'backchannel_stopped');
  }
});
