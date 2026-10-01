# Recorded conversation recall prototype

Status: draft implementation, 2026-10-01. This is a narrow C3 recall slice, not completion of C3 or the knowledge-lifecycle issues. Related work: Runtime #69 and #70; Karte #305 and #306.

## Implemented scope

- The explicit `recorded_conversation` chat source scope uses the existing privileged Go recording client and Karte Context Protocol v2. It does not route private records through v1, generic RAG, or a model.
- Recall performs signed v2 search and target-bound read, validates event identities, binds every separately serialized event and its header to the hash-verified canonical Markdown, selects current user assertions, and searches again to reject changed targets. Denied/failed reads do not fall back to cached snippets.
- A fixed-answer response echoes selected assertions with citations carrying document ID, revision, SHA-256, conversation, turn, and event revision. It repeats recall immediately before output and emits no answer when authorization or selected content changed. On the interaction path, authorized source events precede answer delivery, and the local adapter supplies explicit synthetic terminal framing to the existing generation assembler.
- Correction selection excludes superseded text and assistant output. Ambiguous branching correction lineages fail closed before query filtering.
- Results are bounded to three assertions and 6,000 UTF-8 text bytes. The query planner is a simple lexical stub; natural-language/Japanese recall quality is not established.

Karte remains the canonical, human-editable knowledge platform. No Karte code or protocol schema changes are included. Recording OFF and revoking access are different operations. This prototype does not add a public Runtime correction API, deletion API, tombstone lifecycle, daily summary job, or model integration. The fixture submits a signed correction through Karte's existing v2 operation.

## Source provenance

- Runtime base: `32ef71f495945ca5a577ecd8fe3251b49cf251f3`
- Karte reference: `d877f3bf34a7c39459f6817aee1221b37d6c51d8`
- Complete earlier source archive SHA-256: `fd299cd5074d861a050867a2f4c7fccbfa3e61bd69ba3fa23da45005d866c435`
- Corrected combined-test archive SHA-256: `ae5d72f7e3f53362ebb4b4fecdea39a2fc962bf9e4a5ffe42aa2dbd53e9d234a`

The earlier archive supplies the desktop adapter, frontend, and recording test. The corrected archive overrides `recall.go`, `recall_validate.go`, and `recallselect`, and supplies the real cross-process test. The earlier selector's branching-correction defect is not retained. Both supplied Karte reference files were byte-identical to the pinned Karte commit, so they are not copied into Runtime or proposed as Karte changes.

Publication preparation adds the explicit `karte_integration` build tag to the Linux cross-process test so ordinary unit tests do not require an external Karte executable. The complete frontend source's correct middle-dot character is retained rather than the tracked patch's corrupted rendering. No binaries, raw test logs, private paths, live credentials, or production conversation data are included.

The subsequent Codex P1 review identified missing interaction terminal framing, a missing interaction source-event emission, and unbound `Events` in otherwise hash-valid read responses. The source fixes and regressions address those three findings without changing Karte or weakening the generation assembler's checks. The new binding regression includes the unmodified synthetic read-response fixture from the pinned Karte commit; generated fixtures alone are not its compatibility evidence. Earlier archive binaries predate these fixes.

## Reproduce from source

Use the repository's documented Go 1.25 and frontend prerequisites. On Linux, build the reference Karte control binary from the pinned Karte checkout into a temporary directory outside either checkout. `KARTE_CHECKOUT` denotes that checkout; all credentials and records used by the tests are synthetic and temporary. `TMPDIR` must also resolve outside every Git checkout: the recording store intentionally rejects a private root with any Git ancestor. The usual system temporary directory is suitable; do not disable that guard.

```sh
node --test desktop/frontend/src/chatRouting.test.js desktop/frontend/src/chatSources.test.js
node --check desktop/frontend/src/main.js
python3 scripts/validate_repository.py --check-sensitive-patterns
cd desktop
go test ./recording/recallselect -count=1
go test ./recording -run '^TestSyntheticRecallRestartCorrectionAndRevocation$' -count=1
go test . -run '^TestFixedRecordedRecallCitationAndOutputAuthorization$' -count=1
```

For the opt-in Linux integration test, from the Runtime repository root:

```sh
CONTROL_DIR=$(mktemp -d)
(cd "$KARTE_CHECKOUT" && go build -o "$CONTROL_DIR/karte-ephy-control" ./cmd/karte-ephy-control)
(cd desktop && EPHY_KARTE_CONTROL_BIN="$CONTROL_DIR/karte-ephy-control" go test -tags karte_integration ./recording -run '^TestCombinedKarteRuntimeProcessRoundTrip$' -count=1 -timeout=50s -v)
```

The integration test starts its own bounded processes and uses `t.TempDir`. It performs save, writer-process exit, restarted recall, signed correction to document revision 2, corrected recall, and grant revocation. Its old assertion is 09:00; the current cited correction is 10:00. Ambiguous correction branches have a separate selector regression. An unset `EPHY_KARTE_CONTROL_BIN` is an error when this opt-in test is explicitly built.

## Verification and limits

Current publication-source checks passed: eight frontend unit tests, `main.js` syntax, and the repository validator with the sensitive-pattern scan on the staged changed sources and hash-verified required metadata.

Earlier independent Linux execution of the corrected archive's prebuilt binaries passed four selector tests and one native Runtime/Karte process round-trip test. The earlier complete archive separately passed the synthetic recording test, desktop output-authorization test (three cases), three native Karte tests, and eight frontend tests. Those runs did not independently rebuild binaries from this Git commit. Adding the integration build tag is a publication change after those binary runs.

A Go compiler was unavailable in the publication environment. Compilation and full tests for the exact published commit must be established by CI or a source rebuild. Targeted tests and prebuilt-binary results are not a full-suite pass.

The combined test establishes persisted synthetic state across graceful child-process exits on Linux tmpfs. It does not establish full desktop/Gateway/UI operation, live-model quality, source-card click-to-readback, SIGKILL recovery, disk power-loss durability, race/load behavior, Windows compilation of this exact source, or Windows queue durability. The pinned baseline has Darwin/Linux queue locking only; the separate uncommitted Windows durability experiment is not included. Policy/consent epochs remain limited by the existing recording client's initial grant behavior; a revoked or changed grant fails closed instead of silently re-enabling access.
