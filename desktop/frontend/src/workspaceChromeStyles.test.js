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

test('compact toolbar keeps project and mode selectors operable', () => {
  const selectorRule = compactWorkspaceCss.match(/#chat-project-select,\s*#chat-mode\s*\{([^}]+)\}/)?.[1] || '';
  const projectRule = compactWorkspaceCss.match(/#chat-project-select\s*\{([^}]+)\}/)?.[1] || '';
  const modeRule = [...compactWorkspaceCss.matchAll(/#chat-mode\s*\{([^}]+)\}/g)].at(-1)?.[1] || '';

  assert.match(selectorRule, /display:\s*block/);
  assert.match(selectorRule, /width:\s*100%/);
  assert.match(projectRule, /grid-row:\s*2/);
  assert.match(modeRule, /grid-row:\s*2/);
  assert.doesNotMatch(compactWorkspaceCss, /#chat-project-select,\s*#chat-mode\s*\{[^}]*display:\s*none/);
});
