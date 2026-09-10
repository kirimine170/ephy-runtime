import test from 'node:test';
import assert from 'node:assert/strict';
import {mountVoiceProfiles, voiceProfilesMarkup} from './voiceProfiles.js';
import {mountVoiceInteraction} from './voiceInteraction.js';

const tick = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
};
const neutral = () => ({affect: 'neutral', intensity: 1, pace: 1, pitch_hint: 0, volume: 1, pause_style: 'natural', interruptible: true});
const profile = (id = 'personal-voice', extra = {}) => ({
  voice_profile_id: id, display_name: '選択した声', provider: 'synthetic-provider', model_revision: 'PRIVATE-MODEL',
  language: 'ja-JP', clone_prompt_digest: 'PRIVATE-DIGEST', provenance_id: 'PRIVATE-PROVENANCE',
  default_style: neutral(), available: true,
  capabilities: {controls: {affect: {type: 'enum', values: ['neutral', 'warm']}, volume: {type: 'number', min: 0, max: 1, step: 0.1}}, streaming: true, interruptible: true},
  ...extra,
});
const catalog = (...profiles) => ({default_profile_id: profiles[0]?.voice_profile_id, profiles});

class Element {
  constructor(tagName, ownerDocument) {
    this.tagName = tagName;
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.listeners = new Map();
    this.attributes = {};
    this.value = '';
    this.textContent = '';
    this.disabled = false;
  }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this.children = children; }
  addEventListener(name, handler) { this.listeners.set(name, handler); }
  removeEventListener(name) { this.listeners.delete(name); }
  setAttribute(key, value) { this.attributes[key] = value; }
  change(value) { this.value = String(value); this.listeners.get('change')?.({target: this}); }
  click() { if (!this.disabled) this.listeners.get('click')?.({target: this}); }
  set innerHTML(_) { throw new Error('Dynamic profile content must use text nodes'); }
}

function harness(options = {}) {
  const root = new Element('document');
  root.ownerDocument = root;
  root.createElement = tagName => new Element(tagName, root);
  root.querySelector = selector => {
    const find = node => node.id === selector.slice(1) ? node : node.children.map(find).find(Boolean);
    return find(root) || null;
  };
  for (const id of ['voice-profile-select', 'voice-profile-refresh', 'voice-style-controls', 'voice-profile-status', 'chat-prompt', 'send-chat']) {
    const node = root.createElement(id.endsWith('select') ? 'select' : 'div');
    node.id = id;
    root.appendChild(node);
  }
  const controller = mountVoiceProfiles({root, ...options});
  const node = id => root.querySelector(`#${id}`);
  return {root, controller, node, select: node('voice-profile-select'), status: node('voice-profile-status')};
}

test('optional catalog bridge preserves Kyoko with only its supported pace and volume controls', async () => {
  const h = harness({bridge: {}});
  await h.controller.ready;
  assert.deepEqual(h.controller.readSettings(), {voice_profile_id: 'macos-kyoko', style: neutral()});
  assert.ok(h.node('voice-style-pace'));
  assert.ok(h.node('voice-style-volume'));
  assert.equal(h.node('voice-style-volume').min, '0');
  for (const key of ['affect', 'intensity', 'pitch_hint', 'pause_style', 'interruptible']) assert.equal(h.node(`voice-style-${key}`), null);
  assert.equal(h.node('chat-prompt').disabled, false);
  assert.equal(h.node('send-chat').disabled, false);
  h.controller.dispose();
});

test('catalog default selects a generic profile，hides singleton controls and omits provider and asset metadata', async () => {
  const source = profile('default-voice');
  source.capabilities.controls.pause_style = {type: 'enum', values: ['natural']};
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(source)}});
  await h.controller.ready;
  assert.equal(h.select.value, 'default-voice');
  assert.deepEqual(h.controller.readSettings(), {voice_profile_id: 'default-voice', style: neutral()});
  assert.ok(h.node('voice-style-affect'));
  assert.ok(h.node('voice-style-volume'));
  assert.equal(h.node('voice-style-pace'), null);
  assert.equal(h.node('voice-style-pause_style'), null);
  assert.equal(h.node('voice-style-interruptible'), null);
  assert.ok(h.select.children.some(option => option.value === 'macos-kyoko'));
  assert.doesNotMatch(JSON.stringify(h.controller.readSettings()), /PRIVATE|provider|model_revision|speech_text|provenance|digest/);
  h.controller.dispose();
});

