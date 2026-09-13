# Reuse core検証

固定の記憶検索とPydantic AI，Python Irodoriとaudio.cppを切り替え，同じ合成会話を再生する検証アプリである．通常Conversationは既存アプリに残す．新しい会話観測入口は，検証バイナリの`--reuse-experiment`から起動する小さなローカル画面である．採否と計測は[results.md](results.md)，数値の各試行は[measurements.json](measurements.json)を参照する．

## ブランチとbaseline

| repository | branch | base |
|---|---|---|
| ephy-runtime | codex/spike-ephy-reuse-core | 211e681f9153d0e332ded42f0d9ed2cec0f2ae4e |
| ephy | codex/design-ephy-reuse-core | 03859c627e80e79533ef69dc9ca8afc6a8bbdda4 |

開始時はいずれもclean．通常runtime checkoutは`fix/integration-lifecycle`，`042c3a7c237b034cb8afcc8654956a0d46b08b4b`でcleanだった．その5件の未merge修正を保全し，origin/mainからworktreeを作った．通常config／session／Karte記憶をfixtureへコピーしていない．Reference Architecture PDFと既存ASRの目標は変更していない．

baselineの記憶確認は既存Karte契約と既存llama.cpp adapterによる検索→取得→候補生成である．旧版に存在しなかった能動参加用の入口を追加しており，旧版の通常Conversationそのものを再現する構成ではない．Goの生成・TTS・再生・C2変換を共用する．

## このMacで試す

Workの既存サーバー`http://127.0.0.1:8082/v1`で`qwen3.8-27b`を使用する．ランチャーはこのサーバーを起動・停止しない．合成データの推論先として明示的に共有する．

```sh
cd /Users/kirimine170/Desktop/Ephy_Project/ephy-workspace/ephy-runtime-reuse
./scripts/reuse_core/run-local.sh combined
```

