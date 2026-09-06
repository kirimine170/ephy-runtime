import test from 'node:test';
import assert from 'node:assert/strict';
import {voiceSessionCallbacks} from './voiceSession.js';

test('cancel finishing after new chat cannot insert old transcript or unlock new text request', () => {
  let session = 'old';
  const calls = [];
  const handlers = Object.fromEntries(['onTranscript','onToken','onComplete','onCancel','onFailure','onFallback'].map(name => [name, () => calls.push(name)]));
  const callbacks = voiceSessionCallbacks({getSessionID: () => session, ...handlers, onBusy: busy => calls.push(busy), onDetachedFinish: () => calls.push('refresh-current-state')});
  callbacks.onBusy(true);
  callbacks.onTranscript({session_id:'old'}, 'current transcript');
  session = 'new';
  for (const name of Object.keys(handlers)) callbacks[name]({session_id:'old'}, 'late private transcript');
  callbacks.onBusy(false);
  assert.deepEqual(calls, [true, 'onTranscript', 'refresh-current-state']);
  callbacks.onBusy(true);
  callbacks.onComplete({session_id:'new'});
  callbacks.onBusy(false);
  assert.deepEqual(calls.slice(-3), [true, 'onComplete', false]);
});

test('unidentified failed bridge releases current busy state without adopting a transcript', () => {
  const calls = [];
  const callbacks = voiceSessionCallbacks({getSessionID: () => 'session', onBusy: busy => calls.push(busy), onFailure: () => calls.push('failure')});
  callbacks.onBusy(true);
  callbacks.onFailure({state:'FAILED'});
  callbacks.onBusy(false);
  assert.deepEqual(calls, [true, false]);
});
