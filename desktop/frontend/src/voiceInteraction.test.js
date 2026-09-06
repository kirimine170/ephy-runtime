import test from 'node:test';
import assert from 'node:assert/strict';
import {encodePCM16Wav, mountVoiceInteraction, voiceStatusText} from './voiceInteraction.js';

const tick = () => new Promise((resolve) => setImmediate(resolve));
const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
};
const snapshot = (id, state = 'RECORDING', extra = {}) => ({
  operation_id: `op-${id}`, session_id: 'session', turn_id: `turn-${id}`, trace_id: `trace-${id}`, state, ...extra,
});
const wav = () => Buffer.from(encodePCM16Wav([new Float32Array([0, 0.5, -0.5])], 16000)).toString('base64');

function button() {
  const handlers = new Map();
  return {
    disabled: false, textContent: '', hidden: false, attributes: {},
    setAttribute(key, value) { this.attributes[key] = value; },
    addEventListener(name, handler) { handlers.set(name, handler); },
    removeEventListener(name) { handlers.delete(name); },
    click() { if (!this.disabled) handlers.get('click')?.(); },
  };
}

function stream() {
  const tracks = [{stopped: false}, {stopped: false}].map((track) => ({
    ...track, onended: null,
    stop() { this.stopped = true; this.onended?.(); },
  }));
  return {tracks, getTracks: () => tracks};
}

class FakeContext {
  constructor() {
    this.sampleRate = 16000;
    this.destination = {};
    this.sources = [];
    this.closed = false;
    this.decode = async () => ({duration: 0.1});
  }
  node() {
    return {disconnected: false, connect() {}, disconnect() { this.disconnected = true; }};
  }
  resume() { return Promise.resolve(); }
  close() { this.closed = true; return Promise.resolve(); }
  createMediaStreamSource() { this.input = this.node(); return this.input; }
  createScriptProcessor() { this.processor = this.node(); return this.processor; }
  createGain() { return {...this.node(), gain: {value: 1}}; }
  decodeAudioData(bytes) { return this.decode(bytes); }
  createBufferSource() {
    const source = {
      ...this.node(), started: false, stopped: false, onended: null,
      start() { this.started = true; },
      stop() { this.stopped = true; this.onended?.(); },
      end() { this.onended?.(); },
    };
    this.sources.push(source);
    return source;
  }
  capture(samples = [0, 0.5, -0.5]) {
    this.processor.onaudioprocess?.({inputBuffer: {getChannelData: () => new Float32Array(samples)}});
  }
}

function harness(options = {}) {
  let listener;
  let identity = 0;
  const nodes = Object.fromEntries(['voice-record', 'voice-cancel', 'voice-status', 'voice-fallback'].map((id) => [id, button()]));
  const calls = {start: [], readiness: [], commit: [], cancel: [], playback: [], fail: [], transcript: [], token: [], output: [], complete: [], incomplete: [], failure: [], canceled: [], busy: [], fallback: []};
  const contexts = [];
  const streams = [];
  const timeouts = new Map();
  const bridge = {
    async GetInteractionASRReadiness() { calls.readiness.push(true); return {state: 'ready', can_start: true}; },
    async StartInteraction(request) { calls.start.push(request); calls.request = request; return snapshot(++identity); },
    async CommitInteraction(...args) { calls.commit.push(args); },
    async CancelInteraction(op) {
      calls.cancel.push(op);
      return {...snapshot(op.slice(3), 'CANCELED')};
    },
    async InteractionPlayback(...args) { calls.playback.push(args); },
    async FailInteraction(...args) { calls.fail.push(args); },
    ...options.bridge,
  };
  const controller = mountVoiceInteraction({
    root: {querySelector: (selector) => nodes[selector.slice(1)]},
    bridge,
    subscribe(handler) { listener = handler; return () => { listener = null; }; },
    getRequest: () => ({session_id: 'session', chat: {messages: [{role: 'user', content: 'previous'}]}}),
    mediaDevices: {async getUserMedia() { const result = stream(); streams.push(result); return result; }},
    createAudioContext() { const context = new FakeContext(); contexts.push(context); return context; },
    timers: {
      setTimeout(callback, milliseconds) { const id = Symbol(); timeouts.set(id, {callback, milliseconds}); return id; },
      clearTimeout(id) { timeouts.delete(id); },
    },
    onTranscript: (...args) => calls.transcript.push(args),
    onToken: (...args) => calls.token.push(args),
    onOutput: (...args) => calls.output.push(args),
    onComplete: (...args) => calls.complete.push(args),
    onIncomplete: (...args) => calls.incomplete.push(args),
    onFailure: (...args) => calls.failure.push(args),
    onCancel: (...args) => calls.canceled.push(args),
    onBusy: (...args) => calls.busy.push(args),
    onFallback: (...args) => calls.fallback.push(args),
    ...Object.fromEntries(Object.entries(options).filter(([key]) => key !== 'bridge')),
  });
  return {
    controller, bridge, calls, contexts, streams, timeouts, nodes,
    emit(event) { listener?.(event); },
    event(id, kind, extra = {}) { listener?.({...snapshot(id), kind, ...extra}); },
    state(id, state, extra = {}) { listener?.({...snapshot(id), kind: 'state', snapshot: snapshot(id, state, extra)}); },
    audio(id, sequence) { listener?.({...snapshot(id), kind: 'audio', sequence, audio_base64: wav()}); },
  };
}

