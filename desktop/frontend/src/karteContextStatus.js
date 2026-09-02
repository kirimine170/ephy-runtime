export function formatKarteContextStatus(status = {}) {
  if (status.status !== 'ok') {
    return 'Karte Personal Context is unavailable. Continuing without saved context.';
  }
  const sourceCount = Number(status.source_count) || 0;
  const readCount = Number(status.read_count) || 0;
  const failedCount = Number(status.read_failed_count) || 0;
  const fallback = failedCount > 0 ? ` · ${failedCount} snippet fallback` : '';
  return `Karte Personal Context · ${readCount}/${sourceCount} documents read${fallback}`;
}
