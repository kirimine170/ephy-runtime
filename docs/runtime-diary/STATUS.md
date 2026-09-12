# Runtime会話・日記 C1〜C3 状況

最終更新：2026-09-12．**Step 0完了，Step 1未着手**．本体の機能，稼働schema，model／voice，private設定，実行中appは変更していない．

次の担当は[IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)，[Runtime ADR-0014](../adr/ADR-0014-runtime-conversation-diary-boundaries.md)，[Karte保存契約 v2](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)を読み，ユーザーが指定したStepだけを進める．全Stepを一括実行するgoalは設定しない．

## 1．照合した版と作業状態

両repoで`git fetch origin`を実施し，remote main，local main，HEADを照合した．pull／reset／cleanはしていない．

| 対象 | 照合結果 |
|---|---|
| Runtime source | `main = origin/main = e884bb86d58f09d281bef1d1b81c80bde74e25c4`．開始時clean．C0.4 PR #78まで統合済み |
| Karte source | `main = origin/main = 89db98b87d6af1ee95d8cecbf5be576dc343075c`．PR #295まで統合済み．開始時から未追跡4ファイルあり |
| Runtimeの実行bundle | 現行workspace内の`desktop/build/bin/ephy-runtime.app`が実行中であることをPID／executableで確認．Go 1.25.3，darwin/arm64．Go build infoにvcs revisionがないためHEADとの厳密な対応は未確認 |
| Runtime実行ファイルhash | `f661bba190b1b008530d8dcb28746e274f5eb3a1666fdb4fee8086ec8ccecb2a`．実行path上のfile hashであり，過去受入のsource SHAの証明ではない |
| 同梱Karte | bundleの`karte-build-provenance.json`はsource revision `ee5ae260275fae056270839e6354d0e39260480b`．このcommitと現行mainのtree差分はゼロ．同bundleの実行を確認 |
| Karte実行ファイルhash | `58f081a7aeb78faaf2b1c8e7f17df93ca4daba435c8df373a8f7756c70b6ba63` |
| 旧Karte checkout | `0e675d2`のdetached checkoutが別にあり，未追跡data／tool関連物あり．今回のsource基準にはせず保全した |
| 共通契約 | mutation schema 1.1，Context protocol 1.0．両repoの15 JSONがbyte-for-byte一致 |

実行中PIDの照合はread-onlyで行った．Step 0ではappの停止・再起動・再build・再署名を行っていない．新機能の実機受入へ進む際は対応するsource SHA／build hashを改めて固定する．公開可能な本書には実データroot，private設定path，実会話，音声，secretを記録しない．

### 未統合変更の扱い

| 変更 | 判定と後続への注意 |
|---|---|
| Runtime C0.1 stabilization worktree | detached `0b334c1`に11ファイルの未commit差分．readiness，WAV長検査，再生deadline，sequence／queueとtests・bindingsの試行が残る．後のPR #62，#63，#65，#66で同じ領域が更新されており，現行mainとの単純な同一性はない．旧試行をそのまま適用せず保全．Step 1では現行readiness／playback実装を起点にする |
| `feat/streaming-asr-c02`，`fix/c0-3-1-voice-hardening` | `git cherry origin/main`で対象差分はpatch-equivalent．再実装不要 |
| `feat/c04-filler-controller` | cherryでは未統合に見える5commitがあるが，tip `188ff75`とPR #74 merge `da3d5ee`のtreeは一致．squash統合済みとして扱う |
| `fix/irodori-c032-review` | tip `c1a4292`とPR #67 merge `38a2546`のtreeは一致．squash統合済み |
| Runtime open PR #7，#8，#9，#12 | benchmark，reranker，Wails E2E，SearXNG．現行C1〜C3本体の未統合実装ではない．今回mergeしない |
| Karte未追跡4ファイル | `internal/ephyoutbox/sync_directory_nonwindows 2.go`，`sync_directory_windows 2.go`，`scripts/build_local_app 2.sh`，`uniform_type_identifiers_darwin 2.go`．各対応元とbyte一致．削除せず，Go baselineはcleanな隔離worktreeで検証した |
| Karte PR #268 | `fc05c405085eed5ca566f630e78945db51575a79`．SaveFileのatomic replacement，非破壊競合処理，background job等を含む未統合stack．PRはnative ASR開始順のCI raceが未解決との記載あり．C2に必要なwriter部分の再照合をStep 3の前提とし，stack全体の統合を暗黙要求しない |
| Karte PR #271→#270→#268→#269→#277 | Board，ASR，backend，frontend，toolingのstack．scopeとbase順を保つ．今回のpolicy・日記完成済みとは扱わない |

