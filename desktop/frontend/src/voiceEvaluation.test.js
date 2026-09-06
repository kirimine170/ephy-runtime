import test from 'node:test';
import assert from 'node:assert/strict';
import {mountVoiceEvaluation, voiceEvaluationMarkup, VOICE_FAILURE_TAGS} from './voiceEvaluation.js';

const tick = () => new Promise((resolve) => setImmediate(resolve));
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
const turn = (operation = 'operation_1', session = 'session_1') => ({
  operation_id: operation, session_id: session, turn_id: `turn_${operation}`, trace_id: `trace_${operation}`,
  state: 'COMPLETED', transcript: '元の発話', response_plan: {text: '元の応答'},
});

function element(value = '') {
  const events = new Map();
  return {
    value, checked: false, disabled: false, hidden: false, textContent: '', innerHTML: '',
    addEventListener(name, handler) { events.set(name, handler); },
    removeEventListener(name) { events.delete(name); },
    trigger(name) { if (!this.disabled) events.get(name)?.(); },
    click() { this.trigger('click'); },
  };
}

function harness(options = {}) {
  let session = 'session_1';
  let developer = true;
  let requestID = 0;
  const calls = {feedback: [], generation: [], canceled: [], saved: [], exported: [], replay: [], canceledVoice: [], adopted: []};
  const nodes = new Map();
  const defaults = {'voice-mode-a': 'auto', 'voice-mode-b': 'auto', 'voice-temperature-a': '0.2', 'voice-temperature-b': '0.7'};
  const getNode = (id) => {
    if (!nodes.has(id)) nodes.set(id, element(defaults[id] || ''));
    return nodes.get(id);
  };
  const tags = Object.keys(VOICE_FAILURE_TAGS).map((tag) => element(tag));
  const bridge = {
    async RecordInteractionFeedback(operation) { calls.feedback.push(operation); return {}; },
    async GenerateInteractionComparison(request) {
      calls.generation.push(request);
      return {comparison_id: request.request_id, source_operation_id: request.source_operation_id, candidate_a: '候補の文A', candidate_b: '候補の文B'};
    },
    async CancelInteractionComparison(requestID) { calls.canceled.push(requestID); },
    async SaveInteractionEvaluation(request) { calls.saved.push(request); return {}; },
    async ExportInteractionEvaluations(format) { calls.exported.push(format); return {path: `/local/evaluation.${format}`}; },
    async ReplayInteraction(request) { calls.replay.push(request); return {...turn('operation_replay'), state: 'RECORDING'}; },
    async CancelInteraction(operation) { calls.canceledVoice.push(operation); return {...turn(operation), state: 'CANCELED'}; },
    ...options.bridge,
  };
  const controller = {
    lastSnapshot: turn(), active: false,
    isActive() { return this.active; },
    async adopt(pending) {
      calls.adopted.push(pending);
      this.active = true;
      this.lastSnapshot = {state: 'RECORDING'};
      try { this.lastSnapshot = await pending; return true; }
      catch { this.active = false; return false; }
    },
    async cancel() { this.active = false; calls.canceledVoice.push(this.lastSnapshot.operation_id); },
    ...options.controller,
  };
  const evaluation = mountVoiceEvaluation({
    root: {querySelector: (selector) => getNode(selector.slice(1)), querySelectorAll: () => tags},
    bridge, controller,
    getSessionID: () => session,
    isDeveloper: () => developer,
    makeRequestID: () => `comparison_test_${++requestID}`,
  });
  return {
    evaluation, bridge, controller, calls, tags, node: getNode,
    session(value) { session = value; evaluation.refresh(); },
    developer(value) { developer = value; evaluation.refresh(); },
  };
}

