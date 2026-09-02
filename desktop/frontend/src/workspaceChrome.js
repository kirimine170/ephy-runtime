export const WORKSPACE_VIEWPORT = Object.freeze({
  compact: 'compact',
  split: 'split',
  threePane: 'three-pane',
});

export function resolveWorkspaceViewport(width) {
  const numericWidth = Number(width);
  if (!Number.isFinite(numericWidth) || numericWidth <= 0) return WORKSPACE_VIEWPORT.threePane;
  if (numericWidth <= 900) return WORKSPACE_VIEWPORT.compact;
  if (numericWidth <= 1200) return WORKSPACE_VIEWPORT.split;
  return WORKSPACE_VIEWPORT.threePane;
}

export function workspacePaneState({width, sidebarCollapsed = false, contextPaneOpen = false} = {}) {
  const viewport = resolveWorkspaceViewport(width);
  return {
    viewport,
    sidebarVisible: viewport === WORKSPACE_VIEWPORT.compact ? !sidebarCollapsed : !sidebarCollapsed,
    contextVisible: viewport === WORKSPACE_VIEWPORT.threePane || Boolean(contextPaneOpen),
    contextIsDrawer: viewport !== WORKSPACE_VIEWPORT.threePane,
  };
}

function isEditableTarget(target) {
  const tagName = String(target?.tagName || '').toLowerCase();
  return Boolean(target?.isContentEditable) || ['input', 'textarea', 'select'].includes(tagName);
}

export function resolveWorkspaceShortcut(event = {}) {
  if (event.defaultPrevented) return '';
  if (event.key === 'Escape') return 'dismiss';
  if (isEditableTarget(event.target) && !(event.metaKey || event.ctrlKey)) return '';
  const modifier = Boolean(event.metaKey || event.ctrlKey);
  if (!modifier || event.altKey) return '';
  const key = String(event.key || '').toLowerCase();
  if (key === 'b' && !event.shiftKey) return 'sidebar';
  if (key === 'k' && !event.shiftKey) return 'prompt';
  if (key === '.' && !event.shiftKey) return 'context';
  return '';
}

