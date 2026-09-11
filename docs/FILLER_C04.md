# C0.4：有限音声のfiller controller

実装対象はC0.3.2で再評価対象になったIrodori Anime profileである．default voiceは変更しない．reasoning model，prompt，generation limits，本文のSpeechUnits，conversation history，Karte，memoryは変更しない．

## 実装した境界

- `fillerTiming.js`はGoのLLM／TTS traceとFrontendの到着時刻を別々に計測する．`llm_identity_ready`で実model identityが分かってからsetupを取得する．未確定のrouteだけで別modelの計測を使わない．
- `fillerController.js`は`DISABLED → ARMED → PLAYING → SPENT／CLOSED`の独立状態機械である．本文readyが発火前ならtimerを失効させ，再生中なら有限clipの自然終了を待つ．cancel，barge-in，identity／device変更は即時停止してtimerを失効させる．最大1回で，再生失敗も回数を消費する．continuationのrevision 2以降は無効である．
- `voiceInteraction.js`は本文audioのdecode中にもフィラーを独立再生できる．本文readyでは最初のdecode済みchunkを保持し，再生中のフィラーが自然終了した直後に本文を開始する．有限clip全体を文節境界として扱い，途中では切らない．追加のbridge ACKや接続用timerは待たない．割込み検知は本文開始直前まで続け，cancel後に待機が解けてもturn／playback epochを再検証して旧本文を捨てる．本文のfirst-audio traceをフィラーで置き換えない．
- 生の音声，本文，候補IDをC0.4 traceに残さない．追加traceのschemaは`kind`と`latency_ms`だけである．calibration fileは数値の対応sample，設定のopaque digest，retention時刻を別に保持する．32条件，各200 samples，各256 trace，7日を上限とする．
- 有声音の相槌は発話終了後の中立フィラーだけである．「はい」「うん」「なるほど」「確認しています」「覚えています」等は集合にない．

## 有限assetとidentity

`scripts/filler/prepare.py`は「えっと」「ええと」だけを採用profileで生成する．追加の合成文は人間の本文接続確認用で，live fillerには使わない．生成後はすべて`approved=false`，`enabled=false`である．生成音声はGit外のprivate directoryへ保存する．

live読込はprofile，model revision，reference group，provenance，言語，synthesis設定digestを照合する．source／codec／watermark revision，seed，steps，CFG，device等が変わるとdigestが変わる．本文TTS requestにもdigestをpinし，service側で変更を拒否する．旧serviceのdigestなしprofileは本文TTSの互換性を維持するが，fillerを有効化できない．

初期版はneutral，pace 1，volume 1等の中立設定だけを受け付ける．同じ話者でも未検証のstyleには流用しない．音声長150〜1500 ms，WAVの実長，checksum，0600 file／0700 directory，symlink／hardlinkを検証する．未承認候補は再生対象にしない．

## 計測と発火

C0.3.2のwarm first-audio p50 4201.689 ms／p95 5410.663 msはprovider評価の値である．LLM込みの分布や実speaker停止latencyへ転用しない．現在の実装にこの値や仮のLLM値を初期値として埋め込んでいない．

計測sampleは，endpoint event受信からLLM request，first token，TTS request，first chunk，本文decode完了までの対応したFrontend時刻と，Goの単調時計で測るLLM TTFT／TTS latencyを含む．Frontend値にはevent transport／decodeの影響が含まれる．GoとFrontendの時計を直接減算しない．本文readyはフィラー終了待ちの前に記録するため，待機時間が次回の発火予測へ混入しない．意図した待機時間は`filler_answer_wait`，自然終了から本文の実再生開始までの空白は`filler_gap`で分離する．実speakerの音響開始・停止は別の人間／実機評価が必要である．

同じ実model／voice／設定／条件のsamplesに対し，nearest-rank p50／p95を計算する．個別のp95を加算しない．最初の発火評価は`max(A25 / 2，A50 − 音声長 − 150 ms)`である．ここでAはendpointから本文decode完了までの実測値である．A25の半分までは発火しない．このfast guardは初期policyであり，聴取評価後に校正する．

timer時点で未完了の対応sampleを抽出し，未到着stage，直近stageの到着時刻が近いsampleだけを条件付き分布に残す．全体・条件付きの両方で30 samples以上を要求する．中央値が音声長＋150 msの±150 msに入り，残り時間p95が音声長＋300 ms以内で，本文が目前でない場合だけ再生する．それ以外は50 ms以内で再評価するか，A95で発火機会を閉じる．LLMをtimeout／短縮しない．

本文token／確定本文が表示された後は新規発火しない．既に再生中なら本文audio ready後もclipの自然終了まで再生する．この保守的な条件のため，初期版で実際にフィラーが出るturnは限定される．遅延分布が広い構成や長いreasoningを短い1回の音声で覆えるとは扱わない．

## barge-inと失敗時