test('experimental Irodori profile renders every bounded preset without retaining private identity metadata', async () => {
  const source = profile('irodori-anime', {
    provider: 'irodori-tts', clone_prompt_digest: '', reference_group_digest: 'PRIVATE-GROUP',
    default_style: neutral(), capabilities: {controls: {
      affect: {type: 'enum', values: ['neutral', 'warm', 'cheerful', 'cute', 'sleepy', 'concerned']},
      intensity: {type: 'number', min: 0.75, max: 1.25, step: 0.25},
      pace: {type: 'number', min: 0.85, max: 1.15, step: 0.15},
      volume: {type: 'number', min: 0, max: 1, step: 0.05},
      pause_style: {type: 'enum', values: ['natural', 'short', 'deliberate']},
    }, streaming: true, interruptible: true},
  });
  const h = harness({bridge: {GetVoiceProfiles: async () => ({default_profile_id: 'macos-kyoko', profiles: [source]})}});
  await h.controller.ready;
  h.select.change('irodori-anime');
  h.node('voice-style-affect').change('cute');
  h.node('voice-style-intensity').change('1.25');
  h.node('voice-style-pace').change('0.85');
  h.node('voice-style-pause_style').change('deliberate');
  const settings = h.controller.readSettings();
  assert.equal(settings.voice_profile_id, 'irodori-anime');
  assert.deepEqual(settings.style, {...neutral(), affect: 'cute', intensity: 1.25, pace: 0.85, pause_style: 'deliberate'});
  assert.doesNotMatch(JSON.stringify(settings), /PRIVATE|caption|emoji|reference|provider|model_revision/);
  h.controller.dispose();
});

test('profile selection resets delivery defaults and unavailable options cannot be selected', async () => {
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(profile(), profile('missing-voice', {available: false, error_code: 'PRIVATE diagnostic'}))}});
  await h.controller.ready;
  h.node('voice-style-affect').change('warm');
  h.node('voice-style-volume').change('0.4');
  h.select.change('missing-voice');
  assert.equal(h.select.value, 'personal-voice');
  assert.equal(h.select.children.find(option => option.value === 'missing-voice').disabled, true);
  h.select.change('macos-kyoko');
  assert.deepEqual(h.controller.readSettings(), {voice_profile_id: 'macos-kyoko', style: neutral()});
  assert.equal(h.node('voice-style-affect'), null);
  assert.ok(h.node('voice-style-pace'));
  assert.doesNotMatch(h.status.textContent, /PRIVATE/);
  h.controller.dispose();
});

test('known numeric and enum controls accept valid values and reject out-of-range or injected values explicitly', async () => {
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(profile())}});
  await h.controller.ready;
  const volume = h.node('voice-style-volume');
  volume.change('0');
  assert.equal(h.controller.readSettings().style.volume, 0);
  for (const invalid of ['', '-1', '1.1', '0.15', 'NaN', 'Infinity']) {
    volume.change(invalid);
    assert.equal(volume.value, '0');
    assert.equal(h.controller.readSettings().style.volume, 0);
    assert.match(h.status.textContent, /変更していません/);
  }
  h.node('voice-style-affect').change('warm');
  assert.equal(h.controller.readSettings().style.affect, 'warm');
  h.node('voice-style-affect').change('<script>');
  assert.equal(h.controller.readSettings().style.affect, 'warm');
  assert.doesNotMatch(h.status.textContent, /script/);
  h.controller.dispose();
});

test('number controls without an explicit step permit finite values within their declared bounds', async () => {
  for (const step of [undefined, 0]) {
    const source = profile();
    source.default_style.volume = 0.85;
    source.capabilities.controls.volume = {type: 'number', min: 0, max: 1, step};
    const h = harness({bridge: {GetVoiceProfiles: async () => catalog(source)}});
    await h.controller.ready;
    assert.equal(h.controller.readSettings().voice_profile_id, 'personal-voice');
    assert.equal(h.node('voice-style-volume').step, 'any');
    h.node('voice-style-volume').change('0.125');
    assert.equal(h.controller.readSettings().style.volume, 0.125);
    h.controller.dispose();
  }
});