test('PCM16 encoding writes mono WAV header，clips samples and rejects empty or oversized input', () => {
  const data = encodePCM16Wav([new Float32Array([-2, -1, -0.5, 0, 0.5, 1, 2, NaN])], 16000);
  const view = new DataView(data);
  assert.equal(Buffer.from(data).toString('ascii', 0, 4), 'RIFF');
  assert.equal(Buffer.from(data).toString('ascii', 8, 12), 'WAVE');
  assert.equal(view.getUint32(4, true), data.byteLength - 8);
  assert.equal(view.getUint16(20, true), 1);
  assert.equal(view.getUint16(22, true), 1);
  assert.equal(view.getUint32(24, true), 16000);
  assert.equal(view.getUint32(28, true), 32000);
  assert.equal(view.getUint16(34, true), 16);
  assert.deepEqual(Array.from({length: 8}, (_, i) => view.getInt16(44 + 2 * i, true)), [-32768, -32768, -16384, 0, 16384, 32767, 32767, 0]);
  assert.throws(() => encodePCM16Wav([], 16000));
  assert.throws(() => encodePCM16Wav([new Float32Array(8000 * 60 + 1)], 8000));
  assert.throws(() => encodePCM16Wav([new Float32Array(8 * 1024 * 1024 / 2)], 192000));
  assert.throws(() => encodePCM16Wav([new Float32Array(1)], 0));
});

test('manual stop captures once，stops every track，clears callback/timer and commits only WAV', async () => {
  const h = harness();
  assert.equal(await h.controller.start(), true);
  const context = h.contexts[0];
  context.capture();
  assert.equal(await h.controller.stop(), true);
  assert.equal(await h.controller.stop(), false);
  assert.equal(h.calls.commit.length, 1);
  assert.equal(h.calls.commit[0][0], 'op-1');
  const bytes = Buffer.from(h.calls.commit[0][1], 'base64');
  assert.equal(bytes.toString('ascii', 0, 4), 'RIFF');
  assert.equal(bytes.length, 50);
  assert.ok(h.streams[0].tracks.every((track) => track.stopped));
  assert.equal(context.processor.onaudioprocess, null);
  assert.equal(context.processor.disconnected, true);
  assert.equal(h.timeouts.size, 0);
  assert.deepEqual(h.calls.request.chat.messages, [{role: 'user', content: 'previous'}]);
  await h.controller.cancel();
});

test('96 kHz capture is downsampled to ASR-compatible 48 kHz across chunk boundaries', () => {
  const bytes = encodePCM16Wav([
    new Float32Array([-1]), new Float32Array([1, 0.25]), new Float32Array([0.75]),
  ], 96000);
  const view = new DataView(bytes);
  assert.equal(view.getUint32(24, true), 48000);
  assert.equal(view.getUint32(28, true), 96000);
  assert.equal(view.getUint32(40, true), 4);
  assert.equal(bytes.byteLength, 48);
  assert.equal(view.getInt16(44, true), 0);
  assert.equal(view.getInt16(46, true), 16384);
});

