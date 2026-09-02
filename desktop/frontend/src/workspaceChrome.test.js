import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createWorkspaceChromeController,
  resolveWorkspaceShortcut,
  resolveWorkspaceViewport,
  workspacePaneState,
  WORKSPACE_VIEWPORT,
} from './workspaceChrome.js';

class FakeClassList {
  constructor() { this.values = new Set(); }
  toggle(name, active) { active ? this.values.add(name) : this.values.delete(name); }
  contains(name) { return this.values.has(name); }
}

class FakeElement {
  constructor(id = '') {
    this.id = id;
    this.dataset = {};
    this.classList = new FakeClassList();
    this.attributes = new Map();
    this.listeners = new Map();
    this.inert = false;
    this.focused = false;
    this.textContent = '';
  }
  setAttribute(name, value) { this.attributes.set(name, value); }
  getAttribute(name) { return this.attributes.get(name); }
  addEventListener(name, listener) { this.listeners.set(name, listener); }
  dispatch(name, event = {}) { this.listeners.get(name)?.(event); }
  focus() { this.focused = true; }
}

function fakeWorkspace(width = 800) {
  const ids = Object.fromEntries([
    'workspace-sidebar', 'sidebar-toggle', 'sidebar-reveal', 'chat-sidebar-toggle',
    'chat-context-toggle', 'chat-sources-pane', 'chat-source-scope-select',
    'chat-context-close', 'chat-prompt',
  ].map((id) => [id, new FakeElement(id)]));
  const shell = new FakeElement('shell');
  const nav = new FakeElement('nav');
  const rootListeners = new Map();
  const root = {
    getElementById: (id) => ids[id],
    querySelector: (selector) => selector === '.shell' ? shell : selector === '.sidebar .nav-btn' ? nav : null,
    addEventListener: (name, listener) => rootListeners.set(name, listener),
  };
  const windowListeners = new Map();
  const windowObject = {
    innerWidth: width,
    addEventListener: (name, listener) => windowListeners.set(name, listener),
  };
  return {ids, shell, nav, root, rootListeners, windowObject, windowListeners};
}

test('workspace resolves desktop，split，and compact viewport boundaries', () => {
  assert.equal(resolveWorkspaceViewport(1440), WORKSPACE_VIEWPORT.threePane);
  assert.equal(resolveWorkspaceViewport(1200), WORKSPACE_VIEWPORT.split);
  assert.equal(resolveWorkspaceViewport(900), WORKSPACE_VIEWPORT.compact);
});

test('context is fixed on desktop and becomes an explicit drawer on narrow viewports', () => {
  assert.deepEqual(workspacePaneState({width: 1440, sidebarCollapsed: false}), {
    viewport: WORKSPACE_VIEWPORT.threePane,
    sidebarVisible: true,
    contextVisible: true,
    contextIsDrawer: false,
  });
  assert.deepEqual(workspacePaneState({width: 1024, contextPaneOpen: false}), {
    viewport: WORKSPACE_VIEWPORT.split,
    sidebarVisible: true,
    contextVisible: false,
    contextIsDrawer: true,
  });
  assert.equal(workspacePaneState({width: 720, sidebarCollapsed: true}).sidebarVisible, false);
  assert.equal(workspacePaneState({width: 720, contextPaneOpen: true}).contextVisible, true);
});

test('workspace shortcuts keep editable controls safe and expose Codex-like navigation', () => {
  assert.equal(resolveWorkspaceShortcut({key: 'b', metaKey: true}), 'sidebar');
  assert.equal(resolveWorkspaceShortcut({key: 'k', ctrlKey: true}), 'prompt');
  assert.equal(resolveWorkspaceShortcut({key: '.', metaKey: true}), 'context');
  assert.equal(resolveWorkspaceShortcut({key: 'Escape', target: {tagName: 'TEXTAREA'}}), 'dismiss');
  assert.equal(resolveWorkspaceShortcut({key: 'b', target: {tagName: 'INPUT'}}), '');
});

