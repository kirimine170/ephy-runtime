// Input onset and interruption are deliberately independent．Energy opens a
// bounded recognition candidate；only recognized interruption speech can change
// playback．Speaker evidence is optional and remains metadata-only．
export const DEFAULT_INTERRUPTION = Object.freeze({rms: .025, onsetMS: 80, gapMS: 160,
  candidateMS: 1200, quietMS: 200, minimumSpeechMS: 180, waitingSpeechMS: 260,
  stableMS: 160, waitingStableMS: 240, explicitStableMS: 80, duckVolume: .72});

export function interruptionSettings(overrides = {}, modelActivity = false) {
  const result = {...DEFAULT_INTERRUPTION, ...(modelActivity ? {rms: .004, onsetMS: 40, candidateMS: 1800} : {})};
  for (const key of Object.keys(result)) {
    if (overrides[key] !== undefined) result[key] = overrides[key];
    if (!Number.isFinite(result[key]) || result[key] <= 0) throw new Error('invalid_interruption_setting');
  }
  if (result.rms > 1 || result.duckVolume > 1 || result.onsetMS > 300 || result.candidateMS > (modelActivity ? 1800 : 1200)
    || result.minimumSpeechMS < result.onsetMS || result.waitingSpeechMS < result.minimumSpeechMS
    || result.waitingStableMS < result.stableMS) throw new Error('invalid_interruption_setting');
  return result;
}

function speechText(text) {
  return typeof text === 'string' ? text.normalize('NFKC').toLowerCase().replace(/[\p{P}\p{Z}\s]/gu, '') : '';
}

const ACKNOWLEDGEMENTS = ['そうですね', 'そうだね', 'ありがとう', 'なるほど', 'おつかれ', 'エフィー', 'エフィ',
  'ええと', 'えーと', 'えっと', 'ふーん', 'そうか', 'お疲れ', 'ephy', 'うん', 'はい', 'ええ', 'へえ', 'ほう', 'そう', 'ねえ', 'あの', 'エフ'];
function acknowledgement(value) {
  // Bounded tokenization avoids exponential matching on repeated vowels．
  if (value.length > 128) return false;
  let offset = 0;
  while (offset < value.length) {
    const token = ACKNOWLEDGEMENTS.find(word => value.startsWith(word, offset));
    if (!token) return false;
    offset += token.length;
  }
  return offset > 0;
}

export function interruptionIntent(text) {
  const value = speechText(text);
  if (!/[\p{L}\p{N}]/u.test(value)) return 'empty';
  if (/^(待って|まって|ちょっと待|ちょっとまって|止めて|とめて|やめて|ストップ|stop|違う|ちがう|いや|もういい)/u.test(value)) return 'explicit';
  if (acknowledgement(value)) return 'acknowledgement';
  return [...value].length >= 3 ? 'speech' : 'short';
}

// This boundary deliberately carries only bounded decisions from a future
// target-speaker gate．Embeddings and audio never enter snapshots or traces．
export function speakerEvidence(activity) {
  if (!activity || typeof activity.has_speech !== 'boolean') return null;
  const speakerState = activity.speaker_state ?? 'unknown';
  const targetProbability = activity.target_probability ?? null;
  const confidence = activity.confidence ?? null;
  if (!['target', 'non_target', 'unknown'].includes(speakerState)
    || (targetProbability !== null && (!Number.isFinite(targetProbability) || targetProbability < 0 || targetProbability > 1))
    || (confidence !== null && (!Number.isFinite(confidence) || confidence < 0 || confidence > 1))) return null;
  return {hasSpeech: activity.has_speech, speakerState, targetProbability, confidence};
}

function knownNonTarget(evidence) {
  return evidence?.speakerState === 'non_target'
    || (evidence?.speakerState === 'unknown' && evidence.confidence >= .6 && evidence.targetProbability !== null && evidence.targetProbability <= .2);
}

