# Runtime会話・日記 C1〜C3 実装計画

2026-09-12．対象は添付`Ephy_Karte_Runtime_C1-C3_Codex_Prompts.md`の共通指示とStep 0である．本書は実装に使う設計であり，C1〜C3の機能実装済みを意味しない．現在の根拠と受入状態は[STATUS.md](STATUS.md)を正本とする．各Stepは指定された分だけ実行し，次へ自動進行しない．

## 1．到達点，責務，既存計画との関係

到達点は「許可して会話→自動記録→Ephy視点の日記→終了→翌日起動→想起→訂正」である．C1は連続音声と割込み，C2は正本への自動保存と復旧，C3は根拠のある日記・想起・訂正とする．本書のC系列を既存G系列や過去の音声検証Step 7と置換しない．

| owner | 担当 | 正本 |
|---|---|---|
| Runtime | session／capture／ASR発話区間／回答operation，確定event，配送保全，ローカル要約・日記Job，想起contextと利用者設定UI | 本書，[ADR-0014](../adr/ADR-0014-runtime-conversation-diary-boundaries.md)，後続のRuntime内queue schema |
| Karte | canonical Markdown，doc_id・revision・hash，scope，policy，採用とreceipt，source参照，検索，派生物失効 | [保存契約 v2](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)，[Karte ADR-0005](../../../karte/architecture/adr/ADR-0005-scoped-runtime-diary-adoption.md) |
| Worker | 将来のJob実行先 | 今回は実装しない．Runtime Jobの入力／出力を再利用可能に分ける |

RuntimeのqueueをKarteと独立して編集・確定・検索できる記憶DBにしない．会話保存と学習・外部送信は別の許可である．既存persona，router，Karte read，generation completion，SpeechUnits，ASR final-only，text fallbackを再利用する．

