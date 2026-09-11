import {createTimingObserver} from './fillerTiming.js';
import {createFillerController, createFillerPlayer, createFillerBackchannel} from './fillerController.js';
import {startFillerBargeIn} from './fillerBargeIn.js';

const TERMINAL = new Set(['COMPLETED', 'INCOMPLETE', 'CANCELED', 'FAILED']);
const STATE_ORDER = ['IDLE', 'PREPARING', 'RECORDING', 'TRANSCRIBING', 'THINKING', 'SYNTHESIZING', 'PLAYING', 'CANCELING'];
const STATUS = {
  IDLE: '音声入力を開始できます．',
  PREPARING: '音声認識を利用できるか確認しています．',
  RECORDING: '録音中です．停止すると送信します．',
  TRANSCRIBING: '音声を文字に変換しています．',
  THINKING: '応答を考えています．',
  SYNTHESIZING: '応答の音声を準備しています．',
  PLAYING: '応答を再生しています．',
  CANCELING: '音声対話を停止しています．',
  CANCELED: '音声対話を停止しました．',
  COMPLETED: '音声対話が完了しました．',
  INCOMPLETE: '応答は未完了です．続きを生成するか，テキスト入力で続行できます．',
};
const FAILURES = {
  voice_profile_unavailable: '選択した声を利用できません．声を選び直すか，テキスト入力を利用できます．',
  invalid_voice_profile: '選択した声の設定を確認できません．標準の声かテキスト入力を利用できます．',
  unsupported_voice_control: '選択した声で利用できない話し方の設定です．設定を見直すか，テキスト入力を利用できます．',
  microphone_permission_denied: 'マイクの許可がありません．テキスト入力を利用できます．',
  microphone_unavailable: 'マイクを利用できません．テキスト入力を利用できます．',
  microphone_failed: '録音できませんでした．テキスト入力を利用できます．',
  invalid_audio: '録音した音声を読み取れませんでした．テキスト入力を利用できます．',
  asr_permission_denied: '音声認識の許可がありません．テキスト入力を利用できます．',
  asr_permission_restricted: '音声認識の利用が制限されています．テキスト入力を利用できます．',
  asr_unavailable: '音声認識を利用できません．テキスト入力を利用できます．',
  asr_on_device_unavailable: '端末内の音声認識を利用できません．テキスト入力を利用できます．',
  asr_empty_result: '音声を認識できませんでした．テキスト入力を利用できます．',
  asr_timeout: '音声認識が時間切れになりました．テキスト入力を利用できます．',
  asr_failed: '音声認識を継続できませんでした．テキスト入力を利用できます．',
  asr_backpressure: '音声の送信が追いつかないため録音を停止しました．テキスト入力を利用できます．',
  asr_protocol_error: '音声認識の応答を確認できませんでした．テキスト入力を利用できます．',
  llm_timeout: '応答の生成が時間切れになりました．テキスト入力を利用できます．',
  generation_transport_eof: '応答の通信が途中で終了しました．テキスト入力で再試行できます．',
  incomplete_response: '応答を完了できませんでした．テキスト入力で再試行できます．',
  tts_timeout: '音声の生成が時間切れになりました．テキスト入力を利用できます．',
  playback_timeout: '音声の再生が時間切れになりました．テキスト入力を利用できます．',
  playback_failed: '音声を再生できませんでした．テキスト入力を利用できます．',
};
const MAX_RECORDING_BYTES = 8 * 1024 * 1024;
const MAX_RECORDING_MS = 60_000;
const MAX_ASR_SAMPLE_RATE = 48000;
const MAX_ASR_CHUNK_BYTES = 64 * 1024;
const MAX_ASR_QUEUE_BYTES = 128 * 1024;
const MAX_ASR_TEXT_LENGTH = 16000;
const ASR_PHASES = new Set(['partial', 'stable', 'final', 'failure', 'timeout', 'canceled']);
const MAX_PLAYBACK_BYTES = 16 * 1024 * 1024;
const MAX_PLAYBACK_CHUNK_BYTES = 8 * 1024 * 1024;
const MAX_PLAYBACK_CHUNKS = 64;
const MAX_EARLY_EVENT_BYTES = Math.ceil(MAX_PLAYBACK_BYTES / 3) * 4 + 512 * 1024;
const READINESS_ERROR_CODES = new Set(['asr_unavailable', 'asr_permission_denied', 'asr_permission_restricted', 'asr_on_device_unavailable', 'asr_timeout', 'asr_canceled', 'invalid_voice_config']);
const VOICE_PROFILE_ERROR_CODES = new Set(['voice_profile_unavailable', 'invalid_voice_profile', 'unsupported_voice_control']);
const ASR_BRIDGE_ERROR_CODES = new Set([
  'asr_failed', 'asr_backpressure', 'asr_protocol_error', 'asr_timeout', 'asr_canceled', 'asr_unavailable',
  'asr_on_device_unavailable', 'asr_permission_denied', 'asr_permission_restricted', 'asr_stream_invalid',
  'asr_stream_eof', 'asr_empty_transcript', 'asr_empty_result', 'invalid_audio', 'invalid_voice_config',
]);

function asrBridgeErrorCode(error, fallback) {
  // Wails can reject with either a string or an Error．Only a complete known
  // code crosses this boundary；never extract a code from diagnostic prose．
  const code = typeof error === 'string' ? error : error?.message;
  return ASR_BRIDGE_ERROR_CODES.has(code) ? code : fallback;
}

export function voiceStatusText(snapshot) {
  if (snapshot?.state === 'FAILED') {
    return FAILURES[snapshot.error_code] || '音声対話を完了できませんでした．テキスト入力を利用できます．';
  }
  return STATUS[snapshot?.state] || STATUS.IDLE;
}

