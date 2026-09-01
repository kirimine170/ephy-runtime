import test from 'node:test';
import assert from 'node:assert/strict';
import {compatibleAdapters, modelManagerErrorMessage} from './modelManager.js';

test('adapters require an available exact base ID and digest', () => {
  const catalog = {models: [{id: 'base', sha256: 'abc'}], adapters: [
    {id: 'valid', base_model_id: 'base', base_sha256: 'abc', available: true},
    {id: 'wrong-revision', base_model_id: 'base', base_sha256: 'def', available: true},
    {id: 'wrong-model', base_model_id: 'other', base_sha256: 'abc', available: true},
    {id: 'missing', base_model_id: 'base', base_sha256: 'abc', available: false},
  ]};
  assert.deepEqual(compatibleAdapters(catalog, 'base').map((item) => item.id), ['valid']);
  assert.deepEqual(compatibleAdapters(catalog, 'unknown'), []);
  assert.deepEqual(compatibleAdapters({}, 'base'), []);
});

test('browser development shows a friendly Wails bridge message', () => {
  assert.equal(
    modelManagerErrorMessage(new TypeError("Cannot read properties of undefined (reading 'main')")),
    'Developer Mode設定はデスクトップ版で利用できます．',
  );
  assert.equal(modelManagerErrorMessage(new Error('backend unavailable')), 'Error: backend unavailable');
});
