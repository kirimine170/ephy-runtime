# ADR-0012：Custom Voice TTS

- Status：C0.3 implementation decision．voice identityの実音声評価と，合成fixtureによる実装検証を区別する．
- Base：C0.1安定化`5cdea6f`，C0.2`41b0731`の上に独立したC0.3変更を追加する．
- Authorization：C0.3の実装とcommit／push／PR／mergeが明示的に承認された．ADR-0009／0010／0011の旧Gate制限を，この範囲で更新する．C0.4以降は開始しない．

## 責務と契約

RuntimeはControl Planeを維持し，LLM／Reasoner／Conversation／Karteの意味決定を変更しない．C0.1の`GenerationProgress.SpeechUnits`だけを`SpeechRequest`へ変換する．見出し記号，code本文，未閉鎖Markdown，語中で切れたcontinuationの末尾はこの境界を越えない．`SpeechRequest`の本文は短命memory／process pipe／明示的なloopback接続だけを通り，traceや評価exportへ保存しない．

`VoiceProfile`はvoice profile ID，表示名，provider，model revision，language，clone prompt digest，default style，capabilities，provenance IDを持つ．参照音声・transcript・embedding・clone promptの内容やlocal pathを含めない．`SpeechRequest`はspeech textとprofile IDに加え，affect，intensity，pace，pitch hint，volume，pause style，interruptibleを持つ．voice identityとdelivery styleを別々に検証する．

`VoiceTurnRequest.speech`はprofile IDと任意のstyleだけを受け取る．operation開始時にprofileとstyleを複製して固定し，manual continuationでも同じ設定を使う．catalog再取得は進行中のoperationを変更しない．未対応controlの非既定値は`unsupported_voice_control`で拒否し，未知・無効なprofileを別の声へ暗黙に置換しない．明示cancelは常に許可し，非interruptibleな発話policyは導入しない．

UIは`GetVoiceProfiles`のcapabilityだけから操作を表示する．変更できない単一enum，未対応controlは表示しない．声を選び直すとそのprofileのdefault styleから再構成する．対話中は設定を固定し，text chatはprofile serviceやASRの利用可否に依存しない．

## 最初のadapter

Qwen3-TTS-12Hz-1.7B-Baseを独立したPython service／workerへ置く．Runtimeの既存Python環境へ重いmodel依存を強制しない．公式sourceは`022e286b98fbec7e1e916cb940cdf532cd9f488e`，modelは`fd4b254389122332181a7c3db7f27e918eec64e3`に固定する．source配布metadataと，modelの11 filesの公開hashを照合し，Inference時はoffline読込を強制する．modelやreference assetはGitに置かない．

公式`generate_voice_clone`は1回の合成WAVを返すAPIであり，`non_streaming_mode=false`はcodec単位の音声streamを返す指定ではない．今回のcapabilityは`streaming_mode=phrase`とする．確定済みの句を180 rune以内の句読点・空白境界で分け，1句の合成とEOS検証が済むたびに独立WAVを再生queueへ渡す．上限内に安全な境界がない長いunitは，途中の語を合成せず固定errorでtext fallbackへ移す．応答全文のWAV完成を待たないが，1句の途中のwaveformを先行再生する実装ではない．

Baseはclone promptによるidentityを持つが，affect／pitch／pace等の直接instruction controlを宣言しない．今回Qwen adapterが提供するdelivery controlは，検証済みPCMのvolume 0〜1だけである．localeとQwenのlanguage名の変換はadapter内に限定する．公式API内部のtalkerのterminal EOSを局所的に観測し，生成上限に達してEOSを確認できない音声は`tts_incomplete`として拒否する．出力budgetは有限であり，無制限生成やfine tuningを行わない．

既存Apple SpeechとKyokoを維持する．Kyokoは同じcontractを実装し，明示paceを`/usr/bin/say`のrateへ，volumeを検証済みPCMの振幅へ適用する．neutral設定では従来の合成経路を保つ．既存のprivate一時WAVはcallback前に削除し，失敗・cancelでもcleanupする．

## Transportとcancel

serviceは既定で`127.0.0.1:8767`へbindする．`GET /v1/voice-profiles`は公開metadataだけを返す．`POST /v1/speech`は1句，request ID，固定したmodel revision／clone prompt digestを受け取り，NDJSONのaudio frameと`completed`／`stop`終端を返す．raw referenceをこのAPIでuploadする経路は設けない．Origin付きの合成要求は拒否し，access logと自由文diagnosticを無効にする．

