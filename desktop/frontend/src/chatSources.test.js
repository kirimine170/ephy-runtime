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

test('v2 recorded source shows bound revision, hash, and event citation', () => {
  const source = {
    source_type: 'karte_record_v2', title: 'Recorded conversation',
    chunk_text: '<private> starts at 10:00',
    karte_record_v2: {
      target: {doc_id: 'synthetic-doc', revision: 2, sha256: 'a'.repeat(64)},
      event: {event_id: 'synthetic-event', event_revision: 1},
    },
  };
  const preview = renderChatSourcePreviewHtml(source);
  const list = renderChatSourceListHtml([source]);
  assert.match(preview, /synthetic-doc@2#synthetic-event@1/);
  assert.match(preview, new RegExp('a'.repeat(64)));
  assert.match(preview, /&lt;private&gt;/);
  assert.match(list, /KARTE V2/);
  assert.match(list, /synthetic-doc@2#synthetic-event@1/);
});