test('readSettings returns a detached style and frozen controls cannot change an active operation', async () => {
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(profile())}});
  await h.controller.ready;
  h.node('voice-style-affect').change('warm');
  const captured = h.controller.readSettings();
  h.controller.setBusy(true);
  assert.equal(h.select.disabled, true);
  assert.equal(h.node('voice-profile-refresh').disabled, true);
  assert.equal(h.node('voice-style-affect').disabled, true);
  h.select.change('macos-kyoko');
  h.node('voice-style-affect').change('neutral');
  assert.deepEqual(h.controller.readSettings(), captured);
  assert.equal(await h.controller.refresh(), false);
  captured.style.volume = 0.2;
  assert.equal(h.controller.readSettings().style.volume, 1);
  h.controller.setBusy(false);
  h.select.change('macos-kyoko');
  assert.equal(h.controller.readSettings().voice_profile_id, 'macos-kyoko');
  assert.equal(captured.voice_profile_id, 'personal-voice');
  assert.equal(captured.style.affect, 'warm');
  h.controller.dispose();
});

test('initial catalog must resolve before Start and busy catalog updates remain deferred', async () => {
  const pending = deferred();
  const h = harness({bridge: {GetVoiceProfiles: () => pending.promise}});
  assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
  h.controller.setBusy(true);
  pending.resolve(catalog(profile()));
  await h.controller.ready;
  assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
  assert.equal(h.select.disabled, true);
  h.controller.setBusy(false);
  assert.equal(h.controller.readSettings().voice_profile_id, 'personal-voice');
  h.controller.dispose();
});

test('out-of-order catalog replies cannot replace a newer profile list', async () => {
  const old = deferred();
  const latest = deferred();
  let calls = 0;
  const h = harness({bridge: {GetVoiceProfiles: () => ++calls === 1 ? old.promise : latest.promise}});
  const refreshing = h.controller.refresh();
  latest.resolve(catalog(profile('new-voice')));
  await refreshing;
  old.resolve(catalog(profile('old-voice')));
  assert.equal(await h.controller.ready, false);
  assert.equal(h.controller.readSettings().voice_profile_id, 'new-voice');
  assert.equal(h.select.children.some(option => option.value === 'old-voice'), false);
  h.controller.dispose();
});

test('catalog failure requires an explicit voice choice without disabling or modifying text chat', async () => {
  for (const result of ['reject', {error_code: 'PRIVATE diagnostic', profiles: []}, {profiles: 'PRIVATE invalid'}]) {
    const h = harness({bridge: {GetVoiceProfiles: async () => {
      if (result === 'reject') throw new Error('PRIVATE provider path');
      return result;
    }}});
    await h.controller.ready;
    assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
    assert.match(h.status.textContent, /テキスト入力/);
    assert.doesNotMatch(h.status.textContent, /PRIVATE|path/);
    assert.equal(h.node('chat-prompt').disabled, false);
    assert.equal(h.node('send-chat').disabled, false);
    h.controller.dispose();
  }
});

test('known catalog failure retains unavailable profiles as disabled options beside the native fallback', async () => {
  for (const error_code of ['tts_unavailable', 'tts_service_unavailable', 'voice_profile_unavailable', 'invalid_voice_profile']) {
    const h = harness({bridge: {GetVoiceProfiles: async () => ({
      ...catalog(profile('missing-voice', {available: false, error_code: 'tts_unavailable'})), error_code,
    })}});
    await h.controller.ready;
    assert.equal(h.select.children.find(option => option.value === 'missing-voice')?.disabled, true);
    assert.equal(h.select.children.find(option => option.value === 'macos-kyoko')?.disabled, false);
    assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
    assert.match(h.status.textContent, /選択した声を利用できません/);
    assert.doesNotMatch(h.status.textContent, /tts_|voice_profile|PRIVATE/);
    h.select.change('missing-voice');
    assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
    assert.equal(h.node('chat-prompt').disabled, false);
    assert.equal(h.node('send-chat').disabled, false);
    h.controller.dispose();
  }
});

