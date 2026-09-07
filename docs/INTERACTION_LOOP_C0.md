# Ephy Interaction Loop v0／Gate C0

## 現在の到達点

C0は，明示的な録音操作から既存Chat経路，音声合成，再生，cancel，評価へつなぐRuntime内のinteraction loopである．B0のbaseline修復commitは`db087b4`，C0のfocused branchは`feat/interaction-loop-c0`であり，生成bindingsを含むB0の修復をC0の新機能から分離する．recovery directoryと退避物はGitへ含めない．

実装とsynthetic検証，Apple Speech helperのbuild，権限を要求しない準備確認，実機KyokoのWAV生成・一時file削除を実施した．実マイクからASR，既存model応答，speaker再生までの一周と，実音声のcancelは未検証である．この実機確認が済むまで，C0全条件を合格として扱わない．最終test結果の記録欄は末尾に置く．

変更範囲は`ephy-runtime`だけである．Karte側の実装，Google Sheets，Worker，LoRA，Avatar，Cameraを変更対象に含めない．provider選定と交換契約は[ADR-0009](adr/ADR-0009-local-voice-providers.md)を参照する．

## 既存会話との接続

```text
録音開始 → 録音停止／発話確定 → PCM16 WAV → ASR transcript
  → App.chatWithContext → 既存Gateway /v1/chat/completions
  → session／会話履歴／persona／router／Karte read／SSE
  → ResponsePlan → TTSの独立WAV chunk → Frontend再生ACK
  → 完了／cancel／失敗 → metadata traceと評価
```

Textとvoiceは`App.chatWithContext`と`conversationMessages`を共有する．Frontendの`conversationHistory`が既存会話のuser／assistant本文だけを渡し，最新transcriptをuser messageとして一度だけ追加する．source cardやprivate profileを会話履歴の別messageへ複製しない．Frontendのhistory上限は28 messages／48，000 UTF-8 bytesである．Gatewayへ渡すsessionは既存Chatのconversation IDで，voiceは`session_mode=voice`を使用する．

既存Gatewayがpersona，model routing，Karte Personal Contextのread／policy，streaming responseを処理する．voice専用の第二のLLM会話実装は作らない．routerのSSE `route` eventからprovider／model／configuration識別子を受け取り，LLMのtraceへ反映する．音声失敗時も既存text送信を利用できる．ASR後に失敗した場合は，認識済みtranscriptをtext入力へ戻せる．

Replayは元turnのsession，送信前のhistory，Chat設定を再利用し，別のturn／operation／trace IDを作る．transcriptを明示入力として同じ後段経路へ送るため，microphoneとASR認識を省略する．元turnのKarte本文やmodel内部状態を凍結する機能ではなく，Karteは既存read経路で再取得する．アプリ再起動後にmetadata traceだけから元historyを復元する機能はない．

## 主要ファイルと契約

| ファイル | 責務 |
| --- | --- |
| `desktop/app_interaction.go` | Wails bridge，既存Chat message整形，Runtime rootとprovider接続 |
| `desktop/interaction.go` | operation単位の状態，stage timeout，cancel，chunkと再生ACK |
| `desktop/interaction_store.go` | metadata traceのbounded保存，critical event検証，latency計算 |
| `desktop/voice_provider.go` | `VoiceASR`／`VoiceTTS`のmacOS実装，process・format・一時file管理 |
| `desktop/voice/EphyASR.swift` | Apple Speechのon-device認識，stdin WAV，固定error code |
| `desktop/interaction_evaluation.go` | Replay，blind A／B，評価保存，JSON／JSONL export |
| `desktop/frontend/src/voiceInteraction.js` | メモリ上の録音，Web Audio再生，operation照合，resource解放 |
| `desktop/frontend/src/conversationHistory.js` | Text／voice共通のbounded history |
| `desktop/frontend/src/voiceSession.js`，`voiceEvaluation.js` | session切替時の旧callback破棄，評価とreplayの操作 |
| `desktop/frontend/src/main.js` | 既存Chat画面との接続，通常UIとDeveloper Mode |
| `desktop/app.go`，`apps/gateway/routes.py` | 共通Chat／SSE経路と実router識別子の受渡し |