export function createWorkspaceChromeController({
  root = globalThis.document,
  windowObject = globalThis.window,
  onShowChat = () => {},
  onDismissMenus = () => {},
} = {}) {
  let sidebarCollapsed = false;
  let sidebarCollapsedBeforeCompact = null;
  let contextPaneOpen = false;
  let viewport = resolveWorkspaceViewport(windowObject?.innerWidth);
  let bound = false;

  const element = (id) => root?.getElementById?.(id);
  const focusAfterInteraction = (target) => {
    if (!target) return;
    if (typeof windowObject?.requestAnimationFrame === 'function') {
      windowObject.requestAnimationFrame(() => target.focus());
    } else {
      target.focus();
    }
  };

  function apply() {
    const shell = root?.querySelector?.('.shell');
    const sidebar = element('workspace-sidebar');
    const sidebarToggle = element('sidebar-toggle');
    const sidebarReveal = element('sidebar-reveal');
    const toolbarToggle = element('chat-sidebar-toggle');
    const contextToggle = element('chat-context-toggle');
    const contextPane = element('chat-sources-pane');
    if (!shell || !sidebar || !sidebarToggle || !sidebarReveal || !toolbarToggle || !contextToggle || !contextPane) {
      return;
    }
    const state = workspacePaneState({
      width: windowObject?.innerWidth,
      sidebarCollapsed,
      contextPaneOpen,
    });
    viewport = state.viewport;
    shell.dataset.workspaceViewport = state.viewport;
    shell.classList.toggle('sidebar-collapsed', sidebarCollapsed);
    shell.classList.toggle('context-pane-open', state.contextVisible && state.contextIsDrawer);
    sidebarToggle.textContent = sidebarCollapsed ? 'Show' : 'Hide';
    sidebarToggle.setAttribute('aria-label', sidebarCollapsed ? 'Show sidebar' : 'Hide sidebar');
    toolbarToggle.setAttribute('aria-expanded', String(state.sidebarVisible));
    sidebar.setAttribute('aria-hidden', String(!state.sidebarVisible));
    sidebar.inert = !state.sidebarVisible;
    sidebarReveal.classList.toggle('hidden', !sidebarCollapsed || viewport === WORKSPACE_VIEWPORT.compact);
    contextToggle.setAttribute('aria-expanded', String(state.contextVisible));
    contextPane.setAttribute('aria-hidden', String(!state.contextVisible));
    contextPane.inert = !state.contextVisible;
  }

  function setSidebarCollapsed(collapsed, {focus = false} = {}) {
    sidebarCollapsed = Boolean(collapsed);
    if (viewport === WORKSPACE_VIEWPORT.compact && !sidebarCollapsed) {
      contextPaneOpen = false;
    }
    apply();
    if (focus && !sidebarCollapsed) focusAfterInteraction(root?.querySelector?.('.sidebar .nav-btn'));
  }

  function setContextPaneOpen(open, {focus = false} = {}) {
    contextPaneOpen = Boolean(open);
    if (viewport === WORKSPACE_VIEWPORT.compact && contextPaneOpen) {
      sidebarCollapsed = true;
    }
    apply();
    if (focus && contextPaneOpen) focusAfterInteraction(element('chat-source-scope-select'));
  }

  function toggleContextPane() {
    if (viewport === WORKSPACE_VIEWPORT.threePane) {
      onShowChat();
      focusAfterInteraction(element('chat-source-scope-select'));
      return;
    }
    const opening = !contextPaneOpen;
    if (opening) onShowChat();
    setContextPaneOpen(opening, {focus: opening});
  }

  function dismiss() {
    onDismissMenus();
    if (viewport !== WORKSPACE_VIEWPORT.threePane && contextPaneOpen) {
      setContextPaneOpen(false);
      element('chat-context-toggle')?.focus();
      return true;
    }
    if (viewport === WORKSPACE_VIEWPORT.compact && !sidebarCollapsed) {
      setSidebarCollapsed(true);
      element('chat-sidebar-toggle')?.focus();
      return true;
    }
    return false;
  }

  function handleResize() {
    const previousViewport = viewport;
    const nextViewport = resolveWorkspaceViewport(windowObject?.innerWidth);
    if (nextViewport === WORKSPACE_VIEWPORT.compact && previousViewport !== WORKSPACE_VIEWPORT.compact) {
      sidebarCollapsedBeforeCompact = sidebarCollapsed;
      sidebarCollapsed = true;
    } else if (previousViewport === WORKSPACE_VIEWPORT.compact && nextViewport !== WORKSPACE_VIEWPORT.compact) {
      if (sidebarCollapsedBeforeCompact !== null) {
        sidebarCollapsed = sidebarCollapsedBeforeCompact;
      }
      sidebarCollapsedBeforeCompact = null;
    }
    if (nextViewport === WORKSPACE_VIEWPORT.threePane) contextPaneOpen = false;
    apply();
  }

  function handleShortcut(event) {
    const action = resolveWorkspaceShortcut(event);
    if (!action) return;
    if (action === 'dismiss') {
      dismiss();
      return;
    }
    event.preventDefault();
    if (action === 'sidebar') {
      setSidebarCollapsed(!sidebarCollapsed, {focus: sidebarCollapsed});
    } else if (action === 'prompt') {
      onShowChat();
      element('chat-prompt')?.focus();
    } else if (action === 'context') {
      toggleContextPane();
    }
  }

  function bind() {
    if (bound) return;
    bound = true;
    element('chat-sidebar-toggle')?.addEventListener('click', () => {
      setSidebarCollapsed(!sidebarCollapsed, {focus: sidebarCollapsed});
    });
    element('sidebar-toggle')?.addEventListener('click', () => setSidebarCollapsed(!sidebarCollapsed));
    element('sidebar-reveal')?.addEventListener('click', () => setSidebarCollapsed(false, {focus: true}));
    element('chat-context-toggle')?.addEventListener('click', toggleContextPane);
    element('chat-context-close')?.addEventListener('click', () => {
      setContextPaneOpen(false);
      element('chat-context-toggle')?.focus();
    });
    element('workspace-sidebar')?.addEventListener('click', (event) => {
      if (!event.target?.closest?.('.nav-btn')) return;
      if (viewport !== WORKSPACE_VIEWPORT.threePane && contextPaneOpen) contextPaneOpen = false;
      if (viewport === WORKSPACE_VIEWPORT.compact) sidebarCollapsed = true;
      apply();
    });
    root?.addEventListener?.('keydown', handleShortcut);
    windowObject?.addEventListener?.('resize', handleResize);
  }

  function initialize() {
    viewport = resolveWorkspaceViewport(windowObject?.innerWidth);
    sidebarCollapsed = viewport === WORKSPACE_VIEWPORT.compact;
    sidebarCollapsedBeforeCompact = viewport === WORKSPACE_VIEWPORT.compact ? false : null;
    contextPaneOpen = false;
    bind();
    apply();
  }

  return {
    initialize,
    setSidebarCollapsed,
    setContextPaneOpen,
    snapshot: () => ({sidebarCollapsed, contextPaneOpen, viewport}),
  };
}
