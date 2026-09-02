import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const appCss = readFileSync(new URL('./app.css', import.meta.url), 'utf8');
const compactWorkspaceCss = appCss.slice(appCss.lastIndexOf('@media (max-width: 900px)'));

test('compact sidebar is bounded and independently scrollable', () => {
  const sidebarRule = compactWorkspaceCss.match(/\.sidebar\s*\{([^}]+)\}/)?.[1] || '';
  assert.match(sidebarRule, /height:\s*100vh/);
  assert.match(sidebarRule, /box-sizing:\s*border-box/);
  assert.match(sidebarRule, /max-height:\s*100vh/);
  assert.match(sidebarRule, /overflow-y:\s*auto/);
});
