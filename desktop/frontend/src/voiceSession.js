// A canceled operation may finish after the user has opened a different conversation．
export function voiceSessionCallbacks({getSessionID, onDetachedFinish = () => {}, ...handlers}) {
  let busySession = '';
  const callbacks = {};
  for (const name of ['onTranscript', 'onToken', 'onOutput', 'onComplete', 'onIncomplete', 'onCancel', 'onFailure', 'onFallback']) {
    callbacks[name] = (snapshot, ...args) => {
      if (!snapshot?.session_id || snapshot.session_id !== getSessionID()) return;
      handlers[name]?.(snapshot, ...args);
    };
  }
  callbacks.onBusy = busy => {
    if (busy) busySession = getSessionID();
    if (busySession === getSessionID()) handlers.onBusy?.(busy);
    else if (!busy) onDetachedFinish();
  };
  return callbacks;
}
