# 常駐worktreeの部品検証

2026-09-14，`ephy-runtime-resident`で実行した結果．既存比較commit `c509dfc`の実測と，新規実行を混ぜない．通常会話・feedbackの検証は[常駐手順](../../RESIDENT_FEEDBACK.md)に記載する．

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