test('fractional downsampling preserves duration and a constant signal', () => {
  const bytes = encodePCM16Wav([new Float32Array(88200).fill(0.5)], 88200);
  const view = new DataView(bytes);
  assert.equal(view.getUint32(24, true), 48000);
  assert.equal(view.getUint32(40, true), 48000 * 2);
  for (let offset = 44; offset < bytes.byteLength; offset += 2) {
    assert.equal(view.getInt16(offset, true), 16384);
  }
});

test('higher actual AudioContext rate produces compatible WAV on capture commit', async () => {
  const context = new FakeContext();
  context.sampleRate = 96000;
  const h = harness({createAudioContext: () => context});
  await h.controller.start();
  context.capture([0, 0.5, -0.5, 0]);
  await h.controller.stop();
  const bytes = Buffer.from(h.calls.commit[0][1], 'base64');
  assert.equal(bytes.readUInt32LE(24), 48000);
  assert.equal(bytes.readUInt32LE(40), 4);
  assert.ok(h.streams[0].tracks.every((track) => track.stopped));
  await h.controller.cancel();
});

test('default audio context requests a native ASR-compatible 48 kHz sample rate', async (t) => {
  const original = globalThis.AudioContext;
  let options;
  globalThis.AudioContext = class extends FakeContext {
    constructor(request) { super(); options = request; }
  };
  t.after(() => {
    if (original === undefined) delete globalThis.AudioContext;
    else globalThis.AudioContext = original;
  });
  const h = harness({createAudioContext: undefined});
  await h.controller.start();
  assert.deepEqual(options, {sampleRate: 48000});
  await h.controller.cancel();
});

test('cancel during permission prompt stops a late stream without attaching it', async () => {
  const permission = deferred();
  const h = harness({mediaDevices: {getUserMedia: () => permission.promise}});
  const starting = h.controller.start();
  await tick();
  assert.match(h.nodes['voice-status'].textContent, /許可/);
  await h.controller.cancel();
  const late = stream();
  permission.resolve(late);
  assert.equal(await starting, false);
  assert.ok(late.tracks.every((track) => track.stopped));
  assert.equal(h.contexts[0].processor, undefined);
  assert.equal(h.controller.lastSnapshot.state, 'CANCELED');
  assert.equal(h.calls.commit.length, 0);
  assert.deepEqual(h.calls.busy, [[true], [false]]);
});

test('double cancel while Start is pending waits for identity and cancels once', async () => {
  const start = deferred();
  const h = harness({bridge: {StartInteraction: () => start.promise}});
  const starting = h.controller.start();
  await tick(); // Readiness has passed，and backend Start now owns the pending identity．
  const cancelA = h.controller.cancel();
  const cancelB = h.controller.cancel();
  assert.equal(cancelA, cancelB);
  assert.equal(h.contexts[0].closed, true);
  assert.equal(h.controller.lastSnapshot.state, 'CANCELING');
  start.resolve(snapshot(1));
  await Promise.all([starting, cancelA, cancelB]);
  assert.deepEqual(h.calls.cancel, ['op-1']);
  assert.equal(h.streams.length, 0);
  assert.equal(h.calls.canceled.length, 1);
});

test('late decode after cancel cannot construct or play a source', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const decoding = deferred();
  h.contexts[0].decode = () => decoding.promise;
  h.audio(1, 1);
  await tick();
  await h.controller.cancel();
  decoding.resolve({duration: 1});
  await tick();
  assert.equal(h.contexts[0].sources.length, 0);
  assert.equal(h.calls.playback.length, 0);
});

test('cancel synchronously stops current playback and discards queued and late audio', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  h.audio(1, 1);
  h.audio(1, 2);
  await tick();
  const source = h.contexts[0].sources[0];
  let canceledAfterStop = false;
  h.bridge.CancelInteraction = async () => {
    canceledAfterStop = source.stopped;
    return snapshot(1, 'CANCELED');
  };
  await h.controller.cancel();
  h.audio(1, 3);
  source.end();
  await tick();
  assert.equal(canceledAfterStop, true);
  assert.equal(h.contexts[0].sources.length, 1);
  assert.deepEqual(h.calls.playback, [['op-1', 1, 'started']]);
});

