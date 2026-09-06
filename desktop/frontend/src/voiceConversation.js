function revision(snapshot) { return snapshot?.generation_revision || 1; }

function matches(entry, snapshot) {
  return entry?.voice && entry.requestId === snapshot?.operation_id
    && entry.generationRevision === revision(snapshot);
}

// Display previews separately．Only the Runtime assembler decides which text
// is confirmed，and only its successful terminal can enter conversation history．
export function resumeVoiceEntry(entry, snapshot) {
  return {
    ...entry,
    voice: true,
    generationRevision: revision(snapshot),
    streaming: true,
    terminalState: '',
    finishReason: '',
    canContinue: false,
    meta: '音声 · 生成中',
    text: snapshot.response_plan?.text || '',
    pendingText: '',
    completionPrefix: '',
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
  const meta = {
    COMPLETED: '音声 · 完了',
    INCOMPLETE: '音声 · 未完了 · 続きを生成できます',
    CANCELED: '音声 · 停止',
    FAILED: '音声 · 完了できませんでした · 文字入力で続行できます',
  }[state] || '音声 · 未完了';
  return {
    ...entry,
    text: snapshot.response_plan?.text || '',
    pendingText: '',
    streaming: false,
    terminalState: state,
    finishReason: snapshot.generation?.finish_reason || (state === 'CANCELED' ? 'canceled' : 'unknown'),
    canContinue: state === 'INCOMPLETE',
    meta,
  };
}
