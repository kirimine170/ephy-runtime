export const KARTE_KINDS = [
  'note',
  'meeting',
  'decision',
  'plan',
  'task',
  'research',
  'reference',
  'report',
  'person',
  'organization',
  'journal',
];

export function formatLocalISOString(date = new Date()) {
  const pad = (value) => String(value).padStart(2, '0');
  const offsetMinutes = -date.getTimezoneOffset();
  const sign = offsetMinutes >= 0 ? '+' : '-';
  const absoluteOffset = Math.abs(offsetMinutes);
  return [
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`,
    `T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`,
    `${sign}${pad(Math.floor(absoluteOffset / 60))}:${pad(absoluteOffset % 60)}`,
  ].join('');
}

export function buildKarteConversationRequest({
  conversationId,
  occurredAt,
  entries,
  project = '',
  kind = '',
  sensitivity = 'internal',
  tags = [],
  resolution = 'auto',
  intendedDocId = '',
}) {
  const messages = (entries || [])
    .filter((entry) => ['user', 'assistant'].includes(entry.role) && String(entry.text || '').trim())
    .slice(-30)
    .map((entry) => ({role: entry.role, content: String(entry.text).trim()}));
  if (messages.length < 2 || messages.at(-1)?.role !== 'assistant') {
    throw new Error('Karte候補には利用者とEphyの完了済み会話が必要です．');
  }
  return {
    conversation_id: conversationId,
    occurred_at: occurredAt,
    messages,
    project: String(project || '').trim().toLowerCase() || null,
    kind: String(kind || '').trim() || null,
    sensitivity,
    tags: [...new Set((tags || []).map((tag) => String(tag).trim()).filter(Boolean))],
    resolution,
    intended_doc_id: resolution === 'append' ? (String(intendedDocId || '').trim() || null) : null,
  };
}

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function selected(value, expected) {
  return value === expected ? ' selected' : '';
}

function planTone(plan) {
  if (!plan) return 'neutral';
  if (plan.publishable) return 'ready';
  return 'consultation';
}

export function renderKarteConversationCard(memory, requestId) {
  if (!memory || memory.dismissed) return '';
  const safeRequestId = escapeHtml(requestId);
  if (memory.state === 'planning') {
    return `
      <section class="karte-memory-card is-planning" data-karte-card="${safeRequestId}">
        <div class="karte-memory-head"><strong>Karte候補を整理中…</strong></div>
      </section>
    `;
  }
  if (memory.state === 'error') {
    return `
      <section class="karte-memory-card is-error" data-karte-card="${safeRequestId}">
        <div class="karte-memory-head"><strong>Karte連携を利用できません</strong></div>
        <p>${escapeHtml(memory.error || '候補を作成できませんでした．')}</p>
        <div class="message-actions">
          <button class="ghost-btn compact-btn" type="button" data-karte-action="replan" data-request-id="${safeRequestId}">再試行</button>
          <button class="ghost-btn compact-btn" type="button" data-karte-action="dismiss" data-request-id="${safeRequestId}">閉じる</button>
        </div>
      </section>
    `;
  }

  const plan = memory.plan;
  if (!plan) return '';
  const proposal = plan.proposal || {};
  const placement = proposal.placement || {};
  const project = memory.project ?? (plan.needs_project ? '' : placement.project || '');
  const kind = memory.kind ?? placement.kind ?? '';
  const resolution = memory.resolution || (plan.recommendation === 'append' ? 'append' : plan.recommendation === 'create' ? 'create' : 'auto');
  const intendedDocId = memory.intendedDocId || proposal.target_doc_id || '';
  const reasons = (plan.reasons || []).map((reason) => `<li>${escapeHtml(reason)}</li>`).join('');
  const similarOptions = (plan.similar_documents || []).map((document) => `
    <option value="${escapeHtml(document.doc_id)}"${selected(intendedDocId, document.doc_id)}>
      ${escapeHtml(document.title)} · ${Math.round(Number(document.similarity || 0) * 100)}% · ${escapeHtml(document.relative_path)}
    </option>
  `).join('');
  const state = memory.state || (plan.publishable ? 'ready' : 'consultation');
  const statusLabel = {
    ready: '送信前レビュー',
    consultation: '確認が必要',
    pending: 'Karteでレビュー待ち',
    accepted: 'Karteへ保存済み',
    rejected: 'Karteで破棄済み',
    processed: 'Karteで処理済み',
  }[state] || state;
  const finalState = ['accepted', 'rejected', 'processed'].includes(state);
  const pending = state === 'pending';

  return `
    <section class="karte-memory-card tone-${planTone(plan)}" data-karte-card="${safeRequestId}">
      <div class="karte-memory-head">
        <div>
          <span class="eyebrow dark">Karte候補</span>
          <strong>${escapeHtml(plan.summary_title)}</strong>
        </div>
        <span class="karte-memory-status is-${escapeHtml(state)}">${escapeHtml(statusLabel)}</span>
      </div>
      <div class="karte-memory-meta">
        <span>project=${escapeHtml(placement.project || '-')}</span>
        <span>kind=${escapeHtml(placement.kind || '-')}</span>
        <span>confidence=${Number(placement.confidence || 0).toFixed(2)}</span>
        <span>${escapeHtml(plan.recommendation)}</span>
      </div>
      ${reasons ? `<ul class="karte-memory-reasons">${reasons}</ul>` : ''}
      <details class="karte-memory-summary">
        <summary>まとめ内容を確認</summary>
        <pre>${escapeHtml(plan.summary_markdown || '')}</pre>
      </details>
      ${finalState ? '' : `
        <details class="karte-memory-resolution" ${plan.publishable ? '' : 'open'}>
          <summary>分類と保存方法</summary>
          <div class="karte-memory-fields">
            <label>
              <span>Project</span>
              <input class="text-input" data-karte-field="project" value="${escapeHtml(project)}" placeholder="ephy" />
            </label>
            <label>
              <span>Kind</span>
              <select class="text-input" data-karte-field="kind">
                <option value=""${selected(kind, '')}>自動</option>
                ${KARTE_KINDS.map((candidate) => `<option value="${candidate}"${selected(kind, candidate)}>${candidate}</option>`).join('')}
              </select>
            </label>
            <label>
              <span>保存方法</span>
              <select class="text-input" data-karte-field="resolution">
                <option value="auto"${selected(resolution, 'auto')}>Ephyの推奨</option>
                <option value="create"${selected(resolution, 'create')}>新規文書</option>
                <option value="append"${selected(resolution, 'append')}>既存文書へ追加</option>
              </select>
            </label>
            <label>
              <span>追加先</span>
              <select class="text-input" data-karte-field="intended-doc-id">
                <option value="">文書を選択</option>
                ${similarOptions}
              </select>
            </label>
          </div>
        </details>
      `}
      <div class="message-actions karte-memory-actions">
        ${pending ? `<button class="ghost-btn compact-btn" type="button" data-karte-action="refresh" data-request-id="${safeRequestId}">Karteの処理結果を確認</button>` : ''}
        ${!pending && !finalState ? `<button class="ghost-btn compact-btn" type="button" data-karte-action="replan" data-request-id="${safeRequestId}">候補を再計画</button>` : ''}
        ${!pending && !finalState ? `<button class="primary-btn compact-btn" type="button" data-karte-action="publish" data-request-id="${safeRequestId}" ${plan.publishable ? '' : 'disabled'}>Karteへ送る</button>` : ''}
        <button class="ghost-btn compact-btn" type="button" data-karte-action="dismiss" data-request-id="${safeRequestId}">閉じる</button>
      </div>
    </section>
  `;
}
