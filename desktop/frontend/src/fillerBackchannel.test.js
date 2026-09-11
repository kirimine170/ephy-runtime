import test from 'node:test';
import assert from 'node:assert/strict';
import {createFillerBackchannel} from './fillerController.js';

function rig(duration = .8) {
  let time = 0, valid = true;
  const sources = [], trace = [], timers = new Map();
  const context = {state: 'running', destination: {}, closed: false, close() { this.closed = true; },
    createGain: () => ({gain: {value: 1}, connect() {}, disconnect() {}}),
    createBufferSource() {
      const source = {connect() {}, disconnect() {}, start() { this.started = true; }, stop() { this.stopped = true; }};
      sources.push(source); return source;
    }};
  const ack = createFillerBackchannel({context, buffer: {duration}, now: () => time, isCurrent: () => valid,
    onTrace: t => trace.push(t), timers: {setTimeout(fn) { const key = Symbol(); timers.set(key, fn); return key; }, clearTimeout(key) { timers.delete(key); }}});
  return {ack, context, sources, trace, timers, invalidate() { valid = false; },
    advance(t) { time = t; for (const [id, fn] of [...timers]) { timers.delete(id); fn(); } }};
}
test('interruption acknowledgement waits 300 ms once and closes its output after natural end', () => {
  const r = rig(); r.ack.play(0); r.ack.play(0);
  assert.equal(r.sources.length, 0);
  r.advance(299); assert.equal(r.sources.length, 0);
  r.advance(300);
  assert.equal(r.sources.length, 1); assert.equal(r.sources[0].started, true);
  assert.equal(r.trace[0].latency_ms, 300);
  r.advance(1100); r.sources[0].onended(); r.ack.play();
  assert.equal(r.context.closed, true); assert.equal(r.sources.length, 1);
  assert.deepEqual(r.trace.map(t => t.kind), ['backchannel_started', 'backchannel_ended']);
  assert.ok(r.trace.every(t => Object.keys(t).sort().join() === 'kind,latency_ms'));
});

test('a conversational clip keeps its actual duration after the same 300 ms pause', () => {
  const r = rig(2.4); r.ack.play(); r.advance(300);
  r.advance(1800); assert.equal(r.context.closed, false);
  r.advance(2700); r.sources[0].onended();
  assert.equal(r.trace.at(-1).kind, 'backchannel_ended');
  assert.equal(r.trace.at(-1).latency_ms, 2400);
  const stopped = rig(2.4); stopped.ack.play(); stopped.advance(300); stopped.advance(1800); stopped.ack.stop();
  assert.equal(stopped.sources[0].stopped, true); assert.equal(stopped.context.closed, true);
});
test('unused acknowledgement does not close the answer context', () => {
  const r = rig(); r.ack.stop(); r.ack.play();
  assert.equal(r.context.closed, false); assert.equal(r.sources.length, 0);
});
test('explicit stop，session invalidation and watchdog silence the acknowledgement with no retry', () => {
  for (const reason of ['stop', 'session', 'watchdog']) {
    const r = rig(); r.ack.play(); r.advance(300); const late = r.sources[0].onended;
    if (reason === 'stop') r.ack.stop();
    if (reason === 'session') { r.invalidate(); r.advance(325); }
    if (reason === 'watchdog') r.advance(1201);
    assert.equal(r.sources[0].stopped, true); assert.equal(r.context.closed, true);
    late(); r.ack.play(); assert.equal(r.sources.length, 1);
    assert.equal(r.trace.at(-1).kind, reason === 'watchdog' ? 'backchannel_watchdog' : 'backchannel_stopped');
  }
});

test('cancel，identity loss and an expired pause discard the pending voice and close its context', () => {
  for (const reason of ['cancel', 'session', 'expired']) {
    const r = rig(); r.ack.play(); const late = [...r.timers.values()];
    if (reason === 'cancel') r.ack.stop();
    if (reason === 'session') { r.invalidate(); r.advance(25); }
    if (reason === 'expired') r.advance(401);
    assert.equal(r.context.closed, true); assert.equal(r.sources.length, 0);
    r.advance(500); late.forEach(fn => fn()); r.ack.play();
    assert.equal(r.sources.length, 0);
    assert.equal(r.trace.at(-1).kind, 'backchannel_stopped');
  }
});
