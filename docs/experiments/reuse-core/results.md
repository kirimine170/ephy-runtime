# Reuse core検証結果

2026-09-13，Apple M2 Max，64 GiB，macOS arm64で実行した．これは検証ブランチの技術結果であり，通常経路への一括採用を意味しない．試行ごとの数値は[measurements.json](measurements.json)，起動方法は[README](README.md)に記載する．

## 部品別の判定

| 部品 | 判定 | 実行で確認したこと | 採用までの残件 |
|---|---|---|---|
| 固定workflow | baselineとして維持 | 共有経験の取得，根拠なしでLLMを呼ばない，安全な空候補 | 索引から検索語を変える例には到達できない |
| Pydantic AI slim | 動的検索の実験経路に選定 | 同じモデルで索引→追加検索→正しい版の取得．既存Pythonへ直接接続 | 該当なしの早期終了，timeout率，歓迎される言い方．通常採用は未承認 |
| Pi agent-core | 比較を完了し，統合には不採用 | Pydantic AIと同じ追加検索に到達．限定toolと取消が可能 | この課題では別Node環境とIPCを持つ利点を実証できなかった |
| Pipecat | 現時点では不採用 | 実frameworkのframe試験とlocal ASR→Work→TTSの一往復 | Goの再生・C2を置換できず，epoch／ACK adapterが増えた．保守削減は未実証 |
| audio.cpp | experimental providerとして実験継続 | 参照音声付きMetal生成，24ケース，実worker取消・回収・再起動 | 聴感，実speaker停止時間，長期負荷，CUDA．defaultにはできない |

LangGraph，OpenAI Agents SDK，LiveKit Agentsは資料上の候補にとどめ，実行比較していない．単一PCでの狭い検索課題に追加の実行環境や永続graphが必要な根拠が得られていない．

## 記憶確認の比較

同じ5件の合成fixtureと明示共有許可を使用した．Karteの型付きresponseとscope照合を再利用し，全文hashと引用の整合を検証する．検索可能でも共有不可の記録，訂正前の記録はモデルへ渡さない．固定処理は検索1回，取得最大3件，候補生成1回．動的処理はモデル4回，tool6回まで．全構成でWork，thinking有効／保持，temperature 0.3，出力2048 token，60秒期限をそろえた．固定処理は文書を直接promptへ渡し，動的処理はtool結果を経由するため，構成全体の比較である．

| fixture | 固定workflow | Pydantic AI | Pi |
|---|---|---|---|
| 公園のピクニック | 正しい出典，52.54秒，model 1／tool 2 | 正しい出典，36.00秒，3／2 | 正しい出典，45.30秒，3／2 |
| 索引→青い灯台 | 根拠不足として空候補，25.48秒，1／2 | 正しい出典，46.07秒，4／3 | 正しい出典，42.63秒，4／3 |
| 該当記憶なし | 空候補，0.0002秒，0／1 | 予算上限，17.19秒，4／4 | 予算上限，17.52秒，4／4 |
| 訂正後の記録 | timeout，60.01秒，1／2 | 正しい出典，41.88秒，3／2 | 正しい出典，41.91秒，3／2 |
| 本文に操作要求 | timeout，60.00秒，1／2 | timeout，60.00秒，3／2 | 正しい出典，57.20秒，3／2 |

各セルn=1．各runは順次実行したが，共有Workの背景負荷とキャッシュを排他的に固定していない．この表からframework自体の速度順位や安定した成功率を出さない．追加検索の到達可能性と，Python統合時の負担を根拠にPydantic AIを選んだ．固定処理の空候補は検索到達の失敗であり，出来事の捏造ではない．動的処理の予算上限も成功に数えない．

モデルが生成した内容の自然さや断定の強さは出典一致だけでは保証できない．実行比較の出典合格は，歓迎される発話の合格ではない．比較後に句読点指定と，SDK引数検証前のtool試行数計上を補強した．wire／取消試験で補強を確認したが，上表全体の再測定はしていない．

Python parentのpeak RSSは固定52.8 MB，Pydantic AI 120.6 MB，Pi親44.7 MBだった．PiのNode childと各構成のモデルサーバーを含まないため，総メモリ比較には使わない．

## 音声の比較

