const STYLE_DEFAULTS = Object.freeze({
  affect: 'neutral', intensity: 1, pace: 1, pitch_hint: 0, volume: 1, pause_style: 'natural', interruptible: true,
});
const CONTROL_LABELS = Object.freeze({affect: '話し方', intensity: '表現の強さ', pace: '話す速さ', pitch_hint: '声の高さ', volume: '音量', pause_style: '間の取り方'});
const NUMBER_CONTROLS = new Set(['intensity', 'pace', 'pitch_hint', 'volume']);
const ENUM_LABELS = Object.freeze({neutral: '自然', natural: '自然', calm: '穏やか', warm: '温かく', cheerful: '明るく'});
const PROFILE_ID = /^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$/;
const CATALOG_ERRORS = new Set([
  'tts_unavailable', 'tts_service_unavailable', 'tts_failed', 'tts_timeout', 'tts_canceled', 'tts_incomplete',
  'tts_invalid_audio', 'tts_audio_limit', 'tts_stream_invalid', 'tts_stream_eof', 'voice_profile_unavailable',
  'invalid_voice_profile', 'unsupported_voice_control', 'invalid_speech_text', 'invalid_voice_config',
  'voice_profile_changed', 'tts_busy',
]);

function nativeFallback() {
  return {
    voice_profile_id: 'macos-kyoko', display_name: 'Kyoko（標準）', available: true,
    default_style: {...STYLE_DEFAULTS},
    capabilities: {controls: {pace: {type: 'number', min: 0.5, max: 2, step: 0.1}, volume: {type: 'number', min: 0, max: 1, step: 0.1}}},
  };
}

function validNumber(value, control) {
  if (!Number.isFinite(value) || value < control.min || value > control.max) return false;
  if (!control.step) return true;
  const steps = (value - control.min) / control.step;
  return Math.abs(steps - Math.round(steps)) < 1e-7;
}

function normalizeProfile(source) {
  if (!source || typeof source.voice_profile_id !== 'string' || !PROFILE_ID.test(source.voice_profile_id)) return null;
  const profile = {voice_profile_id: source.voice_profile_id,
    display_name: typeof source.display_name === 'string' && source.display_name.trim() ? source.display_name.slice(0, 160) : source.voice_profile_id,
    available: source.available === true, default_style: {...STYLE_DEFAULTS}, capabilities: {controls: {}}};
  for (const [key, fallback] of Object.entries(STYLE_DEFAULTS)) {
    const value = source.default_style?.[key] ?? fallback;
    if (typeof value !== typeof fallback || (typeof value === 'number' && !Number.isFinite(value))
      || (typeof value === 'string' && (!value || value.length > 80))) return null;
    profile.default_style[key] = value;
  }
  // Provider identity，model details and reference-asset metadata are not needed
  // by this view and are never retained in its settings or copied to requests．
  for (const key of Object.keys(CONTROL_LABELS)) {
    const control = source.capabilities?.controls?.[key];
    if (!control) continue;
    if (NUMBER_CONTROLS.has(key)) {
      const step = control.step ?? 0;
      if (control.type !== 'number' || ![control.min, control.max, step].every(Number.isFinite)
        || control.min > control.max || step < 0 || !validNumber(profile.default_style[key], {...control, step})) return null;
      profile.capabilities.controls[key] = {type: 'number', min: control.min, max: control.max, step};
    } else {
      if (control.type !== 'enum' || !Array.isArray(control.values) || !control.values.length || control.values.length > 32
        || control.values.some(value => typeof value !== 'string' || !value || value.length > 80)
        || !control.values.includes(profile.default_style[key])) return null;
      profile.capabilities.controls[key] = {type: 'enum', values: [...new Set(control.values)]};
    }
  }
  return profile;
}