// Legacy WAV utility for existing recordings and playback fixtures．Live ASR
// uses the stateful raw PCM encoder below and never accumulates a whole WAV．
export function encodePCM16Wav(chunks, sampleRate) {
  if (!Number.isInteger(sampleRate) || sampleRate < 8000 || sampleRate > 192000) {
    throw new Error('invalid_sample_rate');
  }
  const inputSamples = chunks.reduce((count, chunk) => count + chunk.length, 0);
  if (!inputSamples || inputSamples * 2 + 44 > MAX_RECORDING_BYTES || inputSamples > sampleRate * 60) {
    throw new Error('invalid_audio_size');
  }
  // Hardware may ignore the requested AudioContext rate．Average contiguous
  // input intervals before decimation，including intervals crossing chunks．
  const outputRate = Math.min(sampleRate, MAX_ASR_SAMPLE_RATE);
  const samples = Math.ceil(inputSamples * outputRate / sampleRate);
  const bytes = new ArrayBuffer(44 + samples * 2);
  const view = new DataView(bytes);
  const tag = (offset, value) => [...value].forEach((char, index) => view.setUint8(offset + index, char.charCodeAt(0)));
  tag(0, 'RIFF');
  view.setUint32(4, bytes.byteLength - 8, true);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, outputRate, true);
  view.setUint32(28, outputRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  tag(36, 'data');
  view.setUint32(40, samples * 2, true);
  let offset = 44;
  let weighted = 0;
  let filled = 0;
  const writeSample = (value) => {
    view.setInt16(offset, Math.round(value * (value < 0 ? 32768 : 32767)), true);
    offset += 2;
  };
  for (const chunk of chunks) {
    for (const sample of chunk) {
      const bounded = Number.isFinite(sample) ? Math.max(-1, Math.min(1, sample)) : 0;
      // Integer time units avoid drift for rates such as 88．2 kHz．
      let remaining = outputRate;
      while (remaining > 0) {
        const width = Math.min(remaining, sampleRate - filled);
        weighted += bounded * width;
        filled += width;
        remaining -= width;
        if (filled === sampleRate) {
          writeSample(weighted / filled);
          weighted = 0;
          filled = 0;
        }
      }
    }
  }
  if (filled) writeSample(weighted / filled);
  return bytes;
}

/** Average input intervals across callbacks without resetting fractional carry． */
export function createPCM16StreamEncoder(sampleRate, outputRate = Math.min(sampleRate, MAX_ASR_SAMPLE_RATE)) {
  if (!Number.isInteger(sampleRate) || sampleRate < 8000 || sampleRate > 192000
    || !Number.isInteger(outputRate) || outputRate < 8000 || outputRate > MAX_ASR_SAMPLE_RATE || outputRate > sampleRate) {
    throw new Error('invalid_sample_rate');
  }
  let weighted = 0;
  let filled = 0;
  let closed = false;
  const write = (view, index, value) => view.setInt16(index * 2, Math.round(value * (value < 0 ? 32768 : 32767)), true);
  return {
    outputRate,
    push(input) {
      if (closed) throw new Error('encoder_closed');
      const bytes = new ArrayBuffer(Math.floor((filled + input.length * outputRate) / sampleRate) * 2);
      const view = new DataView(bytes);
      let index = 0;
      for (const sample of input) {
        const bounded = Number.isFinite(sample) ? Math.max(-1, Math.min(1, sample)) : 0;
        let remaining = outputRate;
        while (remaining > 0) {
          const width = Math.min(remaining, sampleRate - filled);
          weighted += bounded * width;
          filled += width;
          remaining -= width;
          if (filled === sampleRate) {
            write(view, index++, weighted / filled);
            weighted = 0;
            filled = 0;
          }
        }
      }
      return bytes;
    },
    finish() {
      if (closed) return new ArrayBuffer(0);
      closed = true;
      const bytes = new ArrayBuffer(filled ? 2 : 0);
      if (filled) write(new DataView(bytes), 0, weighted / filled);
      weighted = filled = 0;
      return bytes;
    },
    reset() { weighted = filled = 0; closed = true; },
  };
}

function toBase64(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 8192) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  }
  return btoa(binary);
}

function fromBase64(value) {
  const binary = atob(value);
  return Uint8Array.from(binary, (char) => char.charCodeAt(0)).buffer;
}

function stopTracks(stream) {
  for (const track of stream?.getTracks?.() || []) {
    track.onended = null;
    try { track.stop(); } catch { /* Continue stopping every track． */ }
  }
}

function disconnect(node) {
  try { node?.disconnect(); } catch { /* A disconnected node needs no further work． */ }
}