// The existing PCM buffer stays session-owned and bounded at two seconds．
// Candidate failure clears only speculative input，never output or generation．
let nextCandidateID = 0;
export function createVoiceInterruption({bridge, identity, sampleRate, buffer, createEncoder,
  encodeBase64, now, timers, isCurrent, phase, onDuck, onConfirm, onTelemetry = () => {}, settings = DEFAULT_INTERRUPTION,
  modelActivity = false, duckEnabled = true,
  createID = () => `candidate_${Date.now().toString(36)}_${++nextCandidateID}`}) {
  let candidate = null, stopped = false, transferred = false;
  let activeMS = 0, quietMS = 0, blocked = false, noiseFloor = .003;
  const current = c => !stopped && candidate === c && isCurrent();
  const cancelBackend = c => {
    if (!c.id) return;
    try { Promise.resolve(bridge.CancelInteractionInterruptionCandidate(c.op, c.id, c.reason || 'detached')).catch(() => {}); } catch { /* Optional candidate cleanup is also bounded in Go． */ }
  };
  function duck(c, enabled, outcome = '') {
    if (enabled === !!c.ducked) return;
    c.ducked = enabled;
    if (enabled) {
      c.duckedAt = now();
      onDuck(true);
      onTelemetry({kind: 'duck_started', candidateID: c.id, durationMS: 0, outcome: ''});
      return;
    }
    const durationMS = Math.max(0, Math.round(now() - c.duckedAt));
    onDuck(false);
    onTelemetry({kind: 'duck_ended', candidateID: c.id, durationMS, outcome});
    if (outcome === 'rejected') onTelemetry({kind: 'false_duck', candidateID: c.id, durationMS, outcome});
  }
  function close(reason, preserve = false) {
    const c = candidate; candidate = null;
    if (c) {
      c.reason = reason;
      if (c.timer != null) timers.clearTimeout(c.timer);
      c.queue = []; c.queuedBytes = 0; c.encoder.reset();
      duck(c, false, reason === 'confirmed' ? 'confirmed' : 'rejected');
      cancelBackend(c);
    }
    activeMS = quietMS = 0; blocked = true;
    if (preserve) transferred = true;
    else if (!transferred) buffer.clear();
  }
  function inspect(c, snapshot) {
    if (!current(c)) return;
    if (now() - c.startedAt >= settings.candidateMS) { close('expired'); return; }
    if (!snapshot || snapshot.candidate_id !== c.id || snapshot.request?.operation_id !== c.op
      || snapshot.request?.session_id !== c.session || snapshot.request?.turn_id !== c.turn
      || snapshot.request?.segment_id !== c.segment || snapshot.request?.sample_rate !== c.encoder.outputRate
      || snapshot.error_code) { close('unavailable'); return; }
    const activity = snapshot.activity;
    if (activity) {
      const a = activity.activity;
      if (activity.operation_id !== c.op || activity.session_id !== c.session || activity.turn_id !== c.turn
          || activity.segment_id !== c.segment || activity.phase !== 'activity' || activity.error_code
          || !a || !Number.isSafeInteger(a.speech_ms) || a.speech_ms < 0 || a.speech_ms > (modelActivity ? 3000 : 2000)
          || !speakerEvidence(a)
          || !Number.isSafeInteger(activity.revision) || activity.revision < 1) { close('unavailable'); return; }
      // Model VAD and the RMS onset are separate observations．Text is still
      // required to confirm an interruption，including for a short command．
      if (activity.revision > (c.activityRevision || 0)) {
        c.activityRevision = activity.revision;
        c.modelSpeechMS = a.speech_ms;
        c.modelHasSpeech = a.has_speech;
        c.speaker = speakerEvidence(a);
        if (knownNonTarget(c.speaker)) { close('non_target'); return; }
      }
    }
    const update = snapshot.update;
    if (!update) return;
    if (update.operation_id !== c.op || update.session_id !== c.session || update.turn_id !== c.turn
      || update.segment_id !== c.segment || !Number.isSafeInteger(update.revision) || update.revision <= 0
      || !['partial', 'stable', 'final'].includes(update.phase) || typeof update.transcript !== 'string'
      || update.transcript.length > 16000 || update.error_code) { close('unavailable'); return; }
    if (update.revision < c.revision) return;
    if (update.revision === c.revision && update.transcript !== c.text) { close('unavailable'); return; }
    c.revision = update.revision;
    if (update.transcript !== c.text) { c.text = update.transcript; c.changedAt = now(); }
    const intent = interruptionIntent(c.text);
    c.intent = intent;
    if (intent === 'acknowledgement') duck(c, false, 'rejected');
    const waiting = phase() !== 'PLAYING';
    const speechMS = intent === 'explicit' && modelActivity ? 64 : intent === 'explicit' ? settings.minimumSpeechMS : waiting ? settings.waitingSpeechMS : settings.minimumSpeechMS;
    const stableMS = intent === 'explicit' ? settings.explicitStableMS : waiting ? settings.waitingStableMS : settings.stableMS;
    const observedSpeechMS = modelActivity ? (c.modelHasSpeech ? c.modelSpeechMS : 0) : c.speechMS;
    // Only a recognized explicit command is probable enough to lower playback．
    // VAD activity and ordinary background speech retain normal volume．
    const probableSpeechMS = modelActivity ? 64 : settings.onsetMS;
    if (duckEnabled && intent === 'explicit' && observedSpeechMS >= probableSpeechMS && !knownNonTarget(c.speaker)) duck(c, true);
    if (['explicit', 'speech'].includes(intent) && observedSpeechMS >= speechMS
      && (update.phase === 'final' || now() - c.changedAt >= stableMS)) {
      const candidateMS = Math.max(0, Math.round(now() - c.startedAt));
      close('confirmed', true);
      onConfirm({candidateMS});
    }
  }
  function pump(c) {
    if (!current(c) || !c.ready || c.pumping || !c.queue.length) return;
    c.pumping = true;
    void (async () => {
      try {
        while (current(c) && c.ready && c.queue.length) {
          const bytes = c.queue.shift();
          const snapshot = await bridge.AppendInteractionInterruptionCandidate(c.op, c.id, ++c.sequence, encodeBase64(bytes));
          if (!current(c)) return;
          c.queuedBytes -= bytes.byteLength;
          inspect(c, snapshot);
        }
      } catch { if (current(c)) close('unavailable'); }
      finally { c.pumping = false; if (current(c) && c.queue.length) pump(c); }
    })();
  }
  function queue(c, frame) {
    const bytes = c.encoder.push(frame);
    if (!bytes.byteLength) return;
    // Include the in-flight bridge append in this bound．
    if (c.queuedBytes + bytes.byteLength > c.encoder.outputRate * 2 * (modelActivity ? 3 : 2)) { close('unavailable'); return; }
    c.queue.push(bytes); c.queuedBytes += bytes.byteLength;
    pump(c);
  }
  function begin() {
    if (typeof bridge.BeginInteractionInterruptionCandidate !== 'function'
      || typeof bridge.AppendInteractionInterruptionCandidate !== 'function'
      || typeof bridge.CancelInteractionInterruptionCandidate !== 'function') { blocked = true; return; }
    const owner = identity();
    const c = {op: owner.operation_id, session: owner.session_id, turn: owner.turn_id,
      id: createID(), ready: false, segment: '', encoder: createEncoder(sampleRate), queue: [], queuedBytes: 0,
      sequence: 0, revision: 0, text: '', intent: 'empty', changedAt: now(), startedAt: now(),
      speechMS: activeMS, elapsedMS: 0, silenceMS: 0, threshold: Math.max(settings.rms, noiseFloor * 3),
      ducked: false, duckedAt: 0, speaker: {hasSpeech: false, speakerState: 'unknown', targetProbability: null, confidence: null}};
    candidate = c;
    buffer.hold();
    for (const frame of buffer.snapshot()) queue(c, frame);
    if (!current(c)) return;
    c.timer = timers.setTimeout(() => { if (current(c)) close(c.intent === 'acknowledgement' ? 'acknowledgement' : 'expired'); }, settings.candidateMS);
    void (async () => {
      try {
        const snapshot = await bridge.BeginInteractionInterruptionCandidate(c.op, c.id, owner.generation_revision || 1, c.encoder.outputRate);
        c.segment = snapshot?.request?.segment_id;
        if (!current(c)) { cancelBackend(c); return; }
        if (typeof c.id !== 'string' || !c.id || typeof c.segment !== 'string' || !c.segment) { close('unavailable'); return; }
        c.ready = true;
        inspect(c, snapshot); pump(c);
      } catch { if (current(c)) close('unavailable'); }
    })();
  }
  return {
    get pending() { return candidate !== null; },
    audio(data) {
      if (stopped || transferred || !isCurrent()) return;
      const duration = data.length / sampleRate * 1000;
      if (!data.length || !Number.isFinite(duration)) return;
      let energy = 0;
      for (const sample of data) { if (!Number.isFinite(sample)) { close('noise'); return; } energy += sample * sample; }
      const rms = Math.sqrt(energy / data.length);
      const c = candidate;
      if (!buffer.push(data)) { close('expired'); return; }
      if (c) {
        c.elapsedMS += duration;
        const loud = rms >= c.threshold;
        c.speechMS += loud ? duration : 0;
        c.silenceMS = loud ? 0 : c.silenceMS + duration;
        if (c.elapsedMS >= settings.candidateMS) { close(c.intent === 'acknowledgement' ? 'acknowledgement' : 'expired'); return; }
        // A click can briefly open a candidate but cannot duck the whole reply．
        if (c.silenceMS >= settings.gapMS && (modelActivity ? (!c.modelHasSpeech && c.elapsedMS >= 400) : c.speechMS < settings.minimumSpeechMS)) { close('noise'); return; }
        queue(c, data);
        return;
      }
      // Learn only quiet/rejected background，never raise the threshold while
      // an utterance candidate is being recognized．A continuous fan therefore
      // does not reopen recognizers on every callback．
      const threshold = Math.max(settings.rms, noiseFloor * 3);
      if (blocked || rms < threshold) {
        const alpha = 1 - Math.exp(-duration / (rms < noiseFloor ? 200 : 800));
        noiseFloor += (Math.min(rms, .05) - noiseFloor) * alpha;
      }
      const loud = rms >= Math.max(settings.rms, noiseFloor * 3);
      quietMS = loud ? 0 : quietMS + duration;
      if (blocked) { if (quietMS >= settings.quietMS) { blocked = false; activeMS = 0; } return; }
      activeMS = loud ? activeMS + duration : 0;
      if (activeMS >= settings.onsetMS) begin();
    },
    // Natural completion has no reply left to interrupt．Preserve a pending
    // input for the next ordinary ASR instead of losing its beginning．
    transfer() { if (!candidate) return false; close('completed', true); return true; },
    stop() { if (stopped) return; close('detached', transferred); stopped = true; },
  };
}
