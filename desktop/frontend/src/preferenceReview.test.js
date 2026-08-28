import assert from 'node:assert/strict';
import test from 'node:test';

import {
  assertBlindPreferencePair,
  createPreferenceReviewController,
  PREFERENCE_GENERATION_BATCH_SIZE,
  preferenceEmptyMessage,
  preferenceGenerationLimit,
  preferenceSelectionForKey,
  renderBlindPreferencePair,
  renderPromptComparison,
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


test('preference generation requests only one pair at a time', () => {
  assert.equal(PREFERENCE_GENERATION_BATCH_SIZE, 1);
  assert.equal(preferenceGenerationLimit(4), 1);
  assert.equal(preferenceGenerationLimit(1), 1);
  assert.equal(preferenceGenerationLimit(0), 0);
});


test('empty preference state explains duplicate generations', () => {
  assert.equal(
    preferenceEmptyMessage({remaining: 9, duplicate_generation: 2}),
    '同一出力だった候補を2組除外しました．Resumeでもう1組生成できます．',
  );
  assert.equal(
    preferenceEmptyMessage({remaining: 0, duplicate_generation: 2}),
    'このsessionのレビューは完了しました．',
  );
});


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
  assert.throws(
    () => assertBlindPreferencePair({...pair(), prompt_variant: 'v2'}),
    /not blind/,
  );
});


test('prompt comparison stays blind until completion and then renders version result', () => {
  const blinded = renderPromptComparison({mode: 'prompt_v1_v2', blinded: true});
  const complete = renderPromptComparison({
    mode: 'prompt_v1_v2',
    blinded: false,
    winner: 'v2',
    variants: {
      v1: {wins: 2, losses: 3, win_rate: 0.4},
      v2: {wins: 3, losses: 2, win_rate: 0.6},
    },
  });

  assert.match(blinded, /session完了までblind/);
  assert.doesNotMatch(blinded, /v1 2勝|v2 3勝/);
  assert.match(complete, /Prompt v2/);
  assert.match(complete, /v1 2勝/);
  assert.match(complete, /v2 3勝/);

  const v3Complete = renderPromptComparison({
    mode: 'prompt_v2_v3',
    blinded: false,
    winner: 'v3',
    variants: {
      v2: {wins: 4, losses: 6, win_rate: 0.4},
      v3: {wins: 6, losses: 4, win_rate: 0.6},
    },
  });
  assert.match(v3Complete, /Prompt v3/);
  assert.match(v3Complete, /v2 4勝/);
  assert.match(v3Complete, /v3 6勝/);

  const adapterBlinded = renderPromptComparison({mode: 'base_vs_adapter', blinded: true});
  const adapterComplete = renderPromptComparison({
    mode: 'base_vs_adapter',
    blinded: false,
    winner: 'adapter',
    adapter_scale: 32,
    variants: {
      base: {wins: 3, losses: 5, win_rate: 0.375},
      adapter: {wins: 5, losses: 3, win_rate: 0.625},
    },
  });
  assert.match(adapterBlinded, /Base／LoRA.*blind/);
  assert.doesNotMatch(adapterBlinded, /Base 3勝|LoRA 5勝/);
  assert.match(adapterComplete, /<strong>LoRA ×32<\/strong>/);
  assert.match(adapterComplete, /Base 3勝/);
  assert.match(adapterComplete, /LoRA ×32 5勝/);
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
