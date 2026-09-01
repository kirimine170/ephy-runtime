import assert from 'node:assert/strict';
import test from 'node:test';

import {shouldUseGenericRagEndpoint} from './chatRouting.js';

test('uses the generic RAG endpoint for With Sources without Personal Context', () => {
  assert.equal(shouldUseGenericRagEndpoint({
    mode: 'rag',
    webSearchEnabled: false,
    sourceScope: 'project',
  }), true);
});

test('keeps Personal Context on the Karte-aware chat endpoint', () => {
  assert.equal(shouldUseGenericRagEndpoint({
    mode: 'rag',
    webSearchEnabled: false,
    sourceScope: 'personal_context',
  }), false);
});

test('keeps web-grounded chat on the combined chat endpoint', () => {
  assert.equal(shouldUseGenericRagEndpoint({
    mode: 'rag',
    webSearchEnabled: true,
    sourceScope: 'personal_context',
  }), false);
});
