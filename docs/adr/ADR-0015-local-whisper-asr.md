# ADR-0015：常駐Whisperを交換可能なローカルASRとして接続する

日付：2026-09-13．状態：Implemented，自然会話の品質受入は未完了．関連：[ADR-0011](ADR-0011-streaming-asr-and-live-transcript.md)，[実測・起動手順](../LOCAL_WHISPER_ASR.md)．

## 背景

Apple ASRの途中候補なしの失敗が実会話で観測された．その原因とCERは保存済みtraceだけでは確定できない．ユーザーはASR計画A〜Eを試せるアプリまで一貫して実装し，人の発話が必要な品質項目は未確認として分離するよう指定した．以前の段階ごとの停止は撤回されている．

## 決定

既存のVoiceASR／VoiceStreamingASR契約へ別のWhisper adapterを追加する．Apple専用のprotocol検査は緩めない．pipeで接続するC++ workerはモデルを常駐させ，起動時のhash検証とwarmup，bounded PCM，VAD activity，置換可能なpartial，全区間final，terminal ACKを持つ．取消と次sessionはidentityとACKで分離する．本文と音声は診断へ永続化しない．

whisper.cpp v1.9.4のcommitを固定し，同じ公開日本語朗読100件で非量子化Kotoba v2.0とlarge-v3-turboを比較した．CERは6.98％と4.80％，finalはどちらも100/100であったためlarge-v3-turbo F16を初期選択とする．Metal，4 threads，日本語指定である．これはApple比の精度改善を証明するものではない．Apple比較は利用不可で未測定のまま記録する．

通常発話をRMSで切り出す方式は，activity対応providerでは全PCMの連続入力とモデルVADへ切り替える．割込みはVADと認識内容で確認する．小声と短い指示を既存RMS閾値だけで捨てていた不整合を修正し，候補1,800 msと3秒bufferを一緒に設定する．通常partialは500 ms，候補は200 msで推論開始を調整する．モデルの小型化・量子化やLLM reasoning抑制で数値だけを改善しない．

## 結果と未達

一度だけのfinal採用，no-speechから待受への復帰，late event，cancel／次入力，resampling，実workerの連続turn，production endpointと割込み引継ぎを自動試験で確認する．署名済みhelperとprovenanceをアプリへ組み込み，重みはGit外に配置する．

公開朗読は日常会話の代替ではない．合成割込み6件では相槌の誤停止0，意図した5件全検出だが，最終helperの候補確定p95 1.081秒で1秒目標は未達である．WorkとIrodoriの連続負荷では，入力終了時のpartial打切りと無音再推論の抑制によってVAD最終有声→final p95を5.041秒から2.599秒へ短縮した．モデル・finalのdecode条件と認識内容は保持したが，2秒目標には届いていない．

人の実マイク試験を行わず，公開朗読・合成の成績だけで別エンジンの採用や品質受入を決めない．この版は比較・試用できるアプリとして提供し，単独／同時負荷の未達を分けて記録する．自然な実マイク音声のCER，重要語，部屋の雑音，実再生停止，Apple比の再発話減少は人による確認を残す．基準は引き下げず，次の推論方式比較にも同じ評価条件を使う．
