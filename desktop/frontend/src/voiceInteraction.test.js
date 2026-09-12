import test from 'node:test';
import assert from 'node:assert/strict';
import {createPCM16StreamEncoder, encodePCM16Wav, mountVoiceInteraction, voiceStatusText} from './voiceInteraction.js';

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
const asrSession = (id, sampleRate = 16000) => ({operation_id: `op-${id}`, session_id: 'session', turn_id: `turn-${id}`, segment_id: `segment-${id}`, sample_rate: sampleRate});
const asrUpdate = (id, revision, transcript, extra = {}) => ({
  ...asrSession(id), revision, phase: 'partial', transcript, stable_prefix: '', provider: 'test-asr',
  model_revision: 'test-1', monotonic_ms: revision * 10, ...extra,
});

const fillerSetup = () => ({enabled: true, assets: [{kind: 'hesitation', audio_base64: wav(), duration_ms: 1000}],
  samples: Array.from({length: 100}, () => ({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100,
    tts_chunk_ms: 4970, answer_ready_ms: 5000, llm_ttft_ms: 880, tts_latency_ms: 3870}))});

test('body waits for filler end with microphone active and readiness measured before the wait', async () => {
  let time = 0, monitor, monitoring = false; const telemetry = [], samples = [];
  const h = harness({now: () => time, startBargeIn: async options => {
    monitor = options; monitoring = true; return {stop() { monitoring = false; }};
  },
    bridge: {GetInteractionFiller: async () => fillerSetup(), RecordInteractionFillerTrace: async (...a) => telemetry.push(a),
      RecordInteractionFillerTiming: async (...a) => samples.push(a)}});
  await h.controller.adopt(snapshot(1, 'THINKING'));
  h.contexts[0].decode = async () => ({duration: 1});
  const trace = (name, at) => { time = at; h.event(1, 'trace', {trace: {name, monotonic_ms: at}}); };
  trace('endpoint_commit', 0); trace('llm_requested', 20); trace('llm_identity_ready', 30);
  await tick(); await tick();
  trace('llm_first_token', 900); trace('tts_requested', 1100);
  time = 3850;
  for (const item of [...h.timeouts.values()]) item.callback();
  assert.equal(h.contexts[0].sources.length, 1);
  const filler = h.contexts[0].sources[0]; assert.equal(filler.started, true);
  trace('tts_first_chunk', 4000); time = 4050; h.audio(1, 1);
  await tick();
  assert.equal(filler.stopped, false);
  assert.equal(h.contexts[0].sources.length, 1);
  assert.equal(monitoring && monitor.isCurrent(), true);
  assert.deepEqual(h.calls.playback, []);
  assert.equal(samples.length, 1);
  assert.equal(samples[0][2].answer_ready_ms, 4050);
  time = 4850; filler.end(); await tick();
  assert.equal(h.contexts[0].sources[1].started, true);
  assert.equal(monitoring, false);
  assert.equal(telemetry.find(e => e[2].kind === 'filler_answer_wait')[2].latency_ms, 800);
  assert.equal(telemetry.find(e => e[2].kind === 'filler_gap')[2].latency_ms, 0);
  assert.deepEqual(h.calls.output, []); assert.deepEqual(h.calls.token, []); assert.deepEqual(h.calls.transcript, []);
  assert.equal(telemetry[0][2].kind, 'filler_started');
  assert.ok(telemetry.every(e => Object.keys(e[2]).sort().join() === 'kind,latency_ms'));
  await h.controller.cancel();
});

test('cancel and microphone barge-in stop filler immediately and discard the waiting body', async () => {
  for (const reason of ['cancel', 'barge_in']) {
    let time = 0, monitor, stopped = false;
    const h = harness({now: () => time, startBargeIn: async options => {
      monitor = options; return {stop() { stopped = true; }};
    }, bridge: {GetInteractionFiller: async () => fillerSetup()}});
    await h.controller.adopt(snapshot(1, 'THINKING'));
    h.contexts[0].decode = async () => ({duration: 1});
    const trace = (name, at) => { time = at; h.event(1, 'trace', {trace: {name, monotonic_ms: at}}); };
    trace('endpoint_commit', 0); trace('llm_requested', 20); trace('llm_identity_ready', 30);
    await tick(); await tick();
    trace('llm_first_token', 900); trace('tts_requested', 1100);
    time = 3850; for (const item of [...h.timeouts.values()]) item.callback();
    const context = h.contexts[0], filler = context.sources[0], lateEnd = filler.onended;
    time = 4050; h.audio(1, 1); await tick();
    assert.equal(monitor.isCurrent(), true);
    if (reason === 'barge_in') monitor.onSpeech();
    else void h.controller.cancel();
    assert.equal(filler.stopped, true);
    assert.equal(stopped, true);
    lateEnd(); await tick(); await tick();
    assert.equal(context.sources.length, 1);
    assert.deepEqual(h.calls.playback, []);
    assert.deepEqual(h.calls.cancel, ['op-1']);
    await h.controller.adopt(snapshot(2, 'THINKING'));
    lateEnd(); h.audio(1, 2); await tick();
    assert.equal(h.contexts[1].sources.length, 0);
    await h.controller.cancel();
  }
});

test('late filler setup cannot open microphone or play after body arrival or cancellation', async () => {
  for (const action of ['body', 'cancel']) {
    const setup = deferred(); let microphones = 0;
    const h = harness({startBargeIn: async () => { microphones++; return {stop() {}}; }, bridge: {GetInteractionFiller: () => setup.promise}});
    await h.controller.adopt(snapshot(1, 'THINKING'));
    h.event(1, 'trace', {trace: {name: 'endpoint_commit', monotonic_ms: 0}});
    h.event(1, 'trace', {trace: {name: 'llm_identity_ready', monotonic_ms: 10}});
    if (action === 'cancel') await h.controller.cancel();
    else { h.audio(1, 1); await tick(); }
    setup.resolve(fillerSetup()); await tick(); await tick();
    assert.equal(microphones, 0);
    assert.equal(h.contexts[0].sources.length, action === 'body' ? 1 : 0);
    await h.controller.cancel();
  }
});

test('barge-in replaces canceled filler with one predecoded acknowledgement without waiting for Go', async () => {
  for (const end of ['natural', 'cancel', 'new_turn', 'session', 'dispose', 'failed_stop', 'explicit_cancel',
    'waiting_cancel', 'waiting_session', 'waiting_new_turn', 'waiting_dispose']) {
    let time = 0, monitor, session = 'session', decodes = 0;
    const canceled = deferred(), telemetry = [];
    const h = harness({now: () => time, getSessionID: () => session,
      startBargeIn: async options => { monitor = options; return {stop() {}}; },
      bridge: {GetInteractionFiller: async () => ({...fillerSetup(), assets: [...fillerSetup().assets,
        {kind: 'backchannel', audio_base64: wav(), duration_ms: 800}]}),
      CancelInteraction: () => canceled.promise, RecordInteractionFillerTrace: async (...a) => telemetry.push(a)}});
    await h.controller.adopt(snapshot(1, 'THINKING'));
    const context = h.contexts[0]; context.decode = async () => ({duration: ++decodes === 2 ? .8 : 1});
    const trace = (name, at) => { time = at; h.event(1, 'trace', {trace: {name, monotonic_ms: at}}); };
    trace('endpoint_commit', 0); trace('llm_requested', 20); trace('llm_identity_ready', 30);
    await tick(); await tick(); trace('llm_first_token', 900); trace('tts_requested', 1100);
    time = 3850; for (const t of [...h.timeouts.values()]) t.callback();
    const filler = context.sources[0];
    time = 4050; h.audio(1, 1); await tick();
    if (end === 'explicit_cancel') {
      const pending = h.controller.cancel();
      assert.equal(context.sources.length, 1); assert.equal(filler.stopped, true); assert.equal(context.closed, true);
      canceled.resolve(snapshot(1, 'CANCELED')); await pending;
      assert.equal(telemetry.filter(e => e[2].kind === 'backchannel_started').length, 0);
      await h.controller.dispose(); continue;
    }
    if (end === 'failed_stop') filler.stop = () => { throw new Error('device'); };
    monitor.onSpeech(); monitor.onSpeech();
    if (end === 'failed_stop') {
      await tick();
      assert.equal(context.sources.length, 1); assert.equal(context.closed, true);
      assert.equal(h.controller.lastSnapshot.state, 'FAILED');
      await h.controller.dispose(); continue;
    }
    assert.equal(filler.stopped, true); assert.equal(context.sources.length, 1);
    assert.equal(context.closed, false); assert.equal(h.nodes['voice-cancel'].disabled, false);
    if (end.startsWith('waiting_')) {
      canceled.resolve(snapshot(1, 'CANCELED')); await tick();
      if (end === 'waiting_cancel') await h.controller.cancel();
      if (end === 'waiting_dispose') await h.controller.dispose();
      if (end === 'waiting_new_turn') await h.controller.adopt(snapshot(2, 'THINKING'));
      if (end === 'waiting_session') { session = 'other'; time = 4075; for (const t of [...h.timeouts.values()]) t.callback(); }
      assert.equal(context.closed, true);
      time = 4350; for (const t of [...h.timeouts.values()]) t.callback();
      assert.equal(context.sources.length, 1);
      assert.equal(telemetry.filter(e => e[2].kind === 'backchannel_started').length, 0);
      await h.controller.dispose(); continue;
    }
    time = 4350; for (const t of [...h.timeouts.values()]) t.callback();
    const ack = context.sources[1];
    assert.equal(filler.stopped, true); assert.equal(ack.started, true);
    assert.equal(context.closed, false); assert.equal(context.sources.length, 2);
    assert.equal(h.nodes['voice-cancel'].disabled, false);
    await tick(); assert.equal(context.sources.length, 2); assert.deepEqual(h.calls.playback, []);
    canceled.resolve(snapshot(1, 'CANCELED')); await tick();
    assert.equal(ack.stopped, false); assert.equal(h.nodes['voice-cancel'].disabled, false);
    if (end === 'natural') { time = 5150; ack.end(); }
    if (end === 'cancel') await h.controller.cancel();
    if (end === 'dispose') await h.controller.dispose();
    if (end === 'new_turn') await h.controller.adopt(snapshot(2, 'THINKING'));
    if (end === 'session') { session = 'other'; time = 4375; for (const t of [...h.timeouts.values()]) t.callback(); }
    assert.equal(ack.stopped, true); assert.equal(context.closed, true);
    assert.equal(telemetry.filter(e => e[2].kind === 'backchannel_started').length, 1);
    assert.equal(telemetry.find(e => e[2].kind === 'backchannel_started')[2].latency_ms, 300);
    assert.deepEqual(h.calls.output, []); assert.deepEqual(h.calls.token, []); assert.deepEqual(h.calls.transcript, []);
    h.audio(1, 2); await tick(); assert.equal(context.sources.length, 2);
    await h.controller.dispose();
  }
});