[検証画面](http://127.0.0.1:18880)を開く．同じコマンドの最後を変更して比較する．

| mode | 記憶確認 | TTS |
|---|---|---|
| baseline | 固定workflow | 既存Python Anime FP32／MPS |
| selected | Pydantic AI | 既存Python Anime FP32／MPS |
| audio | 固定workflow | audio.cpp Anime Q8／Metal |
| combined | Pydantic AI | audio.cpp Anime Q8／Metal |

一度に一構成を起動する．`Ctrl-C`で検証アプリと専用serviceを停止してから切り替える．停止処理は自身の子PIDだけをterminate→wait→必要時killし，通常アプリや共有Workを停止しない．元の環境へ戻す操作はこの停止だけでよく，通常checkoutの変更や設定復元は不要である．

専用ポートはTTS `18867`，候補service `18868`，画面 `18880`．専用状態は`local-data/reuse-core/app-<mode>`，PID台帳とtraceもその下に置く．モデル・参照音声は既存の許諾済み資産を読み取る．private configは`local-data/reuse-core/speech-config.json`，計測とWAVは`logs/reuse-core`，venv／バイナリは`local-runtimes/reuse-core`にあり，いずれもGit外である．画面に通常のKarte全文を入力する入口はない．

## 画面での確認

1. 「索引から追加検索が必要な旅行」を再生する．明示speaker ID付きの合成観測を時系列で流し，最終観測から一度だけ検索を始める．候補と出典ID／版が表示される．
2. 候補作成中に「人が話し始める」を押すと候補を待機させる．「会話に間が空く」で有効な候補だけを話す．待機期限は候補到着から15秒，cooldownは8秒である．
3. 候補作成中に参加者変更／話題変更／停止を押す．旧revisionの結果は発話しない．相槌ボタンは割込みを送らない．これは実マイクの相槌分類器ではない．
4. 再生中に停止する．ブラウザの再生を先に止め，開始済み区間のinterrupted ACK後にRuntimeを取消す．再生結果でcompletedとinterruptedを区別する．
5. 「邪魔だった」「この話は今しないで」は同一セッションの次候補を抑制する．自然さの実評価は利用者が行う．自動操作の成功を好意的評価として登録していない．

C2は既存`recordedAssistant`の変換をそのまま表示する．今回の合成記録を通常Karteへ永続化しない．候補全文を発話済み記憶に変換せず，実際の再生ACKだけでspeech unitのdeliveryを確定する．物理speaker停止時間の計測は未実施である．

## 環境の再構築

Python 3.12，Go，CMake，Apple Command Line Toolsを使用する．Pi比較のみNode >=22.19が必要である．検証時はNode 25.2.1．通常依存は`runtime.lock.txt`，選定したagentの追加依存は`selected.lock.txt`，Pipecatを含む比較専用環境は`probes.lock.txt`に分離する．Piは専用ディレクトリのnpm lockを使う．通常`pyproject.toml`へ比較候補の依存を追加していない．

以下の例は上記worktreeをcwdとする．既存Python Irodori環境は従来の[provider手順](../../irodori-tts-provider.md)に従う．

```sh
uv venv --python 3.12 ../../local-runtimes/reuse-core/venv
uv pip install --python ../../local-runtimes/reuse-core/venv/bin/python -r scripts/reuse_core/runtime.lock.txt -e .
uv venv --python 3.12 ../../local-runtimes/reuse-core/selected-venv
uv pip install --python ../../local-runtimes/reuse-core/selected-venv/bin/python -r scripts/reuse_core/runtime.lock.txt -r scripts/reuse_core/selected.lock.txt -e .
../../local-runtimes/reuse-core/venv/bin/python -m scripts.reuse_core.prepare_audio \
  --assets ../../local-runtimes/reuse-core \
  --source-config ../../local-data/speech/irodori/service-config.json \
  --output-config ../../local-data/reuse-core/speech-config.json --download-model
npm --prefix desktop/frontend ci
npm --prefix desktop/frontend run build
go build -C desktop -buildvcs=false -tags 'desktop,wv2runtime.download,production' \
  -o ../../../local-runtimes/reuse-core/ephy-reuse .
```

[設定断片の例](../../../scripts/reuse_core/config.example.json)も置いている．0で埋めたhashと絶対pathはplaceholderであり，許諾を表さない．実際の設定はprepareで既存の許諾済みprofileから生成する．

prepareは固定commitを取得し，公式C APIをMetalでbuildし，GGUFとlibrary／依存dylibのhashを検査する．既存configを上書きせず，比較用configを0600で生成する．推論中のdownloadや外部fallbackはない．build manifestの`dev`は固定commitの実際のbuild文字列であり，v0.7.4と読み替えない．`speech-config-reproduced.json`を生成する再実行でも同じprovider設定を確認済みである．

## 自動再生と計測

候補比較は全て同じ5件の合成資料，scope，Workモデル，thinking有効，preserve_thinking有効，temperature 0.3，出力上限2048，期限60秒で行う．動的runはモデル4回，tool6回，tool結果16 KiBまで．scopeや共有許可はRuntimeが固定し，tool引数から変更できない．run historyは保持しない．

```sh
uv venv --python 3.12 ../../local-runtimes/reuse-core/probes-venv
uv pip install --python ../../local-runtimes/reuse-core/probes-venv/bin/python -r scripts/reuse_core/probes.lock.txt -e .
npm --prefix scripts/reuse_core/pi ci
../../local-runtimes/reuse-core/probes-venv/bin/python -m scripts.reuse_core.memory_probe \
  --engine pydantic --output ../../logs/reuse-core/pydantic-repeat.json
../../local-runtimes/reuse-core/probes-venv/bin/python -m scripts.reuse_core.pipecat_probe \
  --output ../../logs/reuse-core/pipecat-repeat.json
../../local-runtimes/reuse-core/venv/bin/python -m scripts.reuse_core.tts_probe \
  --config ../../local-data/reuse-core/speech-config.json \
  --corpus scripts/voice_ab/corpus.json --limit 24 --cancel --timeout 120 \
  --output ../../logs/reuse-core/audio-repeat
```

`memory_probe --engine fixed|pi`も同じ入口を使う．TTSのPython比較は`--profile voice-irodori-anime-exp --python ../../local-runtimes/irodori/Irodori-TTS-Server/.venv/bin/python`を追加する．コーパス内の29句を順に送り，長文を落とさない．初回はworker起動とlazy model load込み，warmは常駐workerへの句送信からWAV到着まで．独立したmodel-ready信号はないためloadと推論を分離した数値は出さない．

実音声のPipecat往復は次で再生する．`asr.local.json`は既存[ASR評価手順](../../LOCAL_WHISPER_ASR.md)のhelper／model／VAD hash付きconfigで，移動後の実在pathを設定する．`--work-pid`は共有Workの実PIDを確認した場合のみ指定する．

```sh
../../local-runtimes/reuse-core/probes-venv/bin/python -m scripts.reuse_core.voice_roundtrip \
  --config ../../local-data/reuse-core/speech-config.json \
  --asr-config ../../local-data/reuse-core/asr.local.json \
  --input ../../logs/reuse-core/audio-metal/cold-0.wav \
  --output ../../logs/reuse-core/voice-repeat --concurrent-load
```

## 回帰確認

```sh
../../local-runtimes/reuse-core/selected-venv/bin/python -m pytest -q \
  tests/test_participation.py tests/test_participation_agent.py tests/test_audiocpp.py \
  tests/test_speech_service.py tests/test_irodori_tts.py tests/test_karte_context.py tests/test_karte_conversation.py
go test -C desktop -race ./... -run 'TestCandidate|TestPreparedCandidate|TestInteraction|TestRecorded|TestVoice'
python3 scripts/validate_repository.py
```

Pipecatは採用していないため通常アプリ起動に不要である．frame試験のepoch／ACK adapterはprobe専用で，Goの本番turn ownerと並列に動かさない．候補採否の状態管理はGoだけに置き，Python側はrunのscope・予算・根拠検査に限定する．
