import test from 'node:test';
import assert from 'node:assert/strict';
import {startFillerBargeIn} from './fillerBargeIn.js';

test('filler capture forwards PCM without treating 32 ms RMS as confirmed speech', async () => {
  const events = [], frames = []; let processor;
  const track = {readyState: 'live', stop() { events.push('track_stopped'); }};
  const node = () => ({connect() {}, disconnect() {}});
  const context = {sampleRate: 16000, destination: {}, createMediaStreamSource: node,
    createScriptProcessor() { processor = node(); return processor; }, createGain: () => ({...node(), gain: {value: 0}})};
  const monitor = await startFillerBargeIn({context, mediaDevices: {async getUserMedia() { return {getAudioTracks: () => [track], getTracks: () => [track]}; }},
    isCurrent: () => true, onAudio: (data, rate) => frames.push([data.length, rate]), onUnavailable() { assert.fail(); }});
  processor.onaudioprocess({inputBuffer: {getChannelData: () => new Float32Array(256)}});
  assert.deepEqual(events, []);
  const event = {inputBuffer: {getChannelData: () => new Float32Array(256).fill(.1)}};
  processor.onaudioprocess(event); processor.onaudioprocess(event);
  assert.deepEqual(events, []);
  assert.deepEqual(frames, [[256, 16000], [256, 16000], [256, 16000]]);
  assert.notEqual(processor.onaudioprocess, null);
  monitor.stop(); assert.deepEqual(events, ['track_stopped']);
  assert.equal(processor.onaudioprocess, null);
});
test('late microphone permission cannot attach to a canceled turn', async () => {
  let current = true, resolve, stopped = false;
  const stream = new Promise(r => { resolve = r; });
  const pending = startFillerBargeIn({context: {}, mediaDevices: {getUserMedia: () => stream},
    isCurrent: () => current, onSpeech() { assert.fail(); }, onUnavailable() { assert.fail(); }});
  current = false;
  resolve({getTracks: () => [{stop() { stopped = true; }}]});
  assert.equal(await pending, null); assert.equal(stopped, true);
});
test('shared session PCM detects activity without acquiring or closing the microphone', async () => {
  let process, unsubscribed = 0, speech = 0;
  const monitor = await startFillerBargeIn({context: {}, mediaDevices: {getUserMedia() { assert.fail('second capture'); }},
    subscribePCM(callback) { process = callback; return () => unsubscribed++; },
    isCurrent: () => true, onAudio: () => speech++, onUnavailable() { assert.fail(); }});
  process(new Float32Array(512).fill(.1), 16000);
  assert.equal(speech, 1); assert.equal(unsubscribed, 0);
  monitor.stop(); assert.equal(unsubscribed, 1);
});