test('approved short and conversational backchannels rotate across interruptions in a five-asset bundle', async () => {
  let time = 0, monitor;
  const durations = [.8, .88, 2.4];
  const h = harness({now: () => time, startBargeIn: async options => { monitor = options; return {stop() {}}; },
    bridge: {GetInteractionFiller: async () => ({...fillerSetup(), assets: [...fillerSetup().assets, ...fillerSetup().assets,
      ...durations.map(duration => ({kind: 'backchannel', audio_base64: wav(), duration_ms: duration * 1000}))]})}});
  for (let turn = 1; turn <= 4; turn++) {
    time = 0; let decodes = 0;
    const expected = durations[(turn - 1) % durations.length];
    await h.controller.adopt(snapshot(turn, 'THINKING'));
    const context = h.contexts[turn - 1]; context.decode = async () => ({duration: ++decodes === 1 ? 1 : expected});
    const trace = (name, at) => { time = at; h.event(turn, 'trace', {trace: {name, monotonic_ms: at}}); };
    trace('endpoint_commit', 0); trace('llm_requested', 20); trace('llm_identity_ready', 30);
    await tick(); await tick(); trace('llm_first_token', 900); trace('tts_requested', 1100);
    time = 3850; for (const t of [...h.timeouts.values()]) t.callback();
    monitor.onSpeech(); time = 4150; for (const t of [...h.timeouts.values()]) t.callback();
    assert.equal(context.sources.length, 2);
    assert.equal(context.sources[1].buffer.duration, expected);
    time += expected * 1000; context.sources[1].end(); await tick();
    assert.equal(context.closed, true);
  }
  await h.controller.dispose();
});

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
  const nodes = Object.fromEntries(['voice-session', 'voice-pause', 'voice-end', 'voice-record', 'voice-cancel', 'voice-status', 'voice-fallback', 'voice-live-transcript', 'voice-transcript-stable', 'voice-transcript-revisable'].map((id) => [id, button()]));
  if (options.showASR) nodes['voice-asr-status'] = button();
  const calls = {start: [], readiness: [], begin: [], append: [], end: [], commit: [], cancel: [], interrupts: [], playback: [], fail: [], transcript: [], token: [], output: [], complete: [], incomplete: [], failure: [], canceled: [], busy: [], fallback: []};
  calls.candidateBegin = []; calls.candidateAppend = []; calls.candidateCancel = [];
  const candidateRequest = op => ({operation_id: op, session_id: 'session', turn_id: `turn-${op.slice(3)}`, segment_id: `candidate-segment-${op}`, sample_rate: 16000});
  const contexts = [];
  const streams = [];
  const timeouts = new Map();
  const bridge = {
    async StartVoiceSession(conversationID) { return {id: 'voice-1', epoch: 1, conversation_id: conversationID, state: 'starting'}; },
    async ChangeVoiceSession(id, epoch, action) { return {id, epoch: action === 'resume' ? epoch + 1 : epoch, conversation_id: 'session', state: action === 'end' ? 'stopped' : action === 'pause' ? 'paused' : 'starting'}; },
    async GetInteractionASRReadiness() { calls.readiness.push(true); return {state: 'ready', can_start: true}; },
    async StartInteraction(request) { calls.start.push(request); calls.request = request; return snapshot(++identity); },
    async BeginInteractionASR(operationID, sampleRate) {
      calls.begin.push([operationID, sampleRate]);
      return {operation_id: operationID, session_id: 'session', turn_id: `turn-${operationID.slice(3)}`, segment_id: `segment-${operationID.slice(3)}`, sample_rate: sampleRate};
    },
    async AppendInteractionAudio(...args) { calls.append.push(args); },
    async EndInteractionASR(...args) { calls.end.push(args); },
    async BeginInteractionInterruptionCandidate(op, id, revision, sampleRate) {
      calls.candidateBegin.push([op, id, revision, sampleRate]);
      return {candidate_id: id, request: candidateRequest(op)};
    },
    async AppendInteractionInterruptionCandidate(op, id, sequence, bytes) {
      calls.candidateAppend.push([op, id, sequence, bytes]);
      return {candidate_id: id, request: candidateRequest(op), update: {...candidateRequest(op), revision: 1, phase: 'final', transcript: '待って', stable_prefix: '待って'}};
    },
    async CancelInteractionInterruptionCandidate(...args) { calls.candidateCancel.push(args); },
    async CommitInteraction(...args) { calls.commit.push(args); },
    async CancelInteraction(op) {
      calls.cancel.push(op);
      return {...snapshot(op.slice(3), 'CANCELED')};
    },
    async InterruptInteraction(request) {
      calls.interrupts.push(request); calls.cancel.push(request.operation_id);
      return snapshot(request.operation_id.slice(3), 'CANCELED', {interruption: {local_stop_ms: request.local_stop_ms}});
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
    asr(id, revision, transcript, extra = {}) { listener?.({...snapshot(id), kind: 'asr_update', asr: asrUpdate(id, revision, transcript, extra)}); },
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

test('capture sends raw PCM before stop，then drains once and ends without committing a WAV', async () => {
  const h = harness();
  assert.equal(await h.controller.start(), true);
  const context = h.contexts[0];
  context.capture();
  await tick();
  assert.equal(h.calls.append.length, 1);
  assert.equal(h.calls.end.length, 0);
  assert.equal(await h.controller.stop(), true);
  assert.equal(await h.controller.stop(), false);
  assert.equal(h.calls.commit.length, 0);
  assert.deepEqual(h.calls.end, [['op-1']]);
  assert.deepEqual(h.calls.append[0].slice(0, 2), ['op-1', 1]);
  const bytes = Buffer.from(h.calls.append[0][2], 'base64');
  assert.equal(bytes.length, 6);
  assert.deepEqual([bytes.readInt16LE(0), bytes.readInt16LE(2), bytes.readInt16LE(4)], [0, 16384, -16384]);
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

test('higher actual AudioContext rate streams compatible PCM to a 48 kHz ASR session', async () => {
  const context = new FakeContext();
  context.sampleRate = 96000;
  const h = harness({createAudioContext: () => context});
  await h.controller.start();
  context.capture([0, 0.5, -0.5, 0]);
  await h.controller.stop();
  const bytes = Buffer.from(h.calls.append[0][2], 'base64');
  assert.deepEqual(h.calls.begin, [['op-1', 48000]]);
  assert.equal(bytes.length, 4);
  assert.equal(h.calls.commit.length, 0);
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

test('recording timeout ends streaming ASR at 60 seconds and releases microphone resources', async () => {
  const h = harness();
  await h.controller.start();
  h.contexts[0].capture();
  const timer = [...h.timeouts.values()][0];
  assert.equal(timer.milliseconds, 60_000);
  timer.callback();
  await tick();
  assert.equal(h.calls.end.length, 1);
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
  for (let seconds = 0; seconds < 59; seconds += 1) {
    h.contexts[0].capture(new Float32Array(16000));
    await tick();
  }
  h.contexts[0].capture(new Float32Array(16000 * 2));
  await tick();
  assert.equal(h.calls.end.length, 1);
  assert.equal(h.calls.append.reduce((total, call) => total + Buffer.from(call[2], 'base64').length, 0), 16000 * 60 * 2);
  assert.equal(h.calls.commit.length, 0);
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
  h.contexts[0].capture();
  await h.controller.stop();
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

test('permission-required readiness opens the ASR session before recording', async () => {
  const h = harness({bridge: {GetInteractionASRReadiness: async () => ({state: 'permission_required', can_start: true})}});
  assert.equal(await h.controller.start(), true);
  assert.equal(h.streams.length, 1);
  assert.equal(h.calls.begin.length, 1);
  assert.match(h.nodes['voice-status'].textContent, /録音中/);
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

test('stream encoder preserves fractional carry for irregular callbacks and emits raw PCM incrementally', () => {
  for (const rate of [8000, 16000, 44100, 48000, 88200, 96000, 192000]) {
    const signal = Float32Array.from({length: 1013}, (_, i) => Math.sin(i / 11) * 1.2);
    signal[17] = NaN;
    const encoder = createPCM16StreamEncoder(rate);
    const chunks = [];
    for (let offset = 0; offset < signal.length; offset += 7) {
      chunks.push(Buffer.from(encoder.push(signal.subarray(offset, offset + 7))));
    }
    assert.ok(chunks.some(chunk => chunk.length > 0));
    chunks.push(Buffer.from(encoder.finish()));
    assert.deepEqual(Buffer.concat(chunks), Buffer.from(encodePCM16Wav([signal], rate)).subarray(44));
    assert.equal(encoder.finish().byteLength, 0);
    assert.throws(() => encoder.push(signal), /encoder_closed/);
  }
  assert.throws(() => createPCM16StreamEncoder(4000));
  assert.throws(() => createPCM16StreamEncoder(48000, 96000));
  const canceled = createPCM16StreamEncoder(96000);
  assert.equal(canceled.push(new Float32Array([1])).byteLength, 0);
  canceled.reset();
  assert.equal(canceled.finish().byteLength, 0);
});

test('ASR session startup precedes microphone permission and failures never use the legacy WAV bridge', async () => {
  const open = deferred();
  const h = harness({bridge: {BeginInteractionASR: () => open.promise}});
  const starting = h.controller.start();
  await tick();
  assert.equal(h.calls.start.length, 1);
  assert.equal(h.streams.length, 0);
  assert.match(h.nodes['voice-status'].textContent, /音声認識を開始/);
  open.reject(new Error('private provider path'));
  assert.equal(await starting, false);
  assert.equal(h.streams.length, 0);
  assert.equal(h.calls.commit.length, 0);
  assert.deepEqual(h.calls.fail, [['op-1', 'asr_unavailable']]);
  assert.equal(h.controller.isActive(), false);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /private/);
  const missing = harness({bridge: {BeginInteractionASR: undefined}});
  assert.equal(await missing.controller.start(), false);
  assert.equal(missing.streams.length, 0);
  assert.equal(missing.calls.commit.length, 0);
});

test('all returned ASR session identities and the negotiated sample rate must match before opening the microphone', async () => {
  for (const mismatch of [
    {operation_id: 'other'}, {session_id: 'other'}, {turn_id: 'other'}, {segment_id: ''}, {sample_rate: 48000},
  ]) {
    const h = harness({bridge: {BeginInteractionASR: async () => ({...asrSession(1), ...mismatch})}});
    assert.equal(await h.controller.start(), false);
    assert.equal(h.streams.length, 0);
    assert.deepEqual(h.calls.fail, [['op-1', 'asr_protocol_error']]);
  }
});

test('missing microphone API fails without starting an ASR session or changing text chat', async () => {
  const h = harness({mediaDevices: {}});
  assert.equal(await h.controller.start(), false);
  assert.equal(h.calls.begin.length, 0);
  assert.equal(h.calls.commit.length, 0);
  assert.deepEqual(h.calls.busy, [[true], [false]]);
  assert.equal(h.controller.lastSnapshot.error_code, 'microphone_unavailable');
});

test('cancel and session switch during ASR open discard the late session before microphone access', async () => {
  for (const mode of ['cancel', 'session']) {
    const open = deferred();
    let sessionID = 'session';
    const h = harness({getSessionID: () => sessionID, bridge: {BeginInteractionASR: () => open.promise}});
    const starting = h.controller.start();
    await tick();
    if (mode === 'cancel') await h.controller.cancel();
    else sessionID = 'new-session';
    open.resolve(asrSession(1));
    assert.equal(await starting, false);
    await tick();
    assert.equal(h.streams.length, 0);
    assert.deepEqual(h.calls.cancel, ['op-1']);
    assert.equal(h.calls.end.length, 0);
    assert.equal(h.calls.transcript.length, 0);
    assert.equal(h.controller.isActive(), false);
  }
});

test('PCM appends are sequential，bounded to 64 KiB and drained before End', async () => {
  const first = deferred();
  const appends = [];
  let inFlight = 0;
  let peak = 0;
  const context = new FakeContext();
  context.sampleRate = 48000;
  const h = harness({createAudioContext: () => context, bridge: {
    async AppendInteractionAudio(...args) {
      appends.push(args);
      inFlight += 1;
      peak = Math.max(peak, inFlight);
      if (args[1] === 1) await first.promise;
      inFlight -= 1;
    },
  }});
  await h.controller.start();
  context.capture(new Float32Array(48000));
  await tick();
  assert.equal(appends.length, 1);
  const stopped = h.controller.stop();
  assert.equal(h.calls.end.length, 0);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  first.resolve();
  assert.equal(await stopped, true);
  assert.equal(peak, 1);
  assert.deepEqual(appends.map(call => call[1]), [1, 2]);
  assert.deepEqual(appends.map(call => Buffer.from(call[2], 'base64').length), [65536, 30464]);
  assert.deepEqual(h.calls.end, [['op-1']]);
  assert.equal(h.calls.commit.length, 0);
  await h.controller.cancel();
});

test('backpressure includes the in-flight append and stops recording without retaining hypotheses or sending queued audio', async () => {
  const append = deferred();
  const h = harness({bridge: {AppendInteractionAudio: () => append.promise}});
  await h.controller.start();
  h.asr(1, 1, 'private hypothesis');
  h.contexts[0].capture(new Float32Array(8000));
  h.contexts[0].capture(new Float32Array(8000));
  assert.equal(h.calls.failure.length, 0);
  h.contexts[0].capture([0]);
  await tick();
  assert.deepEqual(h.calls.fail, [['op-1', 'asr_backpressure']]);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  assert.equal(h.contexts[0].processor.onaudioprocess, null);
  assert.equal(h.nodes['voice-live-transcript'].hidden, true);
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, '');
  assert.equal(h.controller.lastSnapshot.transcript, undefined);
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.nodes['voice-record'].disabled, false);
  append.resolve();
  await tick();
  assert.equal(h.calls.end.length, 0);
});

test('append failure is a fixed error with immediate microphone cleanup and no whole-recording retry', async () => {
  const h = harness({bridge: {AppendInteractionAudio: async () => { throw new Error('private diagnostic'); }}});
  await h.controller.start();
  h.contexts[0].capture();
  await tick();
  assert.deepEqual(h.calls.fail, [['op-1', 'asr_failed']]);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  assert.equal(h.calls.commit.length, 0);
  assert.equal(h.calls.end.length, 0);
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /private/);
});

test('partial revisions replace one live segment，stable prefix is distinct and neither enters Conversation', async () => {
  const h = harness();
  await h.controller.start();
  h.asr(1, 1, '日本のし');
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, '日本のし');
  h.asr(1, 2, '日本の四季');
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, '日本の四季');
  h.asr(1, 3, '日本の四季について', {phase: 'stable', stable_prefix: '日本の四季'});
  assert.equal(h.nodes['voice-transcript-stable'].textContent, '日本の四季');
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'について');
  assert.equal(h.nodes['voice-live-transcript'].hidden, false);
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.controller.lastSnapshot.transcript, undefined);
  assert.equal(h.calls.token.length, 0);
  h.asr(1, 4, '日本の四季<img src=x>', {stable_prefix: '日本の四季'});
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, '<img src=x>');
  assert.equal(h.nodes['voice-transcript-revisable'].innerHTML, undefined);
  await h.controller.cancel();
});

test('ASR updates reject wrong identities，non-increasing revisions and backwards or invalid monotonic times', async () => {
  const h = harness();
  await h.controller.start();
  h.asr(1, 3, 'accepted');
  for (const change of [
    {operation_id: 'other'}, {session_id: 'other'}, {turn_id: 'other'}, {segment_id: 'other'},
    {revision: 2}, {revision: 3}, {revision: 0}, {revision: 2049}, {revision: 3.5},
    {monotonic_ms: 29}, {monotonic_ms: -1}, {monotonic_ms: NaN}, {monotonic_ms: 1.5}, {phase: 'unknown'},
  ]) h.asr(1, 4, 'rejected', change);
  h.event(1, 'asr_update', {session_id: 'other', asr: asrUpdate(1, 4, 'rejected')});
  h.event(1, 'asr_update', {turn_id: undefined, asr: asrUpdate(1, 4, 'rejected')});
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'accepted');
  h.asr(1, 4, 'next', {monotonic_ms: 30});
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'next');
  assert.equal(h.calls.transcript.length, 0);
  await h.controller.cancel();
});

