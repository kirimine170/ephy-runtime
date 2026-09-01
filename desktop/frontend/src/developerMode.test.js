import test from 'node:test';
import assert from 'node:assert/strict';
import {isDeveloperOnlyTab, resolveDeveloperTab} from './developerMode.js';

test('runtime routing and evaluation require Developer Mode', () => {
  assert.equal(isDeveloperOnlyTab('runtime'), true);
  assert.equal(isDeveloperOnlyTab('router'), true);
  assert.equal(isDeveloperOnlyTab('eval'), true);
  assert.equal(isDeveloperOnlyTab('chat'), false);
});

test('Developer Mode gate redirects advanced panels to settings', () => {
  assert.equal(resolveDeveloperTab('runtime', false), 'settings');
  assert.equal(resolveDeveloperTab('runtime', true), 'runtime');
  assert.equal(resolveDeveloperTab('chat', false), 'chat');
});
