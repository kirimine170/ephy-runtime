# Local voice providers for the interaction loop

## Status

Accepted．C0実装．ASRの実音声認識とmicrophone／speakerを含む対話UATは未検証．

## Context

C0のinteraction loopには，実際のASR／TTS providerと，環境にproviderがない場合の明示的な失敗が必要である．conversationの内容，音声，ローカルファイルpathをdiagnosticやtraceの識別子へ混在させない．CIに音声モデル，macOSの権限，実際の録音・再生を要求しない．

## Decision

初期providerはmacOSのApple Speechと，インストール済み音声を使う`/usr/bin/say`である．cloud ASR／TTSへのfallback，modelのdownload，自動的な権限設定の変更は実装しない．他OSでは`asr_unavailable`／`tts_unavailable`を返す．

ASR helperのsourceは`desktop/voice/EphyASR.swift`に置き，`bash scripts/build_voice_provider.sh`で`bin/ephy-asr`を生成する．実行形式には`desktop/voice/Info.plist`のSpeech usage descriptionを埋め込む．helper自身はmicrophoneを開かず，stdinから受けたmono PCM16 WAVをメモリ上の`AVAudioPCMBuffer`へ変換する．入力上限は8 MiB／60秒，sample rateは8，000〜48，000 Hzである．

`--check --locale ja-JP`は権限状態，`supportsOnDeviceRecognition`，`isAvailable`を確認し，権限を要求しない．権限未決定の場合は`permission_required`をstdoutへ出して成功する．実際のtranscriptionが開始された場合だけ，必要に応じてmacOSのSpeech権限を要求する．認識要求は常に`requiresOnDeviceRecognition = true`とし，端末内認識が利用できなければ停止する．最終transcriptだけをstdoutに返し，stderrにはallowlistされた固定error codeだけを出す．Go側でも未知のdiagnosticを`asr_failed`へ置き換える．

TTSは確定済みの発話単位を，最大180 Unicode runeのchunkへ分割する．上限内では文末（句点・疑問符・感嘆符・改行），読点・空白，rune境界の順に優先する．句読点のない16，000 rune入力も有限長へ分割し，空chunkや不正UTF-8を作らない．未確定の生成末尾をこの分割処理へ渡さない．各chunkをstdinで`/usr/bin/say`へ渡し，22，050 Hz／mono／PCM16の独立したWAVを生成する．textはprocessの引数へ渡さず，`say`の埋込み命令delimiterを除去する．一時directoryは0700，WAVは0600で作成する．生成結果のformatと長さを検証し，音声fileを削除してからcallbackへ渡す．失敗とcancel時もcleanupを行う．callbackは再生を担当せず，interaction loopがplayback queueを管理する．

ASR processは45秒，TTSの各chunkは30秒，準備確認は5秒を上限とし，親contextのcancel／deadlineを引き継ぐ．processのcancelは`exec.CommandContext`で実行する．raw audioをrepository，conversation history，traceへ保存する経路はproviderに設けない．TTSの一時fileだけが短時間存在する．

録音開始前にprovider共通のreadinessを確認する．`ready`と`permission_required`は開始可能，`unavailable`は固定error codeとともに開始不可とする．未許可のSpeech権限を許可済みとは表示しない．Native providerはinstance／configuration／実行file identity単位で並行するreadiness probeを共有し，ASR readyは5秒，permission_requiredは1秒，TTS readyは30秒で失効する．失敗をcacheせず，認識・合成失敗や実行file変更後に再検証する．権限の外部変更は有限TTL後のprobe，および認識失敗で検出する．

設定は以下に限定する．無効な構文は`invalid_voice_config`で停止する．

| 環境変数 | 既定値 | 制約 |
| --- | --- | --- |
| `EPHY_VOICE_LOCALE` | `ja-JP` | 2〜3文字の小文字languageと2文字の大文字region．`-`と`_`を許可 |
| `EPHY_ASR_HELPER` | runtime rootの`bin/ephy-asr` | 実行権限のある通常fileへの絶対path |
| `EPHY_TTS_VOICE` | `Kyoko` | ASCII英字で開始し，英数字・space・丸括弧・`_`・`-`だけの80文字以内．localeと一致するインストール済みvoiceが必要 |