test('early final stops recording and ends only after pending PCM，then one canonical final enters history', async () => {
  const append = deferred();
  const h = harness({bridge: {AppendInteractionAudio: () => append.promise}});
  await h.controller.start();
  h.contexts[0].capture();
  h.asr(1, 1, '日本の四季について', {phase: 'final', stable_prefix: '日本の四季について'});
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  assert.equal(h.calls.end.length, 0);
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.controller.lastSnapshot.transcript, undefined);
  assert.equal(h.nodes['voice-transcript-stable'].textContent, '日本の四季について');
  h.event(1, 'transcript', {text: 'premature'});
  h.state(1, 'TRANSCRIBING', {transcript: 'premature'});
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.controller.lastSnapshot.transcript, undefined);
  append.resolve();
  await tick();
  assert.deepEqual(h.calls.end, [['op-1']]);
  assert.equal(h.calls.transcript.length, 0);
  h.event(1, 'transcript', {text: '日本の四季について'});
  h.event(1, 'transcript', {text: '日本の四季について'});
  h.asr(1, 2, 'late hypothesis');
  assert.deepEqual(h.calls.transcript.map(call => call[1]), ['日本の四季について']);
  assert.equal(h.controller.lastSnapshot.transcript, '日本の四季について');
  assert.equal(h.nodes['voice-live-transcript'].hidden, true);
  assert.equal(h.calls.commit.length, 0);
  await h.controller.cancel();
});