test('two turns have distinct sources and ignore the old turn after new playback starts', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  h.audio(1, 1);
  await tick();
  const first = h.contexts[0].sources[0];
  await h.controller.cancel();
  await h.controller.adopt(snapshot(2, 'SYNTHESIZING'));
  h.audio(1, 2);
  h.event(1, 'token', {text: 'stale'});
  h.state(1, 'COMPLETED');
  h.audio(2, 1);
  await tick();
  const second = h.contexts[1].sources[0];
  assert.notEqual(first, second);
  assert.equal(first.stopped, true);
  assert.equal(second.started, true);
  assert.equal(h.calls.token.length, 0);
  assert.equal(h.calls.complete.length, 0);
  assert.equal(h.controller.lastSnapshot.operation_id, 'op-2');
  await h.controller.cancel();
});

test('audio queue plays sequentially，acknowledges natural completion and suppresses duplicates', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  h.audio(1, 1);
  h.audio(1, 2);
  h.audio(1, 2);
  await tick();
  assert.equal(h.contexts[0].sources.length, 1);
  h.contexts[0].sources[0].end();
  await tick();
  assert.equal(h.contexts[0].sources.length, 2);
  h.contexts[0].sources[1].end();
  await tick();
  assert.deepEqual(h.calls.playback, [
    ['op-1', 1, 'started'], ['op-1', 1, 'stopped'],
    ['op-1', 2, 'started'], ['op-1', 2, 'stopped'],
  ]);
  h.state(1, 'COMPLETED', {transcript: 'hello', response_plan: {text: 'reply'}});
  h.state(1, 'COMPLETED', {transcript: 'hello'});
  h.audio(1, 3);
  assert.equal(h.calls.complete.length, 1);
  assert.equal(h.calls.transcript.length, 1);
  assert.equal(h.controller.isActive(), false);
  assert.equal(h.contexts[0].closed, true);
});

test('recording timeout commits at 60 seconds and releases microphone resources', async () => {
  const h = harness();
  await h.controller.start();
  h.contexts[0].capture();
  const timer = [...h.timeouts.values()][0];
  assert.equal(timer.milliseconds, 60_000);
  timer.callback();
  await tick();
  assert.equal(h.calls.commit.length, 1);
  assert.ok(h.streams[0].tracks.every((track) => track.stopped));
  assert.equal(h.timeouts.size, 0);
  await h.controller.cancel();
});

test('next chunk waits for the previous playback stop acknowledgement', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const stopped = deferred();
  h.bridge.InteractionPlayback = async (...args) => {
    h.calls.playback.push(args);
    if (args[2] === 'stopped') await stopped.promise;
  };
  h.audio(1, 1);
  h.audio(1, 2);
  await tick();
  h.contexts[0].sources[0].end();
  await tick();
  h.audio(1, 3);
  await tick();
  assert.equal(h.contexts[0].sources.length, 1);
  stopped.resolve();
  await tick();
  assert.equal(h.contexts[0].sources.length, 2);
  await h.controller.cancel();
});

test('sample count bounds recording even if the timer has not fired', async () => {
  const h = harness();
  await h.controller.start();
  h.contexts[0].capture(new Float32Array(16000 * 61));
  await tick();
  assert.equal(h.calls.commit.length, 1);
  assert.equal(Buffer.from(h.calls.commit[0][1], 'base64').length, 44 + 16000 * 60 * 2);
  assert.ok(h.streams[0].tracks.every((track) => track.stopped));
  await h.controller.cancel();
});

test('recording permission failure uses a stable code，fixed text and restores text chat availability', async () => {
  const h = harness({mediaDevices: {getUserMedia: async () => { throw Object.assign(new Error('secret provider diagnostic'), {name: 'NotAllowedError'}); }}});
  assert.equal(await h.controller.start(), false);
  assert.deepEqual(h.calls.fail, [['op-1', 'microphone_permission_denied']]);
  assert.equal(h.calls.failure.length, 1);
  assert.equal(h.controller.isActive(), false);
  assert.equal(h.nodes['voice-record'].disabled, false);
  assert.equal(h.nodes['voice-cancel'].disabled, true);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /secret/);
  assert.deepEqual(h.calls.busy, [[true], [false]]);
});

test('failed turn keeps transcript for an explicit text fallback and never injects diagnostics', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'THINKING'));
  h.event(1, 'transcript', {text: '認識した内容'});
  h.state(1, 'FAILED', {transcript: '認識した内容', error_code: '<img src=x onerror=secret()>'});
  assert.equal(h.nodes['voice-fallback'].hidden, false);
  h.nodes['voice-fallback'].click();
  assert.equal(h.calls.fallback[0][0].transcript, '認識した内容');
  assert.equal(h.calls.transcript.length, 1);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /img|secret/);
  h.event(1, 'token', {text: 'late'});
  h.state(1, 'FAILED');
  assert.equal(h.calls.failure.length, 1);
  assert.equal(h.calls.token.length, 0);
});

