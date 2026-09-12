# ローカル日本語ASR

2026-09-13．ASR改善計画A〜Eの実装を常駐Whisper worker，途中表示，発話終了，割込み，アプリ配布へ接続した．人の自然会話による品質受入は未完了である．実装判断は[ADR-0015](adr/ADR-0015-local-whisper-asr.md)を参照する．

## 構成と保護境界

macOS Apple Siliconでwhisper.cpp v1.9.4，large-v3-turbo F16，Silero VAD 6.2を使用する．入力は8–48 kHz mono PCM16で，worker内の状態を保持する64 tap resamplerで16 kHzへ変換する．48 kHzを16 kHzとして扱わない．1発話は最大60秒，PCM chunkは64 KiB，JSON frameは96 KiBである．モデルは起動時にhash照合・load・無音推論によるwarmupを完了してからreadyを返す．ロード中は音声開始を無効にし，モデル名を表示する．

通常partialは500 ms，割込み候補は200 msの推論開始間隔を使う．推論時間が間隔を超える場合は重ねて実行せず，最新のPCMで次を実行する．有声後200 ms以上の余白までdecodeしたら，新しい有声が来るまで無音だけのpartial再推論を止める．入力終了は進行中partialをbackendの取消境界で打ち切り，finalを優先する．partialは直近8秒の置換可能な仮説であり，stable prefixを保証しない．finalは発話全体を再認識し，終端ACKを検証してから一度だけConversationへ渡す．前の発話，TTS，正解文をpromptへ流用しない．日本語指定，transcribe，温度0で，LLMのmode・reasoning設定は変更しない．

VAD activityは本文候補とは別に配送する．モデルが処理したsample位置で無音を判定し，未処理PCMが250 msを超える間は終端を確定しない．通常の無音900 msと100 msの猶予，言いよどみの1,800 ms待機を使う．無発話は`no_speech`としてLLM・canonical入力を作らず待受へ戻す．手動録音は上限で終了処理を通し，連続会話の60秒超過は従来どおり一時停止して候補をテキストへ戻せる．

割込みは低い音量でも候補を収集し，VADの有声判定と認識文を確認する．相槌だけでは元の回答を破棄しない．候補は最大1,800 ms，引継ぎPCMは最大3秒で，取消ACKを待って次sessionへ渡す．通常cancelではモデルを解放しない．worker異常終了は次のreadiness確認で再起動できる．モデル欠損・hash不一致・protocol異常は明示的なエラーとして止まり，Appleへ黙って切り替えない．

通常traceには入力sample数，clipping数，実効capture設定，VAD位置，終端理由，固定エラーだけを残す．PCM・認識本文・Apple例外の自由文を診断へ保存しない．Apple診断は固定domain分類と数値codeである．既存の録音非保存，取消と次入力の分離，Karte記録の境界を維持する．

## 再現と起動

必要なものはCMake，C++17 compiler，Go，Node，既存のdesktop依存関係である．モデルとupstream checkoutはGit外に置く．`ASR_ASSETS`，`ASR_SOURCE`，`ASR_CONFIG`は利用環境の絶対pathに設定する．ダウンロードは以下の明示操作だけで行う．

```sh
git clone https://github.com/ggml-org/whisper.cpp.git "$ASR_SOURCE"
git -C "$ASR_SOURCE" checkout 927cfce34f31707e17f2bff35c349632fb9e2c3a
python3 scripts/asr/prepare_assets.py --destination "$ASR_ASSETS"
python3 scripts/asr/build_helper.py --source "$ASR_SOURCE"
python3 scripts/generate_desktop_bindings.py
bash scripts/build_conversation_app.sh
python3 scripts/asr/configure.py \
  --helper "$PWD/desktop/build/bin/ephy-runtime.app/Contents/Helpers/ephy-whisper" \
  --model-dir "$ASR_ASSETS" --output "$ASR_CONFIG"
EPHY_ASR_PROVIDER=whisper-cpp EPHY_ASR_CONFIG="$ASR_CONFIG" \
  open -W -n "$PWD/desktop/build/bin/ephy-runtime.app"
```

既存のGateway・TTS起動設定も併用する．TTSの選択はASRと独立している．Appleへ明示的に戻す場合は，アプリ終了後に`EPHY_ASR_PROVIDER=macos-speech`で既存Apple helper設定を使って起動する．モデル切替はアプリ再起動時に行う．署名後のbundle helperが変わった場合は設定を再生成する．`runtime-build-provenance.json`はsource revision／dirty状態／最終実行ファイル／bundle helperのhashを記録する．helperのビルド情報とライセンスはbundle Resourcesへ含める．モデルはbundleへ同梱しない．