test('final delivered only after manual End remains display-only until the canonical transcript', async () => {
  const end = deferred();
  const h = harness({bridge: {EndInteractionASR: () => end.promise}});
  await h.controller.start();
  h.contexts[0].capture();
  const stopping = h.controller.stop();
  await tick();
  h.asr(1, 1, 'final only', {phase: 'final', stable_prefix: 'final only'});
  assert.equal(h.calls.transcript.length, 0);
  h.event(1, 'transcript', {text: 'final only'});
  assert.equal(h.calls.transcript.length, 1);
  end.resolve();
  assert.equal(await stopping, true);
  await h.controller.cancel();
});

test('ASR events emitted during Open are buffered only until the matching segment is known', async () => {
  const open = deferred();
  const h = harness({bridge: {BeginInteractionASR: () => open.promise}});
  const starting = h.controller.start();
  await tick();
  h.asr(1, 1, 'wrong segment', {segment_id: 'wrong'});
  h.asr(1, 1, 'early partial');
  assert.equal(h.nodes['voice-live-transcript'].hidden, true);
  open.resolve(asrSession(1));
  assert.equal(await starting, true);
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'early partial');
  assert.equal(h.calls.transcript.length, 0);
  await h.controller.cancel();
});

test('cancel during an append or End releases resources immediately and late callbacks cannot enter a new turn', async () => {
  for (const stage of ['append', 'end']) {
    const pending = deferred();
    let intercepted = false;
    const method = stage === 'append' ? 'AppendInteractionAudio' : 'EndInteractionASR';
    const h = harness({bridge: {[method]: () => {
      if (!intercepted) { intercepted = true; return pending.promise; }
      return Promise.resolve();
    }}});
    await h.controller.start();
    h.contexts[0].capture();
    const stopping = h.controller.stop();
    await tick();
    await h.controller.cancel();
    assert.equal(h.contexts[0].closed, true);
    assert.ok(h.streams[0].tracks.every(track => track.stopped));
    assert.equal(await h.controller.start(), true);
    pending.resolve();
    assert.equal(await stopping, false);
    h.asr(1, 4, 'old partial');
    h.asr(1, 5, 'old final', {phase: 'final', stable_prefix: 'old final'});
    h.event(1, 'transcript', {text: 'old canonical'});
    h.asr(1, 6, '', {phase: 'failure', error_code: 'asr_failed'});
    h.state(1, 'FAILED', {error_code: 'asr_failed'});
    h.audio(1, 1);
    await tick();
    assert.equal(h.controller.lastSnapshot.operation_id, 'op-2');
    assert.equal(h.calls.transcript.length, 0);
    assert.equal(h.calls.commit.length, 0);
    assert.equal(h.nodes['voice-live-transcript'].hidden, true);
    assert.deepEqual(h.calls.cancel, ['op-1']);
    await h.controller.cancel();
  }
});

test('session switch while recording cancels capture before sending more PCM or accepting hypotheses', async () => {
  let sessionID = 'session';
  const h = harness({getSessionID: () => sessionID});
  await h.controller.start();
  sessionID = 'new-session';
  h.contexts[0].capture();
  h.asr(1, 1, 'old hypothesis');
  await tick();
  assert.deepEqual(h.calls.cancel, ['op-1']);
  assert.equal(h.calls.append.length, 0);
  assert.equal(h.calls.transcript.length, 0);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
});

test('failure timeout and cancellation updates clear private hypotheses and restore text input availability', async () => {
  for (const phase of ['failure', 'timeout', 'canceled']) {
    const h = harness();
    await h.controller.start();
    h.asr(1, 1, 'private partial');
    h.asr(1, 2, '', {phase, error_code: 'private provider diagnostic'});
    await tick();
    assert.equal(h.controller.isActive(), false);
    assert.equal(h.calls.transcript.length, 0);
    assert.equal(h.controller.lastSnapshot.transcript, undefined);
    assert.equal(h.nodes['voice-transcript-revisable'].textContent, '');
    assert.equal(h.nodes['voice-record'].disabled, false);
    assert.doesNotMatch(h.nodes['voice-status'].textContent, /private/);
    if (phase === 'canceled') assert.deepEqual(h.calls.cancel, ['op-1']);
    else assert.deepEqual(h.calls.fail, [['op-1', phase === 'timeout' ? 'asr_timeout' : 'asr_failed']]);
  }
});

test('invalid stable prefixes and oversized hypotheses fail with a fixed protocol code', async () => {
  for (const update of [
    {phase: 'partial', transcript: 'text', stable_prefix: 'different'},
    {phase: 'final', transcript: 'text', stable_prefix: ''},
    {phase: 'partial', transcript: 'x'.repeat(16001)},
  ]) {
    const h = harness();
    await h.controller.start();
    h.asr(1, 1, '', update);
    await tick();
    assert.deepEqual(h.calls.fail, [['op-1', 'asr_protocol_error']]);
    assert.equal(h.calls.transcript.length, 0);
    assert.ok(h.streams[0].tracks.every(track => track.stopped));
  }
});

test('provider-guaranteed stable text cannot shrink or change on a later partial revision', async () => {
  for (const next of [
    {transcript: '確定した文章の続き', stable_prefix: '確定'},
    {transcript: '変更した文章', stable_prefix: '変更した文章'},
    {transcript: '確定した文章の続き', stable_prefix: ''},
  ]) {
    const h = harness();
    await h.controller.start();
    h.asr(1, 1, '確定した文章の', {phase: 'stable', stable_prefix: '確定した文章'});
    h.asr(1, 2, '', next);
    await tick();
    assert.deepEqual(h.calls.fail, [['op-1', 'asr_protocol_error']]);
    assert.equal(h.calls.transcript.length, 0);
  }
});

test('a coalesced final arriving after the canonical transcript cannot reopen the live view', async () => {
  const h = harness();
  await h.controller.start();
  h.contexts[0].capture();
  await h.controller.stop();
  h.event(1, 'transcript', {text: '確定'});
  h.asr(1, 1, '確定', {phase: 'final', stable_prefix: '確定'});
  assert.equal(h.nodes['voice-live-transcript'].hidden, true);
  assert.equal(h.calls.transcript.length, 1);
  assert.equal(h.calls.end.length, 1);
  await h.controller.cancel();
});