他の古いlocal branchは名称やahead数だけで未実装と判定せず，今回の関係箇所とopen PRへ絞って照合した．全branch全履歴の監査はStep 0の成果に含めない．

## 2．現行実装と差分

| 領域 | 確認した実装 | C1〜C3で必要な差分 |
|---|---|---|
| Interaction／ASR | `desktop/interaction.go`，`interaction_asr.go`，`voice_asr_session.go`．operation／session／turn／segment／revision検査，PCM上限，partial UI，End／Finishのfinal一致，1回のacceptTranscript | ASR contextが回答operationの子である現状を変更．継続sessionとcapture所有，自動endpoint，再待機 |
| cancel／SpeechUnits | `interaction.go`，`generation_assembler.go`，`generation_types.go`，Frontend `voiceInteraction.js`．generation completion，確定本文，音声sequence，late callback遮断 | captureと出力取消を分離．本文割込みのpre-roll，次のASR保全，中断と再生範囲の履歴契約 |
| C0.4 | `fillerBargeIn.js`はRMS 0.02／32 msの活動検知だけ．`prepareFiller`の条件下で開き，`pump`の本文開始前にmonitorを停止．ASR／転写なし．割込み時は旧run cancelと有限ACK | 本文再生中は検出・内容引継ぎ未実装．filler有効化に依存しない単一captureへ統合し，発話中ACKを重ねない |
| Chat history | `main.js`のonTranscript→beginStreamingChat，onOutput→confirmVoiceEntry，finalizeVoiceChat→settleVoiceEntry．失敗・中断assistantは通常履歴から除外 | 生成・表示・再生の差分と中断を安全に次turnへ渡す．UI配列の上限を永続記録へ使わない |
| 現在の履歴上限 | UIは30 entries，`conversationHistory.js`は28 messages／48 KiB，Karte候補も最後30 messages | 保存はevent streamで全対象turnを保全．context budgetと保存範囲を分離 |
| candidate／publish | `packages/karte_core/conversation.py`の候補IDは会話ID・日時・messages・分類等のdigest．createは受信したbounded会話ログを含み，appendは最後のuser／assistantを中心とした断片．plan SHA照合後にpendingへpublish | 安定event ID・revision，final受理時保全，全文再送と最終turnだけ保存の防止，自動policy採用 |
| receipt／再起動 | `outbox.py`のatomic publishと既知candidate IDのreceipt再取得．Karteの正本とpending／receiptはdisk上に残る | Runtimeで全未配送eventと状態を復元するdurable queueはない．receipt既存時もpayload hashを照合するv2が必要 |
| Karte read／policy | `internal/contextcore`，`app_context_policy.go`．doc_id＋canonical hash，project/tag/sensitivity/provenance/capabilityの判定，denied非開示，metadata-only audit | current privacyを維持しつつexact actor ID，記録scope，source revision，派生失効，自動採用来歴を追加 |
| Karte transaction | `AcceptEphyProposal`，`finishSavedEphyTransaction`にprepared／savedからのreceipt復旧あり | 現行はhuman acceptを入口に復旧．v2は起動時自動復旧，event ledger，最終policy/CAS，receipt後のID再利用防止 |
| Karte canonical writer | `app.go:SaveFile`はVCS競合検査前に一時的な本文writeを行い，最終保存も`os.WriteFile` | transaction fileのatomic性だけでは正本writeの安全性を保証できない．Step 3で#231／#268を再利用・限定修正しfault testで確認 |
| kind／tag／互換 | `journal`を含む11 kinds，独立tags，custom frontmatter保持．JSON schema／decoderはunknown fieldを拒否 | kindの全面追加を避けてrecord_typeを新v2で定義．v1内に新権限・版を混ぜない |

### 実request生成のthinkingとローカルroute

`load_app_config()`の実local selectionと`LlamaCppChatAdapter._build_payload()`にsynthetic音声requestを渡し，送信直前のpayload生成を確認した．実会話本文やmodel内部reasoningを取得・記録していない．これは稼働中Gatewayの全request履歴の監査ではない．

