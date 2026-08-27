const FORBIDDEN_REVIEW_FIELDS = new Set([
  'candidate_a',
  'candidate_b',
  'candidate_id',
  'display_order',
  'model_registration_id',
  'adapter_registration_id',
  'model_sha256',
  'adapter_sha256',
  'prompt_variant',
  'prompt_revision',
  'seed',
  'generated_at',
]);

export function preferenceSelectionForKey(key) {
  const normalized = String(key || '').toLowerCase();
  return {
    '1': 'left',
    '2': 'right',
    '0': 'tie',
    s: 'skip',
  }[normalized] || null;
}

export function assertBlindPreferencePair(pair) {
  const inspect = (value) => {
    if (Array.isArray(value)) {
      value.forEach(inspect);
      return;
    }
    if (!value || typeof value !== 'object') {
      return;
    }
    Object.entries(value).forEach(([key, nested]) => {
      if (FORBIDDEN_REVIEW_FIELDS.has(key)) {
        throw new Error(`Preference review payload is not blind: ${key}`);
      }
      inspect(nested);
    });
  };
  inspect(pair);
  return pair;
}

export function renderBlindPreferencePair(pair, escapeHtml = escapeDefault) {
  if (!pair) {
    return '<div class="preference-empty">生成済みの未評価候補はありません．</div>';
  }
  assertBlindPreferencePair(pair);
  const messages = Array.isArray(pair.messages) ? pair.messages : [];
  const history = messages.map((message) => `
    <div class="preference-message preference-message-${message.role === 'assistant' ? 'assistant' : 'user'}">
      <span>${message.role === 'assistant' ? 'Ephy' : 'User'}</span>
      <p>${escapeHtml(message.content || '')}</p>
    </div>
  `).join('');
  return `
    <div class="preference-category">${escapeHtml(pair.category || '')}</div>
    <div class="preference-history">${history}</div>
    <div class="preference-candidates">
      <button type="button" class="preference-candidate" data-preference-selection="left">
        <span class="preference-candidate-label">左の候補 · 1</span>
        <span class="preference-candidate-response">${escapeHtml(pair.response_left || '')}</span>
      </button>
      <button type="button" class="preference-candidate" data-preference-selection="right">
        <span class="preference-candidate-label">右の候補 · 2</span>
        <span class="preference-candidate-response">${escapeHtml(pair.response_right || '')}</span>
      </button>
    </div>
  `;
}

export function renderPromptComparison(comparison, escapeHtml = escapeDefault) {
  const variantsInMode = String(comparison?.mode || '').match(/^prompt_(v\d+)_(v\d+)$/);
  if (!variantsInMode) {
    return '';
  }
  if (comparison.blinded !== false) {
    return '<div class="runtime-result-text">Prompt versionはsession完了までblindです．</div>';
  }
  const variants = comparison.variants || {};
  const variantNames = variantsInMode.slice(1);
  const winner = comparison.winner === 'tie' ? '同率' : `Prompt ${comparison.winner || '-'}`;
  const results = variantNames.map((name) => {
    const result = variants[name] || {};
    const rate = Math.round(Number(result.win_rate || 0) * 100);
    return `<span>${escapeHtml(name)} ${escapeHtml(String(result.wins || 0))}勝 · ${escapeHtml(String(rate))}%</span>`;
  }).join('');
  return `
    <div class="preference-comparison-result">
      <strong>${escapeHtml(winner)}</strong>
      ${results}
    </div>
  `;
}

export function createPreferenceReviewController({submitVote, loadNext, onError = () => {}}) {
  const state = {
    pair: null,
    selection: null,
    saving: false,
    previous: null,
  };

  return {
    state,
    resume(pair) {
      state.pair = pair || null;
      state.selection = null;
    },
    select(selection) {
      if (['left', 'right', 'tie', 'skip'].includes(selection)) {
        state.selection = selection;
      }
    },
    correctPrevious() {
      if (!state.previous || state.saving) {
        return false;
      }
      state.pair = state.previous.pair;
      state.selection = null;
      return true;
    },
    async submit(extra = {}) {
      if (state.saving || !state.pair || !state.selection) {
        return false;
      }
      state.saving = true;
      const pair = state.pair;
      const selection = state.selection;
      try {
        const result = await submitVote(pair, {selection, ...extra});
        state.previous = {pair, voteId: result?.vote_id || null};
        state.pair = await loadNext();
        state.selection = null;
        return true;
      } catch (error) {
        onError(String(error));
        return false;
      } finally {
        state.saving = false;
      }
    },
  };
}

function escapeDefault(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}
