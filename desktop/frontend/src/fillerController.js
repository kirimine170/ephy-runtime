import {predictFiller} from './fillerTiming.js';

export const FILLER_TRACE_KINDS = new Set(['filler_started', 'filler_ended', 'filler_disabled', 'filler_expired',
  'filler_suppressed_fast', 'filler_stopped_answer', 'filler_stopped_cancel', 'filler_stopped_barge_in',
  'filler_stopped_invalidated', 'filler_failed', 'filler_watchdog', 'filler_gap', 'filler_gap_exceeded']);

// The lifetime is one user turn，not one generation attempt．Ports are synchronous
// because assets must already be decoded before arming．No speech text is accepted．
export function createFillerController({samples, durationMS, ledger, play, stop, isCurrent,
  now = () => performance.now(), timers = globalThis, onTrace = () => {}, onUnsafeStop = () => {}}) {
  let state = 'DISABLED', origin = null, timer = null, epoch = 0, started = null, ended = null, marks = {};
  const emit = (kind, latency) => {
    if (FILLER_TRACE_KINDS.has(kind)) {
      try { onTrace({kind, latency_ms: Math.max(0, Math.round(latency || 0))}); } catch { /* Telemetry cannot block audio． */ }
    }
  };
  const clear = () => { if (timer !== null) timers.clearTimeout(timer); timer = null; epoch++; };
  const halt = () => {
    if (state !== 'PLAYING') return;
    try { stop(); } catch { onUnsafeStop(); }
  };
  function close(kind) {
    if (state === 'CLOSED') return;
    clear();
    halt();
    if (state === 'PLAYING') emit(kind, now() - started);
    else if (state === 'ARMED') emit(kind === 'filler_stopped_answer' ? 'filler_suppressed_fast' : kind, now() - origin);
    state = 'CLOSED';
  }
  function schedule(delay) {
    clear();
    const version = epoch;
    timer = timers.setTimeout(() => {
      if (version !== epoch || state !== 'ARMED') return;
      timer = null;
      evaluate();
    }, Math.max(1, delay));
  }
  function evaluate() {
    if (state !== 'ARMED') return;
    if (!isCurrent() || ledger.used) { close('filler_stopped_invalidated'); return; }
    const prediction = predictFiller(samples, now() - origin, marks, durationMS);
    if (prediction.action === 'wait') { schedule(prediction.delay_ms); return; }
    if (prediction.action !== 'fire') {
      emit(prediction.action === 'expire' ? 'filler_expired' : 'filler_disabled', now() - origin);
      clear(); state = 'CLOSED'; return;
    }
    clear();
    // Consume the budget even if the output device fails．Never retry a syllable．
    ledger.used = true; state = 'PLAYING'; started = now();
    const version = epoch;
    try {
      play(() => {
        if (version !== epoch || state !== 'PLAYING') return;
        ended = now(); clear(); state = 'SPENT'; emit('filler_ended', ended - started);
      });
      emit('filler_started', started - origin);
      timer = timers.setTimeout(() => {
        if (version !== epoch || state !== 'PLAYING') return;
        close('filler_watchdog');
      }, durationMS + 100);
    } catch { close('filler_failed'); }
  }
  return {
    get state() { return state; },
    arm(endpoint) {
      if (state !== 'DISABLED' || ledger.used || !Number.isFinite(endpoint) || !isCurrent()) return;
      origin = endpoint; state = 'ARMED'; evaluate();
    },
    progress(next) { marks = {...next}; if (state === 'ARMED') evaluate(); },
    // Displayed text suppresses new fillers but never cuts a playing clip．
    answerVisible() { if (state === 'ARMED' || state === 'DISABLED') close('filler_stopped_answer'); },
    answerReady() {
      if (ended !== null) {
        const gap = now() - ended;
        emit(gap > 300 ? 'filler_gap_exceeded' : 'filler_gap', gap); ended = null;
      }
      close('filler_stopped_answer');
    },
    cancel(reason = 'cancel') { close(reason === 'barge_in' ? 'filler_stopped_barge_in' : reason === 'cancel' ? 'filler_stopped_cancel' : 'filler_stopped_invalidated'); },
  };
}

export function createFillerPlayer(context, buffer) {
  let source = null, gain = null;
  function stop() {
    const old = source, oldGain = gain;
    source = gain = null;
    if (!old) return;
    // Muting disconnects audibility even if source.stop throws．
    old.onended = null;
    oldGain.gain.value = 0;
    oldGain.disconnect();
    try { old.stop(); } finally { old.disconnect(); }
  }
  return {
    stop,
    play(onEnded) {
      if (source || context.state === 'closed' || context.state === 'suspended') throw new Error('filler_unavailable');
      source = context.createBufferSource(); gain = context.createGain();
      const current = source;
      source.buffer = buffer; source.connect(gain); gain.connect(context.destination);
      source.onended = () => { if (source !== current) return; stop(); onEnded(); };
      source.start();
    },
  };
}