旧13ケースの候補として，Git外の`logs/archive/irodori-evaluation/step3_6_manifest.json`にduration 6件＋reading 7件を確認した．T-130が指す旧比較と同一である確証は得られていない．実referenceを含むmanifestをGitへ複製せず，今回は既存`scripts/voice_ab/corpus.json`の24ケース／29句を共通条件として使用した．過去13ケースの再現とはしない．

同じ許諾済みreference group，有限のneutral style，seed 420，40 steps，CFG text／caption／speaker = 3／3／5，pace 1，auto durationを使用．既存正規化と句境界を共用した．長文もコーパスの各句を順番どおり送り，黙って切り捨てていない．Python Anime FP32＋MPSと配布Anime Q8＋Metalの比較であり，量子化差を含む．高精度GGUFへの独自変換は実装していない．codecはMetalと同じbackendであり，CPU混合の数値ではない．

| 指標 | Python Anime FP32 | audio.cpp Anime Q8 |
|---|---:|---:|
| 共通コーパス | 24ケース／29句成功 | 24ケース／29句成功 |
| process cold first playable，n=1 | 19.245秒 | 7.105秒 |
| warm first playable，n=28，中央値 | 4.531秒 | 3.375秒 |
| warm first playable，最小–最大 | 3.638–7.234秒 | 2.496–5.762秒 |
| warm RTF，中央値 | 0.834 | 0.632 |
| cancel→旧worker回収，n=1 | 194.3 ms | 25.7 ms |
| cancel後の次request→WAV，n=1 | 13.985秒 | 7.196秒 |

coldはworker起動とlazy model load込みであり，OS page cacheやMetal shader cacheのcoldを保証しない．別の初回smokeではaudio.cppのcoldが26.973秒，warm 3.702秒だった．この差もraw記録へ残す．p95は出していない．取消試験は生成開始200 ms後に取消し，旧PIDの終了コード-15とtemporary削除を実確認した．HTTP切断だけをGPU停止とみなしていない．

WAVは有限PCM，mono，sample rate，長さを検査し，result所有のPCMを解放前にコピーする．C APIはofflineなので最初のWAV到着まで句全体を待つ．Runtimeのspeech unitとC API内部chunkは同一とみなさない．`endline`＋句長のoptionを使うが，内部で改行を分割する余地は残る．

余分な末尾，読み，固有名詞・数字・否定，継ぎ目，声の一貫性，style過剰の聴感受入は未実施．生成音声はGit外に保存済みで，利用者が同じコーパスで比較できる．

## Pipecat，統合，同時負荷

実Pipecatのsynthetic frame試験ではfinal context 1回，旧epoch audio 1件破棄，completed／interruptedの対応を確認した．interruption frame処理12.1 msはソフトウェアのqueue処理であり，実speaker停止時間ではない．相槌はEphy側がinterruptionを発行しない入力で，音声分類精度を試していない．

実whisper.cpp＋large-v3-turbo F16＋Silero v6.2，既存Work，audio.cppを接続した一往復では，Pipecatがfinal contextとaudio frameを各1回受け取った．再試行のASR load／warmup 1.292秒，ASR final 411 ms，Work 70.618秒，TTS first playable 9.631秒．合成入力を使用し，マイクと物理再生ACKは使用していない．

同時負荷は各componentへ1要求を同時投入したn=1の小試験である．ASR 1.393秒，Work 44.620秒，TTS 3.788秒で全て完了した．250 ms間隔で読んだ3 processの合計RSS最大は3.127 GB．これはmacOSのresident表示で，GPU割当や総unified memory消費を表さない．queue深さの実測や長期安定性は未確認で，既存ASR目標を満たしたとは宣言しない．

初回の同時負荷は試験コードが終了済みASR operation IDを再使用し，helperが`asr_stream_invalid`で終了した．連続負荷を停止して原因を確認し，各requestへ別IDを発行して再試行した．ASR本体の契約を緩めていない．初回失敗のraw metricsも`logs/reuse-core/voice-roundtrip`に残す．

combinedの検証画面では，Pydantic AIが追加検索した`fixture-lighthouse`の候補を既存Go生成経路へ渡し，audio.cppで合成，ブラウザで再生し，既存C2のgeneration／playbackともcompletedを確認した．候補を再度LLMに質問しない．更新後の画面ではspeaker ID付き再生と待機期限による破棄，生成中の参加者変更による破棄も確認した．