test('synchronous provider final during Append waits for that append and calls End once', async () => {
  const append = deferred();
  let h;
  let ends = 0;
  h = harness({bridge: {
    AppendInteractionAudio() {
      h.asr(1, 1, '同期final', {phase: 'final', stable_prefix: '同期final'});
      return append.promise;
    },
    EndInteractionASR() {
      ends += 1;
      h.asr(1, 1, '同期final', {phase: 'final', stable_prefix: '同期final'});
      h.event(1, 'transcript', {text: '同期final'});
      return Promise.resolve();
    },
  }});
  await h.controller.start();
  h.contexts[0].capture();
  await tick();
  assert.equal(ends, 0);
  assert.equal(h.calls.transcript.length, 0);
  append.resolve();
  await tick();
  assert.equal(ends, 1);
  assert.deepEqual(h.calls.transcript.map(call => call[1]), ['同期final']);
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  await h.controller.cancel();
});

test('a final while microphone permission is pending closes the late stream without attaching capture', async () => {
  const permission = deferred();
  const h = harness({mediaDevices: {getUserMedia: () => permission.promise}});
  const starting = h.controller.start();
  await tick();
  h.asr(1, 1, '早期final', {phase: 'final', stable_prefix: '早期final'});
  const late = stream();
  permission.resolve(late);
  assert.equal(await starting, false);
  await tick();
  assert.ok(late.tracks.every(track => track.stopped));
  assert.equal(h.contexts[0].processor, undefined);
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.calls.end.length, 1);
  await h.controller.cancel();
});

test('premature canonical callbacks during Open do not seal or persist an unconfirmed ASR segment', async () => {
  const open = deferred();
  const h = harness({bridge: {BeginInteractionASR: () => open.promise}});
  const starting = h.controller.start();
  await tick();
  h.event(1, 'transcript', {text: 'premature'});
  open.resolve(asrSession(1));
  await starting;
  h.asr(1, 1, 'visible partial');
  assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.controller.lastSnapshot.transcript, undefined);
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'visible partial');
  await h.controller.cancel();
});

for (const [stage, method] of [['begin', 'BeginInteractionASR'], ['append', 'AppendInteractionAudio'], ['end', 'EndInteractionASR']]) {
  test(`${stage} preserves exact ASR bridge codes when rejection precedes the terminal state event`, async () => {
    for (const code of [
      'asr_failed', 'asr_backpressure', 'asr_protocol_error', 'asr_timeout', 'asr_canceled', 'asr_unavailable',
      'asr_on_device_unavailable', 'asr_permission_denied', 'asr_permission_restricted', 'asr_stream_invalid',
      'asr_stream_eof', 'asr_empty_transcript', 'asr_empty_result', 'invalid_audio', 'invalid_voice_config',
    ]) {
      for (const rejection of [code, new Error(code)]) {
        const h = harness({bridge: {[method]: async () => { throw rejection; }}});
        if (stage === 'begin') assert.equal(await h.controller.start(), false);
        else {
          assert.equal(await h.controller.start(), true);
          h.contexts[0].capture();
          if (stage === 'end') assert.equal(await h.controller.stop(), false);
          await tick();
        }
        assert.equal(h.controller.lastSnapshot.error_code, code);
        assert.deepEqual(h.calls.fail, [['op-1', code]]);
        assert.equal(h.calls.failure.length, 1);
        assert.equal(h.calls.transcript.length, 0);
        assert.equal(h.controller.isActive(), false);
        assert.ok(h.streams.every(item => item.tracks.every(track => track.stopped)));
        h.state(1, 'FAILED', {error_code: code});
        assert.equal(h.calls.failure.length, 1);
      }
    }
  });

  test(`${stage} never extracts an ASR code from arbitrary bridge diagnostics`, async () => {
    for (const rejection of [
      'asr_timeout: private path', new Error('asr_permission_denied private transcript'),
      ' asr_timeout', 'asr_timeout\n', {code: 'asr_timeout'}, null,
    ]) {
      const h = harness({bridge: {[method]: async () => { throw rejection; }}});
      if (stage === 'begin') await h.controller.start();
      else {
        await h.controller.start();
        h.contexts[0].capture();
        if (stage === 'end') await h.controller.stop();
        await tick();
      }
      const fallback = stage === 'begin' ? 'asr_unavailable' : 'asr_failed';
      assert.equal(h.controller.lastSnapshot.error_code, fallback);
      assert.deepEqual(h.calls.fail, [['op-1', fallback]]);
      assert.doesNotMatch(h.nodes['voice-status'].textContent, /private|path|transcript/);
      assert.equal(h.calls.transcript.length, 0);
    }
  });
}

test('voice profile setup failures retain fixed codes without opening ASR or a microphone', async () => {
  for (const code of ['voice_profile_unavailable', 'invalid_voice_profile', 'unsupported_voice_control']) {
    for (const rejection of [code, new Error(code)]) {
      const h = harness({bridge: {StartInteraction: async () => { throw rejection; }}});
      assert.equal(await h.controller.start(), false);
      assert.equal(h.controller.lastSnapshot.error_code, code);
      assert.equal(h.calls.begin.length, 0);
      assert.equal(h.streams.length, 0);
      assert.equal(h.calls.fail.length, 0);
      assert.equal(h.calls.transcript.length, 0);
      assert.equal(h.controller.isActive(), false);
      assert.match(h.nodes['voice-status'].textContent, /声/);
      assert.match(h.nodes['voice-status'].textContent, /テキスト入力/);
      assert.deepEqual(h.calls.busy, [[true], [false]]);
    }
  }
  const h = harness({bridge: {StartInteraction: async () => { throw new Error('unsupported_voice_control PRIVATE provider diagnostic'); }}});
  await h.controller.start();
  assert.equal(h.controller.lastSnapshot.error_code, 'microphone_unavailable');
  assert.doesNotMatch(h.nodes['voice-status'].textContent, /PRIVATE|provider|diagnostic/);
});

function tickContinuous(h) {
  for (const [id, timer] of [...h.timeouts]) if (timer.milliseconds === 20) { h.timeouts.delete(id); timer.callback(); }
}
const speechFrame = () => new Float32Array(512).fill(.1);
const interruptionFrame = () => new Float32Array(4800).fill(.1);
async function interruptSpeech(h) { h.contexts[0].capture(interruptionFrame()); await tick(); await tick(); }

