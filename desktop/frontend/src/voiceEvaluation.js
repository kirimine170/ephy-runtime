const TERMINAL = new Set(['COMPLETED', 'CANCELED', 'FAILED']);
const MODES = ['auto', 'fast', 'work', 'code', 'rag'];
const CHOICES = new Set(['A', 'B', 'tie', 'neither']);
export const VOICE_FAILURE_TAGS = Object.freeze({
  asr_error: '音声認識の誤り',
  early_endpoint: '発話の終了判定が早い',
  late_endpoint: '発話の終了判定が遅い',
  slow_response: '応答が遅い',
  too_long: '応答が長い',
  tone_mismatch: '話し方が合わない',
  tts_pronunciation: '読み上げの発音',
  cancel_failure: 'キャンセルの不具合',
  memory_misuse: '記憶の使い方',
  other: 'その他',
});

export function voiceEvaluationMarkup() {
  const modes = MODES.map((mode) => `<option value="${mode}">${mode}</option>`).join('');
  return `
    <fieldset id="voice-comparison-config">
      <legend>同じ発話と履歴から候補を比較</legend>
      <label class="field"><span>設定1のモード</span><select id="voice-mode-a">${modes}</select></label>
      <label class="field"><span>設定1のtemperature</span><input id="voice-temperature-a" type="number" min="0" max="2" step="0.1" value="0.2"></label>
      <label class="field"><span>設定2のモード</span><select id="voice-mode-b">${modes}</select></label>
      <label class="field"><span>設定2のtemperature</span><input id="voice-temperature-b" type="number" min="0" max="2" step="0.1" value="0.7"></label>
      <p>候補A／Bの表示順はランダムです．モデル名は表示しません．</p>
      <div class="actions">
        <button id="voice-comparison-generate" class="ghost-btn" type="button">比較候補を生成</button>
        <button id="voice-comparison-cancel" class="ghost-btn" type="button" disabled>比較の生成をキャンセル</button>
      </div>
    </fieldset>
    <div id="voice-comparison-results" hidden>
      <div class="voice-comparison">
        <section><h4>候補A</h4><div id="voice-candidate-a" class="voice-candidate"></div></section>
        <section><h4>候補B</h4><div id="voice-candidate-b" class="voice-candidate"></div></section>
      </div>
      <label class="field"><span>選択</span><select id="voice-comparison-choice">
        <option value="">選択してください</option><option value="A">A</option><option value="B">B</option>
        <option value="tie">同程度</option><option value="neither">どちらも選ばない</option>
      </select></label>
      <label class="field"><span>修正文（任意）</span><textarea id="voice-comparison-correction" class="text-area" maxlength="16000" aria-describedby="voice-evaluation-storage"></textarea></label>
      <p id="voice-evaluation-storage">修正文はローカルに保存され，JSON／JSONLにも出力されます．候補文・元の発話・履歴は評価ファイルに保存・出力しません．</p>
      <fieldset><legend>気になった点（複数選択可）</legend>
        ${Object.entries(VOICE_FAILURE_TAGS).map(([tag, label]) => `<label><input type="checkbox" data-voice-failure="${tag}" value="${tag}">${label}</label>`).join('')}
      </fieldset>
      <button id="voice-comparison-save" class="ghost-btn" type="button">評価を保存</button>
    </div>
    <p>保存済みの全評価をローカルファイルへ出力します．修正文も含まれます．</p>
    <div class="actions">
      <button id="voice-evaluation-json" class="ghost-btn" type="button">JSONへ出力</button>
      <button id="voice-evaluation-jsonl" class="ghost-btn" type="button">JSONLへ出力</button>
    </div>
    <p id="voice-evaluation-status" role="status" aria-live="polite"></p>
  `;
}

