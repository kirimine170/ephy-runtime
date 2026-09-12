import test from 'node:test';
import assert from 'node:assert/strict';
import {createVoiceInterruption, interruptionIntent, interruptionSettings} from './voiceInterruption.js';
import {createVoiceInputBuffer} from './voiceInputBuffer.js';
import {createPCM16StreamEncoder} from './voiceInteraction.js';
const tick = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return {promise, resolve}; };
function fixture(options = {}) {
  let at = 0, phase = options.phase || 'SYNTHESIZING', update = null;
  const timers = new Map(), calls = {begin: [], append: [], cancel: [], duck: [], confirmed: 0};
  const request = {operation_id: 'op', session_id: 'session', turn_id: 'turn', segment_id: 'candidate-segment', sample_rate: 16000};
  const snapshot = () => ({candidate_id: 'candidate', request, ...(update ? {update} : {})});
  const buffer = createVoiceInputBuffer(16000);
  const bridge = {
    async BeginInteractionInterruptionCandidate(...args) { calls.begin.push(args); return snapshot(); },
    async AppendInteractionInterruptionCandidate(...args) { calls.append.push(args); return snapshot(); },
    async CancelInteractionInterruptionCandidate(...args) { calls.cancel.push(args); },
    ...options.bridge,
  };
  const gate = createVoiceInterruption({bridge, identity: () => ({...request, generation_revision: 1}),
    sampleRate: 16000, buffer, createEncoder: createPCM16StreamEncoder,
    encodeBase64: bytes => Buffer.from(bytes).toString('base64'), now: () => at,
    timers: {setTimeout(fn, ms) { const id = Symbol(); timers.set(id, {fn, at: at + ms}); return id; }, clearTimeout(id) { timers.delete(id); }},
    isCurrent: options.isCurrent || (() => true), phase: () => phase,
    onDuck: duck => calls.duck.push(duck), onConfirm: () => calls.confirmed++, settings: interruptionSettings(options.settings), createID: () => 'candidate'});
  return {gate, calls, timers, buffer, snapshot,
    hypothesis(text, revision = 1, phase = 'partial', extra = {}) { update = {...request, revision, phase, transcript: text, stable_prefix: phase === 'final' ? text : '', ...extra}; },
    setPhase(value) { phase = value; },
    async audio(ms, amplitude = .1) { at += ms; gate.audio(new Float32Array(ms * 16).fill(amplitude)); await tick(); },
    async advance(ms) { at += ms; for (const [id, timer] of [...timers]) if (timer.at <= at) { timers.delete(id); timer.fn(); } await tick(); },
  };
}

test('recognition distinguishes acknowledgement/call-only from correction and new speech', () => {
  for (const text of ['', '…', '!!!']) assert.equal(interruptionIntent(text), 'empty');
  for (const text of ['うん', 'はい，はい', 'なるほど．', 'そうだね', 'Ephy', 'エフィー', 'ねえ', 'ありがとう']) assert.equal(interruptionIntent(text), 'acknowledgement', text);
  for (const text of ['待って', 'いや，冬だけ', '違うよ', 'ストップ！']) assert.equal(interruptionIntent(text), 'explicit', text);
  for (const text of ['うん，それは違う', 'そうじゃなくて', '明日の予定を教えて', 'エフィ，話を変えよう']) assert.equal(interruptionIntent(text), 'speech', text);
});

test('ordinary input onset settings cannot silently weaken interruption settings', () => {
  const settings = interruptionSettings();
  assert.equal(settings.onsetMS, 80); assert.equal(settings.waitingSpeechMS, 260);
  for (const override of [{onsetMS: 0}, {candidateMS: 2000}, {duckVolume: 2}, {rms: NaN}, {waitingSpeechMS: 50}]) assert.throws(() => interruptionSettings(override));
});

test('clicks，brief impacts and separated noise bursts never open a recognizer or cancel output', async () => {
  const f = fixture();
  for (let n = 0; n < 20; n++) { await f.audio(40, .8); await f.audio(60, 0); }
  assert.equal(f.calls.begin.length, 0); assert.equal(f.calls.confirmed, 0); assert.equal(f.calls.duck.length, 0);
  f.gate.stop();
});

test('longer noise may open a bounded candidate but recognition failure preserves the reply', async () => {
  const f = fixture({bridge: {async AppendInteractionInterruptionCandidate() { throw new Error('asr_failed'); }}});
  await f.audio(100, .1);
  assert.equal(f.calls.confirmed, 0); assert.equal(f.gate.pending, false);
  assert.deepEqual(f.calls.duck, [true, false]); assert.equal(f.buffer.holding, false);
  assert.equal(f.calls.cancel[0][2], 'unavailable'); f.gate.stop();
});

test('unrecognized continuous background is bounded and does not pause or cancel the reply', async () => {
  const f = fixture();
  for (let i = 0; i < 90; i++) await f.audio(40, .03);
  assert.equal(f.calls.confirmed, 0);
  assert.ok(f.calls.begin.length <= 2); assert.ok(f.buffer.samples <= 32000);
  f.gate.stop(); assert.equal(f.timers.size, 0);
});

