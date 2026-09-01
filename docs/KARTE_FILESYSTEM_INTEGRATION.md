# Karte filesystem integration V1.1

## Boundary

Karte owns canonical Markdown under `KARTE_DATA_DIR/content`．Ephy's V1 integration reads only `content/**/*.md` and never routes canonical sources through the generic ingest copy into `LW_data`．Ephy writes only versioned proposal JSON below `.mdsys/ephy/outbox/pending` and reads receipts below `.mdsys/ephy/outbox/receipts`．The JSON bundle in `packages/karte_core` remains a compatibility bridge．

The versioned proposal／receipt schemas and synthetic fixtures are under `schemas/karte-ephy/v1`．Create and append are the only enabled proposal operations．Create contains a complete document candidate and no final path．Append contains only a frontmatter patch and Markdown fragment，and requires a stable `doc_id`，canonical path，and base hash．

Every proposal includes a path-safe project slug，kind，`YYYY-MM`，confidence，filename candidate，and one to three placement candidates．Supported kinds are `note`，`meeting`，`decision`，`plan`，`task`，`research`，`reference`，`report`，`person`，`organization`，and `journal`．Tags are independent retrieval metadata and never determine directory membership by themselves．

When classification is unresolved or a similar document may be the correct append target，Ephy sets `consultation_required` and asks the user before publication．`KarteOutbox.publish` rejects unresolved proposals．After consultation，Karte applies its project-first `content/projects/<project>/<kind>/<YYYY-MM>` policy and remains the final owner of placement．

Ephy recommends append only when a stable `doc_id` matches，the current document's project and kind agree with the placement hint，and the proposal was based on the current canonical byte hash．A project／kind mismatch or a semantic similarity match without exact `doc_id` requires consultation．With no exact or similar document，Ephy recommends create．Karte repeats the identity，content-classification，and byte-hash checks before saving．

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

## Conversation-to-Karte flow

Set `KARTE_DATA_DIR` before starting Ephy．After each completed Chat response，Ephy prepares a Karte card from the completed user／assistant conversation．The card shows the proposed title，complete create document or append-only fragment，project，kind，confidence，and similar canonical documents．A proposal with a missing project or ambiguous similar document remains local to the UI until the user resolves it．Nothing is written to canonical Karte content from Ephy．

The Gateway exposes the same reviewed flow through:

- `POST /v1/karte/conversations/plan` — build a deterministic，non-writing plan．
- `POST /v1/karte/conversations/publish` — re-plan，require all consultations to be resolved，and atomically write the V1.1 proposal to the pending outbox．
- `GET /v1/karte/proposals/<candidate_id>` — read pending／accepted／rejected／processed status and any receipt．

Conversation timestamps must include a timezone．Create proposals contain the complete proposed Markdown document．Append proposals contain only the document-end fragment and require a user-confirmed `doc_id` plus the current canonical SHA-256．The candidate identity includes the conversation，placement choice，tags，sensitivity，and operation so an identical retry is safe while any reviewed change receives a new identity．

## Native acceptance test

Build Ephy with `bash scripts/build_conversation_app.sh` and Karte with its `bash scripts/build_local_app.sh`．A compatible Wails CLI may instead package either app with `wails build`．Start Karte and Ephy with the same absolute data directory:

```bash
export KARTE_DATA_DIR=/absolute/path/to/karte_data
```

In Ephy Chat，select a project or leave it empty to exercise consultation，then complete a conversation．Review the automatically displayed Karte card and choose `Karteへ送る`．Within five seconds Karte's top-bar button changes to `Ephy候補 (1)`．Open it，review the full create preview or append diff，then use `採用`，`編集して採用`，or `破棄`．Back in Ephy，`Karteの処理結果を確認` reads the receipt．An accepted create must appear below `content/projects/<project>/<kind>/<YYYY-MM>` with a stable `doc_id`．

For a self-contained local Runtime，install an Apple Silicon `Karte.app` artifact with `bash scripts/install_karte_runtime.sh /absolute/path/to/Karte-macOS-apple-silicon.zip`．The bundle is copied to the Git-ignored `data/runtime/karte/Karte.app`．`scripts/start_conversation_app.sh` then starts that Karte bundle automatically and passes the same `KARTE_DATA_DIR` to Karte，Ephy Desktop，and Gateway．When `KARTE_DATA_DIR` is not set，the launcher creates `data/runtime/karte-data` for isolated acceptance testing．Set `EPHY_START_KARTE=0` to suppress automatic Karte startup，or `EPHY_KARTE_EXECUTABLE` to test an explicit non-symlink executable．
