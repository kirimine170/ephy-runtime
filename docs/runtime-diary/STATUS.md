# Runtime会話・日記 C1〜C3 状況

最終更新：2026-09-12．**C1 Step 2の本文割込み・発話冒頭引継ぎを実装し，自動検証した**．先行Step 3のKarte基盤は維持した．Step 1・2の人による実機受入は未確認，Step 4以降は未着手である．実設定・実データへの記録は有効化していない．

次の担当は[IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)，[Runtime ADR-0014](../adr/ADR-0014-runtime-conversation-diary-boundaries.md)，[Karte保存契約 v2](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)を読み，ユーザーが指定したStepだけを進める．全Stepを一括実行するgoalは設定しない．

## Step 2／C1 本文割込みと発話冒頭引継ぎ

| 区分 | 状態 |
|---|---|
| 実装済み | 単一captureによるLLM待機・フィラー・TTS待機・本文の割込み，500 ms pre-roll，有限の入力引継ぎ，端末の即時mute／stop，旧operation取消と次ASRの分離 |
| 自動検証済み | 冒頭PCMのbyte一致，4場面，連続割込み，ASR cleanup直列化，pause／resume，旧token／WAV／final／onended，複数WAVのSpeechUnit，v2共通scenario，Work設定・履歴の保持 |
| 実環境smoke | 修正済み隔離Gateway→既存Workモデル→macOS音声合成が成功．入力は合成文，マイク・音声再生なし |
| 人による実機受入 | 未確認．Macがロックされ，native appの画面を操作できなかった．実マイク・ヘッドホン・聴感・次回答への接続を自動testで代用しない |
| 記録 | Step 4未実装．実会話の自動記録，v2 grant，Jobを有効化していない |

開始時のRuntime main／origin/mainは`36e9a3015a986c4ffacece2dcefa93b629737647`，Karteは`e426db4222db39654c87524e45437280aa403210`だった．そこから専用worktreeを作成し，旧C0.1の試行・独立draft PR・Karte stackを変更していない．

### 実装と観測の境界

- sessionがPCM ringを所有し，検出前500 msと検出frameを一度だけ次ASRへ送る．引継ぎ中の合計は2秒，bridge待ちは5秒まで．既存の1秒PCM送信queueへ順番に排出する．上限超過時は冒頭を捨てず，会話をpauseして再発話・text入力を案内する．全PCMを引き継ぐ前にprovider finalが来た場合も，短く切れた内容をcanonicalへ採用せず，previewをtext fallbackへ残してpauseする．raw PCMはdiskへ保存しない．
- 割込み検出の同期処理でgainを0にし，sourceをstop／disconnectしてqueueを破棄する．LLM／TTSの取消完了を待たない．Goは旧operationのcontextだけをcancelし，voice sessionを残す．ASR provider sessionのCancelが戻るまで次のOpenを直列化するため，同時に2つのASRを開かない．
- `InterruptInteraction`はvoice session／epoch，conversation，turn，operation，generation revisionを照合する．開始・自然終了の通知がbridgeで遅れていても，端末が観測したWAV単位の結果を一度に照合する．遅れたcallbackは次turnの状態を変更しない．
- assemblerが確定した時点で全SpeechUnitを登録し，TTS未開始の単位も残す．1 SpeechUnitと1 WAVを同一視しない．全WAVの自然終了とTTS producerの正常終了が揃った単位だけが`completed`になる．中断位置の単語数は推測しない．生成が完了した後の取消でも`generation.complete=true`を保つ．
- UIの表示本文，生成状態，SpeechUnit観測を別に保持する．次の会話には，中断の事実と連続して自然終了を確認できた単位の接頭部分だけを渡す．表示された全文や未再生の残りを「聞かせた内容」にしない．ASRはcanonical finalを一度だけ採用し，連続会話の割込みで固定ACKを流さない．
- Work受入で判明した既存の末尾system messageを修正した．completion guidanceを既存mode／personaの先頭system群の後，最初の会話messageの前へ置く．Qwen3.8のtemplate拒否を解消し，thinking・声・温度・512 token／既存continuation設定を変更しない．Gatewayも更新版から再起動して受入する．

### 自動検証と実環境smoke

