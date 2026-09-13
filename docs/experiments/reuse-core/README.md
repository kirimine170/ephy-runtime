# 常駐版の実行部品と比較

通常会話と指摘の利用手順は[RESIDENT_FEEDBACK.md](../../RESIDENT_FEEDBACK.md)を参照する．常駐版は既存Wails画面とInteractionEngineを使う．旧実験のWeb画面，別candidate HTTP service，4モードの旧launcherは取り込まない．

現在の実測は[resident-results.md](resident-results.md)に記載する．[results.md](results.md)と[measurements.json](measurements.json)は`c509dfc`の2026-09-13の過去結果であり，今回の成績とは区別する．

## 最小の記憶参加を試す

以下は新しい専用状態へ合成資料だけを準備する．既存の個人記憶や会話はコピーしない．既にある資料や変更済み設定を上書きせず，対象が異なる場合はエラーで停止する．

```sh
cd /Users/kirimine170/Desktop/Ephy_Project/ephy-workspace/ephy-runtime-resident
../../local-runtimes/reuse-core/selected-venv/bin/python -m scripts.reuse_core.karte_fixture \
  --state ../../local-data/resident-feedback \
  --karte-root ../karte
```

`karte_fixture.prepare(state)`が合成7資料，Karte V1 policy，`participation-grants.json`を用意する．`build`は既存の固定Karte revision `90c69c58945e4eb00ec5f13c73b444ff298443d2`を一時archiveへ展開し，既存`contextcore.NewProcessor().ProcessPending()`だけを呼ぶ小さなreceiverをbuildする．Karteのcheckoutや検索処理は変更しない．receiverは専用launcherが起動・回収し，通常Karte GUIは必要ない．

観測用合成資料の参加者IDは`owner`であり，架空の花さん・雪さんを自動検出する機能ではない．所有者を選び，観測許可を明示して「観測中」を開始し，「公園のピクニック」を観測として試す．根拠付き候補が返るが，observeでは再生しない．「会話へ参加」では同じ条件の候補を期限・最新設定・発話状態に応じて採否判断する．「夏の旅行」は追加検索が必要な不足ケースで，現在の固定workflowは安全な空候補へ縮退する．

Runtimeは固定grantsのprojects／tags／sensitivityとdoc ID／全文hash／参加者共有許可を二つのKarte toolへ渡す．モデルがscopeを変更する引数は受け付けない．未知の話者をownerとして扱わない．grantsがない場合は候補機能だけunavailableとなり，通常会話を妨げない．

一回の検索・最大3取得・最大1モデル要求，tool結果16 KiB，候補180文字，run最大60秒である．モデルはGatewayの設定済みWork routeを使い，loopback llama.cppに限定する．通常chatの自動RAGを再注入しない．Identity／Profileと型付きfeedback policyは既存処理を共用する．foreground推論が始まれば背景候補を取り消す．

生成候補は15秒の一回限りticketを返す．発話直前の`/v1/resident/candidate/check`でgrants・最新policy・参加者に加え，実Karteを再readして版を照合する．その後Desktopが現在の観測revision／voice epochを再確認して既存SpeechUnitへ渡す．取消・参加者変更・期限切れ・訂正後の資料は採用しない．

## 短い再検証

すべて合成入力であり，出力先には未使用のGit外pathを指定する．通常モデルserverは明示して読み取り共有し，これらのprobeは起動・停止しない．

```sh
../../local-runtimes/reuse-core/selected-venv/bin/python -m scripts.reuse_core.resident_probe \
  --native-karte-state ../../local-data/resident-feedback \
  --cases simple,missing,additional_search \
  --output ../../logs/resident-feedback/reuse-probes/karte-native-new-run

../../local-runtimes/reuse-core/probes-venv/bin/python -m scripts.reuse_core.pipecat_probe \
  --output ../../logs/resident-feedback/reuse-probes/pipecat-new-run.json

TMPDIR="$PWD/../../local-data/resident-feedback/reuse-probes" \
../../local-runtimes/reuse-core/venv/bin/python -m scripts.reuse_core.tts_probe \
  --config ../../local-data/resident-feedback/reuse-probes/speech-config.json \
  --limit 2 --cancel --timeout 120 \
  --output ../../logs/resident-feedback/reuse-probes/audiocpp-new-run
```

`resident_probe`は独自にreceiverを一つ所有するため，常駐launcherを停止した状態で実行する．Karte検索／取得は実receiver，資料は合成，モデルは実ローカルWorkである．`--native-karte-state`を省略した契約切り分け経路は合成protocol responderとなるため，実Karte成績とは区別する．

Pipecatは固定sourceの実frameworkでframe制御を試すが，マイクや物理speakerは使わない．既存のlocal ASR→Work→TTS往復のprobeは`voice_roundtrip.py`に保持し，過去実測は旧結果へ残す．Pipecatを通常アプリのruntime依存へ加えていない．

audio.cppの検証用configは既存の許諾済みreferenceと固定model/libraryを参照する．既存の声のdefaultを変更せず，`voice-irodori-audiocpp-exp`を明示選択する．`prepare_audio.py`と`config.example.json`は再構築用であり，placeholder hashが許諾や実資産を表すものではない．実参照・GGUF・WAV・private設定はGit外に保つ．推論中のdownloadやcloud fallbackはない．Metal試験がsandboxで失敗する環境では，権限を持つTerminal／アプリから同じcommandを実行する．

## 依存と回帰

既存Python依存は変更しない．Pydantic AIは`selected.lock.txt`，Pipecat等の比較専用依存は`probes.lock.txt`，Piは専用npm lockへ分離して保持する．現常駐の固定workflowはこれらSDKをimportしない．audio.cppは公式C ABIの薄いbindingと既存のProcessWorkerを使い，Python推論kernelやTTS serverを新設しない．

```sh
../../local-runtimes/reuse-core/selected-venv/bin/python -m pytest -q \
  tests/test_resident_participation.py tests/test_resident_karte_fixture.py \
  tests/test_participation.py tests/test_participation_agent.py tests/test_audiocpp.py \
  tests/test_speech_service.py tests/test_irodori_tts.py \
  tests/test_karte_context.py tests/test_karte_conversation.py tests/test_model_transition.py
python3 scripts/validate_repository.py
```
