// Partial inactivity is an endpoint hint，never a provider stability guarantee．
export const DEFAULT_ENDPOINT = Object.freeze({rms: .02, onsetMS: 32, silenceMS: 900,
  fallbackSilenceMS: 1800, partialUnchangedMS: 300, graceMS: 100, pollMS: 20,
  maxUtteranceMS: 60_000, idleRefreshMS: 60_000, idlePauseMS: 300_000});
export function endpointSettings(overrides = {}) {
  const result = {...DEFAULT_ENDPOINT};
  for (const key of Object.keys(result)) {
    if (overrides[key] !== undefined) {
      if (!Number.isFinite(overrides[key]) || overrides[key] <= 0) throw new Error('invalid_endpoint_setting');
      result[key] = overrides[key];
    }
  }
  if (result.silenceMS > result.fallbackSilenceMS || result.maxUtteranceMS > 60_000
      || result.idleRefreshMS > 60_000 || result.idlePauseMS > 300_000) throw new Error('invalid_endpoint_setting');
  return result;
}
export function createVoiceEndpoint({now, settings = DEFAULT_ENDPOINT}) {
  let activeMS = 0, speech = false, started = null, lastSpeech = null;
  let partial = '', changed = null, candidate = null, committed = false;
  let modelActivity = false, audioMS = 0, processedMS = 0, lastSpeechMS = 0;
  const hesitation = text => /^(え[ーぇ]*[とっ]+|ええと|あの[ーぉ]*|その[ーぉ]*|うー+ん?)[\s，．、。…・.!?！？]*$/u.test(text.trim());
  return {
    get speech() { return speech; },
    get candidate() { return candidate !== null; },
    setActivityMode(value) { modelActivity = value === true; },
    activity(value) {
      if (!modelActivity || committed || !value || !Number.isSafeInteger(value.audio_ms)
          || value.audio_ms < processedMS || value.audio_ms > audioMS + 33
          || !Number.isSafeInteger(value.last_speech_ms) || value.last_speech_ms < lastSpeechMS
          || value.last_speech_ms > value.audio_ms) return false;
      processedMS = value.audio_ms;
      if (value.has_speech && value.last_speech_ms > lastSpeechMS) {
        speech = true;
        started ??= now() - Math.max(0, audioMS - value.last_speech_ms);
        lastSpeech = now() - Math.max(0, audioMS - value.last_speech_ms);
        lastSpeechMS = value.last_speech_ms;
        candidate = null;
      }
      return true;
    },
    audio(data, sampleRate) {
      if (committed) return false;
      audioMS += data.length / sampleRate * 1000;
      if (modelActivity) return speech;
      let energy = 0; for (const sample of data) energy += sample * sample;
      const loud = data.length > 0 && Math.sqrt(energy / data.length) >= settings.rms;
      activeMS = loud ? activeMS + data.length / sampleRate * 1000 : 0;
      if (loud) {
        candidate = null;
        if (speech || activeMS >= settings.onsetMS) {
          speech = true; started ??= now(); lastSpeech = now();
        }
      }
      return loud;
    },
    hypothesis(text) { if (text !== partial) { partial = text; changed = now(); candidate = null; } },
    tick() {
      if (!speech || committed) return '';
      const at = now();
      if (at - started >= settings.maxUtteranceMS) return 'limit';
      // Never end against a silence result while newer PCM is still awaiting VAD．
      if (modelActivity && audioMS - processedMS > 250) { candidate = null; return ''; }
      const usable = partial.trim() && !hesitation(partial) && changed !== null && at - changed >= settings.partialUnchangedMS;
      const threshold = usable || (modelActivity && !hesitation(partial)) ? settings.silenceMS : settings.fallbackSilenceMS;
      if (at - lastSpeech < threshold) { candidate = null; return ''; }
      candidate ??= at;
      if (at - candidate < settings.graceMS) return 'candidate';
      committed = true;
      return 'endpoint';
    },
    providerFinal() { if (!speech || committed) return false; committed = true; candidate = null; return true; },
  };
}