test('one-click feedback works outside Developer Mode and prevents duplicate pending/saved clicks', async () => {
  const h = harness();
  h.developer(false);
  const pending = deferred();
  h.bridge.RecordInteractionFeedback = async (id) => { h.calls.feedback.push(id); return pending.promise; };
  h.node('voice-feedback').click();
  h.node('voice-feedback').click();
  assert.equal(await h.evaluation.feedback(), false);
  pending.resolve({});
  await tick();
  assert.deepEqual(h.calls.feedback, ['operation_1']);
  assert.equal(h.node('voice-feedback').disabled, true);
  assert.match(h.node('voice-feedback').textContent, /記録しました/);
  assert.equal(await h.evaluation.generate(), false);
  assert.equal(await h.evaluation.export('json'), false);
});

test('feedback error is fixed text and can be retried', async () => {
  const h = harness();
  h.bridge.RecordInteractionFeedback = async () => { throw new Error('secret provider path'); };
  assert.equal(await h.evaluation.feedback(), false);
  assert.match(h.node('voice-feedback').textContent, /再試行/);
  assert.doesNotMatch(h.node('voice-feedback').textContent, /secret/);
  h.bridge.RecordInteractionFeedback = async () => ({});
  assert.equal(await h.evaluation.feedback(), true);
});

test('comparison carries caller ID and defaults，then inserts blinded candidates only as text', async () => {
  const h = harness();
  const hostile = '<img src=x onerror=secret()> & candidate';
  h.bridge.GenerateInteractionComparison = async (request) => {
    h.calls.generation.push(request);
    return {comparison_id: request.request_id, source_operation_id: request.source_operation_id, candidate_a: hostile, candidate_b: 'safe'};
  };
  assert.equal(await h.evaluation.generate(), true);
  assert.deepEqual(h.calls.generation[0], {
    request_id: 'comparison_test_1', source_operation_id: 'operation_1', mode_a: 'auto', mode_b: 'auto', temperature_a: 0.2, temperature_b: 0.7,
  });
  assert.equal(h.node('voice-candidate-a').textContent, hostile);
  assert.equal(h.node('voice-candidate-a').innerHTML, '');
  assert.equal(h.node('voice-comparison-results').hidden, false);
  assert.doesNotMatch(h.node('voice-evaluation').innerHTML, /secret|provider_id|model_id/);
});

test('comparison rejects deblinded or incorrectly associated payloads', async () => {
  const h = harness();
  h.bridge.GenerateInteractionComparison = async (request) => ({
    comparison_id: request.request_id, source_operation_id: request.source_operation_id,
    candidate_a: 'text', candidate_b: 'text', model_id: 'secret-model',
  });
  assert.equal(await h.evaluation.generate(), false);
  assert.equal(h.node('voice-comparison-results').hidden, true);
  assert.equal(h.node('voice-candidate-a').textContent, '');
  assert.doesNotMatch(h.node('voice-evaluation-status').textContent, /secret/);
  h.bridge.GenerateInteractionComparison = async () => ({comparison_id: 'wrong', source_operation_id: 'other', candidate_a: 'text', candidate_b: 'text'});
  assert.equal(await h.evaluation.generate(), false);
});

test('pending generation can be canceled by caller ID before backend returns，including double cancel', async () => {
  const h = harness();
  const pending = deferred();
  h.bridge.GenerateInteractionComparison = async (request) => { h.calls.generation.push(request); return pending.promise; };
  const generating = h.evaluation.generate();
  assert.equal(h.node('voice-comparison-cancel').disabled, false);
  await Promise.all([h.evaluation.cancelComparison(), h.evaluation.cancelComparison()]);
  assert.deepEqual(h.calls.canceled, ['comparison_test_1']);
  pending.resolve({comparison_id: 'comparison_test_1', source_operation_id: 'operation_1', candidate_a: 'lateA', candidate_b: 'lateB'});
  assert.equal(await generating, false);
  assert.equal(h.node('voice-comparison-results').hidden, true);
  assert.equal(h.node('voice-candidate-a').textContent, '');
  assert.match(h.node('voice-evaluation-status').textContent, /停止しました/);
});

