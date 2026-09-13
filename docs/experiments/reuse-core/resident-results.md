# 常駐会話と部品の検証

2026-09-14，`ephy-runtime-resident`で実行した結果．既存比較commit `c509dfc`の実測と，新規実行を混ぜない．操作は[常駐手順](../../RESIDENT_FEEDBACK.md)を参照する．

## 常駐・feedbackの新規検証

| 検証 | 結果と範囲 |
|---|---|
| Python関連回帰 | 325件合格，1件skip．feedback，Profile，既存A/B，Gateway，Karte，speech，worker，起動分離，記録保護を含む．通常venvのskipはoptional SDKで，部品用venvの150件ではSDKも検査した．重複suiteを合算しない |
| Go | `go -C desktop test -race ./...`合格．既存全packageとresident対象・取消・候補・recordingを含む |
| frontend | `npm --prefix desktop/frontend test`で最終330件合格．有限waitのfake clock 12件を含む．Vite build成功 |
| repository | runtimeと共通設計repoのvalidator，差分検査とも合格 |
| 実Gatewayでの指摘 | 合成対象に「いつも説明が長い」を保存・適用，約3.0 ms．正の評価も単独feedbackとして保存し，A/B pair・training exportへ変換しない |
| 実モデルの次の応答 | 専用Gatewayから選択済みFastとLoRAを使い，固定した氷の質問へ3文で応答，10.09秒，`finish_reason=stop`．有効policyとresident revisionの到達を確認．n=1で，自然さや短さの改善率ではない |
| 実音声生成 | 選択済み`voice-irodori-anime-exp`で合成文「こんにちは．短い動作確認です．」を生成，audio→completed，17.09秒．初期loadを含む．物理再生なし |
| 復元・取消 | 専用Gateway／speechを終了・再起動し，新sessionで継続briefを復元．対象変更undoでdefaultへ戻る．sessionの自発抑制は別の新sessionではallowed．試験の継続変更はundo済み |
| 実Whisper＋合成WAV | large-v3-turbo-f16／Metalで準備1.755秒，8入力を4.590秒で完了．確定3，無音3，取消2，最終3件だけ下流へ渡る．この試験の下流LLM/TTSはstubで，実会話往復ではない．マイク／speakerなし |
| 署名済みアプリ | macOS arm64のWails／Go production build，Karte helper，Whisper helper，codesign検証，build provenance生成が成功 |
| native起動 | `f48140b`のbuildで専用IDのアプリPIDを確認．Macロックで画面を検査できず，アプリ内ASR workerは未確認．この後の差分は候補の有限wait，専用artifactの起動環境検査，文書で，最終buildも実行した |
| native終了 | ロック中に通常終了とSIGTERMが完了せず，所有PIDだけSIGKILLで回収した．監視が専用Karte／Gateway／speechを回収し，全件停止を確認．正常な画面終了は未確認 |

Python回帰の記録保護試験1件は，外側のsandbox内で追加の`sandbox-exec`を起動できず失敗し，同じ1件を権限のある実行で再確認して合格した．実Whisperの最初の合成WAVは48 kHzで，試験の1秒chunkが上限を超え`invalid_audio`となった．captureと同じ16 kHzへ変換した試験は上表のとおり合格した．初回の失敗logも保存し，成功値で上書きしていない．

通常版`042c3a7`のHEAD／作業差分／local設定hashを比較し，元のGateway・speech・モデルserverと既存reuse版のPIDを照合した．専用側から共有serverを切替・停止していない．確認の正本はGit外の`local-data/resident-feedback/logs/final-preservation.json`である．共通設計branchは`codex/design-resident-feedback`，主branchは`codex/feat-resident-feedback`．push／merge／deployは実行していない．

実測の保存先は専用stateの`logs/live-chat-smoke.json`，`live-speech-smoke.json`，`restore-undo-smoke.json`，`whisper-installed-smoke/`，`native-cleanup-*-smoke.json`，`final-preservation.json`．最終buildのsource revision／tree／署名後binary hashは，一時build領域の`runtime-build-provenance.json`を参照する．モデル・音声・私有DB・tokenはGitへ追加していない．

実マイクの連続会話，最小化中のcapture，物理speaker停止，sleep/wakeと実device切断，長時間負荷，聴感・距離感の評価は未実施である．ロック中のprocessのuptimeを常駐受入に数えない．指摘は定型表現に限定し，記憶共有抑制は文書ID単位でresident全体に適用する．Karte本文の事実訂正は確認待ちとして記録し，自動更新を完了したとは扱わない．

## 部品の採否

| 部品 | 現常駐版の判定 | 根拠と残件 |
|---|---|---|
| 固定Karte workflow | 最小の能動参加へ採用 | 既存Karte V1 client／実contextcore／設定済みlocal modelを接続．一回検索で足りない話題は安全な空候補へ縮退 |
| Pydantic AI slim | 旧動的比較のprobeとして保持 | 旧比較で追加検索の到達性を確認済み．現在の常駐へ追加loopや依存を増やさない．固定workflowの不足は解消済みとしない |
| Pi agent-core | 常駐へ不採用 | 旧比較では追加検索可能．この範囲でNode常駐／IPCを加える利益を実証できていない |
| Pipecat | 常駐へ不採用 | 新frame再試験は合格したが，Goのturn／再生／deliveryを削減した箇所は0．全体移植をしない |
| audio.cpp | experimental継続 | 新しい参照付きMetal生成と実worker回収・回復を確認．声の聴感受入，物理停止，長期負荷，CUDAは未確認 |

## 実Karteとlocal Work