test('C1 four completed turns share one capture and output context，history is refreshed before relisten', async () => {
  let time = 0, completed = 0;
  const h = harness({now: () => time, getRequest: () => ({session_id: 'session', chat: {messages: Array(completed).fill({role: 'user', content: 'confirmed'})}}), onComplete: () => completed++});
  assert.equal(await h.controller.startSession(), true);
  for (let id = 1; id <= 4; id++) {
    h.contexts[0].capture(speechFrame());
    h.asr(id, 1, id % 2 ? 'うん' : 'はい');
    assert.equal(h.calls.transcript.length, id - 1);
    assert.equal(h.nodes['voice-transcript-stable'].textContent, '');
    time += 900; tickContinuous(h); assert.equal(h.calls.end.length, id - 1);
    time += 100; tickContinuous(h); await tick();
    assert.equal(h.calls.end.length, id);
    h.event(id, 'transcript', {text: id % 2 ? 'うん' : 'はい'});
    h.state(id, 'COMPLETED'); await tick();
    assert.equal(h.calls.start[id].chat.messages.length, id);
    assert.equal(h.streams.length, 1); assert.equal(h.contexts.length, 2);
    assert.equal(h.streams[0].tracks[0].stopped, false);
    assert.match(h.nodes['voice-status'].textContent, /次の発話/);
  }
  assert.equal(h.calls.transcript.length, 4);
  await h.controller.pauseSession();
  assert.equal(h.controller.sessionSnapshot.state, 'paused'); assert.equal(h.streams[0].tracks[0].stopped, true);
  assert.equal(await h.controller.startSession(), true);
  assert.equal(h.calls.start.at(-1).voice_session_epoch, 2);
  assert.equal(h.streams.length, 2);
  await h.controller.endSession(); assert.equal(h.controller.sessionSnapshot.state, 'stopped');
  assert.ok(h.streams.every(s => s.tracks.every(t => t.stopped)));
});
test('C1 silence refreshes idle ASR without End or transcript and pauses after five minutes', async () => {
  let time = 0;
  const h = harness({now: () => time}); await h.controller.startSession();
  for (let i = 1; i <= 5; i++) { time = i * 60000; tickContinuous(h); await tick(); }
  assert.equal(h.calls.end.length, 0); assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.controller.sessionSnapshot.state, 'paused'); assert.equal(h.streams.length, 1);
  assert.ok(h.streams[0].tracks.every(t => t.stopped));
});
test('C1 early final drains once and idle early final never enters conversation', async () => {
  const h = harness(); await h.controller.startSession();
  h.asr(1, 1, '幻覚', {phase: 'final', stable_prefix: '幻覚'}); await tick();
  assert.equal(h.calls.end.length, 0); assert.equal(h.calls.transcript.length, 0);
  h.contexts[0].capture(speechFrame());
  h.asr(2, 1, 'はい', {phase: 'final', stable_prefix: 'はい'}); await tick();
  h.asr(2, 1, 'はい', {phase: 'final', stable_prefix: 'はい'}); await tick();
  assert.equal(h.calls.end.length, 1); assert.equal(h.calls.transcript.length, 0);
  h.event(2, 'transcript', {text: 'はい'}); h.event(2, 'transcript', {text: 'はい'});
  assert.equal(h.calls.transcript.length, 1); await h.controller.endSession();
});
test('C1 permission resolving after end stops its late track without starting ASR', async () => {
  const permission = deferred(); const late = stream();
  const h = harness({mediaDevices: {getUserMedia: () => permission.promise}});
  const started = h.controller.startSession(); await tick(); await h.controller.endSession();
  permission.resolve(late); assert.equal(await started, false);
  assert.ok(late.tracks.every(t => t.stopped)); assert.equal(h.calls.start.length, 0);
});
test('C1 device end and delayed completed callbacks cannot reacquire capture', async () => {
  const h = harness(); await h.controller.startSession();
  h.streams[0].tracks[0].onended(); await tick();
  h.state(1, 'COMPLETED'); await tick();
  assert.equal(h.controller.sessionSnapshot.state, 'paused'); assert.equal(h.streams.length, 1);
  assert.equal(h.calls.start.length, 1);
});
test('C1 60-second continuous speech pauses without sending a truncated final，preview can be edited', async () => {
  let time = 0; const h = harness({now: () => time}); await h.controller.startSession();
  h.contexts[0].capture(speechFrame()); h.asr(1, 1, '続けている内容');
  time = 60000; h.contexts[0].capture(speechFrame()); tickContinuous(h); await tick();
  assert.equal(h.calls.end.length, 0); assert.equal(h.calls.transcript.length, 0);
  assert.equal(h.calls.failure[0][0].error_code, 'utterance_limit');
  assert.equal(h.controller.lastSnapshot.preview, '続けている内容'); assert.equal(h.nodes['voice-fallback'].hidden, false);
  assert.equal(h.controller.sessionSnapshot.state, 'paused');
});
test('C1 session endpoint waits for pending PCM append before End and pause discards it', async () => {
  const pending = deferred(); let time = 0;
  const h = harness({now: () => time, bridge: {AppendInteractionAudio: () => pending.promise}});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); h.asr(1, 1, 'はい');
  time = 900; tickContinuous(h); time = 1000; tickContinuous(h); await tick();
  assert.equal(h.calls.end.length, 0); await h.controller.pauseSession(); pending.resolve(); await tick();
  assert.equal(h.calls.end.length, 0); assert.equal(h.calls.transcript.length, 0);
});
test('C1 pause during output stops sources synchronously and keeps no microphone track', async () => {
  const h = harness(); await h.controller.startSession();
  h.contexts[0].capture(speechFrame()); h.asr(1, 1, 'はい', {phase: 'final', stable_prefix: 'はい'}); await tick();
  h.event(1, 'transcript', {text: 'はい'}); h.audio(1, 1); await tick();
  const source = h.contexts[1].sources[0]; assert.equal(source.started, true);
  const paused = h.controller.pauseSession();
  assert.equal(source.stopped, true); assert.ok(h.streams[0].tracks.every(t => t.stopped));
  await paused; h.audio(1, 2); await tick(); assert.equal(h.contexts[1].sources.length, 1);
});
test('C1 backpressure pauses the session and never sends overflowed audio', async () => {
  const pending = deferred(); const h = harness({bridge: {AppendInteractionAudio: () => pending.promise}});
  await h.controller.startSession();
  for (let i = 0; i < 33; i++) h.contexts[0].capture(speechFrame());
  await tick(); assert.equal(h.calls.failure[0][0].error_code, 'asr_backpressure');
  assert.equal(h.controller.sessionSnapshot.state, 'paused'); assert.equal(h.calls.end.length, 0);
  pending.resolve(); await tick(); assert.equal(h.calls.transcript.length, 0);
});
test('C1 end supersedes a pending pause before a new conversation starts', async () => {
  const paused = deferred(); const changes = [];
  const h = harness({bridge: {ChangeVoiceSession: async (id, epoch, action) => {
    changes.push(action); if (action === 'pause') await paused.promise;
    return {id, epoch, conversation_id: 'session', state: action === 'end' ? 'stopped' : 'paused'};
  }}});
  await h.controller.startSession(); const pause = h.controller.pauseSession(); await tick();
  const end = h.controller.endSession(); paused.resolve(); await Promise.all([pause, end]);
  assert.deepEqual(changes, ['pause', 'end']); assert.equal(h.controller.sessionSnapshot.state, 'stopped');
  await h.controller.startSession(); assert.equal(h.streams.length, 2); await h.controller.endSession();
});
test('C1 startup cancellation fences a delayed backend session and never acquires a microphone', async () => {
  const pending = deferred(); const h = harness({bridge: {StartVoiceSession: () => pending.promise}});
  const start = h.controller.startSession(); const end = h.controller.endSession();
  pending.resolve({id: 'voice-1', epoch: 1, conversation_id: 'session', state: 'starting'});
  await Promise.all([start, end]); assert.equal(h.streams.length, 0); assert.equal(h.calls.start.length, 0);
  assert.equal(h.controller.sessionSnapshot.state, 'stopped'); assert.ok(h.contexts.every(c => c.closed));
});
test('C1 never opens a second permission request while canceled capture is still pending', async () => {
  const permission = deferred(); const late = stream(); let acquisitions = 0;
  const h = harness({mediaDevices: {getUserMedia: () => { acquisitions++; return permission.promise; }}});
  const started = h.controller.startSession(); await tick(); await h.controller.endSession();
  assert.equal(await h.controller.startSession(), false); assert.equal(acquisitions, 1);
  permission.resolve(late); await started; assert.ok(late.tracks.every(t => t.stopped));
});

for (const phase of ['llm_wait', 'filler', 'tts_wait', 'body']) {
  test(`C1 Step2 ${phase} stops locally and preserves the exact onset across cancel and ASR startup`, async () => {
    let time = 0;
    const canceled = deferred(), opened = deferred(), requests = [];
    const h = harness({now: () => time, bridge: {
      InterruptInteraction(request) { requests.push(request); return canceled.promise; },
      BeginInteractionASR(operationID, sampleRate) {
        h.calls.begin.push([operationID, sampleRate]);
        return operationID === 'op-2' ? opened.promise : Promise.resolve(asrSession(1, sampleRate));
      },
      GetInteractionFiller: async () => fillerSetup(),
    }});
    await h.controller.startSession();
    h.contexts[0].capture(speechFrame()); await h.controller.stop();
    h.event(1, 'transcript', {text: '四季を説明して'}); h.state(1, 'THINKING');
    const input = h.contexts[0], output = h.contexts[1];
    if (phase === 'filler') {
      output.decode = async () => ({duration: 1});
      for (const [name, at] of [['endpoint_commit', 0], ['llm_requested', 20], ['llm_identity_ready', 30]]) {
        time = at; h.event(1, 'trace', {trace: {name, monotonic_ms: at}});
      }
      await tick(); await tick();
      for (const [name, at] of [['llm_first_token', 900], ['tts_requested', 1100]]) {
        time = at; h.event(1, 'trace', {trace: {name, monotonic_ms: at}});
      }
      time = 3850; for (const timer of [...h.timeouts.values()]) timer.callback();
      assert.equal(output.sources[0].started, true);
    }
    if (phase === 'tts_wait') h.state(1, 'SYNTHESIZING');
    if (phase === 'body') { h.state(1, 'SYNTHESIZING'); h.audio(1, 1); await tick(); }
    const oldSource = output.sources[0], oldEnd = oldSource?.onended;
    const before = new Float32Array(128), onset1 = new Float32Array(1280).fill(.0625), onset2 = new Float32Array(1600).fill(.125);
    input.capture(before); input.capture(onset1);
    assert.equal(requests.length, 0);
    input.capture(onset2);
    await tick();
    assert.equal(requests.length, 1);
    assert.equal(requests[0].operation_id, 'op-1');
    assert.equal(requests[0].generation_revision, 1);
    assert.equal(requests[0].local_stop_ms, 0);
    if (oldSource) assert.equal(oldSource.stopped, true);
    assert.equal(h.streams.length, 1); assert.equal(h.streams[0].tracks[0].stopped, false);
    input.capture(new Float32Array(256).fill(.1875));
    canceled.resolve(snapshot(1, 'CANCELED', {interruption: {local_stop_ms: 0}})); await tick();
    input.capture(new Float32Array(256).fill(.25));
    assert.equal(h.calls.begin.length, 2);
    assert.equal(h.calls.append.filter(([op]) => op === 'op-2').length, 0);
    opened.resolve(asrSession(2)); await tick(); await tick();
    const bytes = Buffer.concat(h.calls.append.filter(([op]) => op === 'op-2').map(([, , data]) => Buffer.from(data, 'base64')));
    const actual = Array.from({length: bytes.length / 2}, (_, i) => bytes.readInt16LE(i * 2));
    assert.deepEqual(actual, [...Array(128).fill(0), ...Array(1280).fill(2048), ...Array(1600).fill(4096), ...Array(256).fill(6144), ...Array(256).fill(8192)]);
    oldEnd?.(); h.audio(1, 2); h.event(1, 'token', {text: 'old answer'});
    h.asr(1, 8, '旧final', {phase: 'final', stable_prefix: '旧final'}); h.event(1, 'transcript', {text: '旧canonical'});
    h.asr(2, 1, 'いや，冬の話だけ聞きたい', {phase: 'final', stable_prefix: 'いや，冬の話だけ聞きたい'});
    await tick();
    h.event(2, 'transcript', {text: 'いや，冬の話だけ聞きたい'});
    h.event(2, 'transcript', {text: 'いや，冬の話だけ聞きたい'});
    assert.deepEqual(h.calls.transcript.map(([, text]) => text), ['四季を説明して', 'いや，冬の話だけ聞きたい']);
    assert.equal(h.calls.token.length, 0);
    assert.equal(h.calls.end.filter(([op]) => op === 'op-2').length, 1);
    assert.equal(output.sources.length, oldSource ? 1 : 0);
    await h.controller.endSession();
  });
}

