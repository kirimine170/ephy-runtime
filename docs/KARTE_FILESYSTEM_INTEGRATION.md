# Karte filesystem integration V1

## Boundary

Karte owns canonical Markdown under `KARTE_DATA_DIR/content`．Ephy's V1 integration reads only `content/**/*.md` and never routes canonical sources through the generic ingest copy into `LW_data`．Ephy writes only versioned proposal JSON below `.mdsys/ephy/outbox/pending` and reads receipts below `.mdsys/ephy/outbox/receipts`．The JSON bundle in `packages/karte_core` remains a compatibility bridge．

The versioned proposal／receipt schemas and synthetic fixtures are under `schemas/karte-ephy/v1`．Create and update are the only enabled proposal operations．

The `karte-contract` job in `.github/workflows/runtime-tests.yml` checks out public Karte `main` and compares every contract JSON byte-for-byte with `scripts/check_karte_contract.py`．A coordinated contract change must therefore land in Karte before the Ephy Runtime check can pass against the new version．The checker logs only relative filenames and hashes when drift occurs．

## Read-only indexing

Run one full scan with:

```bash
./scripts/run_cli.sh karte-index --data-dir /absolute/path/to/karte_data --project karte
```

The adapter requires valid YAML frontmatter with a stable `doc_id`．It stores `doc_id`，title，tags，relative path，updated time，and the SHA-256 of canonical file bytes as index metadata．Only the Markdown body is used as retrieval text．Invalid frontmatter，duplicate `doc_id` values，broken symlinks，and symlink escapes are returned as structured issues and are not indexed．

Run the incremental polling watcher with:

```bash
./scripts/run_cli.sh karte-watch \
  --data-dir /absolute/path/to/karte_data \
  --project karte \
  --interval 2 \
  --debounce 0.25
```

The watcher detects create，update，delete，and stable-`doc_id` rename events，reindexes only changed documents，keeps bounded event history，and exposes programmatic start，cancel，restart，health，and full-rescan recovery through `KarteWatchService`．

## Proposal and receipt flow

Validate and atomically publish a proposal with:

```bash
./scripts/run_cli.sh karte-propose proposal.json --data-dir /absolute/path/to/karte_data
```

Read final receipts with:

```bash
./scripts/run_cli.sh karte-receipts --data-dir /absolute/path/to/karte_data
```

Publication writes a same-directory temporary file，flushes it，and renames it to `<candidate_id>.json`．Retrying identical content is idempotent．Reusing a `candidate_id` with different content is rejected．Neither command reads from nor writes to canonical Markdown．
