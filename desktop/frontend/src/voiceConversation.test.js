import test from 'node:test';
import assert from 'node:assert/strict';
import {confirmVoiceEntry, previewVoiceEntry, resumeVoiceEntry, settleVoiceEntry} from './voiceConversation.js';
import {conversationHistory} from './conversationHistory.js';

const snapshot = (state = 'THINKING', text = '', revision = 1, generation = {}) => ({
  operation_id: 'op', generation_revision: revision, state,
  response_plan: {text}, generation: {finish_reason: 'stop', complete: state === 'COMPLETED', ...generation},
});
const initial = () => resumeVoiceEntry({requestId: 'op', role: 'assistant'}, snapshot());

test('unconfirmed Japanese fragment stays in display preview and rolls back to confirmed output', () => {
  let entry = previewVoiceEntry(initial(), snapshot(), '春には桃');
  assert.equal(entry.text, '');
  assert.equal(entry.pendingText, '春には桃');
  assert.deepEqual(conversationHistory([entry]), []);
  entry = confirmVoiceEntry(entry, snapshot('THINKING', '春には花が咲きます．'));
  assert.equal(entry.pendingText, '');
  assert.equal(entry.text, '春には花が咲きます．');
  assert.deepEqual(conversationHistory([entry]), []);
});

test('terminal full answer wins over every dropped or stale display preview', () => {
  let entry = initial();
  for (let i = 0; i < 128; i += 1) entry = previewVoiceEntry(entry, snapshot(), '前半');
  const complete = '春には花，夏は暑さ，秋は紅葉，冬は雪を楽しめます．';
  entry = settleVoiceEntry(entry, snapshot('COMPLETED', complete));
  assert.equal(entry.text, complete);
  assert.equal(entry.pendingText, '');
  assert.equal(entry.terminalState, 'COMPLETED');
  assert.deepEqual(conversationHistory([entry]), [{role: 'assistant', content: complete}]);
});

test('bounded continuation resumes the same assistant entry and adds exactly one final history message', () => {
  const entries = [{role: 'user', text: '四季を説明して'}, initial()];
  entries[1] = settleVoiceEntry(entries[1], snapshot('INCOMPLETE', '春には花が咲きます．', 1, {finish_reason: 'length'}));
  assert.equal(entries[1].canContinue, true);
  assert.equal(conversationHistory(entries).length, 1);
  entries[1] = resumeVoiceEntry(entries[1], snapshot('THINKING', entries[1].text, 2));
  entries[1] = previewVoiceEntry(entries[1], snapshot('THINKING', '', 2), '夏は暑');
  const full = '春には花が咲きます．夏は暑く，秋は紅葉，冬は雪です．';
  entries[1] = settleVoiceEntry(entries[1], snapshot('COMPLETED', full, 2));
  assert.equal(entries.length, 2);
  assert.deepEqual(conversationHistory(entries), [{role: 'user', content: '四季を説明して'}, {role: 'assistant', content: full}]);
});

test('canceled failed and incomplete terminals retain only confirmed text and no completed history', () => {
  for (const state of ['CANCELED', 'FAILED', 'INCOMPLETE']) {
    const pending = previewVoiceEntry(initial(), snapshot(), '春．桃');
    const entry = settleVoiceEntry(pending, snapshot(state, '春．', 1, {finish_reason: state === 'CANCELED' ? 'canceled' : 'length'}));
    assert.equal(entry.text, '春．');
    assert.equal(entry.pendingText, '');
    assert.equal(entry.terminalState, state);
    assert.equal(entry.canContinue, state === 'INCOMPLETE');
    assert.deepEqual(conversationHistory([entry]), []);
    assert.equal(settleVoiceEntry(entry, snapshot('COMPLETED', 'late full text')), entry);
  }
});

test('stale generation revisions cannot append confirm or terminate a resumed entry', () => {
  const current = resumeVoiceEntry(initial(), snapshot('THINKING', '春．', 2));
  assert.equal(previewVoiceEntry(current, snapshot(), 'late'), current);
  assert.equal(confirmVoiceEntry(current, snapshot('THINKING', 'late')), current);
  assert.equal(settleVoiceEntry(current, snapshot('COMPLETED', 'late')), current);
});

test('a completed state without confirmed generation metadata fails closed', () => {
  const entry = settleVoiceEntry(initial(), snapshot('COMPLETED', '桃', 1, {complete: false, finish_reason: 'length'}));
  assert.equal(entry.terminalState, 'FAILED');
  assert.deepEqual(conversationHistory([entry]), []);
});

const deliveryFixture = JSON.parse((await import('node:fs')).readFileSync(new URL('../../../schemas/karte-ephy/v2/fixtures/runtime-delivery.scenario.json', import.meta.url)));
for (const scenario of deliveryFixture.cases) {
  test(`shared Karte v2 delivery contract: ${scenario.name}`, () => {
    const snapshot = scenario.snapshot;
    const started = resumeVoiceEntry({requestId: snapshot.operation_id, role: 'assistant'}, snapshot);
    const settled = settleVoiceEntry(started, snapshot);
    const d = settled.voiceDelivery;
    assert.deepEqual({generation: d.generation, display: d.display, playback: d.playback, speech_units: d.speechUnits}, scenario.expected_assistant);
    assert.equal(d.playedPrefix, scenario.expected_heard_prefix);
    assert.equal(d.displayedText, snapshot.response_plan.text);
    const history = conversationHistory([settled]);
    if (d.interrupted) {
      assert.equal(history.length, 1);
      assert.match(history[0].content, /中断された/);
      if (d.playedPrefix) assert.ok(history[0].content.endsWith(d.playedPrefix));
      for (const unit of snapshot.speech_units.filter(unit => unit.state !== 'completed')) {
        assert.ok(!history[0].content.includes(unit.text));
      }
    }
  });
}