ASR interfaceは`Ready(context.Context) error`と`Transcribe(context.Context, []byte) (string, error)`，TTS interfaceは`Ready(context.Context) error`と`Stream(context.Context, string, func([]byte) error) error`である．ASR入力とTTS callbackはそれぞれ独立したWAVを表し，TTS callbackに再生済みという意味はない．provider交換時はinterface，contextのcancel，固定error code，独立WAV，`Identity()`の安全な識別子という契約を維持する．

Runtimeは既存回答を以下へ正規化する．LLMに複雑なJSON出力を要求しない．C0ではvoice hintに安全な既定値を入れ，感情推定や音声の抑揚制御を追加しない．

```json
{
  "text": "既存Chatから得た回答",
  "dialogue_act": "answer",
  "affect": "neutral",
  "voice_hint": {"pace": 1.0, "volume": 1.0},
  "interruptible": true
}
```

## 状態とcancel

通常経路は`IDLE → RECORDING → TRANSCRIBING → THINKING → SYNTHESIZING → PLAYING → COMPLETED`である．全active状態から`CANCELING → CANCELED`または`FAILED`へ遷移できる．録音開始と終了は利用者の操作で確定する．`user_speech_start`は録音開始操作を表し，音響的な発話開始を検出した値ではない．VAD，音声によるbarge-in，相槌はC0に含めない．

同時active operationは一つで，重複startを拒否する．cancelはidempotentで，同じoperationを再度cancelしても二重完了しない．終端後のtokenと音声callbackはRuntimeとFrontendの両方で破棄し，前turnのchunkを次turnのqueueへ入れない．Frontendはcancel操作時に先にmicrophone trackと再生sourceを停止し，その後Goへcancelを伝える．

stageにはASR 30秒，LLM 90秒，TTS全体60秒，未完了playback 30秒の既定timeoutがある．native adapter側にも準備確認5秒，ASR process 45秒，TTSの各process 30秒の上限があり，外側の短いdeadlineを優先する．LLMのHTTP／SSE requestとnative processもoperation contextを受け取る．

TTSは先行chunkの生成後から再生でき，LLMの回答確定を待ってから合成を始める．Runtimeはchunkの`started`／`stopped` ACKを記録し，TTS producerが完了し，すべてのchunkが停止した場合だけturnを完了する．traceの終端metadataを保存してから終端stateを公開する．providerが消失した場合，空音声やformat不正の場合，timeoutの場合は固定error codeで失敗し，text fallbackを表示する．

## Traceと保存境界

critical eventは以下の17種類である．すべてが一つのturnに出るわけではなく，完了／失敗／cancelで終端eventが異なる．複数chunkの再生eventは複数回出るため，event件数とevent種類数を区別する．

```text
user_speech_start
user_speech_end
endpoint_commit
asr_started
asr_final
llm_requested
llm_first_token
llm_completed
tts_requested
tts_first_chunk
tts_completed
audio_play_started
audio_play_stopped
cancel_requested
cancel_acknowledged
turn_completed
turn_failed
```

各eventはschema version，event ID，trace／session／turn／operation ID，`source`，RFC 3339 timestamp，開始からの`monotonic_ms`，status，固定error code，provider／model／configuration識別子を持つ．traceには音声，prompt，transcript，回答，private profile，Karte本文，providerの自由文diagnosticを含めない．

LLMのconfiguration識別子はrequest設定と実際に選ばれたmodel設定のdigestを合成する．同じmodelでもtemperatureやtoken上限が違うturnを区別し，同じ設定では決定的な値になる．

`ValidateInteractionTrace`はcritical eventの欠落を検出し，ASR，LLM TTFT／全体，TTS TTFC／全体，発話確定から最初の再生，playback，turn全体，cancelのlatencyを計算する．ReplayのASR区間はtranscriptを通過させる処理なので，実ASR性能と比較しない．