test('buttons expose action labels，recording toggle and polite live status', async () => {
  const h = harness();
  const record = h.nodes['voice-record'];
  assert.equal(record.attributes['aria-label'], '音声入力を開始');
  assert.equal(record.attributes['aria-pressed'], 'false');
  assert.equal(h.nodes['voice-status'].attributes.role, 'status');
  assert.equal(h.nodes['voice-status'].attributes['aria-live'], 'polite');
  record.click();
  await tick();
  assert.equal(record.attributes['aria-label'], '録音を停止して送信');
  assert.equal(record.attributes['aria-pressed'], 'true');
  assert.equal(h.nodes['voice-cancel'].disabled, false);
  h.contexts[0].capture();
  record.click();
  await tick();
  assert.equal(record.disabled, true);
  h.nodes['voice-cancel'].click();
  await tick();
  assert.equal(record.disabled, false);
  assert.equal(record.attributes['aria-pressed'], 'false');
});

test('early replay events are buffered and only matching operation identity is adopted', async () => {
  const h = harness();
  const response = deferred();
  const adopting = h.controller.adopt(response.promise);
  h.event(99, 'transcript', {text: 'wrong'});
  h.audio(99, 1);
  h.event(1, 'transcript', {text: 'right'});
  h.state(1, 'SYNTHESIZING', {transcript: 'right', response_plan: {text: 'reply'}});
  h.audio(1, 1);
  response.resolve(snapshot(1));
  await adopting;
  await tick();
  assert.deepEqual(h.calls.transcript.map((call) => call[1]), ['right']);
  assert.equal(h.contexts[0].sources.length, 1);
  assert.equal(h.controller.lastSnapshot.state, 'SYNTHESIZING');
  await h.controller.cancel();
});

test('decoder failure fails the sequence once and restores text chat', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  h.contexts[0].decode = async () => { throw new Error('private audio provider path'); };
  h.audio(1, 1);
  await tick();
  assert.equal(h.calls.failure.length, 1);
  assert.deepEqual(h.calls.playback, [['op-1', 1, 'failed']]);
  assert.equal(h.controller.isActive(), false);
  assert.equal(h.contexts[0].sources.length, 0);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /private/);
});

test('playback queue accepts all 64 backend chunks during paused decode and rejects chunk 65', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const pending = deferred();
  h.contexts[0].decode = () => pending.promise;
  for (let sequence = 1; sequence <= 33; sequence += 1) h.audio(1, sequence);
  assert.equal(h.controller.lastSnapshot.state, 'SYNTHESIZING');
  for (let sequence = 34; sequence <= 64; sequence += 1) h.audio(1, sequence);
  assert.equal(h.controller.lastSnapshot.state, 'SYNTHESIZING');
  assert.equal(h.calls.failure.length, 0);
  h.audio(1, 65);
  await tick();
  assert.equal(h.controller.lastSnapshot.state, 'FAILED');
  assert.equal(h.calls.failure.length, 1);
  assert.deepEqual(h.calls.playback, [['op-1', 65, 'failed']]);
  pending.resolve({duration: 1});
  await tick();
  assert.equal(h.contexts[0].sources.length, 0);
});

test('playback queue counts a source awaiting its start acknowledgement only once', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const started = deferred();
  h.bridge.InteractionPlayback = async () => started.promise;
  h.audio(1, 1);
  await tick();
  for (let sequence = 2; sequence <= 64; sequence += 1) h.audio(1, sequence);
  assert.equal(h.calls.failure.length, 0);
  assert.equal(h.contexts[0].sources.length, 1);
  await h.controller.cancel();
  started.resolve();
  await tick();
});