/** Native desktop controller．Dependencies are injectable so tests never open a microphone． */
export function mountVoiceInteraction({
  root,
  bridge,
  subscribe = () => () => {},
  getRequest = () => ({}),
  getSessionID,
  onTranscript = () => {},
  onToken = () => {},
  onOutput = () => {},
  onComplete = () => {},
  onIncomplete = () => {},
  onFailure = () => {},
  onCancel = () => {},
  onBusy = () => {},
  onFallback = () => {},
  mediaDevices = globalThis.navigator?.mediaDevices,
  createAudioContext = () => new (globalThis.AudioContext || globalThis.webkitAudioContext)({sampleRate: MAX_ASR_SAMPLE_RATE}),
  timers = globalThis,
  encodeBase64 = toBase64,
  decodeBase64 = fromBase64,
  now = () => performance.now(),
  startBargeIn = startFillerBargeIn,
} = {}) {
  const record = root?.querySelector('#voice-record');
  const cancelButton = root?.querySelector('#voice-cancel');
  const status = root?.querySelector('#voice-status');
  const fallback = root?.querySelector('#voice-fallback');
  const liveTranscript = root?.querySelector('#voice-live-transcript');
  const stableTranscript = root?.querySelector('#voice-transcript-stable');
  const revisableTranscript = root?.querySelector('#voice-transcript-revisable');
  let current = null;
  let disposed = false;
  let nextFiller = 0;
  let handoff = null;
  function stopHandoff() {
    const old = handoff; handoff = null; old?.stop(); if (old) render();
  }

  function stopFiller(run, reason = 'invalidated') {
    run.filler?.cancel(reason);
    run.bargeIn?.stop(); run.bargeIn = null;
    run.backchannel?.stop(); run.backchannel = null;
  }
  function recordFillerSample(run) {
    if (!run.fillerSample || !run.fillerSetupDone || !attached(run)) return;
    const sample = run.fillerSample; run.fillerSample = null;
    try { Promise.resolve(bridge.RecordInteractionFillerTiming?.(run.snapshot.operation_id,
      run.snapshot.generation_revision || 1, sample)).catch(() => {}); } catch { /* Optional telemetry． */ }
  }
  async function prepareFiller(run) {
    if (run.fillerAttempted || (run.snapshot.generation_revision || 1) !== 1 || typeof bridge.GetInteractionFiller !== 'function') return;
    run.fillerAttempted = true;
    try {
      const setup = await bridge.GetInteractionFiller(run.snapshot.operation_id, 1);
      if (!attached(run)) return;
      run.fillerSetupDone = true; recordFillerSample(run);
      if (setup?.enabled !== true || run.fillerInvalidated || run.bodyVisible || run.bodyReady || !Array.isArray(setup.assets) || !setup.assets.length || setup.assets.length > 4
          || !Array.isArray(setup.samples) || setup.samples.length > 200 || !await run.contextReady) return;
      const fillers = setup.assets.filter(a => a.kind === 'hesitation');
      if (!fillers.length || fillers.length > 2) return;
      const asset = fillers[nextFiller++ % fillers.length];
      if (asset.kind !== 'hesitation' || typeof asset.audio_base64 !== 'string' || asset.audio_base64.length > 210000
          || !Number.isFinite(asset.duration_ms) || asset.duration_ms < 150 || asset.duration_ms > 1500) return;
      const buffer = await run.context.decodeAudioData(decodeBase64(asset.audio_base64));
      if (!attached(run) || run.bodyVisible || run.bodyReady || !Number.isFinite(buffer.duration) || Math.abs(buffer.duration * 1000 - asset.duration_ms) > 2) return;
      const available = () => attached(run) && !run.fillerInvalidated && !run.bodyReady && !run.bodyVisible;
      const trace = event => {
        try { Promise.resolve(bridge.RecordInteractionFillerTrace?.(run.snapshot.operation_id, 1, event)).catch(() => {}); } catch { /* Optional telemetry． */ }
      };
      const acknowledgements = setup.assets.filter(a => a.kind === 'backchannel');
      const acknowledgement = acknowledgements[0];
      if (acknowledgements.length <= 2 && acknowledgement && typeof acknowledgement.audio_base64 === 'string'
          && acknowledgement.audio_base64.length <= 210000 && Number.isFinite(acknowledgement.duration_ms)
          && acknowledgement.duration_ms >= 150 && acknowledgement.duration_ms <= 1500) {
        try {
          const ackBuffer = await run.context.decodeAudioData(decodeBase64(acknowledgement.audio_base64));
          if (!available()) return;
          if (Number.isFinite(ackBuffer.duration) && Math.abs(ackBuffer.duration * 1000 - acknowledgement.duration_ms) <= 2) {
            let ack;
            ack = createFillerBackchannel({context: run.context, buffer: ackBuffer, now, timers, onTrace: trace,
              isCurrent: () => !disposed && current === run && (!getSessionID || getSessionID() === run.snapshot.session_id),
              onEnd: () => { if (handoff === ack) { handoff = null; render(); } }});
            run.backchannel = ack;
          }
        } catch { /* A failed optional acknowledgement cannot block the answer． */ }
      }
      if (!available()) { run.backchannel?.stop(); run.backchannel = null; return; }
      const monitor = await startBargeIn({context: run.context, mediaDevices, isCurrent: () => attached(run) && !run.bodyStarted,
        onSpeech: () => {
          if (!attached(run)) return;
          const detected = now(), ack = run.backchannel; run.backchannel = null;
          stopFiller(run, 'barge_in');
          if (!attached(run)) { ack?.stop(); return; }
          // Cancel owns the old output，but the acknowledgement inherits its
          // running context so it needs neither TTS nor another resume gesture．
          if (ack) run.context = null;
          void cancel();
          if (ack) { handoff = ack; ack.play(detected); render(); }
        },
        onUnavailable: () => { run.fillerInvalidated = true; stopFiller(run); }});
      if (!monitor) return;
      if (!available()) { monitor.stop(); return; }
      run.bargeIn = monitor;
      render();
      const player = createFillerPlayer(run.context, buffer);
      run.filler = createFillerController({samples: setup.samples, durationMS: asset.duration_ms, ledger: run.fillerLedger,
        play: player.play, stop: player.stop, isCurrent: available, now, timers,
        onUnsafeStop: () => { void fail(run, 'playback_failed'); },
        onTrace: trace});
      run.filler.progress(run.timing.marks);
      if (run.timing.origin !== null) run.filler.arm(run.timing.origin);
    } catch { /* Missing or failed filler setup cannot fail the answer． */ }
  }

  function active(run = current) {
    return !disposed && run === current && !!run && !run.finished;
  }

  function live(run) {
    return active(run) && !run.cancelRequested;
  }

  function attached(run) {
    if (!live(run)) return false;
    if (getSessionID && run.snapshot.session_id && getSessionID() !== run.snapshot.session_id) {
      void cancel();
      return false;
    }
    return true;
  }

  function render() {
    const recording = live(current) && current.recording;
    if (record) {
      record.disabled = active() && !recording;
      record.textContent = recording ? '録音を停止して送信' : '音声入力';
      record.setAttribute('aria-label', recording ? '録音を停止して送信' : '音声入力を開始');
      record.setAttribute('aria-pressed', String(!!recording));
    }
    if (cancelButton) {
      cancelButton.disabled = !handoff && (!active() || current.cancelRequested);
      cancelButton.setAttribute('aria-label', '音声対話をキャンセル');
    }
    if (status) {
      status.setAttribute('role', 'status');
      status.setAttribute('aria-live', 'polite');
      status.textContent = handoff ? '割込みを受け止める短い応答を再生中です．' : current?.permissionPending && !current.cancelRequested
        ? 'マイクの許可を確認しています．'
        : current?.asrOpenPending && !current.cancelRequested
          ? '音声認識を開始しています．必要な場合は音声認識の許可を確認してください．'
          : voiceStatusText(current?.snapshot);
      if (current?.bargeIn && live(current)) status.textContent += ' ヘッドホン用の割込み検出中です．';
    }
    if (fallback) {
      fallback.hidden = !['FAILED', 'INCOMPLETE'].includes(current?.snapshot.state) || !current.snapshot.transcript;
      fallback.textContent = '認識した内容をテキスト入力へ戻す';
    }
    if (liveTranscript) {
      liveTranscript.hidden = !live(current) || !current.liveTranscript;
      liveTranscript.setAttribute('aria-label', current?.asrPhase === 'final' ? '確定した認識結果' : '認識中の内容．下線部分は変更される可能性があります');
    }
    if (stableTranscript) stableTranscript.textContent = current?.stablePrefix || '';
    if (revisableTranscript) revisableTranscript.textContent = (current?.liveTranscript || '').slice((current?.stablePrefix || '').length);
  }

  function stopCapture(run) {
    run.recording = false;
    run.permissionPending = false;
    if (run.timer != null) timers.clearTimeout(run.timer);
    run.timer = null;
    if (run.processor) run.processor.onaudioprocess = null;
    disconnect(run.processor);
    disconnect(run.input);
    disconnect(run.mute);
    stopTracks(run.stream);
    run.processor = run.input = run.mute = run.stream = null;
  }

  function stopPlayback(run) {
    stopFiller(run, run.cancelRequested ? 'cancel' : 'invalidated');
    run.playbackEpoch += 1;
    if (run.playing) {
      const source = run.playing.source;
      source.onended = null;
      try { source.stop(); } catch { /* A source can already have ended． */ }
      disconnect(source);
    }
    run.playing = null;
    run.ending = false;
    run.queue = [];
    run.queueBytes = 0;
    run.earlyEvents = [];
    run.earlyBytes = 0;
  }

  function release(run) {
    stopCapture(run);
    stopPlayback(run);
    run.inputQueue = [];
    run.inputQueuedBytes = 0;
    run.encoder?.reset();
    run.encoder = null;
    run.asrEarlyEvents = [];
    run.asrEarlyBytes = 0;
    run.liveTranscript = run.stablePrefix = '';
    try { Promise.resolve(run.context?.close()).catch(() => {}); } catch { /* Already closed． */ }
    run.context = null;
  }

  function notifyTranscript(run, text) {
    if (run.cancelRequested || (run.streaming && !run.endRequested)) return;
    if (text && text !== run.reportedTranscript) {
      run.reportedTranscript = text;
      run.snapshot = {...run.snapshot, transcript: text};
      onTranscript(run.snapshot, text);
    }
  }

  function finish(run, snapshot) {
    if (run !== current || run.finished) return;
    if (run.streaming && !run.endRequested) {
      snapshot = {...snapshot};
      delete snapshot.transcript;
    }
    run.snapshot = {...run.snapshot, ...snapshot};
    notifyTranscript(run, run.snapshot.transcript);
    run.finished = true;
    release(run);
    render();
    onBusy(false);
    if (run.snapshot.state === 'COMPLETED') onComplete(run.snapshot);
    else if (run.snapshot.state === 'INCOMPLETE') onIncomplete(run.snapshot);
    else if (run.snapshot.state === 'FAILED') onFailure(run.snapshot);
    else if (run.snapshot.state === 'CANCELED') onCancel(run.snapshot);
  }

  function applySnapshot(run, snapshot) {
    if (!active(run) || !snapshot || snapshot.operation_id !== run.snapshot.operation_id
      || (snapshot.session_id && snapshot.session_id !== run.snapshot.session_id)
      || (snapshot.turn_id && snapshot.turn_id !== run.snapshot.turn_id)
      || (snapshot.trace_id && snapshot.trace_id !== run.snapshot.trace_id)
      || (snapshot.generation_revision || 1) !== (run.snapshot.generation_revision || 1)) return;
    // Ignore delayed bridge responses and queued states from before cancel．
    if (run.cancelRequested && !['CANCELING', 'CANCELED', 'FAILED'].includes(snapshot.state)) return;
    if (run.streaming && !run.endRequested) {
      snapshot = {...snapshot};
      delete snapshot.transcript;
    }
    if (TERMINAL.has(snapshot.state)) {
      finish(run, snapshot);
      return;
    }
    if (STATE_ORDER.indexOf(snapshot.state) < STATE_ORDER.indexOf(run.snapshot.state)) return;
    run.snapshot = {...run.snapshot, ...snapshot};
    notifyTranscript(run, snapshot.transcript);
    if (snapshot.state !== 'RECORDING') stopCapture(run);
    render();
  }

  async function fail(run, code, sequence) {
    if (!live(run)) return;
    const operationID = run.snapshot.operation_id;
    finish(run, {...run.snapshot, state: 'FAILED', error_code: code});
    if (!operationID) return;
    try {
      if (sequence) await bridge.InteractionPlayback(operationID, sequence, 'failed');
      else await bridge.FailInteraction(operationID, code);
    } catch {
      // A bridge error must still release the backend operation when possible．
      try { await bridge.CancelInteraction(operationID); } catch { /* Local resources are already stopped． */ }
    }
  }

  function begin(expected = {}, preparing = false) {
    if (active() || disposed) return null;
    stopHandoff();
    let ready;
    const run = {
      snapshot: {state: preparing ? 'PREPARING' : 'RECORDING', operation_id: ''},
      finished: false, cancelRequested: false, cancelPromise: null,
      recording: false, permissionPending: false, samples: 0,
      encoder: null, inputQueue: [], inputQueuedBytes: 0, inputTotalBytes: 0, inputSequence: 0, inputPump: null,
      asrSession: null, asrOpenPending: false, asrEarlyEvents: [], asrEarlyBytes: 0,
      asrRevision: 0, asrMonotonicMS: -1, asrPhase: '', asrCanonical: false,
      liveTranscript: '', stablePrefix: '', endPromise: null, endRequested: false,
      timer: null, context: null, contextReady: null,
      queue: [], queueBytes: 0, playing: null, decoding: false, ending: false, playbackEpoch: 0,
      lastSequence: 0, playbackBytesReceived: 0, earlyEvents: [], earlyBytes: 0, earlyOverflow: false,
      identityReady: new Promise((resolve) => { ready = resolve; }),
      resolveIdentity: null,
      expected,
      streaming: preparing,
      operationRequested: !preparing,
      readinessState: '',
      timing: createTimingObserver(now), fillerLedger: {used: false}, filler: null, bargeIn: null,
      fillerAttempted: false, fillerSetupDone: false, fillerSample: null, bodyVisible: false, bodyReady: false, bodyStarted: false,
      fillerInvalidated: false, backchannel: null,
    };
    run.resolveIdentity = ready;
    current = run;
    // Create and resume during the user gesture，before any awaited bridge call．
    try {
      run.context = createAudioContext();
      run.contextReady = Promise.resolve(run.context.resume()).then(() => true, () => false);
    } catch { run.contextReady = Promise.resolve(false); }
    onBusy(true);
    render();
    return run;
  }

  async function identify(run, pendingSnapshot) {
    try {
      let snapshot = await pendingSnapshot;
      if (!snapshot?.operation_id) throw new Error('missing_operation');
      if ((run.expected.operationID && run.expected.operationID !== snapshot.operation_id)
        || (run.expected.revision && run.expected.revision !== (snapshot.generation_revision || 1))) {
        throw new Error('stale_operation');
      }
      run.lastSequence = Number.isSafeInteger(snapshot.last_audio_sequence) ? snapshot.last_audio_sequence : 0;
      if (run.streaming && !run.endRequested) {
        snapshot = {...snapshot};
        delete snapshot.transcript;
      }
      run.snapshot = {...snapshot, state: run.cancelRequested ? 'CANCELING' : snapshot.state};
      run.resolveIdentity(snapshot.operation_id);
      if (run.cancelRequested || disposed || run !== current) return false;
      applySnapshot(run, snapshot);
      if (run.earlyOverflow) { await fail(run, 'playback_failed'); return false; }
      const early = run.earlyEvents;
      run.earlyEvents = [];
      run.earlyBytes = 0;
      for (const event of early) receive(event);
      return live(run);
    } catch (error) {
      run.resolveIdentity(null);
      if (run.cancelRequested) finish(run, {...run.snapshot, state: 'CANCELED'});
      else {
        const code = typeof error === 'string' ? error : error?.message;
        await fail(run, VOICE_PROFILE_ERROR_CODES.has(code) ? code : 'microphone_unavailable');
      }
      return false;
    }
  }

  async function start() {
    const run = begin({}, true);
    if (!run) return false;
    let request;
    try { request = getRequest(); } catch (error) {
      run.resolveIdentity(null);
      await fail(run, VOICE_PROFILE_ERROR_CODES.has(error?.message) ? error.message : 'microphone_unavailable');
      return false;
    }
    if (!live(run)) return false;
    let readiness;
    try { readiness = await bridge.GetInteractionASRReadiness(); } catch {
      run.resolveIdentity(null);
      if (live(run)) await fail(run, 'asr_unavailable');
      return false;
    }
    // Readiness owns no backend turn．A canceled or detached check must not
    // create an operation or ask for microphone access when it returns late．
    if (!live(run)) return false;
    if (getSessionID && getSessionID() !== request.session_id) {
      await cancel();
      return false;
    }
    if (!readiness?.can_start || !['ready', 'permission_required'].includes(readiness.state) || readiness.error_code) {
      run.resolveIdentity(null);
      await fail(run, READINESS_ERROR_CODES.has(readiness?.error_code) ? readiness.error_code : 'asr_unavailable');
      return false;
    }
    run.readinessState = readiness.state;
    run.operationRequested = true;
    let pending;
    try { pending = bridge.StartInteraction(request); } catch { pending = Promise.reject(new Error('start_failed')); }
    if (!await identify(run, pending)) return false;
    if (!await run.contextReady || !mediaDevices?.getUserMedia) {
      await fail(run, 'microphone_unavailable');
      return false;
    }
    if (!attached(run)) return false;
    run.asrOpenPending = true;
    render();
    try {
      run.encoder = createPCM16StreamEncoder(run.context.sampleRate);
      const session = await bridge.BeginInteractionASR(run.snapshot.operation_id, run.encoder.outputRate);
      if (!attached(run)) return false;
      if (!session || session.operation_id !== run.snapshot.operation_id || session.session_id !== run.snapshot.session_id
        || session.turn_id !== run.snapshot.turn_id || typeof session.segment_id !== 'string' || !session.segment_id
        || session.sample_rate !== run.encoder.outputRate) {
        await fail(run, 'asr_protocol_error');
        return false;
      }
      run.asrSession = session;
      run.asrOpenPending = false;
      const early = run.asrEarlyEvents;
      run.asrEarlyEvents = [];
      run.asrEarlyBytes = 0;
      for (const update of early) receiveASR(run, update);
      if (!attached(run) || run.endPromise) return false;
    } catch (error) {
      await fail(run, asrBridgeErrorCode(error, 'asr_unavailable'));
      return false;
    }
    run.permissionPending = true;
    render();
    try {
      const stream = await mediaDevices.getUserMedia({audio: {channelCount: 1}, video: false});
      // Permission prompts can resolve after cancellation or a later turn．
      if (!attached(run) || run.endPromise) { stopTracks(stream); return false; }
      run.stream = stream;
      run.input = run.context.createMediaStreamSource(stream);
      run.processor = run.context.createScriptProcessor(4096, 1, 1);
      run.mute = run.context.createGain();
      run.mute.gain.value = 0;
      run.input.connect(run.processor);
      run.processor.connect(run.mute);
      run.mute.connect(run.context.destination);
      run.permissionPending = false;
      run.recording = true;
      const maxSamples = run.context.sampleRate * 60;
      run.processor.onaudioprocess = (event) => {
        if (!attached(run) || !run.recording) return;
        const input = event.inputBuffer.getChannelData(0);
        const count = Math.min(input.length, maxSamples - run.samples);
        if (count > 0) {
          run.samples += count;
          if (!queueInput(run, run.encoder.push(input.subarray(0, count)))) return;
        }
        if (run.samples >= maxSamples) void stop();
      };
      for (const track of stream.getTracks()) {
        track.onended = () => { if (live(run) && run.recording) void fail(run, 'microphone_failed'); };
      }
      run.timer = timers.setTimeout(() => { if (live(run) && run.recording) void stop(); }, MAX_RECORDING_MS);
      render();
      return true;
    } catch (error) {
      await fail(run, error?.name === 'NotAllowedError' ? 'microphone_permission_denied' : 'microphone_unavailable');
      return false;
    }
  }

  async function stop() {
    const run = current;
    if (!live(run) || !run.recording) return false;
    return endCapture(run);
  }

  function pumpInput(run) {
    if (run.inputPump || !attached(run) || !run.inputQueue.length) return run.inputPump;
    run.inputPump = Promise.resolve().then(async () => {
      while (attached(run) && run.inputQueue.length) {
        const item = run.inputQueue.shift();
        try {
          const encoded = encodeBase64(item.bytes);
          item.bytes = null;
          await bridge.AppendInteractionAudio(run.snapshot.operation_id, item.sequence, encoded);
        } catch (error) {
          await fail(run, asrBridgeErrorCode(error, 'asr_failed'));
          return;
        }
        if (!attached(run)) return;
        run.inputQueuedBytes -= item.size;
      }
    }).finally(() => {
      run.inputPump = null;
      if (attached(run) && run.inputQueue.length) void pumpInput(run);
    });
    return run.inputPump;
  }

  function queueInput(run, bytes) {
    if (!attached(run)) return false;
    if (!bytes.byteLength) return true;
    // Include the in-flight append in the one-second bound．At most one
    // bridge promise owns audio while the remaining bounded chunks wait．
    const limit = Math.min(MAX_ASR_QUEUE_BYTES, run.asrSession.sample_rate * 2);
    if (run.inputQueuedBytes + bytes.byteLength > limit) {
      void fail(run, 'asr_backpressure');
      return false;
    }
    if (run.inputTotalBytes + bytes.byteLength > MAX_RECORDING_BYTES) {
      void fail(run, 'invalid_audio');
      return false;
    }
    run.inputQueuedBytes += bytes.byteLength;
    run.inputTotalBytes += bytes.byteLength;
    for (let offset = 0; offset < bytes.byteLength; offset += MAX_ASR_CHUNK_BYTES) {
      const chunk = bytes.slice(offset, offset + MAX_ASR_CHUNK_BYTES);
      run.inputQueue.push({sequence: ++run.inputSequence, bytes: chunk, size: chunk.byteLength});
    }
    void pumpInput(run);
    return true;
  }

  function endCapture(run) {
    if (run.endPromise) return run.endPromise;
    if (!attached(run) || !run.asrSession) return Promise.resolve(false);
    const tail = run.encoder.finish();
    stopCapture(run);
    run.snapshot = {...run.snapshot, state: 'TRANSCRIBING'};
    render();
    if (!queueInput(run, tail)) return Promise.resolve(false);
    run.endPromise = Promise.resolve().then(async () => {
      while (attached(run) && run.inputPump) await run.inputPump;
      if (!attached(run)) return false;
      try {
        run.endRequested = true;
        await bridge.EndInteractionASR(run.snapshot.operation_id);
        return attached(run) || ['COMPLETED', 'INCOMPLETE'].includes(run.snapshot.state);
      } catch (error) {
        await fail(run, asrBridgeErrorCode(error, 'asr_failed'));
        return false;
      }
    });
    return run.endPromise;
  }

  function cancel() {
    stopHandoff();
    const run = current;
    if (!active(run)) return Promise.resolve(run?.snapshot);
    if (run.cancelPromise) return run.cancelPromise;
    run.cancelRequested = true;
    run.snapshot = {...run.snapshot, state: 'CANCELING'};
    // Stop audible output and every microphone track before contacting Go．
    release(run);
    if (!run.operationRequested) run.resolveIdentity(null);
    render();
    run.cancelPromise = (async () => {
      const operationID = run.snapshot.operation_id || await run.identityReady;
      try {
        const snapshot = operationID ? await bridge.CancelInteraction(operationID) : {...run.snapshot, state: 'CANCELED'};
        finish(run, snapshot);
      } catch {
        finish(run, {...run.snapshot, state: 'FAILED', error_code: 'voice_failed'});
      }
      return run.snapshot;
    })();
    return run.cancelPromise;
  }

  async function pump(run) {
    if (!live(run) || run.decoding || run.playing || run.ending || !run.queue.length) return;
    const item = run.queue.shift();
    const epoch = run.playbackEpoch;
    run.decoding = true;
    try {
      const decoded = await run.context.decodeAudioData(item.bytes);
      item.bytes = null;
      if (!live(run) || epoch !== run.playbackEpoch) return;
      if (!await run.contextReady || !live(run) || epoch !== run.playbackEpoch) return;
      // Measure readiness before waiting，so calibration cannot learn the delay
      // introduced by finishing a filler．Keep the microphone active until then．
      if (!run.bodyReady) {
        run.bodyReady = true;
        run.fillerSample = run.timing.ready(); recordFillerSample(run);
        const boundary = run.filler?.answerReady();
        if (boundary) await boundary;
        if (!live(run) || epoch !== run.playbackEpoch) return;
        run.bargeIn?.stop(); run.bargeIn = null;
        run.backchannel?.stop(); run.backchannel = null;
      }
      const source = run.context.createBufferSource();
      source.buffer = decoded;
      source.connect(run.context.destination);
      item.source = source;
      run.playing = item;
      source.onended = () => {
        if (!live(run) || run.playing !== item || epoch !== run.playbackEpoch) return;
        run.playing = null;
        run.ending = true;
        run.queueBytes -= item.size;
        disconnect(source);
        void (async () => {
          try {
            await item.started;
            if (!live(run) || epoch !== run.playbackEpoch) return;
            await bridge.InteractionPlayback(run.snapshot.operation_id, item.sequence, 'stopped');
            if (live(run) && epoch === run.playbackEpoch) {
              run.ending = false;
              void pump(run);
            }
          } catch { await fail(run, 'playback_failed', item.sequence); }
        })();
      };
      source.start();
      run.bodyStarted = true;
      run.filler?.answerStarted();
      item.started = Promise.resolve(bridge.InteractionPlayback(run.snapshot.operation_id, item.sequence, 'started'));
      await item.started;
    } catch {
      if (live(run) && epoch === run.playbackEpoch) await fail(run, 'playback_failed', item.sequence);
    } finally {
      run.decoding = false;
      if (live(run) && !run.playing) void pump(run);
    }
  }

  function enqueue(run, event) {
    if (!Number.isSafeInteger(event.sequence) || event.sequence < 1) { void fail(run, 'playback_failed'); return; }
    if (event.sequence <= run.lastSequence) return;
    try {
      if (event.sequence !== run.lastSequence + 1 || event.sequence > MAX_PLAYBACK_CHUNKS
        || typeof event.audio_base64 !== 'string'
        || event.audio_base64.length > Math.ceil(MAX_PLAYBACK_CHUNK_BYTES / 3) * 4
        || run.queue.length + Number(!!run.playing || run.decoding) >= MAX_PLAYBACK_CHUNKS) {
        throw new Error('audio_limit');
      }
      const bytes = decodeBase64(event.audio_base64);
      if (bytes.byteLength < 44 || bytes.byteLength > MAX_PLAYBACK_CHUNK_BYTES
        || run.playbackBytesReceived + bytes.byteLength > MAX_PLAYBACK_BYTES) throw new Error('audio_limit');
      const header = new Uint8Array(bytes, 0, 12);
      if (String.fromCharCode(...header.slice(0, 4)) !== 'RIFF' || String.fromCharCode(...header.slice(8)) !== 'WAVE') {
        throw new Error('invalid_audio');
      }
      run.lastSequence = event.sequence;
      run.playbackBytesReceived += bytes.byteLength;
      run.queueBytes += bytes.byteLength;
      run.queue.push({sequence: event.sequence, bytes, size: bytes.byteLength});
      void pump(run);
    } catch { void fail(run, 'playback_failed', event.sequence); }
  }

  function receiveASR(run, update) {
    if (!attached(run) || run.asrCanonical || !update || update.operation_id !== run.snapshot.operation_id
      || update.session_id !== run.snapshot.session_id || update.turn_id !== run.snapshot.turn_id) return;
    if (!run.asrSession) {
      if (!run.asrOpenPending) return;
      const size = (typeof update.transcript === 'string' ? update.transcript.length * 2 : 0)
        + (typeof update.stable_prefix === 'string' ? update.stable_prefix.length * 2 : 0);
      if (run.asrEarlyEvents.length >= 64 || run.asrEarlyBytes + size > MAX_ASR_QUEUE_BYTES) {
        void fail(run, 'asr_protocol_error');
        return;
      }
      run.asrEarlyEvents.push(update);
      run.asrEarlyBytes += size;
      return;
    }
    if (update.segment_id !== run.asrSession.segment_id || !Number.isSafeInteger(update.revision)
      || update.revision <= run.asrRevision || update.revision > 2048
      || (run.asrPhase === 'final' && ['partial', 'stable', 'final'].includes(update.phase))
      || !Number.isSafeInteger(update.monotonic_ms) || update.monotonic_ms < 0 || update.monotonic_ms < run.asrMonotonicMS
      || !ASR_PHASES.has(update.phase)) return;
    const text = update.transcript ?? '';
    const stable = update.stable_prefix ?? '';
    const hypothesis = ['partial', 'stable', 'final'].includes(update.phase);
    if (typeof text !== 'string' || text.length > MAX_ASR_TEXT_LENGTH || typeof stable !== 'string'
      || !text.startsWith(stable)
      || (hypothesis && (update.error_code || !stable.startsWith(run.stablePrefix) || !text.startsWith(run.stablePrefix)))
      || (update.phase === 'stable' && !stable)
      || (update.phase === 'final' && (!text.trim() || stable !== text))
      || (!hypothesis && (text || stable))) {
      void fail(run, 'asr_protocol_error');
      return;
    }
    run.asrRevision = update.revision;
    run.asrMonotonicMS = update.monotonic_ms;
    run.asrPhase = update.phase;
    if (['failure', 'timeout'].includes(update.phase)) {
      void fail(run, update.phase === 'timeout' ? 'asr_timeout'
        : READINESS_ERROR_CODES.has(update.error_code) ? update.error_code : 'asr_failed');
      return;
    }
    if (update.phase === 'canceled') { void cancel(); return; }
    // This view owns hypotheses only．Neither final nor partial ASR callbacks
    // write Conversation history；the canonical transcript follows End in Go．
    run.liveTranscript = text;
    run.stablePrefix = stable;
    render();
    if (update.phase === 'final') void endCapture(run);
  }

  function receive(event) {
    const run = current;
    if (!active(run) || !event || typeof event !== 'object'
      || !['state', 'transcript', 'token', 'output', 'audio', 'asr_update', 'trace'].includes(event.kind)) return;
    if (!run.snapshot.operation_id) {
      // Open starts only after Start has supplied the current identity．An
      // earlier ASR callback therefore belongs to another operation．
      if (event.kind === 'asr_update') return;
      const bytes = (event.audio_base64?.length || 0) + (event.text?.length || 0);
      if (run.earlyEvents.length < 256 && run.earlyBytes + bytes <= MAX_EARLY_EVENT_BYTES) {
        run.earlyEvents.push(event);
        run.earlyBytes += bytes;
      } else run.earlyOverflow = true;
      return;
    }
    if (event.operation_id !== run.snapshot.operation_id
      || (event.session_id && event.session_id !== run.snapshot.session_id)
      || (event.turn_id && event.turn_id !== run.snapshot.turn_id)
      || (event.trace_id && event.trace_id !== run.snapshot.trace_id)
      || (event.generation_revision || 1) !== (run.snapshot.generation_revision || 1)) return;
    if (!attached(run) && !run.cancelRequested) return;
    if (event.kind === 'state') applySnapshot(run, event.snapshot);
    if (!live(run)) return;
    if (event.kind === 'trace') {
      if (event.trace?.name === 'llm_identity_changed') { run.fillerInvalidated = true; stopFiller(run); }
      run.timing.trace(event.trace);
      run.filler?.progress(run.timing.marks);
      if (event.trace?.name === 'llm_identity_ready') void prepareFiller(run);
      return;
    }
    if (event.kind === 'asr_update') {
      if (event.session_id !== run.snapshot.session_id || event.turn_id !== run.snapshot.turn_id) return;
      receiveASR(run, event.asr);
    }
    else if (event.kind === 'transcript') {
      if (run.streaming && !run.endRequested) return;
      notifyTranscript(run, event.text);
      if (run.streaming) run.asrCanonical = true;
      run.liveTranscript = run.stablePrefix = '';
      render();
    }
    else if (event.kind === 'token' && typeof event.text === 'string') {
      if (event.text) { run.bodyVisible = true; run.filler?.answerVisible(); }
      onToken(run.snapshot, event.text);
    }
    else if (event.kind === 'output') {
      const snapshot = event.snapshot;
      if (!snapshot || snapshot.operation_id !== run.snapshot.operation_id
        || snapshot.session_id !== run.snapshot.session_id || snapshot.turn_id !== run.snapshot.turn_id
        || snapshot.trace_id !== run.snapshot.trace_id
        || (snapshot.generation_revision || 1) !== (run.snapshot.generation_revision || 1)) return;
      // Output may arrive while a confirmed unit is playing．It does not move
      // the playback state backwards，but replaces the confirmed document．
      run.snapshot = {...run.snapshot, response_plan: snapshot.response_plan, generation: snapshot.generation};
      if (snapshot.response_plan?.text) { run.bodyVisible = true; run.filler?.answerVisible(); }
      onOutput(run.snapshot);
    }
    else if (event.kind === 'audio') enqueue(run, event);
  }

  async function adopt(snapshotOrPromise, expected = {}) {
    const run = begin(expected);
    if (!run) return false;
    if (!await identify(run, snapshotOrPromise)) {
      return !run.cancelRequested && ['COMPLETED', 'INCOMPLETE'].includes(run.snapshot.state);
    }
    if (!await run.contextReady) { await fail(run, 'playback_failed'); return false; }
    return live(run);
  }

  const onRecord = () => { void (current?.recording ? stop() : start()); };
  const onDeviceChange = () => {
    stopHandoff();
    if (current && active()) { current.fillerInvalidated = true; stopFiller(current); }
  };
  mediaDevices?.addEventListener?.('devicechange', onDeviceChange);
  const onCancelClick = () => { void cancel(); };
  const onFallbackClick = () => { if (['FAILED', 'INCOMPLETE'].includes(current?.snapshot.state)) onFallback(current.snapshot); };
  record?.addEventListener('click', onRecord);
  cancelButton?.addEventListener('click', onCancelClick);
  fallback?.addEventListener('click', onFallbackClick);
  const unsubscribe = subscribe(receive);
  render();
  return {
    start, stop, cancel, adopt,
    isActive: () => active(),
    get lastSnapshot() { return current?.snapshot || null; },
    async dispose() {
      mediaDevices?.removeEventListener?.('devicechange', onDeviceChange);
      record?.removeEventListener('click', onRecord);
      cancelButton?.removeEventListener('click', onCancelClick);
      fallback?.removeEventListener('click', onFallbackClick);
      if (typeof unsubscribe === 'function') unsubscribe();
      const pending = cancel();
      disposed = true;
      await pending;
    },
  };
}
