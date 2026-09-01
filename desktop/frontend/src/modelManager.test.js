import test from 'node:test';
import assert from 'node:assert/strict';
import {
  compatibleAdapters, formatModelLabel, formatModelProfile, modelManagerErrorMessage,
} from './modelManager.js';

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

test('model profile distinguishes native and Runtime-enabled capabilities', () => {
  const model = {
    id: 'qwen3.8-27b', size_bytes: 17.1 * 1024 ** 3, available: true,
    profile_id: 'qwen3.8-27b', family: 'qwen3.8', parameter_count_billions: 27,
    context_size: 32768, capabilities: ['text', 'vision', 'reasoning'],
    enabled_capabilities: ['text', 'reasoning'], startup_timeout_seconds: 420,
    resource_fit: false, resource_warning: 'advisory',
  };
  assert.match(formatModelLabel(model), /27B · ctx 33K · メモリ要確認/);
  assert.match(formatModelProfile(model), /Runtime有効：text・reasoning/);
  assert.match(formatModelProfile(model), /未接続：vision/);
  assert.match(formatModelProfile(model), /420秒/);
});