test('all 64 queued WAV chunks drain in order after a paused decode resumes', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const pending = deferred();
  h.contexts[0].decode = () => pending.promise;
  for (let sequence = 1; sequence <= 64; sequence += 1) h.audio(1, sequence);
  pending.resolve({duration: 0.1});
  await tick();
  for (let sequence = 1; sequence <= 64; sequence += 1) {
    assert.equal(h.contexts[0].sources.length, sequence);
    h.contexts[0].sources[sequence - 1].end();
    await tick();
  }
  assert.equal(h.calls.playback.length, 128);
  assert.deepEqual(h.calls.playback.at(-1), ['op-1', 64, 'stopped']);
  assert.equal(h.calls.failure.length, 0);
  h.state(1, 'COMPLETED');
  assert.equal(h.calls.complete.length, 1);
});

test('playback accepts 16 MiB of valid WAV chunks and rejects bytes beyond the backend limit', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  const pending = deferred();
  h.contexts[0].decode = () => pending.promise;
  const chunk = Buffer.from(encodePCM16Wav([new Float32Array((2 * 1024 * 1024 - 44) / 2)], 48000)).toString('base64');
  for (let sequence = 1; sequence <= 8; sequence += 1) {
    h.event(1, 'audio', {sequence, audio_base64: chunk});
  }
  assert.equal(h.calls.failure.length, 0);
  assert.equal(h.controller.isActive(), true);
  h.audio(1, 9);
  await tick();
  assert.equal(h.calls.failure.length, 1);
  assert.equal(h.controller.lastSnapshot.error_code, 'playback_failed');
  pending.resolve({duration: 1});
  await tick();
  assert.equal(h.contexts[0].sources.length, 0);
});

test('dispose releases resources，unsubscribes events and prevents new recording', async () => {
  const h = harness();
  await h.controller.start();
  await h.controller.dispose();
  assert.ok(h.streams[0].tracks.every((track) => track.stopped));
  assert.equal(h.contexts[0].closed, true);
  h.state(1, 'COMPLETED');
  h.nodes['voice-record'].click();
  assert.equal(await h.controller.start(), false);
  assert.equal(h.calls.complete.length, 0);
});

test('all lifecycle states have fixed status messages with a text fallback on failure', () => {
  for (const state of ['IDLE', 'RECORDING', 'TRANSCRIBING', 'THINKING', 'SYNTHESIZING', 'PLAYING', 'CANCELING', 'CANCELED', 'COMPLETED']) {
    assert.ok(voiceStatusText({state}));
  }
  assert.match(voiceStatusText({state: 'FAILED', error_code: 'unknown private data'}), /テキスト入力/);
  assert.doesNotMatch(voiceStatusText({state: 'FAILED', error_code: 'unknown private data'}), /private/);
});

test('INCOMPLETE releases microphone and playback，preserves fixed status and text fallback without completion', async () => {
  const h = harness();
  await h.controller.start();
  h.state(1, 'INCOMPLETE', {transcript: '四季を説明して', response_plan: {text: '春には花が咲きます．'}, generation: {finish_reason: 'length', complete: false}});
  assert.equal(h.controller.isActive(), false);
  assert.equal(h.calls.complete.length, 0);
  assert.equal(h.calls.incomplete.length, 1);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  assert.equal(h.contexts[0].closed, true);
  assert.match(h.nodes['voice-status'].textContent, /未完了/);
  assert.equal(h.nodes['voice-fallback'].hidden, false);
  h.nodes['voice-fallback'].click();
  assert.equal(h.calls.fallback[0][0].transcript, '四季を説明して');
});

test('confirmed output replaces the document while playback continues and mismatched snapshots are rejected', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'THINKING', {transcript: 'prompt'}));
  h.state(1, 'PLAYING');
  h.event(1, 'output', {snapshot: snapshot(1, 'THINKING', {response_plan: {text: '春．'}, generation: {segment_count: 1}})});
  assert.equal(h.calls.output.length, 1);
  assert.equal(h.calls.output[0][0].state, 'PLAYING');
  assert.equal(h.controller.lastSnapshot.response_plan.text, '春．');
  h.event(1, 'output', {snapshot: snapshot(1, 'THINKING', {session_id: 'old-session', response_plan: {text: 'late'}})});
  assert.equal(h.calls.output.length, 1);
  await h.controller.cancel();
});

