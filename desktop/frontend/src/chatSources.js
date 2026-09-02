function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export function chatSourceTitle(source = {}, index = 0) {
  if (source.source_type === 'web') return source.title || source.source_id || `Web ${index + 1}`;
  if (source.source_type === 'karte_context') return source.title || source.source_id || `Karte ${index + 1}`;
  return source.heading_path?.slice(-1)?.[0] || source.source_path || `Source ${index + 1}`;
}

export function renderChatSourcePreviewHtml(source = null) {
  if (!source) {
    return `
      <div class="source-empty-state" role="status">
        <div class="source-empty-mark">↗</div>
        <strong>参照資料はまだありません</strong>
        <p>Personal ContextやLibraryを使った回答では，根拠と文書previewがここに表示されます．</p>
      </div>
    `;
  }
  const isWeb = source.source_type === 'web';
  const isKarte = source.source_type === 'karte_context';
  const title = chatSourceTitle(source);
  return `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(title)}</span>
        <span class="runtime-pill ${isWeb ? 'optional' : 'neutral'}">${escapeHtml(isWeb ? 'external untrusted' : (isKarte ? 'Karte · local untrusted' : (source.score != null ? Number(source.score).toFixed(3) : (source.project || '-'))))}</span>
      </div>
      ${isWeb ? `
        <div class="runtime-result-meta">${escapeHtml(source.source_id || '-')} · ${escapeHtml(source.url || '-')}</div>
        <div class="runtime-result-text">${escapeHtml(source.snippet || '')}</div>
        ${source.injection_suspected ? '<div class="web-source-warning">Potential instruction-like content was isolated from the answer model.</div>' : ''}
        <button class="ghost-btn compact-btn open-web-source" type="button" data-web-source-url="${escapeHtml(source.url || '')}">Open in Browser</button>
      ` : `
        <div class="runtime-result-meta">${escapeHtml(source.source_path || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml((source.heading_path || []).join(' > ') || '(root)')}</div>
        <div class="runtime-result-meta">${escapeHtml(source.project || '(default)')} | ${escapeHtml((source.tags || []).join(', ') || '(no tags)')}</div>
        <div class="runtime-result-text">${escapeHtml(source.chunk_text || source.snippet || '')}</div>
      `}
    </div>
  `;
}

export function renderChatSourceListHtml(sources = [], activeIndex = 0) {
  return sources.map((source, index) => `
    <button class="source-card ${index === activeIndex ? 'active' : ''}" data-source-index="${index}" aria-pressed="${index === activeIndex}">
      <div class="source-card-top">
        <strong>${escapeHtml(chatSourceTitle(source, index))}</strong>
        <span>${escapeHtml(source.source_type === 'web' ? 'WEB' : (source.source_type === 'karte_context' ? 'KARTE' : (source.score != null ? Number(source.score).toFixed(3) : '-')))}</span>
      </div>
      <div class="source-card-path">${escapeHtml(source.source_type === 'web' ? (source.url || '-') : (source.source_path || '-'))}</div>
      <div class="source-card-meta">${escapeHtml(source.source_type === 'web' ? 'external_untrusted' : (source.project || '(default)'))}</div>
      <div class="source-card-heading">${escapeHtml(source.source_type === 'web' ? (source.snippet || '').slice(0, 120) : ((source.heading_path || []).join(' > ') || '(root)'))}</div>
    </button>
  `).join('');
}