Frontend 266 tests，Runtime Go全体，対象Go race，Karte `internal/ephyrecordsv2`，v1/v2 45 JSONのbyte照合を実行した．Python全体は613 passed／3 skippedとmacOSの二重sandboxで実行できない4件に分かれ，その4件をテスト自身のsandboxが使える状態で再実行して4 passedを確認した．合計617件の成功を確認したが，単一のsandbox内で617件が通ったという報告にはしない．最終のbuild／CI／source SHAは受入成果物節へ追記する．

Workの初回smokeは末尾system指示による`generation_unknown`で失敗し，syntheticな直接requestでQwen3.8の`System message must be at the beginning`を再現した．配置修正後の生成は約38.2秒で完了した．macOS `say`はsandbox内では成功exitでも`data`が0 bytesのWAVになり，厳格な検査で`tts_invalid_audio`として拒否した．検査は弱めず，システム音声サービスを利用できる実行条件で再検証した．

再検証は既存のWork `qwen3.8-27b`／8082を使用した．設定は`enable_thinking=true`，`preserve_thinking=true`，`reasoning_effort=medium`のままである．修正済みGatewayだけを別portで起動し，既存Gatewayや設定を置き換えなかった．生成本文・reasoning・WAVを受入logへ残さず，次のmetadataを記録した．

| 区間／観測 | 今回の値 |
|---|---|
| request→最初のraw delta | 3,746 ms |
| request→最初の表示本文 | 15,054 ms |
| 最初のraw delta→表示本文 | 11,308 ms．thinking token数は取得不能であり，推測しない |
| request→生成完了 | 18,117 ms，1 segment，203 completion tokens，stop＋terminal SSE＋DONE |
| 生成完了後の合成開始→最初のWAV | 2,031 ms |
| 合成全体 | 2,977 ms，2 SpeechUnits／2 WAV，合成音声長14,167 ms |
| 発話開始→partial／endpoint／ASR final | 未計測．このsmokeはマイクを使わない |
| 実際の割込み→聴感上の停止／次ASR／次の回答音声 | 未計測．自動試験の値を実機値にしない |

このsmokeの合成profileは既存の`macos-say`で，アプリで選択中のvoiceを変更する試験ではない．C1の通常実行では`local_stop`，`input_handoff_asr_ready`，`input_handoff_drained`をmetadata traceへ記録し，既存のspeech／endpoint／ASR finalization／LLM／TTS区間と分けて確認できる．音響上の停止は人が確認する．

### 受入成果物とStep 4への引継ぎ

source SHA／署名後binary hashと起動手順は，clean sourceからのbuild後に記録する．実機手順は[C1 Step 2受入](C1_STEP2_ACCEPTANCE.md)を参照する．

Karte正本の`runtime-delivery.scenario.json`とRuntimeのGo／Frontend／Python，Karteの採用・読戻しtestが同じ状態を検査する．v2 schema／protocolは2.0のままで，未知fieldを追加せず既存enumの意味を明確にした．recordに保存する本文と再生された範囲を同一視しない．

Step 4は現在policyに従うKarte v2 search／readへの統一が必要である．`packages/karte_core/source.py`のscan／`read_document`，`packages/rag_core/service.py`の`_iter_files`／`_copy_ingest_source`，既存copy，cached chunkとread-backまで含めて旧経路を遮断・失効させる．新しいscanの除外だけで既存copyやcacheが消えるとは扱わない．実記録の有効化はこの境界，record用UUID／event順序，永続保全・同意が揃うまで行わない．

## Step 3／C2 Karteの現在結果

| 区分 | 状態 |
|---|---|
| 実装済み | Karteの共通writer，限定grant，typed event／派生物の採用，復旧，v2 search／read，human revision，source stale判定 |
| 自動検証済み | 隔離したroot・合成データで非破壊保存，現在policy，ID再利用拒否，同一event再送，各停止境界の復旧，CLIの保存・読戻し・取消 |
| native／利用者受入 | Karte UIでの新機能受入は未実施．実CLI試験は合成rootで実施したが，実ユーザーデータや音声記録の受入ではない |
| Runtime側 | v1/v2の45 JSONをmirrorし，共通fixtureの署名・ID・原文・source参照をPythonで検査．Step 4の記録経路は未実装 |
| 新記録の有効化 | 既定OFF．producer登録・scope・記録同意は人が明示する．既存Developer Modeを同意へ変換しない |

### 対応版と契約