test('same-operation continuation rejects old-revision terminal output tokens and audio and resumes audio sequence', async () => {
  const h = harness();
  await h.controller.adopt(snapshot(1, 'THINKING', {generation_revision: 1, transcript: 'prompt'}));
  h.state(1, 'INCOMPLETE', {generation_revision: 1, transcript: 'prompt', response_plan: {text: '春．'}});
  const pending = deferred();
  const adopted = h.controller.adopt(pending.promise, {operationID: 'op-1', revision: 2});
  h.state(1, 'COMPLETED', {generation_revision: 1});
  pending.resolve(snapshot(1, 'THINKING', {generation_revision: 2, transcript: 'prompt', last_audio_sequence: 3, response_plan: {text: '春．'}}));
  assert.equal(await adopted, true);
  const before = {tokens: h.calls.token.length, outputs: h.calls.output.length};
  h.event(1, 'token', {generation_revision: 1, text: 'old text'});
  h.event(1, 'output', {generation_revision: 1, snapshot: snapshot(1, 'THINKING', {generation_revision: 1, response_plan: {text: 'old full text'}})});
  h.audio(1, 4);
  h.state(1, 'COMPLETED', {generation_revision: 1});
  assert.equal(h.controller.isActive(), true);
  assert.equal(h.calls.token.length, before.tokens);
  assert.equal(h.calls.output.length, before.outputs);
  assert.equal(h.contexts[1].sources.length, 0);
  h.event(1, 'token', {generation_revision: 2, text: '夏．'});
  h.event(1, 'audio', {generation_revision: 2, sequence: 4, audio_base64: wav()});
  await tick();
  assert.equal(h.calls.token.at(-1)[1], '夏．');
  assert.equal(h.contexts[1].sources.length, 1);
  assert.deepEqual(h.calls.playback.at(-1), ['op-1', 4, 'started']);
  const canceled = snapshot(1, 'CANCELED', {generation_revision: 2});
  h.bridge.CancelInteraction = async () => canceled;
  await h.controller.cancel();
  h.event(1, 'audio', {generation_revision: 2, sequence: 5, audio_base64: wav()});
  h.event(1, 'token', {generation_revision: 2, text: 'after cancel'});
  assert.equal(h.contexts[1].sources.length, 1);
  assert.equal(h.contexts[1].sources[0].stopped, true);
  assert.equal(h.calls.token.at(-1)[1], '夏．');
});

test('cancel while continuation identity is pending cancels the resumed generation and blocks late output', async () => {
  const h = harness();
  const pending = deferred();
  const adopted = h.controller.adopt(pending.promise, {operationID: 'op-1', revision: 2});
  const canceled = h.controller.cancel();
  pending.resolve(snapshot(1, 'THINKING', {generation_revision: 2, transcript: 'old session text'}));
  assert.equal(await adopted, false);
  await canceled;
  h.event(1, 'output', {generation_revision: 2, snapshot: snapshot(1, 'THINKING', {generation_revision: 2, response_plan: {text: 'late'}})});
  assert.deepEqual(h.calls.cancel, ['op-1']);
  assert.equal(h.calls.complete.length, 0);
  assert.equal(h.calls.output.length, 0);
  assert.equal(h.controller.isActive(), false);
});

test('ASR readiness runs before operation creation or microphone capture and resumes audio in the user gesture', async () => {
  const readiness = deferred();
  let resumed = false;
  const h = harness({
    bridge: {GetInteractionASRReadiness() { assert.equal(resumed, true); return readiness.promise; }},
    createAudioContext() { const context = new FakeContext(); context.resume = () => { resumed = true; return Promise.resolve(); }; return context; },
  });
  const starting = h.controller.start();
  assert.equal(resumed, true);
  assert.equal(h.calls.start.length, 0);
  assert.equal(h.streams.length, 0);
  assert.equal(h.nodes['voice-record'].attributes['aria-pressed'], 'false');
  assert.match(h.nodes['voice-status'].textContent, /確認/);
  readiness.resolve({state: 'ready', can_start: true});
  assert.equal(await starting, true);
  assert.equal(h.calls.start.length, 1);
  assert.equal(h.streams.length, 1);
  await h.controller.cancel();
});

