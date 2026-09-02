# Karte filesystem integration V1.1

## Boundary

Karte owns canonical Markdown under `KARTE_DATA_DIR/content`．Ephy does not treat canonical paths as durable identity．The formal Personal Context read path is Karte Context Protocol V1 under `.mdsys/context/v1`，while the older direct `content/**/*.md` index remains a migration fallback．Ephy writes reviewed proposal JSON below `.mdsys/ephy/outbox/pending` and reads receipts below `.mdsys/ephy/outbox/receipts`．The JSON bundle in `packages/karte_core` remains a compatibility bridge．

The versioned proposal／receipt schemas and synthetic fixtures are under `schemas/karte-ephy/v1`．Create and append are the only enabled proposal operations．Create contains a complete document candidate and no final path．Append contains only a frontmatter patch and Markdown fragment，and requires a stable `doc_id`，canonical path，and base hash．

Every proposal includes a path-safe project slug，kind，`YYYY-MM`，confidence，filename candidate，and one to three placement candidates．Supported kinds are `note`，`meeting`，`decision`，`plan`，`task`，`research`，`reference`，`report`，`person`，`organization`，and `journal`．Tags are independent retrieval metadata and never determine directory membership by themselves．

When classification is unresolved or a similar document may be the correct append target，Ephy sets `consultation_required` and asks the user before publication．`KarteOutbox.publish` rejects unresolved proposals．After consultation，Karte applies its project-first `content/projects/<project>/<kind>/<YYYY-MM>` policy and remains the final owner of placement．

Ephy recommends append only when a stable `doc_id` matches，the current document's project and kind agree with the placement hint，and the proposal was based on the current canonical byte hash returned by Karte Context Protocol．A project／kind mismatch or a semantic similarity match without exact `doc_id` requires consultation．With no exact or similar document after a successful scoped search，Ephy recommends create．Karte repeats the identity，content-classification，and byte-hash checks before saving．

The `karte-contract` job in `.github/workflows/runtime-tests.yml` checks out public Karte `main` and compares every contract JSON byte-for-byte with `scripts/check_karte_contract.py`．The mirrored Context contract includes request／response，policy，and metadata-only audit schemas．A coordinated contract change must therefore land in Karte before the Ephy Runtime check can pass against the new version．The checker logs only relative filenames and hashes when drift occurs．

## Personal Context search／read

Karte owns search，read，scope policy，sensitivity filtering，canonical `doc_id`，content hash，and provenance．Ephy publishes atomic requests below `.mdsys/context/v1/requests` and reads matching responses below `.mdsys/context/v1/responses`．Karte archives processed requests below `.mdsys/context/v1/processed`．The request and response schemas plus synthetic fixtures live in `schemas/karte-context/v1` and must remain byte-identical across both repositories．

The Gateway exposes the typed client through:

- `POST /v1/karte/context/search` — search within the actor's effective project，tag，and sensitivity scope．
- `POST /v1/karte/context/read` — read one allowed document by stable `doc_id`．
- `POST /v1/chat/completions` with `metadata.source_scope=personal_context` — ground a normal Ephy conversation with Karte results．

For a Personal Context conversation，Ephy searches first and selects at most the top three ranked results．It then reads each selected document by stable `doc_id` with the exact same project，tag，and sensitivity scope．The prompt receives at most 6,000 characters from one document and 12,000 characters in total，centered around the search hit when possible．The UI source card retains only the bounded search snippet and metadata; canonical body text is not copied into the chat response．`karte_context_status` distinguishes `searched_count`，selected `source_count`，successful `read_count`，and per-document `read_failed_count`．A failed individual read falls back only to the snippet already disclosed by the scoped search，while an invalid or out-of-scope protocol response makes the entire Personal Context request unavailable．

Karte Personal Context is always injected as `local_untrusted` reference data．Karte timeout，invalid response，or app shutdown does not stop the conversation．The response instead reports `karte_context_status=unavailable` and continues without saved context．The default Ephy actor policy is capped at `internal`; confidential and restricted data remain denied until an explicit local `.mdsys/context/v1/policy.json` grants a narrower actor scope．

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

The conversation planner does not scan canonical Markdown directly．For `resolution=auto`，it searches the selected project through Karte Context Protocol and reads at most the top three `doc_id` candidates before recommending create or consultation．For explicit `resolution=append`，it reads the selected `doc_id` again and uses the current Karte-owned path，project，kind，and SHA-256 in the append proposal．The proposal records that `doc_id` and hash as `karte-context` provenance．An explicit `resolution=create` is a human override and does not require another similarity search．A missing project never triggers a cross-project search．

The Karte card exposes `context_status` as `ok`，`partial`，`unavailable`，or `not_required` with searched，read，and failed-read counts．A partial or unavailable check cannot silently become a publishable create; Ephy asks the user to retry or explicitly choose create／append．Protocol，scope，or `doc_id` validation failures never fall back to direct filesystem access．