test('workspace controller binds compact drawers，focus，and aria state', () => {
  const workspace = fakeWorkspace(800);
  let showedChat = false;
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
    onShowChat: () => { showedChat = true; },
  });

  controller.initialize();
  assert.equal(controller.snapshot().sidebarCollapsed, true);
  assert.equal(workspace.ids['workspace-sidebar'].inert, true);
  assert.equal(workspace.ids['chat-context-toggle'].getAttribute('aria-expanded'), 'false');

  workspace.ids['chat-sidebar-toggle'].dispatch('click');
  assert.equal(controller.snapshot().sidebarCollapsed, false);
  assert.equal(workspace.nav.focused, true);
  workspace.ids['chat-context-toggle'].dispatch('click');
  assert.equal(controller.snapshot().contextPaneOpen, true);
  assert.equal(workspace.ids['chat-source-scope-select'].focused, true);

  const event = {key: 'k', metaKey: true, preventDefault() { this.prevented = true; }};
  workspace.rootListeners.get('keydown')(event);
  assert.equal(event.prevented, true);
  assert.equal(showedChat, true);
  assert.equal(workspace.ids['chat-prompt'].focused, true);
});

test('context control exposes the chat surface before opening a narrow drawer', () => {
  const workspace = fakeWorkspace(1024);
  let showChatCalls = 0;
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
    onShowChat: () => { showChatCalls += 1; },
  });

  controller.initialize();
  workspace.ids['chat-context-toggle'].dispatch('click');

  assert.equal(showChatCalls, 1);
  assert.equal(controller.snapshot().contextPaneOpen, true);
  assert.equal(workspace.ids['chat-source-scope-select'].focused, true);
});

test('compact drawers are mutually exclusive and Escape dismisses the visible sidebar', () => {
  const workspace = fakeWorkspace(800);
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
  });

  controller.initialize();
  controller.setSidebarCollapsed(false);
  controller.setContextPaneOpen(true);
  assert.equal(controller.snapshot().sidebarCollapsed, true);
  assert.equal(controller.snapshot().contextPaneOpen, true);

  controller.setSidebarCollapsed(false);
  assert.equal(controller.snapshot().contextPaneOpen, false);
  workspace.rootListeners.get('keydown')({key: 'Escape'});

  assert.equal(controller.snapshot().sidebarCollapsed, true);
  assert.equal(workspace.ids['chat-sidebar-toggle'].focused, true);
});

test('composer shortcut closes a compact drawer before focusing the prompt', () => {
  const workspace = fakeWorkspace(800);
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
  });

  controller.initialize();
  controller.setContextPaneOpen(true);
  workspace.rootListeners.get('keydown')({
    key: 'k',
    metaKey: true,
    preventDefault() {},
  });
  assert.equal(controller.snapshot().contextPaneOpen, false);
  assert.equal(controller.snapshot().sidebarCollapsed, true);
  assert.equal(workspace.ids['chat-prompt'].focused, true);

  workspace.ids['chat-prompt'].focused = false;
  controller.setSidebarCollapsed(false);
  workspace.rootListeners.get('keydown')({
    key: 'k',
    ctrlKey: true,
    preventDefault() {},
  });
  assert.equal(controller.snapshot().contextPaneOpen, false);
  assert.equal(controller.snapshot().sidebarCollapsed, true);
  assert.equal(workspace.ids['chat-prompt'].focused, true);
});

test('workspace restores an automatically collapsed sidebar after leaving compact mode', () => {
  const workspace = fakeWorkspace(1440);
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
  });

  controller.initialize();
  assert.equal(controller.snapshot().sidebarCollapsed, false);

  workspace.windowObject.innerWidth = 800;
  workspace.windowListeners.get('resize')();
  assert.equal(controller.snapshot().sidebarCollapsed, true);

  workspace.windowObject.innerWidth = 1024;
  workspace.windowListeners.get('resize')();
  assert.equal(controller.snapshot().sidebarCollapsed, false);
  assert.equal(workspace.ids['workspace-sidebar'].inert, false);
});

test('workspace preserves a deliberate sidebar choice across compact mode', () => {
  const workspace = fakeWorkspace(1440);
  const controller = createWorkspaceChromeController({
    root: workspace.root,
    windowObject: workspace.windowObject,
  });

  controller.initialize();
  controller.setSidebarCollapsed(true);
  workspace.windowObject.innerWidth = 800;
  workspace.windowListeners.get('resize')();
  workspace.windowObject.innerWidth = 1200;
  workspace.windowListeners.get('resize')();

  assert.equal(controller.snapshot().sidebarCollapsed, true);
});