| route | 実選択／loopbackのmodel ID | 生成するrequest制御 |
|---|---|---|
| Fast／voice | `qwen3-8b`，port 8081も一致 | `enable_thinking=false`，`reasoning_format=deepseek` |
| Work／voice | `qwen3.8-27b`，port 8082も一致 | `enable_thinking=true`，`preserve_thinking=true`，`reasoning_effort=medium` |
| Code | `qwen3-coder-30b-a3b`，port 8083で確認 | 今回のC1初期受入routeにはしない |
| embedding | `qwen3-embedding-0.6b`，port 8090で確認 | 既存構成を維持 |

`configs/models.yaml`のWork既定`qwen3-30b-a3b`だけを見ると実選択を誤るため，model registry overrideを含めて確認した．Fast non-thinkingを新しい変更として扱わず，Work thinkingを維持した音声一周をStep 2の実機gateに残す．

## 3．既存の受入結果との照合

| 根拠 | 確認できたこと | 今回の扱い |
|---|---|---|
| C0.2／C0.3の既存recovery報告 | 当時はASR TCC／latency・実マイク受入に未確認があった | 古い未確認をそのまま現在の故障と扱わない．後続PR #66と照合 |
| [Runtime PR #66](https://github.com/kirimine170/ephy-runtime/pull/66)，`a7dbdde` | helper/app bundle署名・TCC修正と，実マイクのpartial→single final→generation→playback metadata完了の記録 | 1音声turnの既存証拠．連続session・本文barge-in・thinking Work受入へ拡張しない |
| [Runtime PR #53](https://github.com/kirimine170/ephy-runtime/pull/53)，Karte #292／[#293](https://github.com/kirimine170/Karte/pull/293) | 実bundle UATでappendのdoc ID/path維持，同名collision，rejectの非write，receipt SHAとContext read-back一致．後続でbuild provenanceとcanonical tree検査を補強 | v1 human reviewの基盤証拠．policy自動採用・Runtime crash queue・日記の受入ではない |
| [Runtime PR #74](https://github.com/kirimine170/ephy-runtime/pull/74) | Python 599 passed／3 skipped，Frontend 198，Go／race／build等の記録．有限fillerとcallback計測 | 実LLM分布，実マイク割込み，実speaker停止p95等はPRで未合格として分離 |
| [Runtime PR #78](https://github.com/kirimine170/ephy-runtime/pull/78) | Frontend 206，Go，Python filler 4，validator／build．ブラウザ開始301 ms／自然終了2400 msの記録 | 新候補の聴感・個別承認とlive有効化はPRの時点で未完了．本照合では後続の人間受入を確認できていない |
| 2026-09-11更新の両管理表 | selective read，候補auto-submit，create会話ログは完了．Karte T-112〜117は未着手，T-103／T-107は進行中 | 上記限定UATと矛盾する一括完了更新をしない．新Issueを増やさず計画へ対応付け |

上記は既存成果の参照であり，今回同じ実機試験を再実行した結果ではない．C1〜C3の実装・自動検証・実機受入はすべて後続Stepのgateである．

## 4．Step 0で確定した契約

- capture／session／ASR発話区間／回答operationのownerと取消範囲．endpoint＋finalの一度だけの採用とfinite PCM上限．
- 会話ID／turn ID／event ID／訂正revisionとcanonical doc ID／revision／hashの分離．生成・表示・再生・中断の区別．
- 原記録，summary，Ephy diaryを人間可読Markdownで分離し，source ID・版・turnを結ぶ．
- 記録ON/OFF，設定済み非公開scope，未配送と保存済み，保持上限，訂正，削除を区別する．
- Karteが所有するv2限定policy，自動採用主体，credential，policy版／consent epochとhuman review互換．
- 正本保存・transaction・receipt・再送の各停止境界と，ID再利用・human edit・取消の処理．
- Runtime内のdurable Job，入力版固定，日付／timezone，取消，会話優先，冪等性，次回起動時再開．
- Stepごとのfixture・受入・両repo統合順，既存データ移行とrollback．

数値の初期値は計画・Karte契約へ集約した．日記はtimezoneに従う日付変更後のアイドル時，未稼働なら次回起動で生成する．raw audioは保持しない．外部知識検証，多Worker，自由探索，Worker本体は範囲外である．

## 5．今回実行した検証

実行基準は上記mainのsourceである．docs-onlyのStep 0に必要な対象回帰を行い，実音声の収録・LLM生成・既存private canonical writeは行っていない．KarteのGo検証は未追跡重複を含まない同一HEADの一時worktreeを使用した．

| コマンド／確認 | 結果 |
|---|---|
| 両repo `git fetch origin`／`git status`／`git worktree list`／`git branch -vv`，対象`git cherry`／tree diff，`gh pr list/view` | main一致，既存変更とsquash統合を照合 |
| `python3 scripts/check_karte_contract.py --karte-root ../karte` | 15 JSON一致 |
| `.venv/bin/python -m pytest -q tests/test_adapter.py tests/test_generation_stream.py tests/test_karte_contract_sync.py tests/test_karte_conversation.py tests/test_karte_conversation_api.py tests/test_karte_context.py` | **55 passed**．既存Starlette／httpx deprecation warning 1件 |
| Frontend `node --test src/conversationHistory.test.js src/voiceConversation.test.js src/voiceInteraction.test.js src/fillerBargeIn.test.js src/fillerController.test.js src/fillerBackchannel.test.js src/karteConversation.test.js` | **99 passed，0 failed，0 skipped** |
| Runtime `GOCACHE=<temporary-cache> go test . -run 'Test(Interaction\|ASR\|Voice\|App.*Interaction)' -count=1`，`desktop`で実行 | **PASS**．対象自動testの合格であり，opt-in実機testの実施証明ではない |
| Karte `GOCACHE=<temporary-cache> go test ./internal/contextcore ./internal/ephyoutbox` | **両package PASS**．現行policy／scope／transaction spool基盤の回帰 |
| `python3 scripts/validate_repository.py` | **PASS** |
| 両repo `git diff --check`，追加文書の相対link・参照file・句読点・公開情報検査 | **PASS**．相対linkの欠落なし．synthetic JSON例のparseと記載SHA-256の再計算も一致 |

pytest初回はcase表記`Tests/`によるcollection errorでtest未実行だったため，Gitの正規path`tests/`へ修正して上記55件を完了した．新しい機能の修正往復は0回であり，本体実装はしていない．全Python／Go suite，frontend build，app build，native cross-app UATはdocs-only範囲のため今回は再実行していない．

## 6．成果，承認，未確認，次の一手

変更したのは次の文書だけである．

- Runtime：本STATUS，IMPLEMENTATION_PLAN，ADR-0014，ADR index．
- Karte：`architecture/KARTE_RUNTIME_DIARY_V2.md`，ADR-0005，ADR index．

新しい実装commit／push／PR／merge／release，Issue・Sheets更新は今回行っていない．成果は両repoのlocal working treeにある．既存の明示承認を失効させたり，再承認条件を追加したりしない．過去C0.3同期報告にはcommit／push／PR／merge承認の使用記録があるが，本Stepの目的は設計文書であり，未指定の公開・releaseへ範囲を拡げていない．文書編集・read-only照合・必要な隔離worktreeとtestは今回依頼の範囲で実施した．

指定資料はAstra／highを基本としている．このtaskの選択モデル／推論設定をUI等から検証できるmetadataは未取得であり，プロンプト記載だけで切替済みとは扱わない．モデル選択は変更していない．Step単位の入力／出力token・再試行込み使用量は取得できず未計測である．account全体の使用率をこのStepの消費量として代用しない．

設計を止める未決事項はない．残る未確認は次のとおりである．

1．指示書が言及する`Ephy Reference Architecture v0.1`のPDF本体は今回の添付に含まれず，対象workspaceとDriveの限定検索でも同定できなかった．既存ADRと管理表を根拠に今回の会話範囲を確定した．原資料入手時は命名・責務との整合だけを追加照合する．
2．Runtime実行binaryのsource revisionを厳密にpinできていない．Step 1実機受入前にbuild SHA／hashを記録する．
3．C1の連続発話，本文割込み冒頭，Work thinking音声一周，C2の自動採用・crash復旧，C3の日記自然さ・翌日想起は未実施である．既存単turn・合成結果を合格に流用しない．
4．Karte #268の未統合writer改善をStep 3開始時に再確認する．既存の重複ファイルと古いRuntime worktreeは保全したままである．

**次の一手は，ユーザーがStep 1を指定した後の，Runtimeの継続音声session・単一capture・endpoint・自動再待機とテストである．Step 0で停止する．**