/** Refresh after voice lifecycle callbacks，session changes and Developer Mode changes． */
export function mountVoiceEvaluation({
  root, bridge, controller, getSessionID, isDeveloper,
  makeRequestID = () => `comparison_${globalThis.crypto.randomUUID()}`,
}) {
  const container = root.querySelector('#voice-evaluation');
  if (container) container.innerHTML = voiceEvaluationMarkup();
  const node = (id) => root.querySelector(`#${id}`);
  const feedbackButton = node('voice-feedback');
  const replayButton = node('voice-replay');
  const transcript = node('voice-replay-transcript');
  const status = node('voice-evaluation-status');
  const generateButton = node('voice-comparison-generate');
  const cancelButton = node('voice-comparison-cancel');
  const results = node('voice-comparison-results');
  const candidateA = node('voice-candidate-a');
  const candidateB = node('voice-candidate-b');
  const choice = node('voice-comparison-choice');
  const correction = node('voice-comparison-correction');
  const saveButton = node('voice-comparison-save');
  const modeA = node('voice-mode-a');
  const modeB = node('voice-mode-b');
  const temperatureA = node('voice-temperature-a');
  const temperatureB = node('voice-temperature-b');
  const exportJSON = node('voice-evaluation-json');
  const exportJSONL = node('voice-evaluation-jsonl');
  const tags = [...root.querySelectorAll('[data-voice-failure]')];
  const listeners = [];
  let disposed = false;
  let epoch = 0;
  let key = null;
  let source = null;
  let developer = false;
  let pending = null;
  let comparison = null;
  let saving = false;
  let saved = false;
  let flagging = false;
  let flagged = false;
  let flagFailed = false;
  let replaying = false;
  let exporting = false;
  let generationVersion = 0;

  function setStatus(text) { if (status) status.textContent = text; }

  function eligibleSnapshot() {
    const snapshot = controller.lastSnapshot;
    return !controller.isActive() && snapshot?.operation_id
      && snapshot.session_id === getSessionID() && TERMINAL.has(snapshot.state) ? snapshot : null;
  }

  function render() {
    if (container) container.hidden = !developer;
    if (feedbackButton) {
      feedbackButton.disabled = !source || flagging || flagged || disposed;
      feedbackButton.textContent = flagging ? '違和感を記録しています' : flagged ? '違和感を記録しました' : flagFailed ? '記録できませんでした．再試行' : '違和感を記録';
    }
    if (replayButton) replayButton.disabled = !developer || !source || replaying || !!pending || saving || disposed;
    if (transcript) transcript.disabled = !developer || !source || replaying || disposed;
    if (generateButton) generateButton.disabled = !developer || !source?.transcript || !!pending || replaying || saving || disposed;
    if (cancelButton) cancelButton.disabled = !pending || pending.canceled || disposed;
    if (results) results.hidden = !developer || !comparison;
    for (const control of [modeA, modeB, temperatureA, temperatureB]) {
      if (control) control.disabled = !developer || !!pending || saving || disposed;
    }
    for (const control of [choice, correction, ...tags]) {
      if (control) control.disabled = !comparison || saving || disposed;
    }
    if (saveButton) {
      saveButton.disabled = !developer || !comparison || saving || !CHOICES.has(choice?.value) || disposed;
      saveButton.textContent = saving ? '評価を保存しています' : saved ? '評価を更新' : '評価を保存';
    }
    for (const button of [exportJSON, exportJSONL]) {
      if (button) button.disabled = !developer || exporting || saving || disposed;
    }
  }

  function cancelTask(task) {
    if (!task) return Promise.resolve();
    if (task.cancelPromise) return task.cancelPromise;
    task.canceled = true;
    task.cancelPromise = (async () => {
      try { await bridge.CancelInteractionComparison(task.id); return true; }
      catch { return false; }
    })();
    return task.cancelPromise;
  }

  function clearResults() {
    comparison = null;
    saved = false;
    if (candidateA) candidateA.textContent = '';
    if (candidateB) candidateB.textContent = '';
    if (choice) choice.value = '';
    if (correction) correction.value = '';
    for (const tag of tags) tag.checked = false;
  }

  function refresh() {
    if (disposed) return;
    developer = !!isDeveloper();
    const snapshot = eligibleSnapshot();
    const nextKey = `${getSessionID()}\n${controller.lastSnapshot?.operation_id || ''}\n${controller.isActive()}\n${developer}`;
    if (nextKey !== key) {
      epoch += 1;
      key = nextKey;
      void cancelTask(pending);
      pending = null;
      clearResults();
      saving = flagging = flagged = flagFailed = replaying = exporting = false;
      if (transcript) transcript.value = snapshot?.transcript || '';
      setStatus('');
    }
    source = snapshot;
    render();
  }

  function unchanged(token, operationID) {
    refresh();
    return !disposed && token === epoch && source?.operation_id === operationID;
  }

  async function feedback() {
    refresh();
    if (!source || flagging || flagged || disposed) return false;
    const token = epoch;
    const operationID = source.operation_id;
    flagging = true;
    flagFailed = false;
    render();
    try {
      await bridge.RecordInteractionFeedback(operationID);
      if (!unchanged(token, operationID)) return false;
      flagged = true;
      return true;
    } catch {
      if (unchanged(token, operationID)) flagFailed = true;
      return false;
    } finally {
      if (unchanged(token, operationID)) { flagging = false; render(); }
    }
  }

  async function generate() {
    refresh();
    if (!developer || !source?.transcript || pending || replaying || saving || disposed) return false;
    const first = Number(temperatureA?.value);
    const second = Number(temperatureB?.value);
    if (!MODES.includes(modeA?.value) || !MODES.includes(modeB?.value)
      || temperatureA?.value.trim() === '' || temperatureB?.value.trim() === ''
      || !Number.isFinite(first) || !Number.isFinite(second) || first < 0 || first > 2 || second < 0 || second > 2
      || (modeA.value === modeB.value && first === second)) {
      setStatus('異なる2つの設定を選んでください．temperatureは0から2です．');
      return false;
    }
    let requestID;
    try { requestID = makeRequestID(); } catch {
      setStatus('比較の準備ができませんでした．再試行してください．');
      return false;
    }
    if (!/^comparison_[A-Za-z0-9_.:-]{1,116}$/.test(requestID)) {
      setStatus('比較の準備ができませんでした．再試行してください．');
      return false;
    }
    const task = {id: requestID, operationID: source.operation_id, epoch, version: ++generationVersion, canceled: false, cancelPromise: null};
    pending = task;
    clearResults();
    setStatus('比較候補を生成しています．');
    render();
    try {
      const pair = await bridge.GenerateInteractionComparison({
        request_id: task.id, source_operation_id: task.operationID,
        mode_a: modeA.value, mode_b: modeB.value, temperature_a: first, temperature_b: second,
      });
      if (!unchanged(task.epoch, task.operationID) || pending !== task || task.canceled) return false;
      const allowed = new Set(['comparison_id', 'source_operation_id', 'candidate_a', 'candidate_b']);
      if (!pair || Object.keys(pair).some((name) => !allowed.has(name))
        || pair.comparison_id !== task.id || pair.source_operation_id !== task.operationID
        || typeof pair.candidate_a !== 'string' || typeof pair.candidate_b !== 'string'
        || !pair.candidate_a.trim() || !pair.candidate_b.trim()
        || pair.candidate_a.length > 64000 || pair.candidate_b.length > 64000) {
        throw new Error('invalid_blind_comparison');
      }
      comparison = {id: pair.comparison_id, operationID: pair.source_operation_id};
      // Candidate text must never enter HTML or an attribute．No identity is retained here．
      if (candidateA) candidateA.textContent = pair.candidate_a;
      if (candidateB) candidateB.textContent = pair.candidate_b;
      setStatus('候補を選び，必要なら修正文と気になった点を入力してください．');
      return true;
    } catch {
      if (unchanged(task.epoch, task.operationID) && pending === task && !task.canceled) {
        setStatus('比較候補を生成できませんでした．再試行してください．');
      }
      return false;
    } finally {
      if (pending === task) { pending = null; render(); }
    }
  }

  async function cancelComparison() {
    refresh();
    const task = pending;
    if (!task) return false;
    const canceled = cancelTask(task);
    setStatus('比較の生成を停止しています．');
    render();
    const acknowledged = await canceled;
    if (unchanged(task.epoch, task.operationID) && task.version === generationVersion) {
      if (pending === task) pending = null;
      setStatus(acknowledged ? '比較の生成を停止しました．' : '停止を確認できませんでした．この比較の結果は表示しません．');
      render();
    }
    return acknowledged;
  }

  async function save() {
    refresh();
    if (!developer || !comparison || saving || disposed || comparison.operationID !== source?.operation_id) return false;
    const selected = choice?.value;
    const corrected = correction?.value || '';
    const failureTags = tags.filter((tag) => tag.checked).map((tag) => tag.value);
    if (!CHOICES.has(selected) || new TextEncoder().encode(corrected).length > 16000
      || failureTags.some((tag) => !Object.hasOwn(VOICE_FAILURE_TAGS, tag))) {
      setStatus('選択内容と修正文の長さを確認してください．');
      return false;
    }
    const token = epoch;
    const operationID = source.operation_id;
    const comparisonID = comparison.id;
    saving = true;
    setStatus('評価を保存しています．');
    render();
    try {
      await bridge.SaveInteractionEvaluation({comparison_id: comparisonID, choice: selected, correction: corrected, failure_tags: failureTags});
      if (!unchanged(token, operationID) || comparison?.id !== comparisonID) return false;
      saved = true;
      setStatus('評価を保存しました．選択や修正文を変更して更新できます．');
      return true;
    } catch {
      if (unchanged(token, operationID)) setStatus('評価を保存できませんでした．入力を保持しています．再試行してください．');
      return false;
    } finally {
      if (unchanged(token, operationID)) { saving = false; render(); }
    }
  }

  async function exportEvaluations(format) {
    refresh();
    if (!developer || exporting || saving || disposed || !['json', 'jsonl'].includes(format)) return false;
    const token = epoch;
    exporting = true;
    setStatus('保存済みの評価を出力しています．');
    render();
    try {
      const result = await bridge.ExportInteractionEvaluations(format);
      refresh();
      if (disposed || token !== epoch) return false;
      setStatus(typeof result?.path === 'string' ? `評価を出力しました：${result.path}` : '評価を出力しました．');
      return true;
    } catch {
      refresh();
      if (!disposed && token === epoch) setStatus('評価を出力できませんでした．再試行してください．');
      return false;
    } finally {
      if (!disposed && token === epoch) { exporting = false; render(); }
    }
  }

  async function replay() {
    refresh();
    if (!developer || !source || replaying || pending || saving || disposed || controller.isActive()) return false;
    const text = (transcript?.value || '').trim();
    if (!text || new TextEncoder().encode(text).length > 16000) {
      setStatus('再実行する発話を入力してください．長い発話は短くしてください．');
      return false;
    }
    const snapshot = source;
    const token = epoch;
    let createdReplay = null;
    replaying = true;
    render();
    // Reserve the controller synchronously，before dispatching the replay bridge．
    const pendingReplay = Promise.resolve().then(() => {
      if (disposed || snapshot.session_id !== getSessionID()) throw new Error('replay_abandoned');
      return bridge.ReplayInteraction({source_operation_id: snapshot.operation_id, transcript: text});
    }).then(async (replayed) => {
      createdReplay = replayed;
      if (disposed || snapshot.session_id !== getSessionID()) {
        if (replayed?.operation_id) await bridge.CancelInteraction(replayed.operation_id);
        createdReplay = null;
        throw new Error('replay_abandoned');
      }
      return replayed;
    });
    try {
      const adopted = await controller.adopt(pendingReplay);
      if (!adopted) {
        const replayed = await pendingReplay.catch(() => null) || createdReplay;
        if (replayed?.operation_id) await bridge.CancelInteraction(replayed.operation_id);
        createdReplay = null;
        if (unchanged(token, snapshot.operation_id)) setStatus('再実行できませんでした．再試行してください．');
        return false;
      }
      if (disposed || snapshot.session_id !== getSessionID()) { await controller.cancel(); return false; }
      refresh();
      return true;
    } catch {
      const replayed = await pendingReplay.catch(() => null) || createdReplay;
      if (replayed?.operation_id) {
        try { await bridge.CancelInteraction(replayed.operation_id); } catch { /* The controller also owns cancellation． */ }
      }
      if (unchanged(token, snapshot.operation_id)) setStatus('再実行できませんでした．再試行してください．');
      return false;
    } finally {
      if (unchanged(token, snapshot.operation_id)) { replaying = false; render(); }
    }
  }

  function bind(element, event, callback) {
    if (!element) return;
    const listener = () => { void callback(); };
    element.addEventListener(event, listener);
    listeners.push(() => element.removeEventListener(event, listener));
  }
  bind(feedbackButton, 'click', feedback);
  bind(replayButton, 'click', replay);
  bind(generateButton, 'click', generate);
  bind(cancelButton, 'click', cancelComparison);
  bind(saveButton, 'click', save);
  bind(exportJSON, 'click', () => exportEvaluations('json'));
  bind(exportJSONL, 'click', () => exportEvaluations('jsonl'));
  bind(choice, 'change', render);
  refresh();
  return {
    refresh, feedback, generate, cancelComparison, save, replay, export: exportEvaluations,
    async dispose() {
      if (disposed) return;
      disposed = true;
      epoch += 1;
      const cancellation = cancelTask(pending);
      pending = null;
      clearResults();
      if (transcript) transcript.value = '';
      setStatus('');
      for (const remove of listeners) remove();
      render();
      await cancellation;
    },
  };
}
