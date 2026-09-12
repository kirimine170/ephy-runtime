# ADR-0014：連続会話の入力・回答・記録とKarte正本を分離する

## Status

Accepted，2026-09-12．Step 1のsession分離，Step 2の本文割込み・有限入力引継ぎ，先行Step 3のKarte policy採用・復旧を実装した．Step 1・2の人による実機受入は未確認，Step 4以降は未着手である．

## Context

設計時のC0.xはoperation配下でASR，LLM，TTS，再生を扱い，cancelが入力も終了させていた．C0.4のmicrophone monitorは本文開始前までの活動検知であり，ASRへの内容引継ぎはない．会話候補の自動送信はKarteのhuman review待ちまでであり，確定user eventの永続保全でも自動採用でもない．

## Decision

[C1〜C3実装計画](../runtime-diary/IMPLEMENTATION_PLAN.md)に従い，session配下のcapture／ASR発話区間と，回答operation配下の生成／再生を分ける．回答取消が次の発言のASRを取り消さない．generation revision，SpeechUnit，endpoint/finalの一度だけの採用，text fallbackは維持する．生成・表示・再生状況・中断を区別し，未再生の全文を次の会話で聞かせた前提にしない．

記録有効の会話はuser finalの受理時に永続保全し，Karteへevent単位で配送する．ASR partial，raw audio，reasoning，system prompt，private profile全文，参照したKarte本文を記録へ複製しない．TTS用の読み正規化を原文保存へ流用しない．

Karteを唯一の正本ownerとし，Runtimeは未配送outboxと有限cacheだけを持つ．ADR-0007／ADR-0008のhuman review必須という記述は，[Karte v2の限定policy採用](../../../karte/architecture/KARTE_RUNTIME_DIARY_V2.md)が設定された範囲でのみ置換する．v1のreview互換，Karteがprivacyを判定する責務，本文をuntrusted contextとして扱う境界は維持する．

`docs/design/03_MEMORY_MODEL.md`の保存同意・解釈区分・訂正削除を，会話原記録とsource revision付き派生物として具体化する．既存`memory_object.schema.json`を非互換変更して第二の記憶DBを作らない．保存同意は学習・外部送信の同意ではない．

要約・日記はRuntimeローカルの有界Jobで生成する．Jobの入力版・model・templateを固定し，resultのKarte採用を実行と分離する．会話優先，取消，restart，冪等性を設け，後のWorker移管は同じ入出力契約を利用する．今回Worker本体へ進まない．

## Consequences

- C1はRuntime内，C2はKarte契約・writer・policyを先にしてRuntime保全・配送へ，C3は保存済みsourceから生成・想起・訂正へ進む．
- v2 schema／fixturesはKarteが正本を持つ．Runtime mirrorは対応版をpinして両言語で照合する．不明版をv1へ黙って変換しない．
- 古いtraceや評価exportを会話本文の保存先へ転用しない．pending，local durable，canonical savedをUIで区別する．
- 記録OFFは以後の保存停止であり，削除とは別である．訂正・権限変更・削除は検索，cache，未配送，Job，派生物に適用する．
- Fast音声がnon-thinkingである現行仕様を記録し，Workのthinkingを維持した実機受入を別gateにする．

## Step 2の実装境界

同一captureの500 ms pre-rollと2秒の引継ぎqueueを使い，端末のmute／stopをprovider取消より先に実行する．SpeechUnitはassembler確定時に登録し，複数WAVの全自然終了とproducer closeを揃えて完了とする．生成完了は音声中断で取り消さない．次requestへは確実に終了した単位の連続接頭部分と中断の事実だけを渡す．v2 wireを拡張せず，共通scenarioで既存enumへ対応付ける．

Workのcompletion guidanceは先頭system群の末尾へ配置し，会話末尾のsystemを拒否するtemplateにも対応する．persona／thinking／予算を変えない．詳細と実機未確認はSTATUSへ集約する．

## Step 3の実装境界

Karteがv2のcreate／append／derivation更新とsearch／readを提供し，RuntimeはKarte正本の45 JSONと署名・ID・原文保持・再送の共通fixtureを照合する．Runtimeに別のcanonical writerやv2記録経路はまだ追加していない．scope登録は既定OFFであり，旧Developer Modeから記録同意を作らない．音声eventの有効化はStep 2の中断・再生契約とStep 4のv2 reader・direct index除外を待つ．人向け訂正・削除・制限と自動purgeはStep 6へ残す．

## Verification

実装・fixture・受入・migration・rollbackは実装計画へ，現行版との照合結果は[STATUS](../runtime-diary/STATUS.md)へ集約する．このADRのAcceptedは設計の採用であり，自動保存の有効化や実機合格ではない．
