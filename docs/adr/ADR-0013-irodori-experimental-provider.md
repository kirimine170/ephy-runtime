# ADR-0013：Irodori-TTSをexperimental Speech Providerとして隔離する

- Status：Accepted for C0.3.2 experimental comparison
- Date：2026-09-09

## Context

Step 3〜3.6のstandalone評価では，`phasefield-audio/Irodori-TTS-v4.1-Anime`のFP32 MPS生成は会話用途の追加評価に値した．一方，公式serverはHTTP disconnect後もMPS synthesisを停止できず，process abortも再現した．また，Anime model cardだけでは学習dataの詳細，客観評価，SilentCipher weightの許諾をproduction用途について確定できない．

## Decision

IrodoriをQwen3-TTSとKyokoを置換しない第3のproviderとして追加する．選択は明示的なprofile指定だけに限り，serviceとRuntimeの両方でIrodoriをdefaultとして拒否する．

共通loopback serviceはproviderごとに独立した子processを起動する．provider切替時は既存processを停止してmodelの同時常駐を避ける．cancel，timeout，HTTP disconnectでは所有中の子processをterminateし，終了しなければkillする．したがって，停止不能な公式HTTP serverをcancel-capableとして中継しない．

Runtimeを唯一のtext segmentation ownerとする．adapterは確定済み`GenerationProgress.SpeechUnits`から切り出された1 phraseを1回だけ受け，`seconds=None`でduration predictorを使う．transport EOF，token上限，未確定suffixはspeech完了の根拠にしない．

`affect`，`intensity`，`pace`，`pause_style`はadapter内の有限表現へ変換する．captionやemoji，reference identity，任意promptをreasoning，ResponsePlan，表示本文，traceへ加えない．暫定presetは`neutral`，`warm`，`cheerful`，`cute`，`sleepy`，`concerned`である．読みはStep 3.6に基づき対象語辞書だけを置換し，全文ひらがな化をしない．

複数reference clipはprivate storeのimmutableなordered groupとして扱う．Irodori v4.1 APIは単一`ref_wav`だけを受けるため，workerがgroupをprivateなrequest-scoped WAVへ結合し，生成直後に削除する．子processの強制停止後は親がprivate temporary directory全体を削除する．元clip，transcript，permission recordは公開profileへ含めない．

## Consequences

- 実素材を使わないcontract testは，認証，profile pin，style bound，group整合，cancel，stale audio，long continuation，200-turn retentionを確認できる．声質の合格は確認できない．
- Step 3の同じMacでの実reference UATは別扱いであり，明示選択のexperimental profileに限る．custom voice失敗時に別voiceへ切り替えず，text fallbackを残す．
- SilentCipher artifactのproduction許諾，複数clip結合後のidentity，人間による読み・途中切れ・style・長文安定性が未解決であるため，default voiceおよびproduction-readyとは判定しない．