初期のマイク入力はヘッドホン使用を確認した構成専用である．発話活動を検出する小さなWeb Audio経路だけを使い，ASR，転写，録音file作成を行わない．RMS 0.02以上が32 ms継続するとuser barge-inを通知する．認識finalを待たず，Frontendで停止してから旧turnをcancelする．この閾値は実機UATで静音発話・雑音を含めて確認する必要があり，汎用VADやspeaker使用時のAEC合格を主張しない．

マイク拒否，track終了，device変更，遅延permission応答ではfillerを無効化する．microphone monitorが準備できなければ発火しない．監視中であることはRuntimeの状態表示へ明記する．通常のendpoint自動検出へ範囲を広げない．

フィラー後の予測外の空白は`filler_gap_exceeded`で計測し，2回目の音声を出さない．同じ条件の保存traceにgap超過，再生失敗，watchdog超過が合計3件あれば，その条件を停止する．自然終了通知が来ない場合は音声長＋100 msのwatchdogで停止し，保持中の本文を解放する．asset失敗は本文を継続し，本文TTS失敗は既存text fallbackへ進む．停止不能時は出力contextごと閉じる．別voiceへの切替はない．

## 人間の動作確認

providerの固定推論Pythonで次を実行する．outputは未作成のprivate directoryを指定する．

```sh
"$IRODORI_PYTHON" scripts/filler/prepare.py --config "$VOICE_CONFIG" --output "$FILLER_BUNDLE"
.venv/bin/python scripts/filler/review_server.py --bundle "$FILLER_BUNDLE" --port 8794
```

表示されたloopback URLを開く．候補単体を聞いた後，「この候補を承認」を選ぶ．同意・困惑への聞こえ方，声の一致，日本語発音を人間が判断する．承認はassetごとに保存し，liveのenableやdefault voiceを変更しない．

通常接続，速い応答，本文早着，予測外の遅延を試す．本文早着ではフィラーを最後まで聞け，重なりや追加の空白なしに本文へ移ることを確認する．本文待機中の割込みでも両方の音声が停止し，後から本文だけが再開しないことを確認する．停止を各状態で試し，ヘッドホン使用時にマイク割込みも確認する．この画面のLLM／TTS時刻は明示した試験用データであり，liveのcalibration storeへ送信しない．画面のtraceも保存しない．review回答はprivate manifestの承認booleanだけで，自由文を保存しない．

## Runtimeのshadow計測と有効化

build済みRuntimeを既存手順で終了してから，`EPHY_FILLER_SHADOW=1`と`EPHY_FILLER_CONDITION`を設定して起動する．conditionは端末，cold／warm，負荷，ヘッドホン構成を区別する明示ラベルである．このラベルを変えると以前のcalibrationは使わない．service再起動・provider切替・推論設定変更時も条件を改め，異なる条件の試行を同じラベルに混ぜない．provider内部のwarm状態を自動検出できるとは扱わない．

```sh
EPHY_FILLER_SHADOW=1 EPHY_FILLER_CONDITION=mac-irodori-warm-headset-v1 \
  bash scripts/start_conversation_app.sh
```

採用Irodoriを明示選択し，同一構成で少なくとも30件，受入れ評価は100件を目安に実turnを測る．`data/runtime/interaction/filler-calibration.json`に数値だけを保存する．LLM identityやsampleが不足すれば記録・発火を見送る．LLM／TTS p50／p95，本文first-audio，停止latencyはon／offで比較する．timeout・cancelは成功latencyの0 ms sampleへ混ぜず，別途失敗率を評価する．

人間の候補承認とヘッドホン割込みUAT後，private manifestの`enabled`と`headphones_confirmed`をtrueにし，`EPHY_FILLER_BUNDLE`へそのdirectoryを指定して同じconditionで起動する．実測が足りなければ引き続き無音で計測する．本変更ではこの有効化を行っていない．止める場合は`EPHY_FILLER_BUNDLE`を外して再起動する．default voiceは変更しない．

## 検証と残る受入れ

synthetic testは時刻境界，1turnの回数，late decode，cancel，本文待機中のbarge-inと旧turn破棄，自然終了での本文接続，watchdogでの待機解放，continuation，native計測の再計算，trace保持，identity変更，私有file整合，人間review APIのorigin検証を対象にする．通常のFrontend／Go／Python testとrepository validatorも実行する．

実機の声質，マイク割込み，実speaker停止p95 100 ms，接続gap p95 300 ms，reasoningと本文readyの非劣性は人間・実機UATの受入れ項目である．synthetic合格だけで達成済みと扱わない．

本文first-audioには意図的なフィラー終了待ちが加わり得る．通常時は残りclip長以内（asset上限1500 ms），異常時はwatchdogまでである．この追加時間を本文readyやLLM／TTS latencyの悪化と混同せず，接続の自然さと併せて評価する．
