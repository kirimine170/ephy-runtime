import assert from 'node:assert/strict';
import test from 'node:test';

import {formatKarteContextStatus} from './karteContextStatus.js';

test('distinguishes selected sources from canonical documents read', () => {
  assert.equal(formatKarteContextStatus({
    status: 'ok',
    source_count: 3,
    searched_count: 5,
    read_count: 2,
    read_failed_count: 1,
  }), 'Karte Personal Context · 2/3 documents read · 1 snippet fallback');
});

test('reports unavailable context without implying the chat failed', () => {
  assert.equal(
    formatKarteContextStatus({status: 'unavailable'}),
    'Karte Personal Context is unavailable. Continuing without saved context.',
  );
});
