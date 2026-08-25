# Local worker CLI is not the remote worker node

## Status

Accepted

## Context

`apps/worker/cli.py` provides local commands for ingest，search，query，evaluation，Karte bundle conversion，smoke checks，and file watching．The Ephy ecosystem also defines `ephy-worker` as an independent-PC node for authorized remote jobs．Using the same word without a boundary risks treating local CLI implementation as a network service．

## Decision

The existing `apps/worker/cli.py` remains a local runtime CLI．The `ephy-worker` repository owns the future remote node protocol，authentication，authorization，job lifecycle，and isolation boundary．No large package rename is performed in this setup; naming cleanup is a later refactor．

## Consequences

Documentation and interfaces must state whether “worker” means the local CLI or the remote node．Remote execution cannot reuse the local CLI as a network boundary without an accepted protocol and security design．

## Alternatives considered

- Rename all local packages immediately．Rejected because it would create a large unrelated compatibility change．
- Implement remote execution inside this repository．Rejected because it would mix runtime and remote-node responsibilities．

## Related repositories

- `ephy-runtime`
- `ephy-worker`

## Date

2026-08-25
