import test from 'node:test';
import assert from 'node:assert/strict';
import {createVoiceEndpoint, endpointSettings} from './voiceEndpoint.js';
function fixture(text = '') {
  let time = 0;
  const endpoint = createVoiceEndpoint({now: () => time, settings: endpointSettings()});
  const speak = () => endpoint.audio(new Float32Array(512).fill(.1), 16000);
  speak(); endpoint.hypothesis(text);
  return {endpoint, speak, at: value => { time = value; }};
}
for (const [text, threshold] of [['明日の予定', 900], ['', 1800], ['えっと……', 1800], ['うん', 900], ['はい', 900]]) {
  test(`endpoint ${text || 'no partial'} waits ${threshold} ms plus revocable grace`, () => {
    const f = fixture(text);
    f.at(threshold - 1); assert.equal(f.endpoint.tick(), '');
    f.at(threshold); assert.equal(f.endpoint.tick(), 'candidate');
    f.at(threshold + 99); assert.equal(f.endpoint.tick(), 'candidate');
    f.at(threshold + 100); assert.equal(f.endpoint.tick(), 'endpoint');
    assert.equal(f.endpoint.tick(), '');
  });
  for (const offset of [-1, 0, 1, 99]) test(`resumption at ${threshold + offset} ms cancels ${text || 'missing partial'} endpoint`, () => {
    const f = fixture(text);
    if (offset >= 0) { f.at(threshold); f.endpoint.tick(); }
    f.at(threshold + offset); f.speak();
    assert.equal(f.endpoint.tick(), '');
    f.at(threshold + offset + threshold - 1); assert.equal(f.endpoint.tick(), '');
  });
}
test('300/600 ms pauses and continuation after hesitation preserve the utterance', () => {
  const f = fixture('えっと……');
  for (const at of [300, 600, 1200, 1801]) { f.at(at); f.speak(); assert.equal(f.endpoint.tick(), ''); }
  f.endpoint.hypothesis('えっと明日の予定は');
  f.at(2701); assert.equal(f.endpoint.tick(), 'candidate');
  f.at(2801); assert.equal(f.endpoint.tick(), 'endpoint');
});
test('changed partial restarts observation without becoming guaranteed stable', () => {
  const f = fixture('今日'); f.at(899); f.endpoint.hypothesis('今日は');
  f.at(900); assert.equal(f.endpoint.tick(), '');
  f.at(1199); assert.equal(f.endpoint.tick(), 'candidate');
  assert.equal(f.endpoint.stablePrefix, undefined);
});
test('early final needs speech and commits exactly once，continuous speech hits a finite limit', () => {
  const empty = createVoiceEndpoint({now: () => 0});
  assert.equal(empty.providerFinal(), false);
  const f = fixture(); assert.equal(f.endpoint.providerFinal(), true); assert.equal(f.endpoint.providerFinal(), false);
  const long = fixture(); long.at(60000); long.speak(); assert.equal(long.endpoint.tick(), 'limit');
});
test('endpoint thresholds are adjustable and bounded', () => {
  assert.equal(endpointSettings({silenceMS: 1000}).silenceMS, 1000);
  for (const value of [0, -1, Infinity, NaN]) assert.throws(() => endpointSettings({graceMS: value}));
  assert.throws(() => endpointSettings({maxUtteranceMS: 60001}));
});