test('source change cancels old generation and late result never replaces a new comparison', async () => {
  const h = harness();
  const pending = deferred();
  const originalGenerate = h.bridge.GenerateInteractionComparison;
  h.bridge.GenerateInteractionComparison = () => pending.promise;
  const oldGeneration = h.evaluation.generate();
  h.controller.lastSnapshot = turn('operation_2');
  h.evaluation.refresh();
  assert.deepEqual(h.calls.canceled, ['comparison_test_1']);
  h.bridge.GenerateInteractionComparison = originalGenerate;
  await h.evaluation.generate();
  pending.resolve({comparison_id: 'comparison_test_1', source_operation_id: 'operation_1', candidate_a: 'oldA', candidate_b: 'oldB'});
  assert.equal(await oldGeneration, false);
  assert.equal(h.node('voice-candidate-a').textContent, '候補の文A');
  assert.equal(h.calls.generation[0].source_operation_id, 'operation_2');
});

test('a delayed cancel acknowledgement cannot overwrite a newer comparison status', async () => {
  const h = harness();
  const generation = deferred();
  const cancellation = deferred();
  const normalGenerate = h.bridge.GenerateInteractionComparison;
  h.bridge.GenerateInteractionComparison = () => generation.promise;
  h.bridge.CancelInteractionComparison = () => cancellation.promise;
  const first = h.evaluation.generate();
  const canceling = h.evaluation.cancelComparison();
  generation.reject(new Error('canceled'));
  await first;
  h.bridge.GenerateInteractionComparison = normalGenerate;
  await h.evaluation.generate();
  const status = h.node('voice-evaluation-status').textContent;
  cancellation.resolve();
  await canceling;
  assert.equal(h.node('voice-evaluation-status').textContent, status);
  assert.equal(h.node('voice-comparison-results').hidden, false);
});

test('new session rejects old-source feedback，comparison and replay and erases unsaved text', async () => {
  const h = harness();
  await h.evaluation.generate();
  h.node('voice-comparison-correction').value = 'private correction';
  h.session('session_2');
  assert.equal(h.node('voice-candidate-a').textContent, '');
  assert.equal(h.node('voice-comparison-correction').value, '');
  assert.equal(h.node('voice-replay-transcript').value, '');
  assert.equal(h.node('voice-comparison-results').hidden, true);
  assert.equal(await h.evaluation.feedback(), false);
  assert.equal(await h.evaluation.generate(), false);
  assert.equal(await h.evaluation.replay(), false);
  assert.equal(h.calls.replay.length, 0);
  assert.equal(h.calls.feedback.length, 0);
});

test('new voice activity and Developer Mode disablement erase old results and block saves', async () => {
  const h = harness();
  await h.evaluation.generate();
  h.controller.active = true;
  h.evaluation.refresh();
  assert.equal(h.node('voice-candidate-b').textContent, '');
  assert.equal(await h.evaluation.save(), false);
  h.controller.active = false;
  h.evaluation.refresh();
  await h.evaluation.generate();
  h.developer(false);
  assert.equal(h.node('voice-evaluation').hidden, true);
  assert.equal(h.node('voice-candidate-a').textContent, '');
  assert.equal(await h.evaluation.replay(), false);
});

