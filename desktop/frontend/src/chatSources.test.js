import test from 'node:test';
import assert from 'node:assert/strict';

import {chatSourceTitle, renderChatSourceListHtml, renderChatSourcePreviewHtml} from './chatSources.js';

test('source feature renders a helpful empty artifact state', () => {
  const html = renderChatSourcePreviewHtml();
  assert.match(html, /参照資料はまだありません/);
  assert.match(html, /role="status"/);
});

test('Karte source keeps provenance visible and escapes untrusted content', () => {
  const source = {
    source_type: 'karte_context',
    title: '<private>',
    source_id: 'doc:private',
    source_path: 'content/projects/ephy/note.md',
    project: 'ephy',
    tags: ['context'],
    snippet: '<script>unsafe</script>',
  };
  assert.equal(chatSourceTitle(source), '<private>');
  const html = renderChatSourcePreviewHtml(source);
  assert.match(html, /Karte · local untrusted/);
  assert.match(html, /&lt;private&gt;/);
  assert.doesNotMatch(html, /<script>/);
});

test('source list exposes stable selection semantics', () => {
  const html = renderChatSourceListHtml([
    {source_type: 'web', title: 'Web source', url: 'https://example.com'},
    {source_type: 'karte_context', title: 'Karte source', source_path: 'content/a.md'},
  ], 1);
  assert.match(html, /data-source-index="1" aria-pressed="true"/);
  assert.match(html, /KARTE/);
});
