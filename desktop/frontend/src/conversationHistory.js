// Legacy completed text entries have no terminalState．Voice entries require
// an explicit COMPLETED terminal and never promote preview or failed text．
export function isConversationHistoryEntry(entry) {
  if (!entry || !['user', 'assistant'].includes(entry.role) || entry.streaming || !entry.text || entry.meta === 'error') return false;
  if (entry.role === 'assistant') {
    if (entry.voice && entry.terminalState !== 'COMPLETED') return false;
    if (entry.terminalState && entry.terminalState !== 'COMPLETED') return false;
    if (['length', 'tool_calls', 'timeout', 'transport_eof', 'canceled', 'unknown'].includes(entry.finishReason)) return false;
  }
  return true;
}

// Only user/assistant text enters the shared conversation history．Never add source cards or profile fields．
export function conversationHistory(entries) {
  const result = [];
  let bytes = 0;
  const encoder = new TextEncoder();
  for (const entry of [...entries].reverse()) {
    const interruption = entry?.role === 'assistant' && entry.voice && !entry.streaming
      && entry.terminalState === 'CANCELED' && entry.voiceDelivery?.interrupted;
    if (!interruption && !isConversationHistoryEntry(entry)) continue;
    const content = interruption ? '[音声応答はユーザーの発話で中断された．自然終了を確認できた発話単位のみを以下に残す．中断した単位のどの単語まで届いたかは不明．]'
      + (entry.voiceDelivery.playedPrefix ? `\n${entry.voiceDelivery.playedPrefix}` : '\n[自然終了を確認できた発話単位はない．]') : entry.text;
    const length = encoder.encode(content).length;
    if (length > 16000 || bytes + length > 48000 || result.length >= 28) break;
    bytes += length;
    result.unshift({role: entry.role, content});
  }
  return result;
}