Karte `90c69c58945e4eb00ec5f13c73b444ff298443d2`の既存contextcoreを固定archiveからbuildした．小さなwrapperは100 msごとに既存`ProcessPending(20)`を呼ぶ．合成専用markerがないdata rootでは起動しない．Karte checkoutと通常appは変更しない．新stateの7合成記録だけを使い，旧版・共有不可のdocはgrantsから除外した．

| 合成ケース | 新規実行の結果 | 時間 | model／tool |
|---|---|---:|---:|
| 公園のピクニック | 正しいdoc IDと実Markdown全文hash，完全一致引用を付けた候補 | 37.454秒 | 1／2 |
| 該当記憶なし | 空候補，モデルを呼ばない | 0.115秒 | 0／1 |
| 夏の旅行→追加検索が必要 | 根拠不足の空候補．期待の灯台docには未到達 | 14.218秒 | 1／2 |

各n=1．共有Work `qwen3.8-27b`，Q4_K_M，既存llama.cpp，context 8192，temperature 0.3，出力上限2048，thinking／preserve_thinking有効．同一モデルのweight／template hashは旧結果の固定値を再利用し，今回weight再hashは行っていない．この少数の試行を安定した成功率・速度順位とは扱わない．内容の自然さ・距離感・また同席してほしいかは利用者評価の残件である．

raw結果はGit外の`logs/resident-feedback/reuse-probes/karte-native-20260914/metrics.json`へ保存した．その前の`karte-local-20260914-retry`は実モデル＋実client＋合成protocol responderの切り分けであり，実Karte検索ではない．初回probeのkeyword引数誤りは修正し，失敗した出力領域を上書きしていない．

最終handoffには一回限り15秒ticketを使い，発話直前に最新grants・policy・参加者と実Karte再readの版を確認する．自動試験で生成後のdoc改版，grants変更，policy更新，参加者変更，取消，期限，二重消費を検証した．同期Karte待機の取消はthreadを強制停止せず，client上限内で回収し，遅延結果を採用しない．HTTP待機取消をGPUの停止完了とは報告しない．

ticket追加後にsimpleだけ再確認し，候補生成29.511秒，実Karteの最終再readで`ready`を確認した．rawは`logs/resident-feedback/reuse-probes/karte-native-final-check-20260914/metrics.json`．helperを終了・回収した．モデルが句読点指定に従わない出力もあり，この内容を利用者の自然さの受入として数えない．

## Pipecat

固定source `f67c18afddbfb0609991cd6830355713baaad01b`の実frameworkで合成frame試験を新規実行した．final contextは1回，旧epoch audio破棄1件，completed／interruptedのACK対応を確認した．frame interruptionは10.423 ms，n=1．これはqueue処理時間であり，実speaker停止ではない．相槌の試験はEphyがinterruptionを発行しない入力であり，音声分類精度ではない．

新rawは`logs/resident-feedback/reuse-probes/pipecat-20260914.json`．実ASR→Work→TTSの往復は旧実験で実行済みだが，今回はPipecat経路の新規物理音声往復として数えない．function-call取消と実マイク／実speakerの移植受入は未実施である．

## audio.cpp

固定source `ff1bcc4555ff99c4383329b0b21b52a18cc8b3cd`，C ABI 256，build version `dev`，Anime Q8 artifact `cef628aa1ce784993025789c24d05b4b9e60fa039d40453bc8bdea32f4cb99e7`を使った．library hashは`3b57bfa4c0dbf77ccd99d7161da9cc2a96706f85d9f0857ef7ba35f020b3e5d7`で，adapter起動時にmodel/library/build manifestを照合した．許諾済みreference group，neutral style，seed 420，40 steps，CFG 3／3／5，pace 1，Metal＋同backend codecである．

| 条件 | 新規実測 |
|---|---:|
| worker cold→最初の再生可能WAV，n=1 | 20.209秒 |
| 同worker warm→WAV，n=1 | 2.361秒 |
| warm RTF，音声2.76秒 | 0.855 |
| cancel→旧worker回収，n=1 | 26.678 ms |
| 回収 | 終了コード−15，temporary消去，次workerは別PID |
| cancel後の次request→WAV，n=1 | 8.549秒 |

入力は2句の合成文と取消用合成文．音声は保存したが自動再生や聴感評価はしていない．coldにはmodel loadと初期化を含み，OS／shader cacheのcoldは保証しない．p95や長期安定性は示さない．今回既存Python FP32全corpusを再測定しておらず，旧24ケース29句の比較は旧結果へ残す．

最初のsandbox試験では任意RSS計測の`ps`権限エラーが結果を隠したため，probeを欠測扱いへ修正した．次のsandbox試験はcold `tts_failed`，2.283秒で停止．同じ専用state／資産を権限のある実行で試すと上記Metal生成が成功した．失敗記録を成功値へ置き換えていない．新rawとWAVは`logs/resident-feedback/reuse-probes/audiocpp-20260914-metal`にあり，モデル・実参照・音声をGitへ追加していない．

## 保守と受入の範囲

既存のKarte検索実装，llama.cpp adapter，Identity／Profile，TTS worker回収を共用し，汎用tool loop・TTS server・推論kernelの自作を避けた．新たに有限candidate service，最終ticket検証，合成用Karte receiverのbuild／所有process管理，C ABI bindingと実資産hash確認が増えた．総保守量が減ったとの結論は出せない．

関連Python回帰140件と追加のfixture保全・ticket競合試験，repository validatorを実行した．最終件数は統合の報告を参照する．T-036は限定された記憶候補のみで，汎用agent全体の完了ではない．T-130はexperimental C API接続と回収までで，default採用と聴感は残る．T-125／129の記憶訂正ライフサイクル，T-127／128，分散Worker，LoRA，CUDAは完了として扱わない．