test('save and correction update use only evaluation fields and protect against duplicate pending saves', async () => {
  const h = harness();
  await h.evaluation.generate();
  assert.equal(await h.evaluation.save(), false);
  h.node('voice-comparison-choice').value = 'A';
  h.node('voice-comparison-choice').trigger('change');
  h.node('voice-comparison-correction').value = 'explicit correction';
  h.tags[0].checked = true;
  h.tags[9].checked = true;
  const pending = deferred();
  h.bridge.SaveInteractionEvaluation = async (request) => { h.calls.saved.push(request); return pending.promise; };
  const saving = h.evaluation.save();
  assert.equal(await h.evaluation.save(), false);
  pending.resolve({model_id: 'private-identity'});
  assert.equal(await saving, true);
  assert.deepEqual(h.calls.saved[0], {
    comparison_id: 'comparison_test_1', choice: 'A', correction: 'explicit correction', failure_tags: ['asr_error', 'other'],
  });
  assert.match(h.node('voice-comparison-save').textContent, /更新/);
  h.node('voice-comparison-choice').value = 'neither';
  h.node('voice-comparison-correction').value = 'updated correction';
  assert.equal(await h.evaluation.save(), true);
  assert.equal(h.calls.saved[1].comparison_id, h.calls.saved[0].comparison_id);
  assert.equal(h.calls.saved[1].choice, 'neither');
  assert.equal(h.calls.saved[1].correction, 'updated correction');
  assert.doesNotMatch(h.node('voice-evaluation-status').textContent, /private-identity/);
});

test('save failure retains correction and retry while oversized UTF-8 correction is rejected', async () => {
  const h = harness();
  await h.evaluation.generate();
  h.node('voice-comparison-choice').value = 'tie';
  h.node('voice-comparison-correction').value = '修正';
  h.bridge.SaveInteractionEvaluation = async () => { throw new Error('private storage path'); };
  assert.equal(await h.evaluation.save(), false);
  assert.equal(h.node('voice-comparison-correction').value, '修正');
  assert.equal(h.node('voice-comparison-save').disabled, false);
  assert.doesNotMatch(h.node('voice-evaluation-status').textContent, /private/);
  h.node('voice-comparison-correction').value = 'あ'.repeat(6000);
  h.bridge.SaveInteractionEvaluation = async (request) => h.calls.saved.push(request);
  assert.equal(await h.evaluation.save(), false);
  assert.equal(h.calls.saved.length, 0);
});

test('a save resolving after session change cannot restore old results or status', async () => {
  const h = harness();
  await h.evaluation.generate();
  h.node('voice-comparison-choice').value = 'B';
  const pending = deferred();
  h.bridge.SaveInteractionEvaluation = () => pending.promise;
  const saving = h.evaluation.save();
  h.session('session_2');
  pending.resolve({});
  assert.equal(await saving, false);
  assert.equal(h.node('voice-evaluation-status').textContent, '');
  assert.equal(h.node('voice-comparison-results').hidden, true);
});

test('exports call backend by format only and clearly disclose persisted corrections', async () => {
  const h = harness();
  assert.equal(await h.evaluation.export('json'), true);
  assert.equal(await h.evaluation.export('jsonl'), true);
  assert.equal(await h.evaluation.export('csv'), false);
  assert.deepEqual(h.calls.exported, ['json', 'jsonl']);
  assert.match(h.node('voice-evaluation-status').textContent, /evaluation.jsonl/);
  const html = voiceEvaluationMarkup();
  assert.match(html, /修正文はローカルに保存され，JSON／JSONLにも出力/);
  assert.match(html, /候補文・元の発話・履歴は評価ファイルに保存・出力しません/);
  assert.match(html, /保存済みの全評価/);
  assert.equal(Object.keys(VOICE_FAILURE_TAGS).length, 10);
});

test('late export result from another session is suppressed and export errors stay generic', async () => {
  const h = harness();
  const pending = deferred();
  h.bridge.ExportInteractionEvaluations = () => pending.promise;
  const exporting = h.evaluation.export('json');
  h.session('session_2');
  pending.resolve({path: '/old/session.json'});
  assert.equal(await exporting, false);
  assert.equal(h.node('voice-evaluation-status').textContent, '');
  h.bridge.ExportInteractionEvaluations = async () => { throw new Error('secret'); };
  assert.equal(await h.evaluation.export('jsonl'), false);
  assert.doesNotMatch(h.node('voice-evaluation-status').textContent, /secret/);
});