管理表は[Runtimeタスク](https://docs.google.com/spreadsheets/d/1b6-QifgaXWl3TeMMEf7yTq3VxduIrLewyVVlGhiVudo/edit#gid=1509234070)と[Karteタスク](https://docs.google.com/spreadsheets/d/1Px0MACPdErnLUAZwnsCRxQ74bY83k4MBC-xh8yPie2U/edit#gid=1002822573)を参照した．進捗表を本書へ複製せず，今回の差分を以下へ対応付ける．

| 既存Task／Issue | 今回の利用範囲 |
|---|---|
| Ephy T-122／[ephy #5](https://github.com/kirimine170/ephy/issues/5) | SourceRevision／Derivation等の会話に必要な共通部分を先に確定する．全知識モデル完成をC1の依存にしない |
| Karte T-112／[#301](https://github.com/kirimine170/Karte/issues/301) | 会話原資料のID・版と要約／日記の分離．Step 3，5 |
| Karte T-115／[#304](https://github.com/kirimine170/Karte/issues/304) | policy＋proposal／receipt基盤を再利用．今回のローカル会話領域は限定適用．公開調査の自動取込みを完了扱いにしない |
| Karte T-116／[#305](https://github.com/kirimine170/Karte/issues/305)，T-117／[#306](https://github.com/kirimine170/Karte/issues/306) | 訂正・削除・派生失効とContext返却．Step 3で必要契約を確定し，Step 5〜6で会話・日記を受入 |
| Runtime T-120，T-121／PR #48，#50 | 同一scopeでの選択readとcurrent hashによるappend推薦は既存実装として再利用 |
| Karte表のT-110，T-111／Runtime PR #55〜#57 | Developer Modeのpending自動送信とcreate候補の会話ログを再利用可能な既存機能として維持．durable event保存や自動採用へ同一視しない |
| Karte T-045／[#231](https://github.com/kirimine170/Karte/issues/231)，[PR #268](https://github.com/kirimine170/Karte/pull/268) | atomic canonical writerと非破壊な競合検査はStep 3の前提．未統合変更を再照合する |
| Runtime T-126／[#69](https://github.com/kirimine170/ephy-runtime/issues/69) | source版と解釈区分をgroundingへ渡す部分を共有する．一般知識の検証・利用集計は今回の完了条件にしない |

新Issueの重複登録はしない．管理表でT-103／T-107が進行中なのにPRにnative UAT証拠がある点はSTATUSで区別し，表の全gateを推測で完了へ変更しない．

## 2．C1の所有と取消契約

現行`InteractionEngine`はactive operationを1つ持ち，`BeginASR`が`t.ctx`の子となり，cancelでASRも終了する．Frontendの`release(run)`もマイクtrackと出力contextを一緒に解放する．C1では以下の寿命へ分離する．既存callback検査とstate machineを延長し，別の汎用session基盤を新設しない．

| 単位 | owner／寿命 | 取消対象 |
|---|---|---|
| conversation | Runtimeの会話controller．text／音声共通のstable ID | 新会話は別ID．過去記録を消さない |
| voice session | Goのsession状態とFrontend lifecycle controller．`stopped→starting→listening／responding→paused→stopped` | 明示pause／end，permission喪失，device切替でcapture・現ASR・現在回答を終了．resumeはsession epochを更新 |
| microphone capture | Frontendで1つのMediaStreamと入力AudioContextを所有．session内で共有 | capture停止はsession操作だけ．回答cancelはtrackを止めない |
| utterance／ASR segment | session配下の1発話．PCM sequence，segment ID，ASR revisionを所有 | endpoint／明示discard／session停止で閉じる．旧回答operationのcancelから独立 |
| answer operation | `InteractionEngine`．採用済みturnに対してLLM→SpeechUnits→TTS→playback | 割込み・cancelで旧generation，TTS，本文・filler音声，timer，queued WAVを取消．次のASRは保持 |
| playback | Frontend出力AudioContextとSpeechUnit sequence／playback epoch | 発話検出時にgain mute／source stopを同期実行．Go・LLM・TTS cancel完了を待たない |

session／turn／operation／segment／ASR revision／generation revision／playback epochが一致したcallbackだけを採用する．初期ASRの`operation_id`互換fieldを残す場合も，その取消contextは入力所有者へ移す．adapter wire formatを変える必要が出た場合はhelperとbindingsを同時更新し，旧helperへ黙って新fieldを渡さない．

`endpoint_id＋segment_id＋final_revision`を一度だけ採用する．endpoint前のfinalはpendingに置き，endpoint後に同じfinalがcallbackとFinishの両方から来ても1件にする．partial／stableはUIに表示するだけで，Chat history・保存・LLM requestへ入れない．

### 初期endpoint設定

ヘッドホン環境から受入する．新しいVAD modelは導入せず，既存PCM活動量とASRの安定性を組み合わせる．以下は初期設定であり，実測の達成値ではない．fixtureの固定時計で誤送信と有限性を先に検査し，聴取・発話試験で調整する．

- 活動検出は既存RMS 0.02／32 msを出発点とする．短い「うん」「はい」を150 ms未満という理由だけで捨てない．短いnoiseだけでLLMを呼ばず，発話活動と非空finalの両方を必要とする．
- providerが保証する`stable_prefix`は撤回されない接頭辞であり，UI表示と整合性検査に使う．partial文字列が300 ms変化しないという観測とは別である．Apple ASRはfinal以前のstable通知を必須にしない．partialの不変時間をstable prefixとして表示・保存しない．
- 発話後900 msの無音と，非空partial文字列が300 ms不変であることをendpoint候補の補助信号にする．候補後100 msは発話再開で取消できる猶予とし，commit直前にも無音を再検査する．partialが得られない場合でも1800 msの無音から同じ猶予を経て有限にFinishへ進む．無音またはpartialだけで会話・記録・LLMへ送らず，endpoint後の一致する非空finalだけを採用する．
- 「えっと」「ええと」「あの」等のためらいだけのpartialは900 ms側で閉じず，1800 ms側まで続きを待つ．900／1800 msの直前・一致・直後の再開，猶予後の確定，early final，短い相槌を固定時計で検証する．これらの閾値は調整可能な初期値であり，実機合格の測定値ではない．
- providerのearly finalはproviderが入力を閉じた明示endpointとして一度だけ扱い，既存のdrain→End→一致final採用を通す．partialや句点をfinalへ昇格しない．無音のsessionではearly finalが来ても会話へ送らない．
- 1発話60秒／8 MiB，PCM chunk 64 KiB／送信queue 128 KiB，ASR finalization最大30秒を維持する．上限時に話し続けている場合は`utterance_limit`として停止し，切れた発言を完了とみなして自動送信しない．previewからtext訂正・明示再送できる．
- listening中の無発話はturnを生成しない．5分無活動で表示付きpauseとしてマイクを解放する．設定変更は明示し，録音上限をsession全長へ伸ばさない．

Step 1はsession継続と自動ターン交替を完成させる．回答待機中の既存C0.4活動検出は同じcaptureからPCMを受けるようにし，二重`getUserMedia`を作らない．本文割込みの内容引継ぎはStep 2で完成させる．fillerが無効でもsessionは機能する．

Step 1の連続sessionでは入力AudioContext／MediaStreamをsessionが，出力AudioContextの再生handleを回答runが所有する．ASR segmentは発話単位で閉じる．無発話時は本文を送らず有限時間でASRを更新し，累積5分無活動でsessionをpauseする．従来の手動録音は別の利用モードとして保つ．本文再生中の発話はStep 1の引継ぎ対象外であり，状態UIで回答中／次発話待ちを区別する．

Step 2では500 msのpre-roll ringを同一captureへ追加する．48 kHz／mono PCM16なら48,000 bytesが基準で，resamplingやdevice epoch変更時の上限・破棄も検査する．検出点より前から新しいASR segmentへ順番に送り，検出点のframeを二重送信しない．session継続中も音声をdiskへ書かない．

C0.4の300 ms待機ACKは初期C1では割込み発話中に再生しない．ACKを必須にせず，入力を重ねずに扱える条件が確定した場合だけ後続で使う．ユーザーが発話している間にASRを止めない．

### 履歴とthinking

`voiceConversation.js`のpreview／confirmedと`conversationHistory.js`の成功terminal判定を基に拡張する．生成した確定本文と表示した範囲，再生開始／自然終了したSpeechUnits，中断状態を別に保持する．中断assistantを通常の完了回答にせず，次のrequestには中断の事実と確実に提示した範囲だけを限定contextとして渡す．再生途中のunitの単語位置や，全生成本文を聞かせた前提を作らない．provider内部reasoningを中断contextや原記録へ入れない．

現行Fast音声のrequestは`enable_thinking=false`，`reasoning_format=deepseek`である．現行Workは実選択`qwen3.8-27b`，`enable_thinking=true`，`preserve_thinking=true`，`reasoning_effort=medium`である．C1はこれを暗黙変更しない．UIにある`max_tokens=512`は既存generation completion／continuationの入力であり，回答を途中で合格扱いにする上限として使わない．Step 2受入にはWorkのthinkingを維持した実音声一周を含める．

## 3．C2の保存と復旧

wire field，Karte正本format，policy，ID・revision，transaction境界，保持上限の詳細は[Karte保存契約 v2](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)へ集約する．Runtimeはそれを独自に再定義しない．

記録設定は`recording_enabled`，Karteの保存領域ID／scope ID，producer登録，timezone，保持設定を明示する．設定済み保存先と同意があれば尊重し，未設定時に公開領域や既存private projectを推測しない．新設定は初回構成が完了するまでOFFであり，利用者がONにした会話は毎turnの採用クリックなしで保存する．既存auto-submitのbooleanだけから新しい保存同意を作らない．

Runtimeの単一writerが，確定user入力を受け付けた時点でevent ID／seqを付け，Git外のinstance private領域へtemp→fsync→rename→directory fsyncで保全する．記録有効時はこのdurable acknowledgementをLLM起動より先に完了する．disk failureでは未保存を明示して記録をpauseし，既存eventを保持する．同じ入力を自動で再生成・二重送信しない．text fallbackは使えるが保存済みと表示しない．

assistantのconfirmed本文・terminalと再生状態は別eventで追記する．tokenごとのdisk writeは行わない．Runtime強制終了では保存済みuser eventから`assistant outcome unknown/interrupted`を復旧し，回答が完成していたと推測しない．

1つの会話segmentの配送を直列化する．初回create receiptからdoc ID／revision／hashを得て以後はeventだけをappendする．Frontendの30 entriesや回答contextの28 messages／48 KiBの切捨てに記録経路を依存させない．最終turnだけのappendや全文再送による重複を防ぐ．

状態は`recording_off`，`locally_preserved`，`delivery_pending`，`canonical_saved`，`save_failed`，`permission_blocked`，`conflict`，`deleted`を区別する．`canonical_saved`は受領したcandidate／proposal hash／applied event IDs／doc ID／revision／canonical hashと，権限内のread-backを照合してから表示する．pendingやファイル存在だけでは成功にしない．同じ会話でも保存済みeventと未配送eventが混在する場合は件数・範囲を表示する．

再送は5秒から指数backoffし最大300秒，jitter付き，1回の起動で最大8連続失敗後は15分休止する．次回起動または明示retryで再開し，queue自体は期限で捨てない．permission／conflict／schema不一致は自動反復しない．復旧時は現在のconsent epoch・scope・capabilitiesを再検査する．記録OFFは以後の保存停止，削除は過去本文と派生物への別操作である．

記録OFF前に保全したデータの配送と過去記録のJobは，現在も許可が有効なら続けられる．OFF区間を後からbackfillせず，進行中turnを閉じる場合も新しい本文を保存しない．scopeの取消・明示削除は未配送とJobにも適用する．

## 4．C3のローカルJobと想起

### Job契約

実行は既存ローカルLLM adapterを使うRuntime内の単一runnerとする．Job descriptorとresult envelopeを分け，後からWorkerが同じdescriptorを実行できるようにする．Workerのlease serverや多node schedulerは追加しない．

```yaml
job_schema_version: '1.0'
job_id: <hash-of-kind-logical-key-input-refs-template-model-and-scope>
kind: conversation_summary  # または daily_diary
logical_record_key: <summary-or-diary-key>
input_refs: [{doc_id: <uuid>, revision: 3, sha256: <actual-hash>, event_ids: [e1, e2, e3]}]
timezone: Asia/Tokyo
local_date: '2026-09-12'
scope_id: <registered-scope>
consent_epoch: 1
model_id: qwen3.8-27b
template_id: ephy-diary
template_revision: '1'
attempt: 1
state: queued
```

実fixtureは実UUID・計算hashを用いる．Job IDは入力版・model／template設定を固定した内容から作る．retryでは同じID，source訂正や設定変更では別Job ID＋同じlogical record keyの新revisionとなる．保存権限とsource currentnessは実行直前と結果採用直前の両方で確認する．

`queued→running→result_ready→delivery_pending→succeeded`を基本とし，`deferred`，`retry_wait`，`stale`，`canceled`，`failed`を持つ．状態とattempt generationをdurableに保存する．再起動時のrunningは新attemptでqueuedへ戻し，旧attemptの結果を拒否する．result_readyなら同じresult hashを再送し，再生成・二重日記を避ける．成功はKarte receipt照合後である．

要約のtriggerは会話終了，明示新会話への切替，または10分の無活動による区切りとする．session pauseだけでは毎回Jobを増やさない．同じ入力集合には1Job．sourceが未配送なら`waiting_for_source`としてcanonical source refs確定まで生成しない．

日記は設定timezoneで日付が変わった後，60秒以上アイドルのときに前日分を生成する．明示timezoneがなければOSのIANA timezoneを初回設定に記録し，この環境の既定はAsia/Tokyoとする．未稼働日の分は次回起動で未処理日だけを列挙する．起動直後は会話を優先し，1日ずつ処理する．会話ゼロの日は`no_source`で終了し，架空の日記を埋めない．DST，timezone変更，日付境界にかかるturnを固定時計で検査する．timezone変更は過去のlocal_dateを遡って書き換えない．

同時実行は1，入力chunkは最大8,000 tokensと実model context残量の小さい方を目安にmodel tokenizerで計算する．1chunkの出力予算2,048 tokens，最大120秒，1attempt全体15分を初期値とする．これはbackground Jobだけの有限予算である．`finish_reason=length`等は未完了として小分け／再試行し，切れた日記を正本にしない．会話のthinkingや回答上限をこの予算に合わせて変えない．

会話開始で即座にJob cancelを要求し，旧結果を失効させ，providerの解放後に再開する．会話をJobのqueue末尾へ置かない．既存providerの取消完了が遅い場合は資源待ちを明示し，本文だけ先に破棄して裏で重いJobを走らせ続けない．一時失敗は最大3attemptで停止表示し，手動retryは同じ入力を再検査する．

長い会話は全event IDをcoverage manifestへ並べ，重複なし・欠落なしのchunk要約→統合要約→日記とする．30件を越えた残りを黙って捨てない．中間要約はJobの有限private作業物であり，Karte receipt後24時間以内に削除する．元会話への参照を失った要約を独立した根拠にしない．

### 想起と訂正

「昨日」は設定timezoneの前日範囲へ解決し，現在の利用者・scope・projectでKarte v2 search→doc_id readを行う．元会話，summary，diaryの区分・版・source refsを回答contextへ渡し，Ephyの解釈を利用者の発言として引用しない．会話のない日や権限不足を架空の記憶で埋めない．

利用者が内容を訂正したら，対象eventの有効revisionと訂正内容をcontrol intentへ結ぶ．Karteの採用・失効後に次の検索と日記Jobを更新する．古いsourceをpinした結果は不採用にし，human編集済み日記との衝突は自動上書きせず表示する．検索・生成開始後の権限変更についても，出力利用直前にsource lease／epochを再検査する．許可を失ったcached snippetへfallbackしない．

## 5．実装箇所と変更順

| Step | 主な既存変更箇所と追加物の予定 | 順序／依存 |
|---|---|---|
| 0 | 本書，STATUS，Runtime ADR-0014，Karte保存契約・ADR-0005 | 設計と照合のみ．本体・schema・設定は変更しない |
| 1／C1前半 | `desktop/frontend/src/voiceInteraction.js`，`fillerBargeIn.js`，`voiceSession.js`，`main.js`，`desktop/interaction.go`，`interaction_asr.go`，`app_interaction.go`，`voice_asr_session.go`．必要なsession state型・controllerを既存境界へ追加 | Runtimeだけ．captureとoutput取消の所有分離→endpoint→自動再待機→UI／bindings |
| 2／C1後半 | 同上＋`voiceConversation.js`，`conversationHistory.js`，`desktop/generation_assembler.go`，`generation_types.go`，`app_generation_stream.go` | Step 1にpre-roll，新入力独立，本文停止，履歴中断情報を追加．新しいTTS providerを作らない |
| 3／C2 Karte | `app.go`のSaveFile／AcceptEphyProposal／finishSavedEphyTransaction，`app_context_policy.go`，`internal/ephyoutbox/{contracts,store,placement}.go`，`internal/contextcore/{contracts,policy,service,store,processor}.go`，frontmatter，v2 schemas／fixtures | atomic-save未統合変更の再照合→writer安全性→v2 record／policy→transaction・復旧→search/read→Karte検証・統合→Runtime共通fixture mirror |
| 4／C2 Runtime | `packages/karte_core/{contracts,outbox,context,conversation,source}.py`，`apps/gateway`，Go Chat／Interaction入口，Frontend設定・状態表示．durable event spool／単一dispatcherの追加 | Step 3の確定版をpin→user finalの永続化→assistant状態→配送・receipt→restart read-back．旧候補UIは互換維持．新recordを旧direct indexから除外 |
| 5／C3生成 | Runtime内Job descriptor／store／runner，既存LLM adapter，`prompts/`に要約・日記template，Karteの派生revision採用，日記本文UI | C2 canonical source refs→summary→日付Job→日記保存．model・template・入力版をpin |
| 6／C3利用 | `packages/karte_core/context.py`と既存grounding／Sources UI，Karte v2失効・control経路，Runtime queue／Job invalidation | source訂正・削除・制限→依存失効→検索・read→回答・再生成を一周確認 |
| 7／監査 | Step 0〜6の差分・証拠・日常手順 | 別途指定後．Runtime/Karteの一周を監査し，Workerへの契約引継ぎまで |

各Step開始時に対象repoの最新main，対象branch差分，未統合PR，STATUSを再照合する．Karteのdirty checkoutやC0.1 recovery worktreeをreset／clean／pullしない．Karte contractはKarteを先に統合し，Runtime mirrorは対応SHAとfixtures一致を確認してから統合する．承認済み操作は引き継ぎ，merge等の実行結果を未実施なのに記録しない．

## 6．受入gateとfixture

新しい保存契約のfixture一覧とsemantic outcomeは[Karte保存契約の共有fixture節](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)を正本とする．以下はStep単位の機能gateである．自動・syntheticと実マイク／聴感の結果を別列でSTATUSへ残す．実機未確認でも実装作業と試験準備を完了し，必要な人の操作だけを示す．

| Step | 自動・障害・境界の完了条件 | 実機／利用者の完了条件 |
|---|---|---|
| 0 | 実装／branch／実行版／受入／backlogを照合．8契約項目，owner，版，fixtures，実装順，migration／rollbackを文書化．v1の照合と対象回帰が通る | 実音声を新規収録する必要なし．未確認を明記する |
| 1 | 連続4 user turns，無音0送信，短い300／600 msの間から再開，endpoint/final各1回，60秒上限，backpressure，permission遅延，device終了，pause/resume，旧callbackで再開しない．capture最大1 | ヘッドホンで開始1回→4往復→pause/resume→終了．first partial，endpoint，finalization，回答開始を別々に実測．誤送信・二重送信なし |
| 2 | LLM待機／filler中／TTS待機／本文中の割込み，500 ms pre-roll，連続割込み，遅延token/WAV/final，generation revision，device切替，中断履歴 | 本文中に「いや，冬の話だけ聞きたい」と発話し冒頭保持と出力停止を確認．Fastに加えthinking有効Workの一周．停止latencyは実測とcallback計測を分離し，C0.4の音響停止p95 100 ms目標を未測定のまま合格にしない |
| 3 | 登録actorの許可／偽造・scope外拒否，policy取消，ID再利用，同名collision，human edit，writer failure，各transaction境界で停止・再送，旧v1 human review，schema版互換 | synthetic専用Karte rootで自動採用→restart→receipt再取得→read-back．通常編集の非破壊性も確認 |
| 4 | user final直後の強制停止，assistant cancel/failure，記録OFF，4／8／32 messages保持，Karte停止，二重再送，disk failure／容量，receipt前後の停止，権限取消中の復旧 | 設定済み非公開保存先でtext／音声を同じ会話へ記録．毎turn採用操作なし．両app再起動後に原文とID／hashをread-back |
| 5 | 日付跨ぎ／DST／未稼働日の起動，重複Job，途中停止，会話優先，長い入力全範囲coverage，source訂正・削除・policy変更，人編集競合，会話ゼロ日 | 元turnへ辿れる要約とEphy一人称日記を表示．自然さ，事実・申告・解釈の区分は利用者確認として独立記録 |
| 6 | 昨日の決定・未解決事項を別に想起，訂正による次回検索更新，OFFと削除を別実行，cache・outbox・Job・派生物・旧版・reindexから非復活 | 今日会話→自動保存→日記→終了→翌日起動→質問→訂正→再質問を実行．根拠と訂正反映を利用者が確認 |
| 7 | C1/C2/C3の必要版・差分・証拠を監査．privacyと旧C0.x／v1の回帰確認 | 対応buildと日常起動・停止・復旧・rollbackを記録．Worker本体は着手しない |

実装Stepの基本コマンドは`python3 scripts/validate_repository.py`，対象`tests/`のpytest，対象Go tests，対象Frontend testsとproduction buildである．schema変更時は両repoの共通fixture検査，C2時はfault injectionとnative cross-app gateを追加する．同じ作業treeでfrontend buildとGo embed読取りを競合させない．Step 0は文書変更のため全model smokeやapp再buildを不要とし，実行中appを置換しない．

## 7．移行とrollback

v1のpending／receipts／documentsと既存memory schemaはそのまま残す．v1会話候補には安定event ID・正確なturn範囲がないため，C2 queueへ自動移行しない．既存候補はhuman review経路で処理する．過去UI履歴を新しい記録同意でbackfillしない．新recordだけにID・revision・scope metadataを付与する．

Step 1〜2のrollbackはsession featureを停止して手動録音／textへ戻す．旧操作のlate callbackを閉じ，C0.4 assetと個別承認を保つ．model，voice，raw audio，private設定，recoveryを変更しない．

Step 3以降はproducer・Job停止→durable queueの保全→Karteのv2 grant無効化→in-flight transactionを復旧または保留へ固定→旧clientのv2領域アクセス遮断→旧binaryへ復帰の順にする．正本文書・ID ledger・tombstone・未配送eventを消すrollbackはしない．旧clientがv2非対応なら未配送を表示して止める．新設領域の旧Karteによるscanを隔離できない場合は，Karteをv2 reader可能版に維持してproducerだけをrollbackする．

## 8．Step 0の完了と次の範囲

Step 0は設計と現行基盤照合で完了する．次に変更する範囲はStep 1のsession/capture所有分離，endpoint，自動再待機，状態UI，その境界テストである．保存機能，本文割込み内容の完成，日記生成，Karte schema変更はStep 1へ含めない．

設計を停止させる未決事項はない．添付指示が言及するReference Architecture PDFそのもの，現行Runtime binaryとsource SHAの厳密な対応，最新実機音声・日記品質の受入は未確認としてSTATUSへ残す．実保存先の新policy登録はStep 3〜4で既存設定の範囲を確認して行い，Step 0では有効化しない．
