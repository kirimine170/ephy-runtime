// C0.4 timing only．No token，transcript，answer，audio or candidate is accepted．
export const TIMING_KEYS = Object.freeze(['llm_request_ms', 'llm_first_ms', 'tts_request_ms', 'tts_chunk_ms', 'answer_ready_ms', 'llm_ttft_ms', 'tts_latency_ms']);
const STAGES = Object.freeze({llm_requested: 'llm_request_ms', llm_first_token: 'llm_first_ms', tts_requested: 'tts_request_ms', tts_first_chunk: 'tts_chunk_ms'});
export const MIN_SAMPLES = 30;
export const MAX_SAMPLES = 200;
export function quantile(values, p) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.max(0, Math.ceil(sorted.length * p) - 1)];
}
export function validTimingSample(s) {
  if (!s || Object.keys(s).length !== TIMING_KEYS.length || TIMING_KEYS.some(k => !Number.isFinite(s[k]) || s[k] < 0 || s[k] > 180000)) return false;
  return s.llm_request_ms <= s.llm_first_ms && s.llm_first_ms <= s.tts_request_ms
    && s.tts_request_ms <= s.tts_chunk_ms && s.tts_chunk_ms <= s.answer_ready_ms;
}
export function summarizeTimings(samples) {
  const valid = samples.filter(validTimingSample).slice(-MAX_SAMPLES);
  const summary = {count: valid.length};
  for (const k of TIMING_KEYS) summary[k] = {p50: quantile(valid.map(s => s[k]), .5), p95: quantile(valid.map(s => s[k]), .95)};
  return summary;
}

// All scheduling timestamps use one frontend monotonic clock at event delivery．
// Native LLM TTFT／TTS latency separately use Go's monotonic trace differences．
export function createTimingObserver(now = () => performance.now()) {
  let origin = null;
  const marks = {}, native = {};
  return {
    trace(trace) {
      const name = trace?.name;
      if (name === 'endpoint_commit' && origin === null) origin = now();
      if (origin === null || !STAGES[name] || marks[STAGES[name]] !== undefined) return;
      marks[STAGES[name]] = Math.max(0, now() - origin);
      if (Number.isFinite(trace.monotonic_ms)) native[name] = trace.monotonic_ms;
    },
    get origin() { return origin; },
    get marks() { return {...marks}; },
    ready() {
      if (origin === null || marks.answer_ready_ms !== undefined) return null;
      marks.answer_ready_ms = Math.max(0, now() - origin);
      const sample = {...marks, llm_ttft_ms: native.llm_first_token - native.llm_requested,
        tts_latency_ms: native.tts_first_chunk - native.tts_requested};
      return validTimingSample(sample) ? sample : null;
    },
  };
}

// Pair samples within one pinned model／voice／hardware condition．Never add
// marginal p95s．Survivor conditioning prevents subtracting elapsed time from
// an unconditional median after that median has already passed．
export function predictFiller(samples, elapsed, marks, duration, options = {}) {
  const gap = options.gap_ms ?? 150, maxGap = options.max_gap_ms ?? 300;
  const imminent = options.imminent_ms ?? 150;
  if (![elapsed, duration, gap, maxGap, imminent].every(Number.isFinite) || elapsed < 0 || duration < 150 || duration > 1500
      || gap < 0 || gap > maxGap || maxGap > 300 || imminent < 100 || imminent > 300) return {action: 'disable'};
  const all = samples.filter(validTimingSample).slice(-MAX_SAMPLES);
  if (all.length < MIN_SAMPLES) return {action: 'disable'};
  const a50 = quantile(all.map(s => s.answer_ready_ms), .5), a95 = quantile(all.map(s => s.answer_ready_ms), .95);
  if (elapsed >= a95) return {action: 'expire'};
  // Reserve the first half of the fast quartile's waiting time as silence．
  const base = Math.max(quantile(all.map(s => s.answer_ready_ms), .25) / 2, a50 - duration - gap);
  if (elapsed < base) return {action: 'wait', delay_ms: base - elapsed};
  const stage = ['tts_chunk_ms', 'tts_request_ms', 'llm_first_ms', 'llm_request_ms'].find(k => Number.isFinite(marks[k]));
  const pending = ['llm_request_ms', 'llm_first_ms', 'tts_request_ms', 'tts_chunk_ms'].filter(k => !Number.isFinite(marks[k]));
  const survivors = all.filter(s => s.answer_ready_ms > elapsed && pending.every(k => s[k] > elapsed)
    && (!stage || Math.abs(s[stage] - marks[stage]) <= Math.max(150, .2 * s[stage])));
  // A small tail must not masquerade as a reliable conditional p95．
  if (survivors.length < MIN_SAMPLES) return {action: 'wait', delay_ms: Math.min(50, a95 - elapsed)};
  const r50 = quantile(survivors.map(s => s.answer_ready_ms - elapsed), .5);
  const r95 = quantile(survivors.map(s => s.answer_ready_ms - elapsed), .95);
  if (r50 >= imminent && Math.abs(r50 - duration - gap) <= 150 && r95 <= duration + maxGap) return {action: 'fire'};
  return {action: 'wait', delay_ms: Math.min(50, a95 - elapsed)};
}