## 依存と保守の増減

| 対象 | 固定値／根拠 |
|---|---|
| Pydantic AI slim／graph | source `5cbacfc8f86d653baa0ca2e31970cbf4f0fcec95`，slim `2.43.1.dev1+5cbacfc8`，MIT |
| OpenAI client／HTTP | `openai 3.13.0`，`httpx2 2.12.0`．明示loopback，proxy／redirect／retryなし |
| Pi | `@earendil-works/pi-agent-core`／`pi-ai 0.85.1`，npm lockのintegrity，MIT．CLIやshell拡張を載せない |
| Pipecat | source `f67c18afddbfb0609991cd6830355713baaad01b`，`1.10.0`，BSD-2-Clause |
| audio.cpp | source `ff1bcc4555ff99c4383329b0b21b52a18cc8b3cd`，Apache-2.0，C ABI 256，build version `dev` |
| 実library SHA-256 | `3b57bfa4c0dbf77ccd99d7161da9cc2a96706f85d9f0857ef7ba35f020b3e5d7` |
| GGUF配布revision | `audio-cpp/audio.cpp-gguf`，`1509fa38c945c13ffd645d8def423f0c4cb21254` |
| Anime Q8 artifact | 1，112，547，264 bytes，SHA-256 `cef628aa1ce784993025789c24d05b4b9e60fa039d40453bc8bdea32f4cb99e7` |
| Work artifact | Qwen3.8-27b Q4_K_M，17，106，775，008 bytes，SHA-256 `7e78da5d7e3ae28d178121f58646953305f3e5bd3cb46f4a75584e8b6c6fe169` |
| Work template | SHA-256 `12827f24b742ea4e80cdc12dbcf9622227056b9f797252a3149263d4f9aaadce`，context 8192，4 slots |
| ASR | whisper.cpp 1.9.4，large-v3-turbo F16 SHA-256 `1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69`，Silero v6.2 SHA-256 `2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987` |

モデル／codec／referenceの条件はrepository licenseと別である．GGUF配布repositoryのcardは`license: other`で，収録モデルごとの条件確認が必要．Anime元モデルとcodecの既存確認は[provider manifest](../../irodori-tts-provider.md)を引き継ぐ．変換artifactの出自はHF revisionとhashで記録するが，全変換過程を独立再現したとはしない．参照音声の許諾・provenance・digest照合を維持し，実音声やpermission evidenceをGitへ入れない．

新しい推論kernel，TTS HTTP server，汎用tool loop実装を避けた．参照groupの検証と一時WAV作成は純粋helperへ抽出し，両providerで共用する．取消・timeout・disconnect・連続取消・temporary回収は既存ProcessWorkerの一つの実装を使う．Pydantic AIは一時historyとmodel/tool dispatchを担当し，scope・共有許可・根拠検査はRuntimeが担当する．

一方，C ABI adapter，hash付きbuild/config準備，selected用venv，小さな候補serviceと検証画面・ランチャーが増えた．Pipecatの採用でGo制御を削除した箇所は0であり，adapter追加を保守削減とは数えない．現時点で総保守量が減ったとの結論は出せない．

## 回帰と受入の範囲

Pythonのscope・版・引用・期限・取消・SDK wire／不正引数・C pointer lifetime・speech service・Karte関連試験124件が合格した．SDK試験は実際の固定SDKとMockTransportを用い，null contentのtool call，順序，取消によるHTTP処理の終了を確認する．実モデル試験とは別の根拠である．Goの候補経路・再生・既存音声制御はrace付きで確認した．frontend buildと検証バイナリbuild，両repositoryのvalidatorも合格した．4モードの起動時profileと停止後の3専用ポート解放を確認し，切替直後のTIME_WAITにより起動確認が失敗する問題をランチャー側で修正した．Wailsの公開App bindingは変更していない．

T-036は限定された記憶検索tool loopと候補検証を確認した範囲だけであり，汎用Agent全体の完了ではない．T-130はC API／既存workerによるexperimental接続，参照付きMetal生成，回収・復帰を確認した範囲だけであり，聴感とdefault採用は残る．T-125／T-129の記憶ライフサイクル，T-127／T-128の継続探索，分散Worker，LoRA，実マイク相槌判定，RTX 3090 Ti CUDAは未実施である．公開，push，merge，deployは行っていない．
