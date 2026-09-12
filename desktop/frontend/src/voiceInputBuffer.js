// PCM lives only in this session-owned bounded buffer．The rolling 500 ms
// becomes a lossless handoff queue at onset；overflow never drops its beginning．
export function createVoiceInputBuffer(sampleRate) {
  const preRoll = Math.ceil(sampleRate * .5), maximum = Math.ceil(sampleRate * 2);
  let frames = [], samples = 0, holding = false;
  return {
    get holding() { return holding; },
    get samples() { return samples; },
    snapshot() { return frames.map(frame => new Float32Array(frame)); },
    push(data) {
      if (holding && samples + data.length > maximum) return false;
      frames.push(new Float32Array(data)); samples += data.length;
      if (!holding) {
        while (samples > preRoll && frames.length) {
          const excess = samples - preRoll, first = frames[0];
          if (first.length <= excess) { samples -= first.length; frames.shift(); }
          else { frames[0] = first.slice(excess); samples -= excess; }
        }
      }
      return true;
    },
    hold() { holding = true; },
    shift() { const frame = frames.shift(); if (frame) samples -= frame.length; return frame; },
    release() { holding = false; },
    clear() { frames = []; samples = 0; holding = false; },
  };
}
