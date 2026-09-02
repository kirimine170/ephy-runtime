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

export function validateChatPrompt(input) {
  const isValid = Boolean(String(input?.value || '').trim());
  input?.setCustomValidity?.(isValid ? '' : 'Enter a message before sending.');
  if (!isValid) {
    input?.reportValidity?.();
    input?.focus?.();
  }
  return isValid;
}

export function transitionChatEntryToFailure(entry, message = '') {
  if (!entry?.streaming) return null;
  const failed = {
    ...entry,
    streaming: false,
    meta: 'error',
    canContinue: false,
    finishReason: '',
  };
  if (!String(failed.text || '').trim()) {
    failed.text = message || 'Streaming request failed.';
  }
  return failed;
}

export function mergeChatCompletionMetadata(entry, {
  meta = '',
  answer = '',
  thinking = '',
  finishReason = '',
} = {}) {
  if (!entry) return null;
  const completed = {
    ...entry,
    streaming: false,
    meta: meta || entry.meta,
    finishReason: finishReason || entry.finishReason || '',
  };
  if (thinking && !String(completed.thinking || '').trim()) {
    completed.thinking = thinking;
  }
  if (answer && !String(completed.text || '').trim()) {
    completed.text = answer;
  }
  return completed;
}

export function transitionChatEntryToComplete(entry, metadata = {}) {
  if (!entry?.streaming) return null;
  return mergeChatCompletionMetadata(entry, metadata);
}