test('unavailable denied restricted and malformed readiness never open a microphone or create a turn', async () => {
  for (const result of [
    {state: 'unavailable', can_start: false, error_code: 'asr_unavailable'},
    {state: 'unavailable', can_start: false, error_code: 'asr_permission_denied'},
    {state: 'unavailable', can_start: false, error_code: 'asr_permission_restricted'},
    {state: 'unavailable', can_start: false, error_code: 'asr_on_device_unavailable'},
    {state: 'ready', can_start: true, error_code: 'private provider diagnostic'},
    {state: 'private provider state', can_start: true},
    {state: 'permission_required', can_start: false},
  ]) {
    const h = harness({bridge: {GetInteractionASRReadiness: async () => result}});
    assert.equal(await h.controller.start(), false);
    assert.equal(h.calls.start.length, 0);
    assert.equal(h.calls.fail.length, 0);
    assert.equal(h.streams.length, 0);
    assert.equal(h.contexts[0].closed, true);
    assert.equal(h.controller.isActive(), false);
    assert.deepEqual(h.calls.busy, [[true], [false]]);
    assert.equal(h.nodes['voice-record'].attributes['aria-pressed'], 'false');
    assert.match(h.nodes['voice-status'].textContent, /テキスト入力/);
    assert.doesNotMatch(h.nodes['voice-status'].textContent, /private/);
  }
});

test('permission-required readiness permits explicit recording without claiming Speech authorization', async () => {
  const h = harness({bridge: {GetInteractionASRReadiness: async () => ({state: 'permission_required', can_start: true})}});
  assert.equal(await h.controller.start(), true);
  assert.equal(h.streams.length, 1);
  assert.match(h.nodes['voice-status'].textContent, /送信時に音声認識の許可/);
  await h.controller.cancel();
});

test('cancel during readiness returns immediately and a late ready result cannot start the old turn', async () => {
  const pending = deferred();
  let probes = 0;
  const h = harness({bridge: {GetInteractionASRReadiness: () => ++probes === 1 ? pending.promise : Promise.resolve({state: 'ready', can_start: true})}});
  const oldStart = h.controller.start();
  assert.equal((await h.controller.cancel()).state, 'CANCELED');
  assert.equal(h.calls.cancel.length, 0);
  assert.equal(h.calls.start.length, 0);
  assert.equal(await h.controller.start(), true);
  pending.resolve({state: 'ready', can_start: true});
  assert.equal(await oldStart, false);
  assert.equal(h.calls.start.length, 1);
  assert.equal(h.streams.length, 1);
  assert.equal(h.controller.lastSnapshot.operation_id, 'op-1');
  await h.controller.cancel();
});

test('session switch and dispose discard readiness without creating old operations', async () => {
  for (const action of ['session', 'dispose']) {
    const pending = deferred();
    let session = 'session';
    const h = harness({getSessionID: () => session, bridge: {GetInteractionASRReadiness: () => pending.promise}});
    const starting = h.controller.start();
    if (action === 'dispose') await h.controller.dispose();
    else session = 'new-session';
    pending.resolve({state: 'ready', can_start: true});
    assert.equal(await starting, false);
    assert.equal(h.calls.start.length, 0);
    assert.equal(h.calls.cancel.length, 0);
    assert.equal(h.streams.length, 0);
    assert.equal(h.controller.isActive(), false);
  }
});

test('rejected readiness returns text chat availability without exposing provider diagnostics', async () => {
  const h = harness({bridge: {GetInteractionASRReadiness: async () => { throw new Error('private /provider/path'); }}});
  assert.equal(await h.controller.start(), false);
  assert.equal(h.controller.lastSnapshot.error_code, 'asr_unavailable');
  assert.equal(h.calls.start.length, 0);
  assert.equal(h.streams.length, 0);
  assert.deepEqual(h.calls.busy, [[true], [false]]);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /private|provider/);
});

test('playback accepts a complete 60-second 48-kHz mono PCM16 WAV within the provider contract', async () => {
  const header = Buffer.from(encodePCM16Wav([new Float32Array([0])], 48000));
  const dataBytes = 60 * 48000 * 2;
  const bytes = Buffer.alloc(44 + dataBytes);
  header.copy(bytes, 0, 0, 44);
  bytes.writeUInt32LE(bytes.length - 8, 4);
  bytes.writeUInt32LE(dataBytes, 40);
  const h = harness();
  await h.controller.adopt(snapshot(1, 'SYNTHESIZING'));
  h.event(1, 'audio', {sequence: 1, audio_base64: bytes.toString('base64')});
  await tick();
  assert.equal(h.calls.fail.length, 0);
  assert.equal(h.contexts[0].sources.length, 1);
  assert.deepEqual(h.calls.playback[0], ['op-1', 1, 'started']);
  await h.controller.cancel();
});
