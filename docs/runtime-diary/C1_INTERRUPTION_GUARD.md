# C1 外部環境向け割込み確認とspeaker gate境界

2026-09-20．通常の発話開始，割込み候補，ASRによる発話確認，割込みintent，duck／cancelを分離した．既存の単一capture，500 ms pre-roll，有界の入力引継ぎ，canonical finalの採用経路，operation／session／turn identityは維持する．

## きっかけと確認できた事実

Irodori Animeを使った実機試行で，文章生成完了後，TTS開始から17.7秒／8.3秒で `user_barge_in` により取り消され，最初のWAVが届かなかった．続く入力は認識候補なしで `asr_failed` になった．固定短文の単体合成は初回23.6秒／2回目3.1秒で成功した．この記録だけでは割込み入力が発話かノイズかは特定できないが，従来実装は通常の発話開始と同じRMS 0.02／32 msで応答を直ちに取り消していた．

## 今回の動作

1．通常の入力待ちでは従来のendpointを使う．応答中には独立した割込み判定を使い，短い活動だけで旧operationを取り消さない．
2．RMSは候補を開くためだけに使う．Whisper VADの`has_speech`もspeech activityとして扱い，それだけで音量を変えない．静かな区間・棄却後の環境音からnoise floorを学習し，候補PCMは従来どおり有界のメモリ内bufferにだけ保持する．
3．既存の端末内ASRで候補を確認する．「待って」「止めて」「違う」などの明確語を認識した場合だけ，confirmed待ちの短い期間に通常音量の72%へ軽くduckする．通常の発話は音量を維持し，認識の安定と活動量が揃ったconfirmedでのみplaybackをcancelする．
4．短い物音，一定ノイズ，BGM相当の連続音，認識できない音，候補ASRの失敗，期限切れ，相づちは候補だけを捨てる．明確語の認識後に棄却した場合は即時に通常音量へ戻す．
5．確定した割込みは，候補中の冒頭を含むPCMを次turnの通常ASRへ引き継ぐ．通常ASRの検証済みfinalだけを会話へ採用する．元の回答が先に自然終了した場合も，候補中の入力を次の通常ASRへ引き継ぐ．
6．`fillerBargeIn` はPCM captureだけを担当する．従来の32 ms RMS即時cancelは廃止し，通常と同じ`voiceInterruption`のcandidate／ASR／intent確認を通す．fillerは短いためprobableのduckは行わず，confirmedで停止する．

`voiceInterruption.js` の初期設定は，音声活動180 ms以上，認識の変動停止160 ms以上を本文再生中の基準とする．生成待ちでは活動260 ms／変動停止240 msを必要とする．Whisper VADが利用できる場合，明確な中断語は64 msのmodel speech／80 msの認識安定で高速に確定でき，provider finalがあれば安定待ちを省く．

## Target Speaker Gateの接続点

`ASRAudioActivity` とfrontendの`speakerEvidence()`に，`has_speech`と独立した`target_probability`，`speaker_state: target | non_target | unknown`，`confidence`を追加した．現在のWhisper adapterはこれらを生成しないため`unknown`として従来のASR intent確認へ進む．将来のTarget Speaker GateはASR updateのactivity生成時に数値と固定enumだけを渡せばよい．`non_target`または高confidence／低target probabilityの候補はASR本文が明確語でも棄却する．

speaker embedding，speaker ID，raw audioはこのschemaに含めない．activityはtransientなASR callback／candidate snapshotの範囲に限定し，Conversation，LLM request，trace，evaluationへ保存しない．

## 寿命・取消・記録

- 候補の確認は最大1.2秒．候補中もPCM合計は2秒に制限する．未確定候補の上限超過で元の返答やvoice sessionを止めない．確定後の引継ぎ上限超過は従来どおりpauseして再発話を案内する．
- 候補IDはASR起動前に確定し，起動が遅れていても取消できる．Go側にも3秒の上限を置く．元のturn，generation revision，voice session／epoch，ASR segmentの一致を確認する．
- 候補認識と通常認識は同じASR gateを使い，古いprovider sessionの取消が完了してから次を開く．候補の認識失敗・遅延callbackは元のLLM／TTSや次turnを失敗させない．
- `interruption_candidate_started`と`interruption_candidate_confirmed`，固定の棄却理由を数える．`interruption_duck_started`，`interruption_duck_ended`，`interruption_false_duck`には固定kind，outcome，duration msだけを残す．候補の文字列，PCM，speaker evidence，音声ファイルはtraceへ保存しない．
- `interruption.candidate_ms`／latencyの `interruption_candidate` は，候補開始から確定までの待ち時間である．`local_stop` とinput handoffの時間は確定後から測る．これらを混ぜて，呼びかけから0 msで停止したとは報告しない．

## 検証と実機確認

単体試験はクリック，短い衝撃音，断続ノイズ，継続した背景音，相づち，呼びかけ，訂正，認識の揺れ，ASR失敗・起動遅延・取消，buffer上限，自然終了との競合を対象とする．Controller試験でLLM待機／TTS待機／本文を維持し，明確な割込み後の冒頭PCM一致，単一capture，旧callbackの無効化，pause／resumeを確認する．Go試験で候補が元turnを失敗・取消しないこと，ASRの直列化，identity，固定metadata，候補待ちと停止時間の分離を確認する．

再ビルドしたappをIrodori Anime／明るくで起動し，次を人が確認する．今回の自動試験だけを聴感や実マイクでの合格にはしない．

| 場面 | 期待する結果 |
| --- | --- |
| 生成待ち・再生中に軽い机の音，キーボード音 | 回答をキャンセルしない |
| 再生中に「うん」「はい」，短く名前だけを呼ぶ | 回答と通常音量を維持する |
| 再生中に「待って，冬の話だけ聞きたい」 | 発話を確認してから中断し，「待って」を含む入力で次の回答へ進む |
| 上記を繰り返し，pause→resume | microphoneや認識sessionが重複せず，新しい会話を続けられる |

現在はTarget Speakerモデルがないため，背景の他人の声がASRで明確な中断語または十分な発話と認識された場合は完全には除外できない．speakerへのechoもAECと既存のASRに依存する．従って，今回の改善はVAD／RMSだけのfalse duck／false cancelを減らすものであり，カクテルパーティー効果の完成ではない．