| データ | 保存場所と上限 | 内容 |
| --- | --- | --- |
| 録音 | Frontend／Go／helperのメモリ，8 MiB／60秒以内 | mono PCM16 WAV．既定の永続保存なし |
| TTS一時WAV | OSのprivate一時directory，directory 0700／file 0600 | chunk検証後，callback前に削除．失敗・cancelでもcleanup |
| Trace | `data/runtime/interaction/operation_*.json`，file 0600 | 256 turns，1 turn 128 events，7日以内．反復playback eventを必要に応じて集約 |
| Replay元request | Runtime processのメモリ | bounded turn cacheの範囲内．再起動後は復元しない |
| A／Bの対応表 | Runtime processのメモリ | 最大16 comparisons，1時間．候補本文や元transcriptをこのcacheへ保存しない |
| 評価record | `data/runtime/interaction-evaluations/records.json`，file 0600 | 最大256件．選択，修正文，failure tag，turnと候補の識別子，latency |
| 明示export | `data/exports/interaction-evaluations-*.json`または`.jsonl`，file 0600 | 評価recordのみ．export fileを自動送信しない |

Traceは起動，保存，対象turnの読取などでretentionを適用する．アプリを閉じている間に期限を監視するdaemonは追加しない．終端前のtraceはメモリにあり，processの強制終了から途中turnを復旧するdurable journalではない．評価recordには件数上限を設けるが，利用者が明示したexportの期限管理や自動削除は行わない．

Raw audioのdebug保存機能は実装しない．そのためdebug opt-in，TTL，削除UIを伴わない隠れた音声保存経路は作らない．評価exportには履歴，候補回答，prompt，Karte本文，private contextを自動で追加しない．修正文だけは利用者が明示入力した文字列を保存・exportするため，入力した内容がexport対象になる．synthetic fixture以外の実データをGitへ追加しない．

## Replay，blind A／B，評価

通常Chatには録音開始／停止，Ephyの発話停止，現在状態，text fallback，違和感記録を置く．詳細trace，latency，replay，A／BはSettingsのDeveloper Modeを有効にした場合だけ表示する．

`ReplayInteraction`は終端済みturnをsourceとし，修正したtranscript，または元transcriptを同じ送信前historyから再実行する．元回答をhistoryへ追加してから再実行する挙動ではない．sourceがretentionで失効した場合や，transcriptが利用できない場合は明示的に失敗する．

`GenerateInteractionComparison`は同じsource transcriptとhistoryに対して，既存routerの`auto`／`fast`／`work`／`code`／`rag`とtemperatureの異なる組合せから候補を生成する．候補ごとに最大512 tokens，比較全体90秒，同時比較一件である．同じmodeと同じtemperatureの比較は拒否する．比較の外部Web承認を再利用しない．

候補はrandomにA／Bへ割り当て，選択前のFrontend responseにはmodel名と対応表を含めない．比較対象は回答textであり，二つの候補を自動的に音声再生する機能ではない．A，B，引分け，どちらでもないの選択，修正文，failure tagを保存できる．同じcomparisonへの再保存は最新の評価で置き換わる．通常UIの「違和感を記録」は，本文をコピーせずturn識別子と`other` tagを一操作で保存する．

初期failure tagは`asr_error`，`early_endpoint`，`late_endpoint`，`slow_response`，`too_long`，`tone_mismatch`，`tts_pronunciation`，`cancel_failure`，`memory_misuse`，`other`である．`ExportInteractionEvaluations`はJSON／JSONLをlocal fileへ出力し，保存先を返す．

## 同じMacでの実機確認

Runtime repositoryのrootで実行する．Python環境，Node.js，Go，Xcodeと既存model stackの準備は[README](../README.md)，会話の起動・終了は[Conversation MVP](CONVERSATION_MVP.md)の既存手順を使う．modelの追加downloadはこの確認の前提にしない．

```bash
bash scripts/build_voice_provider.sh
bin/EphyASR.app/Contents/MacOS/ephy-asr --check --locale ja-JP
bash scripts/build_conversation_app.sh
```

