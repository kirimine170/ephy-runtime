# Karte adapter remains a compatibility layer

## Status

Accepted

## Context

`packages/karte_core` imports and exports a simple JSON bundle and renders it to Markdown．It does not read Karte's canonical `KARTE_DATA_DIR/content` structure and does not implement an approved write path．

## Decision

Keep the JSON bundle adapter as a compatibility layer．For the first formal boundary，Karte Markdown remains canonical，Ephy treats `KARTE_DATA_DIR/content` as read-only，and Ephy output goes to staging or outbox for user review．Tests use synthetic data only．The choice among API，file watcher，and IPC remains undecided and requires a later ADR．

## Consequences

The current adapter is not described as completed Karte integration．No automatic write to actual Karte data is enabled．Interchange designs must preserve source path，project，tags，and updated time．

## Alternatives considered

- Remove the JSON adapter now．Rejected because it remains useful for compatibility and testing．
- Write directly into canonical Karte content．Rejected because it bypasses review and an accepted integration contract．

## Related repositories

- `ephy-runtime`
- `karte`
- `karte-renderer`

## Date

2026-08-25
