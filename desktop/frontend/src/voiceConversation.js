function revision(snapshot) { return snapshot?.generation_revision || 1; }

function matches(entry, snapshot) {
  return entry?.voice && entry.requestId === snapshot?.operation_id
    && entry.generationRevision === revision(snapshot);
}

// Display previews separately．Only the Runtime assembler decides which text
// is confirmed，and only its successful terminal can enter conversation history．
export function resumeVoiceEntry(entry, snapshot) {
  const inputKind = snapshot.input_kind || entry.inputKind || 'microphone';
  return {
    ...entry,
    voice: true,
    inputKind,
    generationRevision: revision(snapshot),
    streaming: true,
    terminalState: '',
    finishReason: '',
    canContinue: false,
    meta: `${inputKind === 'text' ? 'チャット' : '音声'} · 生成中`,
    text: snapshot.response_plan?.text || '',
    pendingText: '',
    completionPrefix: '',
    voiceDelivery: null,
  };
}

export function previewVoiceEntry(entry, snapshot, delta) {
  if (!matches(entry, snapshot) || !entry.streaming) return entry;
  return {...entry, pendingText: `${entry.pendingText || ''}${delta || ''}`.slice(0, 65536)};
}

export function confirmVoiceEntry(entry, snapshot) {
  if (!matches(entry, snapshot) || !entry.streaming) return entry;
  return {...entry, text: snapshot.response_plan?.text || '', pendingText: ''};
}

export function settleVoiceEntry(entry, snapshot) {
  if (!matches(entry, snapshot) || !entry.streaming) return entry;
  const state = snapshot.state === 'COMPLETED' && snapshot.generation?.complete !== true
    ? 'FAILED' : snapshot.state;
  const label = entry.inputKind === 'text' ? 'チャット' : '音声';
  const meta = {
    COMPLETED: `${label} · 完了`,
    INCOMPLETE: `${label} · 未完了 · 続きを生成できます`,
    CANCELED: `${label} · 停止`,
    FAILED: `${label} · 完了できませんでした · 文字入力で続行できます`,
  }[state] || `${label} · 未完了`;
  const text = snapshot.response_plan?.text || '';
  const units = Array.isArray(snapshot.speech_units) ? snapshot.speech_units : [];
  let playedPrefix = '';
  for (const unit of units) {
    if (unit.state !== 'completed' || !unit.synthesis_complete) break;
    playedPrefix += unit.text || '';
  }
  // A displayed sentence is not proof that its audio was heard．Keep the
  // generation，rendered text and observed unit boundaries as separate facts．
  const delivery = snapshot.interruption || Array.isArray(snapshot.speech_units) ? {
    generation: snapshot.generation?.complete === true ? 'completed' : state === 'CANCELED' ? 'canceled' : 'failed',
    display: !text ? 'none' : snapshot.generation?.complete === true ? 'confirmed_full' : 'confirmed_prefix',
    displayedText: text,
    playback: snapshot.speech_error_code ? 'failed' : units.some(u => u.state === 'interrupted') ? 'interrupted'
      : units.length && units.every(u => u.state === 'completed') && snapshot.generation?.complete === true ? 'completed'
      : snapshot.interruption && units.length ? 'interrupted'
      : units.some(u => u.playback_started) ? 'unknown' : 'not_started',
    speechUnits: units.map(({unit_id, state}) => ({unit_id, state})),
    playedPrefix,
    interrupted: !!snapshot.interruption,
  } : null;
  return {
    ...entry,
    text,
    voiceDelivery: delivery,
    pendingText: '',
    streaming: false,
    terminalState: state,
    finishReason: snapshot.generation?.finish_reason || (state === 'CANCELED' ? 'canceled' : 'unknown'),
    canContinue: state === 'INCOMPLETE',
    meta: snapshot.speech_error_code && snapshot.generation?.complete === true ? `${label} · 回答は完了 · 読み上げ失敗`
      : state === 'CANCELED' && snapshot.generation?.complete === true ? `${label} · 再生を停止 · 文章の生成は完了` : meta,
  };
}
