// C0.4 headset-only activity interrupt．No ASR，transcript or recording is made．
// A failed／ended input disables filler；it never silently claims readiness．
export async function startFillerBargeIn({context, mediaDevices, isCurrent, onSpeech, onUnavailable}) {
  let stream = null, input = null, processor = null, mute = null, stopped = false, activeMS = 0;
  const stop = () => {
    stopped = true;
    if (processor) processor.onaudioprocess = null;
    for (const track of stream?.getTracks() || []) { track.onended = null; track.stop(); }
    for (const node of [input, processor, mute]) { try { node?.disconnect(); } catch { /* Already disconnected． */ } }
    input = processor = mute = stream = null;
  };
  try {
    stream = await mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: false}, video: false});
    if (stopped || !isCurrent()) { stop(); return null; }
    if (!stream.getAudioTracks?.().some(t => t.readyState === 'live')) { stop(); return null; }
    input = context.createMediaStreamSource(stream);
    processor = context.createScriptProcessor(256, 1, 1);
    mute = context.createGain(); mute.gain.value = 0;
    input.connect(processor); processor.connect(mute); mute.connect(context.destination);
    processor.onaudioprocess = event => {
      if (stopped || !isCurrent()) { stop(); return; }
      const data = event.inputBuffer.getChannelData(0);
      let energy = 0; for (const value of data) energy += value * value;
      activeMS = Math.sqrt(energy / data.length) >= .02 ? activeMS + data.length / context.sampleRate * 1000 : 0;
      if (activeMS >= 32) { stop(); onSpeech(); }
    };
    for (const track of stream.getTracks()) track.onended = () => { stop(); onUnavailable(); };
    return {stop};
  } catch { stop(); return null; }
}