Every displayed plan also carries `plan_sha256` over the exact proposal．Publish requires that value as `reviewed_plan_sha256` and recomputes the plan against the current Karte context．If a selected document，base hash，placement，summary，or other proposal field changed after review，Ephy rejects publication，shows the new plan，and requires another review．This prevents a publish-time re-plan from silently substituting content the user did not approve．

For the native permission boundary，run `.venv/bin/python scripts/karte_context_permission_retry_uat.py --report /private/tmp/karte-permission-retry.json` with the bundled `Karte.app` installed．The runner creates an isolated temporary Karte workspace，verifies that default Ephy policy returns a non-disclosing `denied` response for a synthetic Restricted document，atomically grants the test actor Restricted access，and retries through a fresh Context request until the same `doc_id` is read successfully．It never changes the user's normal Karte workspace，and its optional report contains metadata and hashes only．

The Gateway exposes the same reviewed flow through:

- `POST /v1/karte/conversations/plan` — build a deterministic，non-writing plan．
- `POST /v1/karte/conversations/publish` — re-plan，require all consultations to be resolved，and atomically write the V1.1 proposal to the pending outbox．
- `GET /v1/karte/proposals/<candidate_id>` — read pending／accepted／rejected／processed status and any receipt．

Conversation timestamps must include a timezone．Create proposals contain the complete proposed Markdown document．Append proposals contain only the document-end fragment and require a user-confirmed `doc_id` plus the current canonical SHA-256．The candidate identity includes the conversation，placement choice，tags，sensitivity，and operation so an identical retry is safe while any reviewed change receives a new identity．

## Native acceptance test

Append，同名別`doc_id` collision，rejectを一括で再現する場合は，Ephy repositoryとKarte repositoryを同じ親directoryに置き，次を実行します．実Karte bundleがContext Protocolを処理し，Ephyが3件のreview済みproposalを作成します．Karteの`ListEphyProposals`／`AcceptEphyProposal`／`RejectEphyProposal`でreviewを処理した後，Ephyがreceiptを再読し，採用した2文書をPersonal Contextから`doc_id`でread-backします．作業用data rootとreportにはsynthetic dataだけを使います．

```bash
.venv/bin/python scripts/karte_conversation_mutation_uat.py \
  --report /tmp/ephy-karte-conversation-mutation-uat.json
```

UATは既存append先のidentityと本文を維持し，同名createでは新しい`doc_id`先頭8文字をfilenameへ付け，rejectではcanonical Markdownを作らないことを検証します．Karte repositoryから署名済みartifactに埋め込まれたrevisionのclean cloneを一時利用するため，利用者のuntracked fileや作業中のcheckoutを変更しません．

Build Ephy with `bash scripts/build_conversation_app.sh` and Karte with its `bash scripts/build_local_app.sh`．A compatible Wails CLI may instead package either app with `wails build`．Start Karte and Ephy with the same absolute data directory:

```bash
export KARTE_DATA_DIR=/absolute/path/to/karte_data
```

In Ephy Chat，choose `Karte Personal Context` as Source Scope and select a project，then ask about an existing canonical document．The Sources pane must label the result `KARTE`．Next，complete a conversation，review the automatically displayed Karte card，and choose `Karteへ送る`．Within five seconds Karte's top-bar button changes to `Ephy候補 (1)`．Open it，review the full create preview or append diff，then use `採用`，`編集して採用`，or `破棄`．Back in Ephy，`Karteの処理結果を確認` reads the receipt．An accepted create must appear below `content/projects/<project>/<kind>/<YYYY-MM>` with a stable `doc_id`．A subsequent Personal Context search must return that `doc_id` without requiring generic ingest．

For a self-contained local Runtime，install an Apple Silicon `Karte.app` artifact with `bash scripts/install_karte_runtime.sh /absolute/path/to/Karte-macOS-apple-silicon.zip`．The bundle is copied to the Git-ignored `data/runtime/karte/Karte.app`．`scripts/start_conversation_app.sh` then starts that Karte bundle automatically and passes the same `KARTE_DATA_DIR` to Karte，Ephy Desktop，and Gateway．When `KARTE_DATA_DIR` is not set，the launcher creates `data/runtime/karte-data` for isolated acceptance testing．It also atomically persists the absolute path in `data/runtime/karte/.karte-data-dir`．The bundled Karte reads that pointer when a later Dock／Finder restart has no inherited environment，so accepted Markdown remains visible in the same workspace．

Installed macOS artifacts must contain the signed `Contents/Resources/karte-build-provenance.json` generated by Karte packaging．The mutation UAT verifies the app code seal，reads its exact source revision，and checks out that same revision for the review bridge before accepting or rejecting any proposal．Reject review additionally snapshots every canonical Markdown path and SHA-256 immediately before and after the action，and fails if the tree changes．

Karte publishes its current process ID below the selected root at `.mdsys/runtime/karte.pid`．The launcher reuses a process only when that data-root-local marker and the executable identity both match．A stale PID file or a Karte instance opened on another root is never silently treated as the shared Personal Context provider．Set `EPHY_START_KARTE=0` to suppress automatic Karte startup，or `EPHY_KARTE_EXECUTABLE` to test an explicit non-symlink executable．