test('C1 Step2 repeated interruptions keep one capture and distinct ASRs，without fixed acknowledgements', async () => {
  const h = harness(); await h.controller.startSession();
  for (let id = 1; id <= 3; id++) {
    h.contexts[0].capture(speechFrame()); await h.controller.stop();
    h.event(id, 'transcript', {text: `確定${id}`}); h.state(id, 'SYNTHESIZING'); h.audio(id, 1); await tick();
    await interruptSpeech(h);
    assert.equal(h.controller.lastSnapshot.operation_id, `op-${id + 1}`);
    assert.equal(h.calls.interrupts.length, id);
    assert.equal(h.calls.begin.length, id + 1);
    assert.equal(h.streams.length, 1); assert.equal(h.contexts.length, 2);
    assert.equal(h.contexts[1].sources.length, id);
  }
  await h.controller.pauseSession(); await h.controller.startSession();
  assert.equal(h.calls.start.at(-1).voice_session_epoch, 2);
  await h.controller.endSession();
});

test('C1 Step2 a full handoff buffer pauses instead of discarding the onset', async () => {
  const canceled = deferred();
  const h = harness({bridge: {InterruptInteraction: () => canceled.promise}});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop();
  h.state(1, 'THINKING'); await interruptSpeech(h);
  h.contexts[0].capture(new Float32Array(32000).fill(.1));
  assert.ok(h.streams[0].tracks.every(track => track.stopped));
  assert.equal(h.controller.sessionSnapshot.state, 'paused');
  assert.match(h.nodes['voice-status'].textContent, /引継ぎが間に合わなかった/);
  canceled.resolve(snapshot(1, 'CANCELED')); await tick();
  assert.equal(h.calls.begin.length, 1);
  await h.controller.endSession();
});

test('C1 Step2 local natural ending survives a delayed started ACK during interruption', async () => {
  const started = deferred(), reports = [];
  const h = harness({bridge: {
    InteractionPlayback: (_op, _seq, phase) => phase === 'started' ? started.promise : Promise.resolve(),
    InterruptInteraction: async request => { reports.push(request); return snapshot(1, 'CANCELED', {interruption: {local_stop_ms: 0}}); },
  }});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop();
  h.state(1, 'SYNTHESIZING'); h.audio(1, 1); await tick();
  h.contexts[1].sources[0].end(); await interruptSpeech(h);
  assert.deepEqual(reports[0].playback, [{sequence: 1, state: 'completed'}]);
  await tick(); started.resolve(); await tick();
  assert.equal(h.controller.lastSnapshot.operation_id, 'op-2');
  await h.controller.endSession();
});

test('C1 Step2 next Work request inherits settings and only the proven spoken prefix', async () => {
  const {resumeVoiceEntry, settleVoiceEntry} = await import('./voiceConversation.js');
  const {conversationHistory} = await import('./conversationHistory.js');
  const fixture = JSON.parse((await import('node:fs')).readFileSync(new URL('../../../schemas/karte-ephy/v2/fixtures/runtime-delivery.scenario.json', import.meta.url))).cases[0];
  const entries = [{role: 'user', text: '四季を説明して'}];
  const settings = {speech: {voice_profile_id: 'selected-voice', style: {pace: 1.1, volume: .7}}, chat: {mode: 'work', project: 'selected-project', source_scope: 'personal_context', max_tokens: 512, temperature: .2}};
  const h = harness({
    getRequest: () => ({session_id: 'session', speech: settings.speech, chat: {...settings.chat, messages: conversationHistory(entries)}}),
    onTranscript(s) { if (s.operation_id === 'op-1') entries.push(resumeVoiceEntry({requestId: s.operation_id, role: 'assistant'}, s)); },
    onCancel(s) { if (s.operation_id === 'op-1') entries[1] = settleVoiceEntry(entries[1], s); },
    bridge: {InterruptInteraction: async () => ({...fixture.snapshot, ...snapshot(1, 'CANCELED'), generation_revision: 1})},
  });
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop();
  h.event(1, 'transcript', {text: '四季を説明して'}); h.state(1, 'THINKING');
  await interruptSpeech(h);
  const next = h.calls.start[1];
  assert.deepEqual(next.speech, settings.speech);
  for (const [key, value] of Object.entries(settings.chat)) assert.deepEqual(next.chat[key], value);
  assert.equal(next.chat.messages.length, 2);
  assert.ok(next.chat.messages[1].content.endsWith('春には花が咲きます．'));
  assert.ok(!next.chat.messages[1].content.includes('冬は雪が降ります．'));
  assert.match(next.chat.messages[1].content, /中断された/);
  h.asr(2, 1, 'いや，冬の話だけ聞きたい', {phase: 'final', stable_prefix: 'いや，冬の話だけ聞きたい'});
  await tick();
  h.event(2, 'transcript', {text: 'いや，冬の話だけ聞きたい'});
  h.event(2, 'transcript', {text: '遅れて届いた別のfinal'});
  assert.equal(h.controller.lastSnapshot.transcript, 'いや，冬の話だけ聞きたい');
  await h.controller.endSession();
});

test('C1 Step2 failed source.stop still mutes body and keeps the next ASR independent', async () => {
  const h = harness(); await h.controller.startSession();
  const output = h.contexts[1], gains = [];
  // The active run already owns its body gain；retain it through source.connect．
  const originalSource = output.createBufferSource.bind(output);
  output.createBufferSource = () => { const source = originalSource(); source.connect = target => gains.push(target); return source; };
  h.contexts[0].capture(speechFrame()); await h.controller.stop(); h.state(1, 'SYNTHESIZING'); h.audio(1, 1); await tick();
  output.sources[0].stop = () => { throw new Error('device stop failed'); };
  await interruptSpeech(h);
  assert.equal(gains[0].gain.value, 0);
  assert.equal(gains[0].disconnected, true);
  await tick(); await tick();
  assert.equal(h.controller.lastSnapshot.operation_id, 'op-2');
  assert.equal(h.calls.interrupts.length, 1);
  assert.ok(h.streams[0].tracks.every(t => !t.stopped));
  await h.controller.endSession();
});

test('C1 Step2 provider final before handoff drain pauses without accepting a truncated canonical input', async () => {
  const pending = deferred(); let once = false;
  const h = harness({bridge: {
    InterruptInteraction: () => pending.promise,
    AppendInteractionAudio(op, sequence, bytes) {
      h.calls.append.push([op, sequence, bytes]);
      if (op === 'op-2' && !once) { once = true; h.asr(2, 1, 'いや', {phase: 'final', stable_prefix: 'いや'}); }
      return Promise.resolve();
    },
  }});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop();
  h.event(1, 'transcript', {text: '四季を説明して'}); h.state(1, 'THINKING');
  await interruptSpeech(h); h.contexts[0].capture(speechFrame());
  pending.resolve(snapshot(1, 'CANCELED', {interruption: {local_stop_ms: 0}})); await tick(); await tick();
  assert.equal(h.controller.lastSnapshot.state, 'FAILED');
  assert.equal(h.controller.lastSnapshot.error_code, 'asr_protocol_error');
  assert.equal(h.controller.sessionSnapshot.state, 'paused');
  h.event(2, 'transcript', {text: 'いや'});
  assert.equal(h.calls.transcript.length, 1);
  assert.equal(h.calls.end.filter(([op]) => op === 'op-2').length, 0);
  assert.equal(h.controller.lastSnapshot.preview, 'いや');
  await h.controller.endSession();
});

