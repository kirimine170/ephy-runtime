const TERMINAL = new Set(['COMPLETED', 'CANCELED', 'FAILED']);
const STATE_ORDER = ['IDLE', 'RECORDING', 'TRANSCRIBING', 'THINKING', 'SYNTHESIZING', 'PLAYING', 'CANCELING'];
const STATUS = {
  IDLE: '音声入力を開始できます．',
  RECORDING: '録音中です．停止すると送信します．',
  TRANSCRIBING: '音声を文字に変換しています．',
  THINKING: '応答を考えています．',
  SYNTHESIZING: '応答の音声を準備しています．',
  PLAYING: '応答を再生しています．',
  CANCELING: '音声対話を停止しています．',
  CANCELED: '音声対話を停止しました．',
  COMPLETED: '音声対話が完了しました．',
};
const FAILURES = {
  microphone_permission_denied: 'マイクの許可がありません．テキスト入力を利用できます．',
  microphone_unavailable: 'マイクを利用できません．テキスト入力を利用できます．',
  microphone_failed: '録音できませんでした．テキスト入力を利用できます．',
  invalid_audio: '録音した音声を読み取れませんでした．テキスト入力を利用できます．',
  asr_permission_denied: '音声認識の許可がありません．テキスト入力を利用できます．',
  asr_unavailable: '音声認識を利用できません．テキスト入力を利用できます．',
  asr_on_device_unavailable: '端末内の音声認識を利用できません．テキスト入力を利用できます．',
  asr_empty_result: '音声を認識できませんでした．テキスト入力を利用できます．',
  asr_timeout: '音声認識が時間切れになりました．テキスト入力を利用できます．',
  llm_timeout: '応答の生成が時間切れになりました．テキスト入力を利用できます．',
  tts_timeout: '音声の生成が時間切れになりました．テキスト入力を利用できます．',
  playback_timeout: '音声の再生が時間切れになりました．テキスト入力を利用できます．',
  playback_failed: '音声を再生できませんでした．テキスト入力を利用できます．',
};
const MAX_RECORDING_BYTES = 8 * 1024 * 1024;
const MAX_RECORDING_MS = 60_000;
const MAX_ASR_SAMPLE_RATE = 48000;
const MAX_PLAYBACK_BYTES = 16 * 1024 * 1024;
const MAX_PLAYBACK_CHUNK_BYTES = 2 * 1024 * 1024;
const MAX_PLAYBACK_CHUNKS = 64;
const MAX_EARLY_EVENT_BYTES = Math.ceil(MAX_PLAYBACK_BYTES / 3) * 4 + 512 * 1024;

export function voiceStatusText(snapshot) {
  if (snapshot?.state === 'FAILED') {
    return FAILURES[snapshot.error_code] || '音声対話を完了できませんでした．テキスト入力を利用できます．';
  }
  return STATUS[snapshot?.state] || STATUS.IDLE;
}