準備確認の`ready`はSpeech許可済み，`permission_required`は初回Speech許可がまだ必要という意味で，このcommand自体は許可dialogやmicrophoneを開かない．on-device認識が使えない場合は`asr_on_device_unavailable`などで停止する．`build_conversation_app.sh`は親appへMicrophone／Speechのusage descriptionを設定する．ASR helperのbuildは別commandである．

既存Gatewayとmodel stackが動作していることをhealthと通常text chatで確認してから，build済みappを既存の起動手順で開く．macOSのTCCがEphy RuntimeをSpeech権限のresponsible applicationとして扱うよう，app bundle内の実行fileを直接実行せずLaunchServices経由で起動する．

```bash
bash scripts/start_conversation_app.sh
```

| 操作 | 期待結果 |
| --- | --- |
| 通常textで短い会話を2 turn行う | 既存historyとsessionが保持される |
| 「音声入力」を押し，macOSのmicrophone許可後，短い合成文を話して停止する | 録音表示が停止し，必要ならSpeech許可dialogが出る．許可後にtranscriptが同じChatへ入る |
| Ephyの回答を待つ | 既存modelが応答し，回答textと音声が出る．最後の再生停止後にCOMPLETEDになる |
| 別turnで認識中，応答中，合成中，再生中に停止する | 音声・録音が止まり，CANCELEDになる．連打で二重完了しない |
| 停止後すぐ次のturnを開始する | 前turnのtoken／音声が混ざらない |
| 音声turn中に新しい会話へ切り替える | 旧turnのtranscript／応答が新しい会話へ入らない |
| ASR helperを設定上利用不能にしてappを再起動し，音声操作を行う | voiceが明示失敗し，既存text chatは利用できる．確認後は設定を戻す |
| Developer Modeでtraceとlatencyを表示する | 終端に必要なcritical eventが揃い，本文や音声を含まない |
| 元turnからtranscript replayする | 元turnの送信前historyから別operationとして回答・再生される |
| 異なるrouter設定でblind A／Bを生成し，選択・修正文・tagを保存する | model名を見せずに選択でき，同じcomparisonの再保存は重複recordにならない |
| 評価をJSONとJSONLへexportする | 保存先が表示され，候補本文・履歴・Karte本文が自動混入していない |

利用不能providerの確認には，一時的な環境変数`EPHY_ASR_HELPER`を存在しない絶対pathへ設定した起動を使える．実fileを削除・移動する必要はない．Speech拒否・制限，未インストールvoice，96 kHzなどの入力device，出力device切替は，対象環境ごとの追加確認項目である．

録音と再生を伴わない実機TTS smokeは`desktop`で以下を実行する．macOS serviceにアクセスできる環境が必要で，実行sandboxでは空WAVや権限エラーになる場合がある．詳細はADR-0009に記録する．

```bash
EPHY_VOICE_INTEGRATION=1 go test -run TestNativeVoiceTTSInstalledSmoke -v .
```

すでに起動中のlocal Gateway／modelと実TTSを接続する検証は次で行う．ASR入力とplayback ACKはsyntheticであり，録音・speaker出力を行わず，modelをdownloadしない．出力はlatency，識別子，metadata traceだけで，回答本文や音声をlogへ出さない．

```bash
EPHY_INTERACTION_INTEGRATION=1 go test -run '^TestInteractionInstalledGatewayAndTTS$' -v .
```

## 自動検証と合格判定

通常CIはmock／synthetic providerを使い，実マイク，speaker再生，Speech許可，model downloadを要求しない．各active状態のcancel，idempotency，遅延chunk，stage失敗／timeout，playback ACK，200 synthetic turns，raw audio非保持，bounded trace，replay，A／B record，Frontend操作とaccessibilityを対象とする．

正規の回帰commandは以下である．実行結果とrevisionは最終検証時に下の表へ記録する．

```bash
python3 -m pytest -q
go -C desktop test ./...
npm --prefix desktop/frontend test
npm --prefix desktop/frontend run build
python3 scripts/validate_repository.py
git diff --check
```