provider identityは`macos-speech`／locale／`on-device`と，`macos-say`／voice／`pcm16-22050-<locale>`である．helperや一時fileのpathは含めない．

## Validation and limitations

2026-09-06のmacOS 26.5.2／Xcode環境でSwift helperのcompileと，権限を要求しない`--check`の成功を確認した．通常のmacOS serviceアクセスでは結果が`permission_required`であり，on-device supportとavailabilityは利用可能だった．初回のSpeech許可は未実施であり，ASRによる実transcript取得を成功扱いにしない．

実機Kyokoによる合成文「テストです．」から，38，462 bytesのWAVをGo adapter経由で生成した．音声dataは34，366 bytes，PCM16／mono／22，050 Hzであり，callback時の一時file削除を検証した．音声の録音と再生は行っていない．

実行sandboxからmacOSのSpeech serviceへのアクセスが制限されると，ASR準備確認は`asr_permission_denied`を返し，`say`はexit 0でも音声dataが空のWAVを生成する場合がある．adapterはこの空WAVを`tts_invalid_audio`として拒否する．実機smokeはmacOS serviceアクセスが可能な環境で行う．

通常testはprocess runnerの注入を使い，権限不要のreadiness，stdin境界，format検証，locale／voice検証，chunk長，errorの秘匿，cancel，一時fileの権限とcleanupを検証する．実機合成だけを追加する場合は，`desktop`で`EPHY_VOICE_INTEGRATION=1 go test -run TestNativeVoiceTTSInstalledSmoke -v .`を実行する．このtestにも録音と再生は含まれない．

## Source evidence

実装時にlocal Xcode SDKの`Speech.framework/Headers/SFSpeechRecognizer.h`と`SFSpeechRecognitionRequest.h`を確認し，on-device強制，availability，authorization APIを照合した．`man say`のinstalled voices，stdin入力，WAVE，linear PCM，sample rate，channel指定を確認し，実機出力でformatを検証した．

## Date

2026-09-06．

## C0.3.1 stabilization／2026-09-07

ASR live callbackの直列化gateはprovider共有からsession専用へ変更した．callbackのpanic・timeoutを固定errorとしてprocess停止前に確定し，旧callbackが停止したままでも後続sessionは独立して進行する．明示cancelと親context cancelの後に非同期failure通知を開始しない．すでに実行中の任意のGo callbackを強制終了することはできないため，受信側のoperation／session／turn／segmentとgeneration revisionの照合を維持する．旧callbackが復帰しても旧turnのpartial・final・audio・failureを新turnへ採用しない．

Speech serviceのdefault profileをcombined catalogと省略時の選択へ反映する．構成済みserviceのcatalog未確認時，defaultや選択中profileの利用不能・消失時には暗黙の別voice選択を行わず，text fallbackと明示的なvoice再選択を残す．active operationのprepared profileとcontinuation契約は維持する．

独立TTS inference serviceの全HTTP routeは`EPHY_TTS_BEARER_TOKEN`による認証を必須とする．secretを設定していないserviceは起動せず，未認証requestは401，browser Originと非loopback Hostは403で拒否する．bindは`127.0.0.1`を維持し，Runtimeの接続はloopback IPのHTTPだけとし，proxyとredirectを使用しない．secretはUI・trace・logへ出さず，inference子processへも継承しない．同一OS user／管理者からのsecret読取りを防ぐOS隔離ではない．private fileの生成，両processへのsecret設定，確認手順は[`custom-voice-tts.md`](../custom-voice-tts.md)を参照する．

Go公開APIのsignature，Wails bridgeと公開DTOは変更していない．ASR live partial，final-only commit，GenerationProgress，continuation，ResponsePlan，private asset storeの契約を維持する．Irodori TTSとC0.4は対象外である．
