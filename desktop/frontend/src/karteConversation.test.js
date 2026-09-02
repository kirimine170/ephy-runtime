import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildKarteConversationRequest,
  formatKarteConversationContextStatus,
  formatLocalISOString,
  renderKarteConversationCard,
} from './karteConversation.js';


test('conversation request keeps only completed user and assistant messages', () => {
  const payload = buildKarteConversationRequest({
    conversationId: 'conversation-001',
    occurredAt: '2026-09-01T10:30:00+09:00',
    entries: [
      {role: 'user', text: '方針を決めたい'},
      {role: 'assistant', text: 'project優先で決定します．'},
      {role: 'system', text: 'not exported'},
    ],
    project: 'EPHY',
    kind: 'decision',
    resolution: 'create',
    intendedDocId: 'ignored-for-create',
    reviewedPlanSha256: 'a'.repeat(64),
  });

  assert.equal(payload.project, 'ephy');
  assert.equal(payload.kind, 'decision');
  assert.equal(payload.intended_doc_id, null);
  assert.equal(payload.reviewed_plan_sha256, 'a'.repeat(64));
  assert.deepEqual(payload.messages, [
    {role: 'user', content: '方針を決めたい'},
    {role: 'assistant', content: 'project優先で決定します．'},
  ]);
});


test('local ISO timestamp preserves the local offset', () => {
  const date = new Date(2026, 8, 1, 10, 30, 45);
  const rendered = formatLocalISOString(date);
  const offsetMinutes = -date.getTimezoneOffset();
  const sign = offsetMinutes >= 0 ? '+' : '-';
  const absoluteOffset = Math.abs(offsetMinutes);
  const offset = `${sign}${String(Math.floor(absoluteOffset / 60)).padStart(2, '0')}:${String(absoluteOffset % 60).padStart(2, '0')}`;

  assert.equal(rendered, `2026-09-01T10:30:45${offset}`);
});


test('conversation context status distinguishes partial reads from complete checks', () => {
  assert.equal(
    formatKarteConversationContextStatus({
      status: 'partial', searched_count: 3, read_count: 2, read_failed_count: 1,
    }),
    'Karte一部確認 · 2件読取／3件検索 · 1件未読',
  );
  assert.equal(formatKarteConversationContextStatus({status: 'not_required'}), 'Karte確認不要');
  assert.equal(
    formatKarteConversationContextStatus({
      status: 'ok', searched_count: 0, read_count: 1, read_failed_count: 0,
    }),
    'Karte確認済み · 1件読取',
  );
});


test('Karte card renders consultation controls and escapes summary content', () => {
  const html = renderKarteConversationCard({
    state: 'consultation',
    plan: {
      publishable: false,
      needs_project: true,
      recommendation: 'consult',
      reasons: ['project is required'],
      summary_title: '<script>alert(1)</script>',
      summary_markdown: '# <unsafe>',
      context_status: {status: 'unavailable', searched_count: 0, read_count: 0, read_failed_count: 0},
      proposal: {placement: {project: 'master', kind: 'note', confidence: 0.5}},
      similar_documents: [{
        doc_id: 'doc:existing',
        title: 'Existing',
        relative_path: 'content/projects/ephy/note/2026-09/existing.md',
        similarity: 0.9,
      }],
    },
  }, 'chat-001');

  assert.match(html, /Karte候補/);
  assert.match(html, /data-karte-field="project"/);
  assert.match(html, /Karte確認不可/);
  assert.match(html, /doc:existing/);
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /data-karte-action="publish"[^>]*disabled/);
});


test('publishable card enables send action', () => {
  const html = renderKarteConversationCard({
    state: 'ready',
    plan: {
      publishable: true,
      needs_project: false,
      recommendation: 'create',
      reasons: [],
      summary_title: '方針',
      summary_markdown: '# 方針',
      proposal: {placement: {project: 'ephy', kind: 'decision', confidence: 0.9}},
      similar_documents: [],
    },
  }, 'chat-001');

  assert.match(html, /data-karte-action="publish"/);
  assert.doesNotMatch(html, /data-karte-action="publish"[^>]*disabled/);
});
