// The filler monitor owns capture only．It forwards PCM to the shared
// interruption candidate gate and never treats RMS as confirmed speech．
// A failed／ended input disables filler；it never silently claims readiness．
export async function startFillerBargeIn({context, mediaDevices, isCurrent, onAudio, onUnavailable, subscribePCM}) {
  let unsubscribe = null;
  let stream = null, input = null, processor = null, mute = null, stopped = false;
  const stop = () => {
    stopped = true;
    unsubscribe?.(); unsubscribe = null;
    if (processor) processor.onaudioprocess = null;
    for (const track of stream?.getTracks() || []) { track.onended = null; track.stop(); }
    for (const node of [input, processor, mute]) { try { node?.disconnect(); } catch { /* Already disconnected． */ } }
    input = processor = mute = stream = null;
  };
  const process = (data, sampleRate) => {
    if (stopped || !isCurrent()) { stop(); return; }
    if (!data?.length || !Number.isFinite(sampleRate) || sampleRate <= 0) return;
    onAudio(data, sampleRate);
  };
  if (subscribePCM) { unsubscribe = subscribePCM(process); return {stop}; }
  try {
    stream = await mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: false}, video: false});
    if (stopped || !isCurrent()) { stop(); return null; }
    if (!stream.getAudioTracks?.().some(t => t.readyState === 'live')) { stop(); return null; }
    input = context.createMediaStreamSource(stream);
    processor = context.createScriptProcessor(256, 1, 1);
    mute = context.createGain(); mute.gain.value = 0;
    input.connect(processor); processor.connect(mute); mute.connect(context.destination);
    processor.onaudioprocess = event => {
      process(event.inputBuffer.getChannelData(0), context.sampleRate);
    };
    for (const track of stream.getTracks()) track.onended = () => { stop(); onUnavailable(); };
    return {stop};
  } catch { stop(); return null; }
}