変更したshell scriptには`bash -n`を行う．Wails bridgeを変更した場合は，正規generatorでbindingsを更新し，生成差分を確認する．C0の新しいAPIに伴うbindings変更とB0のbaseline修復を混同しない．

| 判定対象 | 証拠／最終結果 |
| --- | --- |
| B0 | 別commit `db087b4`．移動manifestとbaselineの証拠はGit外のrecovery reportを参照 |
| Python正規suite | 2026-09-06，実checkoutで366 passed／3 skipped，10.78秒 |
| Go正規suite／race | `go test ./...`成功，1.620秒．`go test -race ./...`成功，5.013秒 |
| Frontend test／production build | 114件成功．production build成功．通常UIとDeveloper評価を実browser＋合成bridgeで操作し，JS errorなし |
| Repository validation／shell syntax／diff check | 成功．shell syntaxは43 scripts．Wails正規生成を2回行いbyte-identical |
| Swift helper build／ASR準備 | build成功．2026-09-06の実機確認は`permission_required`．ASR認識は未実施 |
| 実機TTS／一時file削除 | Kyokoで38，462 bytesのPCM16／mono／22，050 Hz WAV生成成功．audio data 34，366 bytes．callback前の削除成功 |
| Desktop build | Swift helperとproduction appのbuild成功．Microphone／Speech usage descriptionを設定済み |
| 既存Gatewayと実TTSの接続 | 1回成功．`llama_cpp`／`qwen3-8b`からKyokoの41，262 bytes WAVを生成．実providerとconfigurationをtraceで識別 |
| Latency baseline | 上記1回のLLM TTFT 14，286 ms，全LLM 14，410 ms，TTS TTFC 2，470 ms，発話確定から合成playback ACK開始16，880 ms．実ASRと実speaker，実音声cancelは未計測 |
| 実マイクからspeakerまでの一周 | 未検証．手動確認の結果が出るまでGate C0の実機合格は保留 |

Pythonには既存依存のStarlette／httpx非推奨warning，race linkにはmacOSの`LC_DYSYMTAB` warningがあるが，test失敗はない．latencyは1サンプルであり，実機一周の合否や日常使用時の性能保証には使わない．

## RollbackとC1への境界

運用上は音声操作を使わず，既存text chatへ戻せる．C0のcode rollbackは，未commit変更とlocal dataを保全したうえでC0のlogical commitだけをrevertし，Frontend／desktop／bindingsをbaselineへ再buildする．B0 commit `db087b4`は維持する．稼働GatewayへC0のroute event変更を反映している場合は，対象Runtime所有の起動・停止手順で対象Gatewayだけを更新する．

`reset --hard`，`git clean`，無断stash，recovery directoryの削除はrollback手段にしない．trace，評価，export，ASR helperの生成物をrollbackの副作用で削除しない．他repositoryとKarteのデータに手を加えない．

C1へ進む前に実機一周，実音声cancel，複数turnの混入防止，実latencyの記録を完了する．VADによる自動交替，音声barge-in，相槌，echo cancellation，能動発話，学習用export加工，Karteへの新しい書込みは別Gateの検討対象である．

## C0.3.1安定化

2026-09-07に取得した`origin/main`は`3d7efd5e3cc25becb9ad67d5bbac5784fb4edc37`である．`fix/c0-3-1-voice-hardening`でASR callbackのsession隔離，構成済みdefault profileの保持，独立inference serviceのbearer認証を修正した．契約の詳細は[ADR-0009のC0.3.1節](adr/ADR-0009-local-voice-providers.md#c031-stabilization2026-09-07)，service設定は[custom voice手順](custom-voice-tts.md)を参照する．

callbackを実行する任意のGo goroutine自体は強制終了できない．timeout／cancel後に旧callbackが残っても次sessionのgateを占有せず，RuntimeとFrontendが旧operationのeventを破棄する．実マイク／speakerの一周と実音声cancel，許諾済みclone素材によるQwen実推論・声質・latencyの受入れは引き続き未確認である．C0.4，Irodori TTS，次Gateへは進まない．