// Capture stays in memory．Only a bounded mono PCM16 WAV crosses the bridge．
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
  onTranscript = () => {},
  onToken = () => {},
  onComplete = () => {},
  onFailure = () => {},
  onCancel = () => {},
  onBusy = () => {},
  onFallback = () => {},
  mediaDevices = globalThis.navigator?.mediaDevices,
  createAudioContext = () => new (globalThis.AudioContext || globalThis.webkitAudioContext)({sampleRate: MAX_ASR_SAMPLE_RATE}),
  timers = globalThis,
  encodeBase64 = toBase64,
  decodeBase64 = fromBase64,
} = {}) {
  const record = root?.querySelector('#voice-record');
  const cancelButton = root?.querySelector('#voice-cancel');
  const status = root?.querySelector('#voice-status');
  const fallback = root?.querySelector('#voice-fallback');
  let current = null;
  let disposed = false;

  function active(run = current) {
    return !disposed && run === current && !!run && !run.finished;
  }

  function live(run) {
    return active(run) && !run.cancelRequested;
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
      cancelButton.disabled = !active() || current.cancelRequested;
      cancelButton.setAttribute('aria-label', '音声対話をキャンセル');
    }
    if (status) {
      status.setAttribute('role', 'status');
      status.setAttribute('aria-live', 'polite');
      status.textContent = current?.permissionPending && !current.cancelRequested
        ? 'マイクの許可を確認しています．'
        : voiceStatusText(current?.snapshot);
    }
    if (fallback) {
      fallback.hidden = current?.snapshot.state !== 'FAILED' || !current.snapshot.transcript;
      fallback.textContent = '認識した内容をテキスト入力へ戻す';
    }
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
    run.chunks = [];
    run.samples = 0;
  }

  function stopPlayback(run) {
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
    try { Promise.resolve(run.context?.close()).catch(() => {}); } catch { /* Already closed． */ }
    run.context = null;
  }

  function notifyTranscript(run, text) {
    if (text && text !== run.reportedTranscript) {
      run.reportedTranscript = text;
      run.snapshot = {...run.snapshot, transcript: text};
      onTranscript(run.snapshot, text);
    }
  }

  function finish(run, snapshot) {
    if (run !== current || run.finished) return;
    run.snapshot = {...run.snapshot, ...snapshot};
    notifyTranscript(run, run.snapshot.transcript);
    run.finished = true;
    release(run);
    render();
    onBusy(false);
    if (run.snapshot.state === 'COMPLETED') onComplete(run.snapshot);
    else if (run.snapshot.state === 'FAILED') onFailure(run.snapshot);
    else if (run.snapshot.state === 'CANCELED') onCancel(run.snapshot);
  }

  function applySnapshot(run, snapshot) {
    if (!active(run) || !snapshot || snapshot.operation_id !== run.snapshot.operation_id
      || (snapshot.session_id && snapshot.session_id !== run.snapshot.session_id)
      || (snapshot.turn_id && snapshot.turn_id !== run.snapshot.turn_id)
      || (snapshot.trace_id && snapshot.trace_id !== run.snapshot.trace_id)) return;
    // Ignore delayed bridge responses and queued states from before cancel．
    if (run.cancelRequested && !['CANCELING', 'CANCELED', 'FAILED'].includes(snapshot.state)) return;
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

  function begin() {
    if (active() || disposed) return null;
    let ready;
    const run = {
      snapshot: {state: 'RECORDING', operation_id: ''},
      finished: false, cancelRequested: false, cancelPromise: null,
      recording: false, permissionPending: false, chunks: [], samples: 0,
      timer: null, context: null, contextReady: null,
      queue: [], queueBytes: 0, playing: null, decoding: false, ending: false, playbackEpoch: 0,
      lastSequence: 0, playbackBytesReceived: 0, earlyEvents: [], earlyBytes: 0, earlyOverflow: false,
      identityReady: new Promise((resolve) => { ready = resolve; }),
      resolveIdentity: null,
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
      const snapshot = await pendingSnapshot;
      if (!snapshot?.operation_id) throw new Error('missing_operation');
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
    } catch {
      run.resolveIdentity(null);
      if (run.cancelRequested) finish(run, {...run.snapshot, state: 'CANCELED'});
      else await fail(run, 'microphone_unavailable');
      return false;
    }
  }

  async function start() {
    const run = begin();
    if (!run) return false;
    let request;
    try { request = getRequest(); } catch {
      run.resolveIdentity(null);
      await fail(run, 'microphone_unavailable');
      return false;
    }
    let pending;
    try { pending = bridge.StartInteraction(request); } catch { pending = Promise.reject(new Error('start_failed')); }
    if (!await identify(run, pending)) return false;
    if (!await run.contextReady || !mediaDevices?.getUserMedia) {
      await fail(run, 'microphone_unavailable');
      return false;
    }
    if (!live(run)) return false;
    run.permissionPending = true;
    render();
    try {
      const stream = await mediaDevices.getUserMedia({audio: {channelCount: 1}, video: false});
      // Permission prompts can resolve after cancellation or a later turn．
      if (!live(run)) { stopTracks(stream); return false; }
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
      const maxSamples = Math.min(run.context.sampleRate * 60, Math.floor((MAX_RECORDING_BYTES - 44) / 2));
      run.processor.onaudioprocess = (event) => {
        if (!live(run) || !run.recording) return;
        const input = event.inputBuffer.getChannelData(0);
        const count = Math.min(input.length, maxSamples - run.samples);
        if (count > 0) {
          run.chunks.push(new Float32Array(input.subarray(0, count)));
          run.samples += count;
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
    let bytes;
    try { bytes = encodePCM16Wav(run.chunks, run.context.sampleRate); } catch {
      await fail(run, 'invalid_audio');
      return false;
    }
    stopCapture(run);
    run.snapshot = {...run.snapshot, state: 'TRANSCRIBING'};
    render();
    try {
      const encoded = encodeBase64(bytes);
      bytes = null;
      await bridge.CommitInteraction(run.snapshot.operation_id, encoded);
      return live(run) || run.snapshot.state === 'COMPLETED';
    } catch {
      await fail(run, 'invalid_audio');
      return false;
    }
  }

  function cancel() {
    const run = current;
    if (!active(run)) return Promise.resolve(run?.snapshot);
    if (run.cancelPromise) return run.cancelPromise;
    run.cancelRequested = true;
    run.snapshot = {...run.snapshot, state: 'CANCELING'};
    // Stop audible output and every microphone track before contacting Go．
    release(run);
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

  function receive(event) {
    const run = current;
    if (!active(run) || !event || typeof event !== 'object'
      || !['state', 'transcript', 'token', 'audio'].includes(event.kind)) return;
    if (!run.snapshot.operation_id) {
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
      || (event.trace_id && event.trace_id !== run.snapshot.trace_id)) return;
    if (event.kind === 'state') applySnapshot(run, event.snapshot);
    if (!live(run)) return;
    if (event.kind === 'transcript') notifyTranscript(run, event.text);
    else if (event.kind === 'token' && typeof event.text === 'string') onToken(run.snapshot, event.text);
    else if (event.kind === 'audio') enqueue(run, event);
  }

  async function adopt(snapshotOrPromise) {
    const run = begin();
    if (!run) return false;
    if (!await identify(run, snapshotOrPromise)) return false;
    if (!await run.contextReady) { await fail(run, 'playback_failed'); return false; }
    return live(run);
  }

  const onRecord = () => { void (current?.recording ? stop() : start()); };
  const onCancelClick = () => { void cancel(); };
  const onFallbackClick = () => { if (current?.snapshot.state === 'FAILED') onFallback(current.snapshot); };
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