export function voiceProfilesMarkup() {
  return `<details id="voice-profile-settings" class="voice-profile-settings">
    <summary>声と話し方</summary>
    <p class="helper-text">次に始める音声会話に適用します．</p>
    <div class="voice-profile-picker"><label class="field"><span>声</span><select id="voice-profile-select" aria-describedby="voice-profile-status"></select></label>
      <button id="voice-profile-refresh" class="ghost-btn" type="button">声の一覧を更新</button></div>
    <div id="voice-style-controls" class="voice-style-controls"></div>
    <p id="voice-profile-status" role="status" aria-live="polite"></p>
  </details>`;
}

/** Profile settings are captured once for Start．Continue and Replay retain the
 * original Runtime request rather than reading the currently selected controls． */
export function mountVoiceProfiles({root, bridge, isBusy = () => false} = {}) {
  const select = root?.querySelector('#voice-profile-select');
  const refreshButton = root?.querySelector('#voice-profile-refresh');
  const controlsNode = root?.querySelector('#voice-style-controls');
  const status = root?.querySelector('#voice-profile-status');
  const document = root?.ownerDocument || root;
  let profiles = [nativeFallback()];
  let selected = profiles[0].voice_profile_id;
  let style = {...profiles[0].default_style};
  let busy = !!isBusy();
  let loading = false;
  let disposed = false;
  let epoch = 0;
  let pendingCatalog = null;
  let catalogApplied = false;
  let selectionTouched = false;
  let message = '';
  const controlNodes = new Map();
  const controlListeners = [];

  const currentProfile = () => profiles.find(profile => profile.voice_profile_id === selected && profile.available);
  function setMessage(text) { message = text; if (status) status.textContent = text; }
  function clearControlListeners() {
    for (const [node, handler] of controlListeners) node.removeEventListener('change', handler);
    controlListeners.length = 0;
    controlNodes.clear();
  }
  function setDisabled() {
    const locked = busy || isBusy() || disposed;
    if (select) select.disabled = locked;
    if (refreshButton) refreshButton.disabled = locked || loading;
    for (const input of controlNodes.values()) input.disabled = locked;
  }
  function renderControls() {
    clearControlListeners();
    if (!controlsNode || !document?.createElement) return;
    controlsNode.replaceChildren();
    const profile = currentProfile();
    for (const [key, control] of Object.entries(profile?.capabilities.controls || {})) {
      if ((control.type === 'enum' && control.values.length < 2) || (control.type === 'number' && control.min === control.max)) continue;
      const label = document.createElement('label');
      label.className = 'field';
      const title = document.createElement('span');
      title.textContent = CONTROL_LABELS[key];
      const input = document.createElement(control.type === 'enum' ? 'select' : 'input');
      input.id = `voice-style-${key}`;
      if (control.type === 'enum') {
        for (const value of control.values) {
          const option = document.createElement('option');
          option.value = value;
          option.textContent = ENUM_LABELS[value] || value;
          input.appendChild(option);
        }
      } else {
        input.type = 'number';
        input.min = String(control.min);
        input.max = String(control.max);
        input.step = control.step ? String(control.step) : 'any';
      }
      input.value = String(style[key]);
      const handler = () => {
        if (busy || isBusy() || disposed) { input.value = String(style[key]); return; }
        const value = control.type === 'number' && input.value !== '' ? Number(input.value) : input.value;
        if (control.type === 'number' ? !validNumber(value, control) : !control.values.includes(value)) {
          input.value = String(style[key]);
          setMessage('この声で利用できる値を指定してください．設定は変更していません．');
          return;
        }
        style = {...style, [key]: value};
        selectionTouched = true;
        setMessage('');
      };
      input.addEventListener('change', handler);
      controlListeners.push([input, handler]);
      controlNodes.set(key, input);
      label.appendChild(title);
      label.appendChild(input);
      controlsNode.appendChild(label);
    }
    setDisabled();
  }
  function render() {
    if (select && document?.createElement) {
      select.replaceChildren();
      for (const profile of profiles) {
        const option = document.createElement('option');
        option.value = profile.voice_profile_id;
        option.textContent = profile.display_name + (profile.available ? '' : '（利用できません）');
        option.disabled = !profile.available;
        select.appendChild(option);
      }
      select.value = selected;
    }
    if (status) status.textContent = message;
    renderControls();
    setDisabled();
  }
  function applyCatalog(catalog) {
    const previous = currentProfile();
    profiles = catalog.profiles;
    const desired = (selectionTouched || (catalogApplied && selected)) ? selected : catalog.default_profile_id;
    const next = profiles.find(profile => profile.voice_profile_id === desired);
    const retained = previous && next?.available && previous.voice_profile_id === next.voice_profile_id;
    selected = typeof desired === 'string' && PROFILE_ID.test(desired) ? desired : '';
    catalogApplied = true;
    // Keep an unavailable selection visible until the user explicitly changes it．
    if (!next) profiles.push({voice_profile_id: selected, display_name: '選択した声', available: false,
      default_style: {...STYLE_DEFAULTS}, capabilities: {controls: {}}});
    if (!retained || JSON.stringify(previous) !== JSON.stringify(next)) style = {...(next?.default_style || STYLE_DEFAULTS)};
    setMessage(!next?.available ? '選択した声を利用できません．声を選び直すか，テキスト入力を利用できます．' : catalog.message || '');
    render();
  }
  async function refresh() {
    if (disposed || busy || isBusy()) return false;
    const version = ++epoch;
    loading = true;
    setDisabled();
    let catalog;
    try {
      if (typeof bridge?.GetVoiceProfiles !== 'function') catalog = {default_profile_id: 'macos-kyoko', profiles: [nativeFallback()]};
      else {
        const result = await bridge.GetVoiceProfiles();
        if (!result || (result.error_code && !CATALOG_ERRORS.has(result.error_code))
          || !Array.isArray(result.profiles) || result.profiles.length > 64) throw new Error('voice_profiles_unavailable');
        const seen = new Set();
        const normalized = result.profiles.map(normalizeProfile).filter(profile => {
          if (!profile || seen.has(profile.voice_profile_id)) return false;
          seen.add(profile.voice_profile_id);
          return true;
        });
        if (!normalized.some(profile => profile.voice_profile_id === 'macos-kyoko')) normalized.push(nativeFallback());
        catalog = {default_profile_id: result.default_profile_id, profiles: normalized,
          message: result.error_code ? '一部の声を利用できません．利用できる声またはテキスト入力を選んでください．' : ''};
      }
    } catch {
      catalog = {default_profile_id: '', profiles: [nativeFallback()], message: '声の一覧を取得できません．声を選び直すか，テキスト入力を利用できます．'};
    }
    if (disposed || version !== epoch) return false;
    loading = false;
    if (busy || isBusy()) pendingCatalog = catalog;
    else applyCatalog(catalog);
    setDisabled();
    return true;
  }
  function setBusy(value) {
    busy = !!value;
    if (!busy && pendingCatalog) {
      const pending = pendingCatalog;
      pendingCatalog = null;
      applyCatalog(pending);
    }
    setDisabled();
  }
  function onSelect() {
    if (busy || isBusy() || disposed) { if (select) select.value = selected; return; }
    const profile = profiles.find(item => item.voice_profile_id === select?.value && item.available);
    if (!profile) { if (select) select.value = selected; return; }
    selected = profile.voice_profile_id;
    selectionTouched = true;
    style = {...profile.default_style};
    setMessage('');
    renderControls();
  }
  const onRefresh = () => { void refresh(); };
  select?.addEventListener('change', onSelect);
  refreshButton?.addEventListener('click', onRefresh);
  render();
  const ready = refresh();
  return {
    ready, refresh, setBusy,
    readSettings() {
      if ((!catalogApplied && !selectionTouched && typeof bridge?.GetVoiceProfiles === 'function') || !currentProfile()) throw new Error('voice_profile_unavailable');
      return {voice_profile_id: selected, style: {...style}};
    },
    dispose() {
      disposed = true;
      epoch += 1;
      pendingCatalog = null;
      setDisabled();
      clearControlListeners();
      select?.removeEventListener('change', onSelect);
      refreshButton?.removeEventListener('click', onRefresh);
      setDisabled();
    },
  };
}