test('replay reserves controller before starting bridge and uses the same source history', async () => {
  const h = harness();
  h.node('voice-replay-transcript').value = '  corrected transcript  ';
  let reserved = false;
  h.bridge.ReplayInteraction = async (request) => {
    reserved = h.controller.active;
    h.calls.replay.push(request);
    return {...turn('operation_replay'), state: 'RECORDING'};
  };
  assert.equal(await h.evaluation.replay(), true);
  assert.equal(reserved, true);
  assert.deepEqual(h.calls.replay, [{source_operation_id: 'operation_1', transcript: 'corrected transcript'}]);
  assert.equal(h.calls.adopted.length, 1);
  assert.ok(h.calls.adopted[0] instanceof Promise);
  assert.equal(h.node('voice-comparison-results').hidden, true);
});

test('active controller blocks replay without creating a backend operation', async () => {
  const h = harness();
  h.controller.active = true;
  assert.equal(await h.evaluation.replay(), false);
  assert.equal(h.calls.replay.length, 0);
  assert.equal(h.calls.adopted.length, 0);
});

test('replay cancels a created backend operation if controller adoption fails', async () => {
  const h = harness({controller: {async adopt() { return false; }}});
  assert.equal(await h.evaluation.replay(), false);
  assert.deepEqual(h.calls.canceledVoice, ['operation_replay']);
});

test('session switch during replay cancels its late operation instead of adopting old-session output', async () => {
  const h = harness();
  const pending = deferred();
  h.bridge.ReplayInteraction = () => pending.promise;
  const replaying = h.evaluation.replay();
  await tick();
  h.session('session_2');
  pending.resolve({...turn('operation_replay'), state: 'RECORDING'});
  assert.equal(await replaying, false);
  assert.deepEqual(h.calls.canceledVoice, ['operation_replay']);
  assert.equal(h.node('voice-replay-transcript').value, '');
  assert.equal(h.node('voice-candidate-a').textContent, '');
});

test('replay cancellation retries a transient bridge rejection without losing the created operation ID', async () => {
  const h = harness();
  const pending = deferred();
  let attempts = 0;
  h.bridge.ReplayInteraction = () => pending.promise;
  h.bridge.CancelInteraction = async (operation) => {
    h.calls.canceledVoice.push(operation);
    if (++attempts === 1) throw new Error('temporary bridge failure');
  };
  const replaying = h.evaluation.replay();
  await tick();
  h.session('session_2');
  pending.resolve({...turn('operation_replay'), state: 'RECORDING'});
  assert.equal(await replaying, false);
  assert.deepEqual(h.calls.canceledVoice, ['operation_replay', 'operation_replay']);
});

test('invalid comparison configuration never calls backend', async () => {
  const h = harness();
  h.node('voice-temperature-b').value = '0.2';
  assert.equal(await h.evaluation.generate(), false);
  h.node('voice-temperature-b').value = 'Infinity';
  assert.equal(await h.evaluation.generate(), false);
  h.node('voice-temperature-b').value = '0.7';
  h.node('voice-mode-a').value = '<script>';
  assert.equal(await h.evaluation.generate(), false);
  assert.equal(h.calls.generation.length, 0);
});

test('dispose cancels pending comparison，removes listeners and prevents late candidates', async () => {
  const h = harness();
  const pending = deferred();
  h.bridge.GenerateInteractionComparison = () => pending.promise;
  const generating = h.evaluation.generate();
  await h.evaluation.dispose();
  assert.deepEqual(h.calls.canceled, ['comparison_test_1']);
  pending.resolve({comparison_id: 'comparison_test_1', source_operation_id: 'operation_1', candidate_a: 'late', candidate_b: 'late'});
  assert.equal(await generating, false);
  h.node('voice-feedback').click();
  assert.equal(h.calls.feedback.length, 0);
  assert.equal(h.node('voice-candidate-a').textContent, '');
});
