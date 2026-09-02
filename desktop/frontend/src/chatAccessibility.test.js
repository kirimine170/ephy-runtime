import test from 'node:test';
import assert from 'node:assert/strict';

import {
  announceChatStream,
  chatMessageAccessibilityAttributes,
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
