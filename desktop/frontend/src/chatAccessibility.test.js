import test from 'node:test';
import assert from 'node:assert/strict';

import {
  announceChatStream,
  chatMessageAccessibilityAttributes,
  mergeChatCompletionMetadata,
  transitionChatEntryToComplete,
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

test('only an actively streaming entry transitions to an announced completion', () => {
  assert.equal(transitionChatEntryToComplete(null, {answer: 'done'}), null);
  assert.equal(
    transitionChatEntryToComplete({requestId: 'done', streaming: false}, {answer: 'done'}),
    null,
  );
  assert.deepEqual(
    transitionChatEntryToComplete(
      {requestId: 'active', streaming: true, text: '', thinking: '', meta: 'streaming'},
      {answer: 'done', thinking: 'trace', meta: 'local', finishReason: 'stop'},
    ),
    {
      requestId: 'active',
      streaming: false,
      text: 'done',
      thinking: 'trace',
      meta: 'local',
      finishReason: 'stop',
    },
  );
});

test('late completion metadata merges without reopening the completed entry', () => {
  assert.deepEqual(
    mergeChatCompletionMetadata(
      {
        requestId: 'done',
        streaming: false,
        text: 'answer',
        thinking: '',
        meta: 'Quick · streaming',
        finishReason: '',
      },
      {meta: 'Quick · done', thinking: 'trace', finishReason: 'length'},
    ),
    {
      requestId: 'done',
      streaming: false,
      text: 'answer',
      thinking: 'trace',
      meta: 'Quick · done',
      finishReason: 'length',
    },
  );
});

test('terminal full text replaces a nonempty truncated preview including after 128 deltas', () => {
  const chunks = Array.from({length: 243}, (_, index) => `${index}春夏秋冬`);
  const entry = {streaming: true, text: chunks.slice(0, 128).join('')};
  const completed = transitionChatEntryToComplete(entry, {answer: chunks.join(''), finishReason: 'stop'});
  assert.equal(completed.text, chunks.join(''));
  assert.equal(mergeChatCompletionMetadata(completed, {answer: chunks.join('')}).text, completed.text);
});

test('authoritative empty answer removes an unconfirmed preview', () => {
  assert.equal(transitionChatEntryToComplete({streaming: true, text: '桃'}, {answer: ''}).text, '');
});

test('text continuation preserves original prefix and replaces only the current preview segment', () => {
  const entry = {streaming: true, text: '春です．夏は途', completionPrefix: '春です．'};
  const completed = transitionChatEntryToComplete(entry, {answer: '夏は暑いです．', finishReason: 'stop'});
  assert.equal(completed.text, '春です．夏は暑いです．');
  assert.equal(mergeChatCompletionMetadata(completed, {answer: '夏は暑いです．'}).text, completed.text);
});

test('incomplete and canceled streams are announced without claiming successful completion', () => {
  const node = {};
  const root = {getElementById: () => node};
  announceChatStream(root, 'incomplete');
  assert.match(node.textContent, /未完了/);
  announceChatStream(root, 'canceled');
  assert.match(node.textContent, /停止/);
});