モデルの提供元revision・サイズ・SHA-256・licenseは[scripts/asr/assets.json](../scripts/asr/assets.json)が正本である．[Whisper](https://github.com/openai/whisper)と[Silero VAD](https://github.com/snakers4/silero-vad)はMIT，比較用[Kotobaモデル](https://huggingface.co/kotoba-tech/kotoba-whisper-v2.0-ggml)はApache-2.0として提供されている．

## 実測と限界

実機はM2 Max，64 GiB，macOS 26.5.2．公開FLEURS `ja_jp/test` の同一音声を使用し，モデルへ正解文を渡していない．100件は異なるdataset IDから2–15秒を選択した公開朗読であり，自然な往復会話ではない．20件のendpoint比較は2–10秒のsubsetである．文字はNFKC・小文字化・句読点と空白除去で比較し，数値・否定を消していない．未取得finalは全欠落としてCERへ含め，未試行は別計数にする．

| 試験 | 結果 | 判断の限界 |
|---|---|---|
| 公開朗読100件，Kotoba F16 | final 100/100，CER 6.98％，入力終了→final p95 476 ms | batch入力の処理時間 |
| 同じ100件，large-v3-turbo F16 | final 100/100，CER 4.80％，入力終了→final p95 520 ms | モデル選定の根拠．Apple比の改善率ではない |
| 公開朗読20件，実worker＋production endpoint | final 20/20，CER 4.64％．VAD最終有声→final p95 1,531 ms，最初の非空partial p95 505 ms．worker最大RSS約1.78 GiB | cadence最適化前の記録．時間基準はモデルVADで，人がannotateした発声境界ではない |
| 合成の無音・白色雑音・ファン・クリック40件 | 通常認識の`no_speech` 40/40，誤final 0 | 実生活の雑音40件の受入とは別 |
| 合成の短文・小声・日英混在9件 | final 9/9，CER 7.95％ | 日英混在と言いよどみで誤りが残る |
| 合成44.1／48 kHz各1件 | final 2/2，CER 0 | resamplerの周波数・chunk試験も別途実施 |
| 合成60秒の冒頭・末尾 | final取得，重要語の厳密表記3/4 | 冒頭と末尾のmarkerは両方保持．末尾の「紅葉」が仮名表記になった．判定基準は変更せず3/4とする |
| 最終helperで同じ公開朗読5件，単独 | final 5/5，VAD最終有声→final p95 1,494 ms，非空partial p95 486 ms | 20件の測定とは別のsubset再確認 |
| 同じ5件，Work連続生成のみ | final 5/5，CER 6.45％，VAD最終有声→final p95 1,595 ms，非空partial p95 606 ms | 既存GatewayのWork／voice経路を使用．reasoning overrideなし |
| 同じ5件，Irodori連続合成のみ | final 5/5，CER 6.45％，VAD最終有声→final p95 1,859 ms，非空partial p95 588 ms | Anime／明るくの合成．再生はしていない |
| 同じ5件，Work＋Irodori連続負荷 | final 5/5，CERは単独と同じ6.45％，VAD最終有声→final p95 2,599 ms，非空partial p95 850 ms | 最終推論優先の修正前は5,041 ms．2秒目標は未達 |
| 合成の割込み6件，最終helper | 意図した5件全検出，相槌の誤停止0，引継ぎ欠落・重複0．候補確定p95 1,081 ms | 実再生停止を測っていない．1秒目標は未達 |
| Apple比較 | providerが利用不可または権限未取得で試行0 | CER・相対改善率は算出できない |

最終helperの再確認，同時負荷試験，署名・起動結果の個別JSONはローカルの`recovery/ephy-runtime/asr-implementation-20260913`に置く．音声・正解文はGit外の明示的評価ディレクトリに限り，評価結果には本文を出力しない．FLEURSは[提供元](https://huggingface.co/datasets/google/fleurs)のCC BY 4.0データで，manifestに出典・入力hashを記録する．

## 検証手順と人による残件

通常回帰は`go test -race ./...`，frontend `npm test`，`pytest tests/test_asr_tools.py tests/test_runtime_build_provenance.py`，repository validatorである．C++ resamplerはhelper build内で`ctest`する．`EPHY_WHISPER_INSTALLED=1`と`EPHY_ASR_FIXTURE_MANIFEST`を明示して`TestWhisperInstalledEngine`を実行すると，実helperでWAV入力，無発話，取消，次入力，final一度のみの採用を試験する．マイクやspeakerは開かない．Appleのresult/error優先順位はhelperの`--policy-self-test`で権限照会なしに検査する．

実装時の回帰結果はFrontend 299 pass，Go全体のrace検査pass，Python 620 pass・3 opt-in skip，C++ resampler pass，repository validator passである．Pythonのうち4件は外側のsandboxがmacOS `sandbox-exec`の起動を拒否したため通常環境で再実行してpassした．Wails bindingは2回生成して6 filesのhash一致を確認した．実モデル・人の受入とは別の結果として扱う．

`scripts/asr/evaluate.py`は明示manifestのbatch／realtime比較，`evaluate_endpoint.mjs`はproduction endpoint，`evaluate_interruption.mjs`はproduction割込み判定とPCM引継ぎを実workerへ接続する．実録音で使う場合は，事前に発話・場所・保存対象を合意し，同意されたfixtureのみ渡す．通常会話を評価用に常時保存しない．

人の操作が必要な残件は以下をまとめて実施する．

1．ASRのready表示を確認し，Irodori Anime／明るくで音声開始を押す．普段の短文，数字の訂正，否定，固有名詞，日英混在，言いよどみ，小声を試す．発話終了前の切断と，表示したfinalの内容を確認する．
2．回答待機中・読み上げ中に「待って」「違う」「はい，でも…」を発話し，実際の停止までの時間と，次入力の冒頭が残ることを確認する．無音・相槌・物音の誤停止も記録する．
3．同意した60発話を40調整／20未使用評価に分け，Appleと同じPCM・区間で比較する．実マイク100有声，40無発話，重要語正答率，再発話・手修正数を評価する．

品質目標は変更しない．自然なclean発話CER 10％以下を目安，Apple比30％改善を目標，重要語95％以上，有声final 99/100以上，無発話誤入力0/40，warm最終発声→final p95 2秒以内，有用partial p95 1.5秒以内，明示割込み95％以上かつ発声→再生停止p95 1秒以内である．未達時は同じ評価条件で推論方式・候補decodeの改善，WhisperKitなどを比較する．公開朗読・合成の合格で自然会話の受入を代替しない．
