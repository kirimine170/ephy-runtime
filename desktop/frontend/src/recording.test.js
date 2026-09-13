import test from 'node:test';
import assert from 'node:assert/strict';
import {recordingSummary, recordingMarkup} from './recording.js';

test('OFF and pending delivery remain separately visible', () => {
  assert.equal(recordingSummary({settings: {enabled: false}, local: 3, pending: 2, saved: 40, failed: 1}), '記録OFF · ローカル保全 3 · 配送待ち 2 · Karte保存済み 40 · 失敗 1');
});
test('recording UI has an explicit ON operation and current-policy readback', () => {
  const markup = recordingMarkup();
  assert.match(markup, /設定して記録ON/);
  assert.match(markup, /読戻しは現在の権限を毎回確認/);
  assert.match(markup, /receiptと本文のID・hash/);
});
