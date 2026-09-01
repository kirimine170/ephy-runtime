import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildKarteConversationRequest,
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
  });

  assert.equal(payload.project, 'ephy');
  assert.equal(payload.kind, 'decision');
  assert.equal(payload.intended_doc_id, null);
  assert.deepEqual(payload.messages, [
    {role: 'user', content: '方針を決めたい'},
    {role: 'assistant', content: 'project優先で決定します．'},
  ]);
});


test('local ISO timestamp preserves the local offset', () => {
  const date = new Date('2026-09-01T10:30:45+09:00');
  const rendered = formatLocalISOString(date);
  assert.match(rendered, /^2026-09-01T10:30:45[+-]\d{2}:\d{2}$/);
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
