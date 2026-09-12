import test from 'node:test';
import assert from 'node:assert/strict';
import {startFillerBargeIn} from './fillerBargeIn.js';

test('activity interrupt stops input before notifying and never records or transcribes', async () => {
  const events = []; let processor;
  const track = {readyState: 'live', stop() { events.push('track_stopped'); }};
  const node = () => ({connect() {}, disconnect() {}});
  const context = {sampleRate: 16000, destination: {}, createMediaStreamSource: node,
    createScriptProcessor() { processor = node(); return processor; }, createGain: () => ({...node(), gain: {value: 0}})};
  const monitor = await startFillerBargeIn({context, mediaDevices: {async getUserMedia() { return {getAudioTracks: () => [track], getTracks: () => [track]}; }},
    isCurrent: () => true, onSpeech: () => events.push('speech'), onUnavailable() { assert.fail(); }});
  processor.onaudioprocess({inputBuffer: {getChannelData: () => new Float32Array(256)}});
  assert.deepEqual(events, []);
  const event = {inputBuffer: {getChannelData: () => new Float32Array(256).fill(.1)}};
  processor.onaudioprocess(event); processor.onaudioprocess(event);
  assert.deepEqual(events, ['track_stopped', 'speech']);
  assert.equal(processor.onaudioprocess, null);
  monitor.stop(); assert.deepEqual(events, ['track_stopped', 'speech']);
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
    isCurrent: () => true, onSpeech: () => speech++, onUnavailable() { assert.fail(); }});
  process(new Float32Array(512).fill(.1), 16000);
  assert.equal(speech, 1); assert.equal(unsubscribed, 1);
  monitor.stop(); assert.equal(unsubscribed, 1);
});