test('known catalog warning preserves an available selected profile while unknown diagnostics discard the catalog', async () => {
  let result = catalog(profile());
  const h = harness({bridge: {GetVoiceProfiles: async () => result}});
  await h.controller.ready;
  h.node('voice-style-volume').change('0.4');
  result = {...catalog(profile(), profile('offline', {available: false})), error_code: 'tts_unavailable'};
  await h.controller.refresh();
  assert.equal(h.controller.readSettings().voice_profile_id, 'personal-voice');
  assert.equal(h.controller.readSettings().style.volume, 0.4);
  assert.equal(h.select.children.find(option => option.value === 'offline')?.disabled, true);
  result = {...result, error_code: 'PRIVATE provider path'};
  await h.controller.refresh();
  assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
  assert.equal(h.select.children.length, 2);
  assert.doesNotMatch(h.status.textContent, /PRIVATE|path/);
  h.controller.dispose();
});

test('untrusted labels are text nodes and unsupported controls never reach settings', async () => {
  const source = profile('generic-voice', {display_name: '<img src=x onerror=bad()>'});
  source.capabilities.controls.private_reference = {type: 'enum', values: ['PRIVATE-ASSET']};
  source.default_style.private_reference = 'PRIVATE-ASSET';
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(source)}});
  await h.controller.ready;
  assert.equal(h.select.children[0].textContent, '<img src=x onerror=bad()>');
  assert.equal(h.node('voice-style-private_reference'), null);
  assert.doesNotMatch(JSON.stringify(h.controller.readSettings()), /PRIVATE|private_reference/);
  h.controller.dispose();
});

test('catalog refresh preserves edits under the same contract and resets when capabilities change', async () => {
  let source = profile();
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(source)}});
  await h.controller.ready;
  h.node('voice-style-volume').change('0.4');
  source = {...profile(), model_revision: 'metadata-change'};
  await h.controller.refresh();
  assert.equal(h.controller.readSettings().style.volume, 0.4);
  source.capabilities.controls.volume = {type: 'number', min: 0.5, max: 1, step: 0.1};
  await h.controller.refresh();
  assert.equal(h.controller.readSettings().style.volume, 1);
  assert.equal(h.node('voice-style-volume').min, '0.5');
  h.controller.dispose();
});

test('malformed profiles and excessive catalogs retain the bounded native fallback', async () => {
  for (const result of [
    catalog(profile('bad/id')), catalog(profile(null)), catalog(profile(123)), catalog(profile('bad-number', {default_style: {...neutral(), volume: NaN}})),
    {profiles: Array.from({length: 65}, (_, index) => profile(`profile-${index}`))},
  ]) {
    const h = harness({bridge: {GetVoiceProfiles: async () => result}});
    await h.controller.ready;
    assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
    assert.equal(h.select.children.length, 2);
    h.controller.dispose();
  }
});

test('dispose disables controls and discards late catalog callbacks', async () => {
  const pending = deferred();
  const h = harness({bridge: {GetVoiceProfiles: () => pending.promise}});
  const input = h.node('voice-style-volume');
  h.controller.dispose();
  assert.equal(input.disabled, true);
  assert.equal(h.select.disabled, true);
  pending.resolve(catalog(profile()));
  assert.equal(await h.controller.ready, false);
  assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
  assert.equal(await h.controller.refresh(), false);
});

test('profile markup keeps settings separate from the live transcript and composer', () => {
  const markup = voiceProfilesMarkup();
  assert.match(markup, /<details[^>]*voice-profile-settings/);
  assert.match(markup, /<summary>声と話し方<\/summary>/);
  assert.match(markup, /aria-live="polite"/);
  assert.doesNotMatch(markup, /speech_text|voice-live-transcript|chat-prompt|type="file"|upload/);
});

