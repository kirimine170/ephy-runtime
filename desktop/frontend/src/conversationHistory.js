// Only user/assistant text enters the shared conversation history．Never add source cards or profile fields．
export function conversationHistory(entries) {
  const result = [];
  let bytes = 0;
  const encoder = new TextEncoder();
  for (const entry of [...entries].reverse()) {
    if (!['user', 'assistant'].includes(entry.role) || entry.streaming || !entry.text || entry.meta === 'error') continue;
    const length = encoder.encode(entry.text).length;
    if (length > 16000 || bytes + length > 48000 || result.length >= 28) break;
    bytes += length;
    result.unshift({role: entry.role, content: entry.text});
  }
  return result;
}
