# Karte adapter remains a compatibility layer

## Status

Accepted

## Context

`packages/karte_core` imports and exports a simple JSON bundle and renders it to Markdown．It does not read Karte's canonical `KARTE_DATA_DIR/content` structure and does not implement an approved write path．

## Decision

Keep the JSON bundle adapter as a compatibility layer．The V1 formal boundary is a read-only filesystem adapter plus a reviewed outbox．Karte Markdown remains canonical，Ephy reads only `KARTE_DATA_DIR/content/**/*.md` without copying source documents into `LW_data`，and Ephy publishes versioned proposals atomically to `KARTE_DATA_DIR/.mdsys/ephy/outbox/pending`．Karte validates and presents those proposals for explicit human review．Only an accept or edit-and-accept action may call Karte's existing `SaveFile` path．Karte publishes an atomic receipt after processing，and Ephy reads the receipt without writing canonical content．

API，localhost server，and Wails IPC are not V1 integration boundaries．Wails `file-changed` remains an application-internal event．Deletion and forgetting proposals are excluded from V1．Tests use synthetic data only．The versioned schemas and cross-repository fixtures under `schemas/karte-ephy/v1` define the machine-readable contract．

## Consequences

The current bundle adapter is not described as completed Karte integration．Ephy cannot create，update，or delete canonical content．Interchange designs preserve stable `doc_id`，relative path，title，tags，updated time，canonical byte hash，source references，and sensitivity．A later boundary change requires an ADR and synchronized schema／fixture updates in both repositories．

## Alternatives considered

- Remove the JSON adapter now．Rejected because it remains useful for compatibility and testing．
- Write directly into canonical Karte content．Rejected because it bypasses review and an accepted integration contract．

## Related repositories

- `ephy-runtime`
- `karte`
- `karte-renderer`

## Date

2026-08-25
