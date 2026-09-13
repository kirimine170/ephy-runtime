// One candidate may wait briefly for a conversational gap．This owns one timer
// and never generates，refreshes or requeues a candidate after its deadline．
export function createResidentCandidateWait({now = () => Date.now(), timers = globalThis, onDecision = () => {}} = {}) {
  let pending = null;
  let disposed = false;
  function settle(item, decision) {
    if (pending !== item) return;
    if (item.timer != null) timers.clearTimeout(item.timer);
    pending = null;
    onDecision(decision);
    item.resolve(decision);
  }
  function check(item) {
    if (pending !== item) return;
    item.timer = null;
    const time = now();
    if (disposed || !item.isCurrent() || time >= item.deadline) { settle(item, 'discard'); return; }
    const cooldownRemaining = Math.max(0, item.cooldownUntil - time);
    // A long cooldown cannot be shortened by waiting or refreshing the ticket．
    if (cooldownRemaining >= item.deadline - time) { settle(item, 'discard'); return; }
    if (!item.isHumanSpeaking() && cooldownRemaining === 0) { settle(item, 'speak'); return; }
    if (!item.waiting) { item.waiting = true; onDecision('wait'); }
    item.timer = timers.setTimeout(() => check(item), Math.min(250, item.deadline - time));
  }
  return {
    offer({expiresAt, isCurrent, isHumanSpeaking, cooldownUntil = 0}) {
      if (pending) settle(pending, 'discard');
      if (disposed || !Number.isFinite(expiresAt) || !Number.isFinite(cooldownUntil)) return Promise.resolve('discard');
      return new Promise(resolve => {
        const time = now();
        const item = {resolve, isCurrent, isHumanSpeaking, cooldownUntil,
          deadline: Math.min(time + 3000, expiresAt), timer: null, waiting: false};
        pending = item;
        check(item);
      });
    },
    cancel() { if (pending) settle(pending, 'discard'); },
    dispose() { disposed = true; if (pending) settle(pending, 'discard'); },
    get waiting() { return !!pending; },
  };
}
