export function compatibleAdapters(catalog, modelId) {
  const model = (catalog.models || []).find((item) => item.id === modelId);
  if (!model) return [];
  return (catalog.adapters || []).filter((item) => item.available
    && item.base_model_id === modelId && item.base_sha256 === model.sha256);
}

export function modelManagerErrorMessage(error) {
  const detail = String(error || '');
  if (detail.includes("reading 'main'") || detail.includes('window.go')) {
    return 'Developer Mode設定はデスクトップ版で利用できます．';
  }
  return detail;
}

function contextLabel(tokens) {
  if (!tokens) return '';
  return tokens >= 1000000 ? `${(tokens / 1000000).toFixed(1)}M`
    : tokens >= 1000 ? `${Math.round(tokens / 1000)}K` : String(tokens);
}

export function formatModelLabel(model) {
  const size = `${(model.size_bytes / 1024 ** 3).toFixed(1)} GiB`;
  const profile = model.profile_id
    ? ` · ${model.parameter_count_billions}B · ctx ${contextLabel(model.context_size)}` : '';
  const warning = model.resource_fit === false ? ' · メモリ要確認' : '';
  const missing = model.available ? '' : ' · ファイル未検出';
  return `${model.id} · ${size}${profile}${warning}${missing}`;
}

export function formatModelProfile(model) {
  if (!model?.profile_id) return 'プロファイル未設定．登録済みのコンテキスト値で起動します．';
  const enabled = (model.enabled_capabilities || []).join('・') || 'なし';
  const unavailable = (model.capabilities || [])
    .filter((capability) => !(model.enabled_capabilities || []).includes(capability));
  const pending = unavailable.length ? ` ／ 未接続：${unavailable.join('・')}` : '';
  const warning = model.resource_warning ? ' ／ ホストメモリが目安を下回ります' : '';
  return `${model.family} ／ ${model.parameter_count_billions}B ／ Runtime有効：${enabled}${pending}`
    + ` ／ 起動待ち：${model.startup_timeout_seconds}秒${warning}．`;
}

