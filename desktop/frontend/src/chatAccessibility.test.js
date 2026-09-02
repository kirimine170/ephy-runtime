import test from 'node:test';
import assert from 'node:assert/strict';

import {
  announceChatStream,
  chatMessageAccessibilityAttributes,
  transitionChatEntryToFailure,
  validateChatPrompt,
} from './chatAccessibility.js';

test('chat stream announcements use a dedicated status without replacing the transcript', () => {
  const transcript = {innerHTML: '<article>Earlier message</article>'};
  const announcer = {textContent: ''};
  const root = {
    getElementById(id) {
      return id === 'chat-stream-announcement' ? announcer : id === 'chat-output' ? transcript : null;
    },
  };

  announceChatStream(root, 'streaming');
  assert.equal(announcer.textContent, 'Assistant response in progress.');
  assert.equal(transcript.innerHTML, '<article>Earlier message</article>');

  announceChatStream(root, 'complete');
  assert.equal(announcer.textContent, 'Assistant response complete.');
  assert.equal(transcript.innerHTML, '<article>Earlier message</article>');

  announceChatStream(root, 'error', 'offline');
  assert.equal(announcer.textContent, 'Assistant response failed: offline');
});

test('historical error cards remain non-live after transcript rerenders', () => {
  assert.equal(
    chatMessageAccessibilityAttributes({meta: 'error', streaming: false}),
    'aria-busy="false"',
  );
  assert.doesNotMatch(
    chatMessageAccessibilityAttributes({meta: 'error', streaming: false}),
    /role="alert"|aria-live=/,
  );
});

test('empty prompt uses input validation instead of a response failure announcement', () => {
  const events = [];
  const input = {
    value: '   ',
    setCustomValidity(message) { events.push(['validity', message]); },
    reportValidity() { events.push(['report']); },
    focus() { events.push(['focus']); },
  };

  assert.equal(validateChatPrompt(input), false);
  assert.deepEqual(events, [
    ['validity', 'Enter a message before sending.'],
    ['report'],
    ['focus'],
  ]);

  input.value = 'Continue';
  assert.equal(validateChatPrompt(input), true);
  assert.deepEqual(events.at(-1), ['validity', '']);
});

test('only an actively streaming entry transitions to an announced failure', () => {
  assert.equal(transitionChatEntryToFailure(null, 'offline'), null);
  assert.equal(
    transitionChatEntryToFailure({requestId: 'done', streaming: false}, 'offline'),
    null,
  );
  assert.deepEqual(
    transitionChatEntryToFailure({requestId: 'active', streaming: true, text: ''}, 'offline'),
    {
      requestId: 'active',
      streaming: false,
      text: 'offline',
      meta: 'error',
      canContinue: false,
      finishReason: '',
    },
  );
});
