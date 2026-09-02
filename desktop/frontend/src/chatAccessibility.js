export function announceChatStream(root, state, message = '') {
  const announcer = root?.getElementById?.('chat-stream-announcement');
  if (!announcer) return;
  const detail = String(message || '').trim();
  if (state === 'streaming') {
    announcer.textContent = 'Assistant response in progress.';
  } else if (state === 'complete') {
    announcer.textContent = 'Assistant response complete.';
  } else if (state === 'error') {
    announcer.textContent = detail ? `Assistant response failed: ${detail}` : 'Assistant response failed.';
  } else {
    announcer.textContent = '';
  }
}

export function chatMessageAccessibilityAttributes(entry = {}) {
  return `aria-busy="${entry.streaming ? 'true' : 'false'}"`;
}