Runtime adapterは`EPHY_TTS_ENDPOINT`でloopback IPのHTTP endpointだけを指定できる．別Inference nodeへの接続は明示的なSSH port forwarding等を使用する．HTTP redirectとproxy環境変数を使って音声本文を別hostへ転送しない．profile ID／capabilityによるRuntime設定と，adapterの接続設定を分離する．

frameのrequest ID，sequence，UTF-8，WAV構造，有限size，最終frame，実transport EOFを検証する．byte上限による人工EOFを正常終端と誤認しない．固定error codeだけをRuntimeへ渡す．generation operationと共有したcontextで接続を切り，serviceはそのrequestを所有するworkerだけを停止する．cancelされた別requestが生成中workerを停止しない．次の要求で必要ならworkerを再起動する．

workerはmodelを保持して句間で再利用するが，同時生成は1 requestだけで，追加要求は`tts_busy`となる．request期限は最大60秒，Runtimeは従来の累積TTS予算60秒を維持する．LLMのidle待機はこの予算を消費しない．WAVはPCM16／mono／24 kHzで，既存の1 chunk 8 MiB／60秒，operation 16 MiB／64 audio chunks，検証済みdurationからのplayback期限とcancel fenceを引き継ぐ．

## Private asset store

参照素材は，所有または複製・音声生成利用について明示的な許諾を持つものだけを登録する．clean referenceと正確なtranscriptの確認，保管・clone・生成許可，attestationと根拠をprivate metadataへ記録する．この情報の内容をGit／recovery log／trace／A/B exportへコピーしない．

storeはGit repository外の絶対pathに限定し，worktree，bare repository，ignored directoryを含むGit内配置を拒否する．directory 0700／file 0600，symlink拒否，descriptorに基づくpath検証，内容digest，immutableな登録先，非上書きのpublishを使用する．既存assetを削除せず，失敗時はその登録処理が作ったstagingだけをcleanupする．

参照WAVは検証済みPCMだけへ正規化して不要metadataを除く．clone promptは参照codeとspeaker embeddingをbounded NPZで保存し，load時は`allow_pickle=False`とshape／dtype／size／digestを検証する．reference transcriptを含むprivate JSONも同じ境界で保護する．公開profileはdigestとprovenance IDだけを参照する．

## 検証と受入れの区別

合成testはcapability拒否，style固定，Unicode分割，phraseの先行配信，EOS欠落，最終frame／改行欠落，終端後のframe，body limit，worker再利用・cancel・timeout・次turnの混入防止，trace非保存，private storeの保存境界を確認する．ASRの1，536 revisionsに続く8 generation revisions／24 segments／64 audio chunksの既存統合testをprofile契約経由でも実施し，128 events／256 KiB以内のdurable保存とcritical event保持を確認する．trace schema 3の形を変えず，TTS provider／modelとprofile・styleのconfiguration digestだけを既存eventへ記録する．

Python，Go，Go race，Frontend，production build，repository validation，shell syntax，bindings determinismを再検証する．実機合成・model読込み・実声のclone品質・microphone／speakerの利用者試験は，それぞれ別の観測結果として報告する．参照素材未提供時はclone成功，声の一致度，blind A/B合格を宣言しない．

CosyVoice 3はQwenで声の一致度またはstyleが不足すると確認された後の比較候補である．今回は導入しない．VAD，barge-in，filler，Avatar，LoRA，speech-to-speech，C0.4は実装しない．C0.2の実機ASRに残ったTCC／readinessの不確実性を，C0.3の合成testで解消済みとは扱わない．

## Sources

- [Qwen公式sourceとBase API](https://github.com/QwenLM/Qwen3-TTS/blob/022e286b98fbec7e1e916cb940cdf532cd9f488e/qwen_tts/inference/qwen3_tts_model.py)
- [公式model revision](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-Base/tree/fd4b254389122332181a7c3db7f27e918eec64e3)
- [公式model機能と使用例](https://github.com/QwenLM/Qwen3-TTS/tree/022e286b98fbec7e1e916cb940cdf532cd9f488e)

利用手順は[Custom voice TTS](../custom-voice-tts.md)を参照する．