for (const phase of ['THINKING', 'SYNTHESIZING', 'PLAYING']) test(`noise bursts preserve ${phase} and later output events`, async () => {
  const h = harness({endpointConfig: {onsetMS: 1, rms: .001}});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop();
  h.event(1, 'transcript', {text: '説明して'}); h.state(1, phase === 'PLAYING' ? 'SYNTHESIZING' : phase);
  if (phase === 'PLAYING') { h.audio(1, 1); await tick(); }
  for (let i = 0; i < 15; i++) { h.contexts[0].capture(new Float32Array(640).fill(.8)); h.contexts[0].capture(new Float32Array(960)); }
  await tick();
  assert.equal(h.calls.candidateBegin.length, 0); assert.equal(h.calls.interrupts.length, 0);
  assert.equal(h.calls.cancel.length, 0); assert.equal(h.streams[0].tracks[0].stopped, false);
  h.event(1, 'token', {text: '継続する返答'}); assert.equal(h.calls.token.at(-1)[1], '継続する返答');
  if (phase === 'PLAYING') assert.equal(h.contexts[1].sources[0].stopped, false);
  await h.controller.endSession();
});

for (const reason of ['asr_failure', 'acknowledgement', 'missing_recognition']) test(`${reason} restores playback without canceling its producer or opening a new turn`, async () => {
  const h = harness({bridge: {async AppendInteractionInterruptionCandidate(op, id) {
    if (reason === 'asr_failure') throw new Error('asr_failed');
    const request = {operation_id: op, session_id: 'session', turn_id: 'turn-1', segment_id: `candidate-segment-${op}`, sample_rate: 16000};
    return {candidate_id: id, request, ...(reason === 'acknowledgement' ? {update: {...request, revision: 1, phase: 'final', transcript: 'うん'}} : {})};
  }}});
  await h.controller.startSession(); const output = h.contexts[1]; let gain;
  const originalSource = output.createBufferSource.bind(output);
  output.createBufferSource = () => { const source = originalSource(); source.connect = target => { gain = target; }; return source; };
  h.contexts[0].capture(speechFrame()); await h.controller.stop(); h.state(1, 'SYNTHESIZING'); h.audio(1, 1); await tick();
  h.contexts[0].capture(interruptionFrame()); await tick();
  if (reason !== 'asr_failure') { assert.equal(gain.gain.value, .3); for (const timer of [...h.timeouts.values()]) if (timer.milliseconds === 1200) timer.callback(); }
  await tick();
  assert.equal(gain.gain.value, 1); assert.equal(output.sources[0].stopped, false);
  assert.equal(h.calls.interrupts.length, 0); assert.equal(h.calls.cancel.length, 0);
  assert.equal(h.calls.failure.length, 0); assert.equal(h.calls.start.length, 1);
  assert.ok(h.streams[0].tracks.every(t => !t.stopped));
  h.event(1, 'token', {text: '元の返答を継続'}); assert.equal(h.calls.token.at(-1)[1], '元の返答を継続');
  await h.controller.endSession();
});

test('reply completing during candidate recognition preserves onset for the next ordinary input', async () => {
  const opened = deferred(); const h = harness({bridge: {BeginInteractionInterruptionCandidate: () => opened.promise}});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop(); h.state(1, 'SYNTHESIZING');
  h.contexts[0].capture(interruptionFrame()); await tick(); h.state(1, 'COMPLETED'); await tick(); await tick();
  assert.equal(h.calls.interrupts.length, 0); assert.equal(h.calls.start.length, 2);
  assert.equal(h.calls.begin.length, 2); assert.ok(h.calls.append.some(([op]) => op === 'op-2'));
  const bytes = Buffer.concat(h.calls.append.filter(([op]) => op === 'op-2').map(([, , body]) => Buffer.from(body, 'base64')));
  assert.equal(bytes.length, 4800 * 2); assert.equal(bytes.readInt16LE(0), 3277);
  opened.resolve({candidate_id: 'late-candidate', request: {operation_id: 'op-1', session_id: 'session', turn_id: 'turn-1', segment_id: 'late', sample_rate: 16000}}); await tick();
  assert.equal(h.calls.candidateCancel.at(-1)[2], 'completed'); assert.equal(h.calls.cancel.length, 0);
  await h.controller.endSession();
});

test('pause and resume fence a late candidate startup without reactivating its microphone', async () => {
  const opened = deferred(); const h = harness({bridge: {BeginInteractionInterruptionCandidate: () => opened.promise}});
  await h.controller.startSession(); h.contexts[0].capture(speechFrame()); await h.controller.stop(); h.state(1, 'THINKING');
  h.contexts[0].capture(interruptionFrame()); await tick(); await h.controller.pauseSession(); await h.controller.startSession();
  opened.resolve({candidate_id: 'old-candidate', request: {operation_id: 'op-1', session_id: 'session', turn_id: 'turn-1', segment_id: 'old', sample_rate: 16000}}); await tick();
  assert.equal(h.calls.interrupts.length, 0); assert.equal(h.calls.candidateCancel.at(-1)[2], 'detached');
  assert.ok(h.streams[0].tracks.every(t => t.stopped)); assert.ok(h.streams[1].tracks.every(t => !t.stopped));
  await h.controller.endSession();
});

const whisperReadiness = () => ({state: 'ready', can_start: true, provider: 'whisper-cpp', model: 'large-v3-turbo-f16', capabilities: {partial: true, activity: true, no_speech: true}});
test('Whisper continuous capture feeds quiet PCM and activity preserves revisable text', async () => {
  let time = 0;
  const h = harness({now: () => time, bridge: {GetInteractionASRReadiness: async () => whisperReadiness()}});
  assert.equal(await h.controller.startSession(), true);
  h.contexts[0].capture(new Float32Array(1600).fill(.001)); await tick();
  assert.equal(h.calls.append.length, 1);
  h.asr(1, 1, 'はい');
  h.asr(1, 2, '', {phase: 'activity', activity: {audio_ms: 96, last_speech_ms: 96, speech_ms: 64, speaking: true, has_speech: true}});
  assert.equal(h.nodes['voice-transcript-revisable'].textContent, 'はい');
  assert.match(h.nodes['voice-status'].textContent, /聞いています/);
  assert.equal(h.calls.transcript.length, 0);
  time = 1000; h.contexts[0].capture(new Float32Array(16000)); await tick();
  // Do not use an obsolete activity timestamp while new input awaits VAD．
  tickContinuous(h); assert.equal(h.calls.end.length, 0);
  h.asr(1, 3, '', {phase: 'activity', activity: {audio_ms: 1088, last_speech_ms: 96, speech_ms: 64, speaking: false, has_speech: true}});
  tickContinuous(h); time += 100; tickContinuous(h); await tick();
  assert.equal(h.calls.end.length, 1);
  h.asr(1, 4, 'はい', {phase: 'final', stable_prefix: 'はい'});
  h.event(1, 'transcript', {text: 'はい'}); h.event(1, 'transcript', {text: 'はい'});
  assert.equal(h.calls.transcript.length, 1);
  await h.controller.endSession();
});

test('Whisper no-speech returns to listening without Conversation or cancel placeholder', async () => {
  let time = 0;
  const h = harness({now: () => time, bridge: {GetInteractionASRReadiness: async () => whisperReadiness()}});
  await h.controller.startSession();
  h.contexts[0].capture(new Float32Array(1600)); await tick();
  time = 59000; tickContinuous(h); await tick();
  assert.equal(h.calls.end.length, 1);
  h.asr(1, 1, '', {phase: 'no_speech'});
  h.state(1, 'CANCELED', {input_outcome: 'no_speech'}); await tick();
  assert.equal(h.calls.start.length, 2);
  assert.equal(h.calls.transcript.length, 0); assert.equal(h.calls.canceled.length, 0);
  assert.equal(h.streams.length, 1); assert.equal(h.streams[0].tracks[0].stopped, false);
  // An old terminal event must not touch the replacement recognizer．
  h.asr(1, 2, 'old', {phase: 'final', stable_prefix: 'old'});
  assert.equal(h.calls.transcript.length, 0);
  await h.controller.endSession();
});

test('Whisper readiness polls warmup，disables starting and displays selected model', async () => {
  let checks = 0;
  const h = harness({showASR: true, bridge: {GetInteractionASRReadiness: async () => ++checks === 1
    ? {...whisperReadiness(), state: 'loading', can_start: false} : whisperReadiness()}});
  await tick();
  assert.equal(h.nodes['voice-session'].disabled, true); assert.equal(h.nodes['voice-record'].disabled, true);
  assert.match(h.nodes['voice-asr-status'].textContent, /large-v3-turbo-f16.*準備中/);
  for (const [id, timer] of [...h.timeouts]) if (timer.milliseconds === 1000) { h.timeouts.delete(id); timer.callback(); }
  await tick(); assert.equal(h.nodes['voice-session'].disabled, false);
  assert.match(h.nodes['voice-asr-status'].textContent, /準備完了/);
  assert.equal(h.streams.length, 0);
  await h.controller.dispose(); assert.equal(h.timeouts.size, 0);
});