for (const text of ['うん', 'はい', 'エフィ', 'なるほど']) test(`short acknowledgement ${text} does not cancel generation`, async () => {
  const f = fixture(); f.hypothesis(text, 1, 'final');
  await f.audio(100); await f.audio(160); await f.audio(200, 0); await f.advance(1200);
  assert.equal(f.calls.confirmed, 0); assert.equal(f.gate.pending, false);
  assert.equal(f.calls.cancel.at(-1)[2], 'acknowledgement');
  assert.equal(f.calls.duck.at(-1), false); f.gate.stop();
});

for (const phase of ['THINKING', 'SYNTHESIZING', 'PLAYING']) test(`${phase} waits for recognized correction and preserves every onset sample`, async () => {
  const f = fixture({phase});
  await f.audio(40, 0); await f.audio(80, .0625);
  assert.equal(f.calls.confirmed, 0); assert.equal(f.gate.pending, true);
  f.hypothesis('いや，冬だけ', 1, 'partial');
  await f.audio(100, .125); assert.equal(f.calls.confirmed, 0);
  await f.audio(80, .25);
  assert.equal(f.calls.confirmed, 1); assert.equal(f.buffer.holding, true);
  assert.equal(f.calls.cancel.at(-1)[2], 'confirmed');
  f.gate.stop(); assert.equal(f.buffer.holding, true);
  assert.deepEqual([...f.buffer.snapshot().flatMap(x => [...x])], [...Array(640).fill(0), ...Array(1280).fill(.0625), ...Array(1600).fill(.125), ...Array(1280).fill(.25)]);
  await f.audio(80); assert.equal(f.calls.confirmed, 1);
});

test('waiting reply needs stronger evidence than playback，and short partials may grow into requests', async () => {
  const f = fixture(); f.hypothesis('うん');
  await f.audio(80); await f.audio(120); assert.equal(f.calls.confirmed, 0);
  f.hypothesis('うん，話を変えたい', 2);
  await f.audio(80); await f.audio(160); assert.equal(f.calls.confirmed, 0);
  await f.audio(80); assert.equal(f.calls.confirmed, 1); f.gate.stop();
});

test('unstable，foreign and failed recognizer results never confirm an interruption', async () => {
  for (const extra of [{operation_id: 'other'}, {segment_id: 'old'}, {error_code: 'asr_failed'}, {phase: 'failure'}]) {
    const f = fixture(); f.hypothesis('待って', 1, 'final', extra);
    await f.audio(260); assert.equal(f.calls.confirmed, 0); f.gate.stop();
  }
  const f = fixture(); f.hypothesis('別の話'); await f.audio(280);
  f.hypothesis('違う認識', 2); await f.audio(160);
  assert.equal(f.calls.confirmed, 0); f.gate.stop();
});

test('a canceled or expired startup cannot duck or cancel a later reply', async () => {
  const opened = deferred(); const f = fixture({bridge: {BeginInteractionInterruptionCandidate: () => opened.promise}});
  await f.audio(100); f.gate.stop();
  f.hypothesis('待って', 1, 'final'); opened.resolve(f.snapshot()); await tick();
  assert.equal(f.calls.confirmed, 0); assert.equal(f.calls.duck.at(-1), false);
  assert.equal(f.calls.cancel[0][2], 'detached'); assert.equal(f.timers.size, 0);
});

test('hung append，buffer overflow and missing bridge reject only the candidate', async () => {
  const pending = deferred(); const f = fixture({bridge: {AppendInteractionInterruptionCandidate: () => pending.promise}});
  await f.audio(100); await f.advance(1200);
  assert.equal(f.calls.confirmed, 0); assert.equal(f.gate.pending, false); assert.equal(f.buffer.holding, false);
  pending.resolve(f.snapshot()); await tick(); f.gate.stop();
  const overflow = fixture(); await overflow.audio(100); await overflow.audio(2000);
  assert.equal(overflow.calls.confirmed, 0); assert.equal(overflow.buffer.holding, false); overflow.gate.stop();
  const missing = fixture({bridge: {BeginInteractionInterruptionCandidate: undefined}});
  await missing.audio(300); assert.equal(missing.calls.confirmed, 0); assert.equal(missing.gate.pending, false); missing.gate.stop();
});

test('natural reply completion hands pending input to ordinary ASR and fences late results', async () => {
  const f = fixture(); await f.audio(160);
  assert.equal(f.gate.transfer(), true); f.gate.stop();
  assert.equal(f.buffer.holding, true); assert.equal(f.buffer.samples, 2560);
  f.hypothesis('待って', 1, 'final'); await f.audio(300);
  assert.equal(f.calls.confirmed, 0); assert.equal(f.calls.cancel[0][2], 'completed');
});