test('voice Start captures profile and delivery once while ASR partial and cancellation remain isolated', async () => {
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(profile())}});
  await h.controller.ready;
  h.node('voice-style-volume').change('0.3');
  let request;
  let listener;
  let transcriptCalls = 0;
  const readiness = deferred();
  const track = {stop() { this.stopped = true; }};
  const node = () => ({connect() {}, disconnect() {}});
  const snapshot = {operation_id: 'op-1', session_id: 'session-1', turn_id: 'turn-1', trace_id: 'trace-1', state: 'RECORDING'};
  const context = {sampleRate: 16000, destination: {}, resume: async () => {}, close: async () => {},
    createMediaStreamSource: node, createScriptProcessor: node, createGain: () => ({...node(), gain: {value: 1}})};
  const voice = mountVoiceInteraction({
    root: h.root,
    bridge: {
      GetInteractionASRReadiness: () => readiness.promise,
      StartInteraction: async value => { request = value; return snapshot; },
      BeginInteractionASR: async () => ({...snapshot, segment_id: 'segment-1', sample_rate: 16000}),
      CancelInteraction: async () => ({...snapshot, state: 'CANCELED'}),
    },
    getSessionID: () => 'session-1',
    getRequest: () => ({session_id: 'session-1', chat: {messages: []}, speech: h.controller.readSettings()}),
    onBusy: busy => h.controller.setBusy(busy), onTranscript: () => { transcriptCalls += 1; },
    subscribe: callback => { listener = callback; }, createAudioContext: () => context,
    mediaDevices: {getUserMedia: async () => ({getTracks: () => [track]})},
    timers: {setTimeout: () => 1, clearTimeout() {}},
  });
  const starting = voice.start();
  h.select.change('macos-kyoko');
  h.node('voice-style-volume').change('1');
  readiness.resolve({state: 'ready', can_start: true});
  assert.equal(await starting, true);
  assert.deepEqual(request.speech, {voice_profile_id: 'personal-voice', style: {...neutral(), volume: 0.3}});
  listener({...snapshot, kind: 'asr_update', asr: {...snapshot, segment_id: 'segment-1', revision: 1, monotonic_ms: 1,
    phase: 'partial', transcript: '確定前の認識', stable_prefix: '', provider: 'test', model_revision: 'test'}});
  assert.equal(transcriptCalls, 0);
  await voice.cancel();
  listener({...snapshot, kind: 'transcript', text: 'late'});
  assert.equal(transcriptCalls, 0);
  assert.equal(track.stopped, true);
  assert.equal(h.select.disabled, false);
  h.select.change('macos-kyoko');
  assert.equal(request.speech.voice_profile_id, 'personal-voice');
  assert.equal(request.speech.style.volume, 0.3);
  await voice.dispose();
  h.controller.dispose();
  await tick();
});


test('missing or offline default and selected profiles never switch voice implicitly', async () => {
  for (const missing of [false, true]) {
    let result = catalog(profile());
    const h = harness({bridge: {GetVoiceProfiles: async () => result}});
    await h.controller.ready;
    result = {default_profile_id: 'macos-kyoko', profiles: missing ? [] : [profile('personal-voice', {available: false})]};
    await h.controller.refresh();
    assert.equal(h.select.value, 'personal-voice');
    assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
    assert.match(h.status.textContent, /テキスト入力/);
    assert.equal(h.node('send-chat').disabled, false);
    h.select.change('macos-kyoko');
    assert.equal(h.controller.readSettings().voice_profile_id, 'macos-kyoko');
    h.controller.dispose();
  }
  const h = harness({bridge: {GetVoiceProfiles: async () => ({default_profile_id: 'missing-default', profiles: [profile()]})}});
  await h.controller.ready;
  assert.equal(h.select.value, 'missing-default');
  assert.throws(() => h.controller.readSettings(), /voice_profile_unavailable/);
  h.controller.dispose();
});

test('unavailable voice Start reports voice error before microphone or ASR access', async () => {
  const h = harness({bridge: {GetVoiceProfiles: async () => catalog(profile('offline', {available: false}))}});
  await h.controller.ready;
  let calls = 0;
  const voice = mountVoiceInteraction({root: h.root,
    bridge: {GetInteractionASRReadiness: () => { calls++; }},
    getRequest: () => ({speech: h.controller.readSettings()}),
    mediaDevices: {getUserMedia: () => { calls++; }},
    subscribe: () => {}, timers: {setTimeout: () => 1, clearTimeout() {}},
  });
  assert.equal(await voice.start(), false);
  assert.equal(calls, 0);
  await voice.dispose();
  h.controller.dispose();
});