export function mountModelManager(container, api) {
  container.innerHTML = `
    <div class="panel-head"><h2>Developer Mode</h2></div>
    <label class="developer-toggle"><input type="checkbox" data-model="enabled"> Developer Modeを有効にする</label>
    <p class="helper-text">通常の会話やKarte連携には不要です．model，LoRA，runtime，routing，evaluationの実験・診断機能を使う場合だけ有効にします．</p>
    <fieldset class="developer-model-controls" data-model="controls" disabled hidden>
      <h3>Karte automation</h3>
      <label class="developer-toggle"><input type="checkbox" data-model="karte-auto-submit"> publish可能な候補をKarteへ自動送信する</label>
      <p class="helper-text">project，confidence，Personal Context確認，類似文書，doc_id／digest検証は省略しません．Karteでの最終採用は引き続きまとめて確認できます．</p>
      <h3>Model &amp; LoRA</h3>
      <p class="helper-text">登録済みGGUFを選択します．稼働中の対象モデルだけを再起動し，失敗時は元の選択へ戻します．</p>
      <div class="developer-model-grid">
        <label>役割<select class="text-input" data-model="role"><option value="fast">Fast · 会話</option><option value="work">Work · 詳細作業</option><option value="code">Code · 開発</option></select></label>
        <label>モデル<select class="text-input" data-model="model"></select></label>
        <label>口調LoRA<select class="text-input" data-model="adapter"></select></label>
      </div>
      <p class="helper-text" data-model="current"></p>
      <p class="helper-text" data-model="profile"></p>
      <div class="actions"><button class="primary-btn" data-model="apply">選択を適用</button><button class="ghost-btn" data-model="refresh">再読込</button></div>
      <details class="developer-import"><summary>手元のGGUFを追加</summary>
        <p class="helper-text">ファイルは移動・再ダウンロードしません．登録IDは英小文字・数字・ハイフン等で指定します．LoRAは現在選択中のbase modelに結び付けます．</p>
        <label>登録ID<input class="text-input" data-model="import-id" placeholder="qwen3-8b-q6-k" autocomplete="off"></label>
        <label>Runtimeプロファイル<select class="text-input" data-model="import-profile"></select></label>
        <div class="actions"><button class="ghost-btn" data-model="import-model">モデルを選んで登録</button><button class="ghost-btn" data-model="import-adapter">LoRAを選んで登録</button></div>
      </details>
    </fieldset>
    <p class="helper-text" data-model="status" role="status" aria-live="polite">モデル一覧を確認中…</p>`;
  const element = (name) => container.querySelector(`[data-model="${name}"]`);
  let catalog = {models: [], adapters: [], selections: {}};
  let busy = false;

  function message(text) { element('status').textContent = text; }
  function setBusy(value) {
    busy = value;
    element('controls').disabled = value || !catalog.developer_mode;
    element('enabled').disabled = value;
    container.setAttribute('aria-busy', String(value));
  }
  function fill(select, entries, emptyLabel) {
    select.replaceChildren(new Option(emptyLabel, ''));
    for (const entry of entries) {
      const option = new Option(entry.label, entry.id);
      option.disabled = entry.disabled || false;
      select.add(option);
    }
  }
  function renderAdapters(selected = '') {
    const entries = compatibleAdapters(catalog, element('model').value);
    fill(element('adapter'), entries.map((item) => ({id: item.id,
      label: item.id + (item.experimental ? ' · 実験版' : '')})), 'なし · Profileのみ');
    element('adapter').value = entries.some((item) => item.id === selected) ? selected : '';
  }
  function renderRole() {
    const selection = catalog.selections?.[element('role').value] || {};
    fill(element('model'), catalog.models.map((item) => ({id: item.id,
      label: formatModelLabel(item),
      disabled: !item.available})), '既定モデル · 起動スクリプトの設定');
    element('model').value = selection.model_id || '';
    renderAdapters(selection.adapter_id);
    element('current').textContent = `保存中：${selection.model_id || '既定モデル'} ／ LoRA：${selection.adapter_id || 'なし'}．停止中のモデルは次回起動時に適用します．`;
    element('profile').textContent = formatModelProfile(
      catalog.models.find((item) => item.id === element('model').value));
  }
  function render(next) {
    catalog = next;
    const enabled = Boolean(catalog.developer_mode);
    element('enabled').checked = enabled;
    element('karte-auto-submit').checked = Boolean(catalog.karte_auto_submit);
    element('controls').hidden = !enabled;
    api.onDeveloperModeChange?.(enabled);
    api.onKarteAutoSubmitChange?.(Boolean(catalog.karte_auto_submit));
    fill(element('import-profile'), Object.entries(catalog.profiles || {}).map(([id, profile]) => ({
      id, label: `${id} · ${profile.parameter_count_billions}B · ctx ${contextLabel(profile.default_context_size)}`,
    })), '自動判定または汎用');
    renderRole();
    setBusy(busy);
  }
  async function perform(action, pending, success) {
    if (busy) return;
    setBusy(true);
    message(pending);
    try {
      const next = await action();
      if (next) render(next);
      message(success);
    } catch (error) {
      element('enabled').checked = Boolean(catalog.developer_mode);
      message(modelManagerErrorMessage(error));
    } finally { setBusy(false); }
  }
  element('role').addEventListener('change', renderRole);
  element('model').addEventListener('change', () => {
    renderAdapters();
    element('profile').textContent = formatModelProfile(
      catalog.models.find((item) => item.id === element('model').value));
  });
  element('enabled').addEventListener('change', () => perform(async () => {
    await api.SetDeveloperMode(element('enabled').checked);
    return api.GetLocalModelCatalog();
  }, '開発者モードを更新中…', '設定を保存しました．'));
  element('karte-auto-submit').addEventListener('change', () => perform(async () => {
    await api.SetKarteAutoSubmit(element('karte-auto-submit').checked);
    return api.GetLocalModelCatalog();
  }, 'Karte自動送信を更新中…', 'Karte自動送信を設定しました．'));
  element('refresh').addEventListener('click', () => perform(api.GetLocalModelCatalog,
    'モデル一覧を確認中…', 'モデル一覧を更新しました．'));
  element('apply').addEventListener('click', () => perform(() => api.ApplyLocalModel({
    role: element('role').value, model_id: element('model').value, adapter_id: element('adapter').value,
  }), 'checksumを検証し，選択を適用中です…', '適用しました．Chatで応答を確認できます．'));
  for (const kind of ['model', 'adapter']) {
    element(`import-${kind}`).addEventListener('click', () => {
      const id = element('import-id').value.trim();
      const base = kind === 'adapter' ? element('model').value : '';
      if (!id || (kind === 'adapter' && !base)) {
        message('登録IDを入力してください．LoRAの場合はbase modelも選択してください．');
        return;
      }
      perform(() => api.ImportLocalModel({id, path: '', base_model_id: base,
        profile_id: kind === 'model' ? element('import-profile').value : '', context_size: 0}),
        'ファイルを選択し，checksumを計算します…', '登録処理が完了しました．適用するモデルを選んでください．');
    });
  }
  perform(api.GetLocalModelCatalog, 'モデル一覧を確認中…', '開発者モードを有効にするとモデルを変更できます．');
}
