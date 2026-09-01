const DEVELOPER_ONLY_TABS = new Set(['runtime', 'router', 'eval']);

export function isDeveloperOnlyTab(tab) {
  return DEVELOPER_ONLY_TABS.has(String(tab || ''));
}

export function resolveDeveloperTab(tab, enabled) {
  return !enabled && isDeveloperOnlyTab(tab) ? 'settings' : tab;
}

export function applyDeveloperModeVisibility(root, enabled) {
  const active = Boolean(enabled);
  if (root?.documentElement) {
    root.documentElement.dataset.developerMode = active ? 'true' : 'false';
  }
  root?.querySelectorAll?.('[data-developer-only]').forEach((element) => {
    element.hidden = !active;
    element.setAttribute('aria-hidden', String(!active));
  });
  return active;
}