- Karte：[PR #307](https://github.com/kirimine170/Karte/pull/307)．2026-09-12にCI 6件成功・PR時の公開job 1件skipを確認し，squash統合済み．対応mainは**`e426db4222db39654c87524e45437280aa403210`**．検証したPR headは`97a49e509390d7843d1fb63fc8b392e2dbfb17d6`，機能sourceは`591e767a5eee374fb170a2d66fcae39bd1100190`である．
- C1：[Runtime PR #79](https://github.com/kirimine170/ephy-runtime/pull/79)をCI 6件成功後にsquash統合済み．統合commit `338bd92dfbe1593000bc1e8c8e1ee9e87add6ebc`．下記の受入build source／binary hashは変わらない．
- Runtime契約mirrorと本STATUS：[PR #80](https://github.com/kirimine170/ephy-runtime/pull/80)は2026-09-12に統合済み．Step 3の統合commitは`36e9a3015a986c4ffacece2dcefa93b629737647`で，Step 2開始時のmain／origin/mainとも一致した．Karteの上記統合版を照合先とし，Runtime側のcanonical writerや記録ONは追加しない．
- 保存契約：record schema／context protocol **2.0**．v1の既存15 JSONは維持し，Step 3の29 JSONにStep 2の共通scenario 1件を加えたv2の30 JSON，合計45 JSONをKarteとbyte照合する．不明版をv1やdirect filesystemへfallbackしない．
- ownerと設定：[Karte保存契約](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)，[Step 3設定・復旧手順](../../../karte/architecture/RUNTIME_RECORDS_V2_SETUP.md)，[ADR-0005](../../../karte/architecture/adr/ADR-0005-scoped-runtime-diary-adoption.md)．Runtimeはcanonical Markdownを独自生成しない．

Karteの合成試験用CLIは上記機能sourceのclean tree `6c7d006f4e08e72a65371cb812f458962d7a9555`からGo 1.25.3／darwin arm64でbuildした．binary SHA-256は`b942680b354eeed664f6d2c505d9626f87601cd6c14f52251d4a28ffc3227065`．Desktop UIの実行版や受入の証明へ流用しない．

### 保存・認可・復旧の確認

共通writerはKarteのUI保存，v2採用，policy更新をプロセス間で直列化する．旧SaveFileが競合検出前に正本へ書いていた処理を除き，base hash，temp／fsync／replaceを使う．PR #268の`fc05c405085eed5ca566f630e78945db51575a79`から競合検出と回帰検査に必要な部分だけを照合・再利用した．同名の既存文書，人の編集，replace失敗では元bytesを保持する．v2の人による保存は新revisionを作り，以後の自動上書きを停止する．

exact actor／producer／scopeの登録とMACを検査し，受信時・commit時に現在grantと共有privacy policyで再認可する．検索・読取り・過去revisionにも現在policyを適用し，取消時に古いcontext応答を破棄する．source revision／hashが変わった派生物はstaleとなり，既定検索から外れる．AI解釈は利用者の発言と区別する．

`prepared → canonical → revision/event ledger → saved → receipt → archive`の各境界で停止を注入し，再起動・再送で1回の保存結果へ収束することを確認した．保存後receipt前に停止しても二重追記しない．既に保存済みの内容を後から人が変更しても，旧receiptの復旧で現在本文を上書きしない．同じeventを別candidateで再送しても同じeffectとなり，異なる本文へのcandidate／event ID再利用を拒否する．receipt詳細を除いた後も最小ledgerが再送を防ぐ．

### 自動検証

| gate | 結果 |
|---|---|
| Karte backend CI相当のroot＋cmd／internal対象22 package | PASS．GitHub Backend CIも成功．変更しないASR／audioの専用gateは別管理 |
| Karte Desktop CI | darwin-arm64／darwin-amd64／Linux／Windowsすべて成功 |
| canonical／v2 records／context／outbox／Gitのrace | PASS |
| 別プロセスwriter，CAS，collision，人編集，停止6境界，旧review，UI read／site／Git除外 | PASS |
| 実CLIのconfigure→採用→再送→ID再利用拒否→read-back→取消 | PASS．一時rootのみ．1文書・revision 1のまま，hash `33d8a0d76e4b9b1b27596e5fa95c1c7892cf3b74b5433b8178f8daad281ef85c` |
| Karte frontend | Node 22.13.0で50 tests，typecheck，production build PASS．Node 25では既存localStorage testが失敗したため指定版へ合わせた |
| Windows amd64 | クロスコンパイルに加え，CI上のroot／canonical／v2 recordsの回帰検査とDesktop buildがPASS．native電源断耐性は未確認 |
| v2 schema／共通fixture | PASS．strict JSON，canonical bytes／MAC，stable ID，8 events，再送，解釈とsource refs，中断unitの自然終了を推定しないことを検査 |
| Runtime側Python | 契約同期とv2 fixtureの16 tests PASS．44 JSON byte一致 |

最初のWindows CIではUnix mode bitによる資格情報判定が失敗した．Windows用に利用者本人のowner／ACL検査・設定と置換時のACL保持を実装し，専用回帰検査を追加した．続くWindows実行では機能検査が通り，共有fixtureのcheckout時のCRLF変換だけが残ったため，Karteのcontract JSONをLF固定にした．Runtimeは既存のLF設定を維持し，44 JSONがautocrlf有効時にもbyte一致することを確認した．

macOS／Linuxのdirectory fsyncと異なり，Windowsはfile fsync・replaceを行いdirectory flushは行わない．非協調の外部editorとOSレベルで完全なCASを保証しない．これらの限界を設定手順に明記した．自動purge，tombstone，削除・制限のhuman intent UIは未実装であり，Step 6へ残る．

### 未統合・未commit変更の整理

ユーザーのcommit／push／PR／merge許可を引き継ぎ，C1と今回のStep 3を独立したPRにした．元checkoutのStep 0文書は開始時hashと一致を確認し，元bytesを保全してからcleanにした．Karte mainは`e426db4`へfast-forward済みでclean，Runtime mainはC1統合済みでcleanと確認した．Karteの同名末尾「2」の4ファイルは各対応元とbyte一致を確認し，ローカルrecoveryへ保全後に元checkoutの重複だけを取り除いた．古いC0.1の11ファイルの試行差分はpatchとして保全し，今回のmainへ混ぜない．

Runtimeのdraft PR #7／#8／#9／#12はbenchmark，reranker，Wails E2E，SearXNGの独立backlogとして維持する．Karte #268と#271→#270→#268→#269→#277のstackも維持し，未完のCIを新しいStep 3の故障と同一視しない．旧Karte／Renderer checkoutのprivate・tool・生成物は公開commitへ加えていない．詳細はローカルのrepository inventoryと保全patchで追跡する．古いbranchのahead数を未実装の証拠にせず，既存squash・patch同等性を優先する．

### 次の指示対象

C1 Step 1の既存手順による受入と，Step 2のpre-roll・割込み・新入力ASR・中断履歴・SpeechUnitの再生開始／自然終了／不明状態を先に確定する．今回Step 4へは進まない．Step 4の開始時は本節のKarte版をpinし，v2 readerと旧direct reader／generic RAG除外，記録ON／OFFと同意，永続queue，event配送・receipt・読戻し・restartを実装する．これらが揃う前に実会話の記録を有効にしない．具体的な旧経路は`packages/karte_core/source.py`の`scan`／`read_document`と，`packages/rag_core/service.py`の`_iter_files`／`_copy_ingest_source`，既存chunk storeである．Step 4では新規ingestだけでなく，既存copy・cached chunk・source表示からの再読取りも遮断し，現在policyに従うKarte v2 readへ統一する．今回これらのRuntime本体は変更していない．

## Step 1／C1の結果

下記の第1〜6節はStep 0時点の照合記録として維持する．現在の実装差分と受入状態は本節を優先する．

### 版と保全

| 区分 | 版／状態 |
|---|---|
| 作業branch | `feat/c1-continuous-session`．既存dirty checkoutを変更せず，専用worktreeで実装 |
| Step 0文書保全commit | `b70684a`．元checkoutの4文書は作業開始時のSHA-256と全件一致 |
| endpoint設計差分commit | `0b7b401`．provider保証とpartial不変時間，猶予，ためらい，early finalを明確化 |
| 機能実装source | `ca9d90a7f904430af66fc1d7f79bf227df5465f3` |
| build対象tree | `c70ad925c362426a0bdd15647255612bffc61fc9`．ビルド前後でsource一致，`source_dirty=false` |
| 署名後の実行ファイルSHA-256 | `778670fe65aaa399636fefbe803b111e3805bb84191ae4b98c75b431bd5258fa` |
| build | Go 1.25.3，darwin/arm64，production tags，ad-hoc署名．`desktop/build/bin/runtime-build-provenance.json`で対応付け |
| 外部操作 | PR #79をCI成功後に統合済み．上のStep 3節の更新を優先 |

本STATUSを追記した文書commitとbuildのsource SHAは別である．受入対象は上表の機能sourceとbinary hashで固定する．build scriptは作成前後のsource不一致を拒否し，dirty buildを明記する．署名直後の検査はPASS．File Providerが後からbundleへFinderInfoを付けた際にstrict検査が1回失敗したため，当該属性だけを除去して再検査PASS，同じbinary hashを確認した．受入launcherにも同じ属性除去と署名・hash検査を用意した．

### 実装した動作

- 「会話を開始」で1つのMediaStream／入力AudioContextを保持する．発話ごとにASRを閉じ，回答の正常完了後に同じcaptureから次の発話を受け付ける．出力AudioContextも開始gesture内でresumeし，session内で再利用する．
- 「一時停止」「会話を再開」「会話を終了」，マイク許可待ち，発話待ち，認識中，endpoint候補，回答中，停止理由を表示する．pause／終了／device変更・track終了はcaptureと出力を直ちに止める．
- Goのvoice session ID／epochと会話IDを分け，旧epochの開始・停止要求を拒否する．ASR contextはsession配下とし，旧回答の取消は次のASRを取消さない．Step 1では同時active operationは1つである．
- providerが保証する`stable_prefix`だけを確定寄りの表示に使う．Apple ASRのfinal前stable通知は不要．partialが300 ms不変という観測からstable prefixを作らず，endpoint補助判定にだけ使用する．
- RMS 0.02／活動32 ms，partial不変300 ms＋無音900 ms，partialなし／ためらいだけなら1800 ms，取消可能な猶予100 ms，poll 20 msを初期値とした．実装は`voiceEndpoint.js`の設定を注入して調整でき，実機最適値とは主張しない．32 ms未満の活動だけで自動送信せず，150 ms未満という理由だけで短い相槌を捨てない．
- early finalはprovider endpointとしてdrain→End→Finish一致検査へ進む．発話活動がなければ破棄して再待機する．会話・記録候補・LLMへ入るのはendpoint後のcanonical finalだけである．callbackとFinishの二重finalを1件にする既存backendを維持する．
- PCMは発話60秒／8 MiB，chunk 64 KiB，送信待ちqueueは128 KiB以下かつ1秒相当，finalization 30秒以下．継続発話が上限へ達した場合は`utterance_limit`でpauseし，途中のpartialを自動送信しない．previewは明示操作でtext入力へ戻して編集できる．
- 無発話時は60秒ごとにASRを更新し，累積5分の無活動でマイクを解放してpauseする．無音からChat turn／LLM requestを作らない．許可待ちの取消後は，古い許可要求が返るまで新しいcapture要求を重ねない．
- C0.4の活動monitorは同じcaptureのPCMを購読する．連続modeでは割込み時ACKを重ねない．本文再生中の発言内容引継ぎとpre-rollはStep 2のままである．手動録音とtext fallbackを残した．
- traceの`listening_started`と最初の活動PCMの`user_speech_start`を分離した．`speech_first_partial`，`speech_to_endpoint`，`asr_finalization`，`first_audio`を別に確認できる．これらはcallback／bridge時計による計測であり，音響的な発話・再生の実測と同一視しない．

### 自動検証

| 検証 | 結果 |
|---|---|
| Frontend `npm test` | **248 passed**．追加42件を含む |
| endpoint境界 | 900／1800 msの直前・一致・直後・猶予内再開，300／600 msの間，ためらいからの続き，短い「うん」「はい」，early final，partial変更を固定時計で検査 |
| session統合fixture | 開始1回で4 user turns，capture最大1，履歴更新後の再待機，無音0送信，pause／resume／終了，pending permission，device終了，旧callback，PCM drain／backpressure，60秒上限を検査 |
| Go `go test ./... -count=1` | **PASS**．opt-inの実機testは実施証明に含めない |
| Go race `go test -race ./... -run 'TestVoiceSession\|TestInteraction\|TestStreamingASR\|TestFiller' -count=1` | **PASS**．既存macOS linkerのLC_DYSYMTAB warningあり，race検出なし |
| Python build provenance／launcher／voice AB／filler | **22 passed** |
| Python Karte conversation／API回帰 | **21 passed**．既存Starlette／httpx deprecation warning 1件 |
| bindings再生成，Frontend production build，native production build，署名検査 | **PASS** |
| `python3 scripts/validate_repository.py`，`git diff --check` | **PASS** |

Pythonは既存Runtimeのvenvを利用し，Go cacheは一時ディレクトリへ指定した．最初のツール呼出しではcache権限，未生成のFrontend embed，未導入のworktree依存，system Pythonのpytest不足を検出したため，専用cache・`npm ci`・既存venvで解消して上記gateを完了した．失敗した呼出しをPASSへ数えていない．

### 実機受入と残る操作

| 項目 | 現在 |
|---|---|
| source SHAと署名後binary hashの対応 | **確認済み**．上表とprovenance JSON |
| native UI起動・表示・状態遷移 | **新buildは未確認**．後の確認で既存buildのUIへ到達したが，C1受入の証明には数えない |
| ヘッドホンで開始1回→4往復→pause／resume→終了 | **未確認**．人の発話・聴感確認は未実施 |
| 実音声でのfirst partial／endpoint／finalization／回答開始 | **未測定**．synthetic境界値を実測欄へ転記しない |
| 実音声での誤送信・二重送信なし，短い相槌とためらいの自然さ | **未確認** |

UI操作可能な状態で既存Runtimeを通常終了してから受入用bundleを起動する．単一instance制約により，旧appが動いたままの起動を新buildの確認と数えない．ローカルrecoveryに置いた`launch-c1-acceptance.command`は既存instance検査，hash／署名検査，導入済みASR helper指定を行う．既存loopback Gateway／TTSを利用し，model／private設定を変更しない．

人の必要操作は，ヘッドホン装着→「会話を開始」→「はい」「うん」と，途中に短い間を入れた発話，「えっと……」から続く発話で4往復→「一時停止」→「会話を再開」→「会話を終了」である．各turnの本文なしtraceでlatencyとendpoint／finalが各1回であることを照合し，聴感と誤送信の有無は別に記録する．人の確認がないまま実機合格へ更新しない．

rollbackは受入sessionを終了して元bundleへ戻す．Step 0文書の元bytesはrecoveryへ保全し，sourceのmainは統合版へ更新する．既存app，モデル・音声assetは保持している．次の一手は上記Step 1実機受入であり，Step 2はユーザーの別指示後に開始する．

## 1．Step 0時点の版と作業状態

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

## 2．Step 0時点の実装と差分

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

## 5．Step 0で実行した検証

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

## 6．Step 0完了時の成果と引継ぎ

Step 0で変更したのは次の文書だけである．

- Runtime：本STATUS，IMPLEMENTATION_PLAN，ADR-0014，ADR index．
- Karte：`architecture/KARTE_RUNTIME_DIARY_V2.md`，ADR-0005，ADR index．

新しい実装commit／push／PR／merge／release，Issue・Sheets更新は今回行っていない．成果は両repoのlocal working treeにある．既存の明示承認を失効させたり，再承認条件を追加したりしない．過去C0.3同期報告にはcommit／push／PR／merge承認の使用記録があるが，本Stepの目的は設計文書であり，未指定の公開・releaseへ範囲を拡げていない．文書編集・read-only照合・必要な隔離worktreeとtestは今回依頼の範囲で実施した．

指定資料はAstra／highを基本としている．このtaskの選択モデル／推論設定をUI等から検証できるmetadataは未取得であり，プロンプト記載だけで切替済みとは扱わない．モデル選択は変更していない．Step単位の入力／出力token・再試行込み使用量は取得できず未計測である．account全体の使用率をこのStepの消費量として代用しない．

設計を止める未決事項はない．残る未確認は次のとおりである．

1．指示書が言及する`Ephy Reference Architecture v0.1`のPDF本体は今回の添付に含まれず，対象workspaceとDriveの限定検索でも同定できなかった．既存ADRと管理表を根拠に今回の会話範囲を確定した．原資料入手時は命名・責務との整合だけを追加照合する．
2．Runtime実行binaryのsource revisionを厳密にpinできていない．Step 1実機受入前にbuild SHA／hashを記録する．
3．C1の連続発話，本文割込み冒頭，Work thinking音声一周，C2の自動採用・crash復旧，C3の日記自然さ・翌日想起は未実施である．既存単turn・合成結果を合格に流用しない．
4．Karte #268の未統合writer改善をStep 3開始時に再確認する．既存の重複ファイルと古いRuntime worktreeは保全したままである．

Step 0はこの状態で停止し，ユーザーからのStep 1指定を受けて冒頭の実装・自動検証まで進めた．現在の次の一手は冒頭のStep 1実機受入である．
