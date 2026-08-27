import assert from 'node:assert/strict';
import test from 'node:test';

import {
  assertBlindPreferencePair,
  createPreferenceReviewController,
  preferenceSelectionForKey,
  renderBlindPreferencePair,
} from './preferenceReview.js';


function pair(id = 'pair-1') {
  return {
    pair_id: id,
    messages: [
      {role: 'user', content: '相談があります'},
      {role: 'assistant', content: '聞かせてください'},
      {role: 'user', content: '迷っています'},
    ],
    response_left: '左の応答',
    response_right: '右の応答',
    category: '合成テスト',
    progress: {reviewed: 0, remaining: 1, total: 1},
  };
}


test('blind pair render includes conversation and responses without model metadata', () => {
  const html = renderBlindPreferencePair(pair());
  assert.match(html, /相談があります/);
  assert.match(html, /左の応答/);
  assert.match(html, /右の応答/);
  assert.doesNotMatch(html, /model_registration_id|adapter_registration_id|display_order|candidate_id/);
});


test('blind guard rejects deblinded API payloads', () => {
  assert.throws(
    () => assertBlindPreferencePair({...pair(), model_registration_id: 'secret-model'}),
    /not blind/,
  );
});


test('key bindings map left right tie and skip', () => {
  assert.equal(preferenceSelectionForKey('1'), 'left');
  assert.equal(preferenceSelectionForKey('2'), 'right');
  assert.equal(preferenceSelectionForKey('0'), 'tie');
  assert.equal(preferenceSelectionForKey('S'), 'skip');
  assert.equal(preferenceSelectionForKey('x'), null);
});


test('controller prevents empty and double submission then advances to next', async () => {
  let calls = 0;
  let resolveVote;
  const controller = createPreferenceReviewController({
    submitVote: async () => {
      calls += 1;
      return new Promise((resolve) => { resolveVote = resolve; });
    },
    loadNext: async () => pair('pair-2'),
  });
  controller.resume(pair());
  assert.equal(await controller.submit(), false);
  controller.select('left');
  const first = controller.submit();
  assert.equal(await controller.submit(), false);
  resolveVote({vote_id: 'vote-1'});
  assert.equal(await first, true);
  assert.equal(calls, 1);
  assert.equal(controller.state.pair.pair_id, 'pair-2');
});


test('controller submits tie and skip and supports resume and correction', async () => {
  const selections = [];
  const controller = createPreferenceReviewController({
    submitVote: async (_pair, payload) => {
      selections.push(payload.selection);
      return {vote_id: `vote-${selections.length}`};
    },
    loadNext: async () => pair(`pair-${selections.length + 1}`),
  });
  controller.resume(pair());
  controller.select('tie');
  assert.equal(await controller.submit(), true);
  assert.equal(controller.correctPrevious(), true);
  controller.select('skip');
  assert.equal(await controller.submit({supersedes_vote_id: 'vote-1'}), true);
  assert.deepEqual(selections, ['tie', 'skip']);
});


test('controller reports API errors and keeps the current pair', async () => {
  let message = '';
  const controller = createPreferenceReviewController({
    submitVote: async () => { throw new Error('Gateway unavailable'); },
    loadNext: async () => null,
    onError: (error) => { message = error; },
  });
  controller.resume(pair());
  controller.select('right');
  assert.equal(await controller.submit(), false);
  assert.match(message, /Gateway unavailable/);
  assert.equal(controller.state.pair.pair_id, 'pair-1');
});
