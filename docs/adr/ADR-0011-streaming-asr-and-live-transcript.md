# ADR-0011：Streaming ASR and Live Transcript

- Status：C0.2 implementation decision．実装・自動test・実機計測の完了は，それぞれ根拠を確認して報告する．
- Base：C0.1安定化branch `fix/c01-stabilization`の`5cdea6f`から`feat/streaming-asr-c02`を作成した．作成時のmainは`0b334c1`である．
- Scope：C0.2のみ．C0.1の生成完全性，cancel，privacy，確定発話境界を維持する．

## 問題と責務

一括ASRだけでは，録音中の認識結果を表示できず，停止後の認識待ち時間も分けて評価できない．partial hypothesisは後のrevisionで変わるため，追記やConversation履歴への早期保存は誤った発言を確定してしまう．

RuntimeはInteraction／Conversation／Policy／Orchestratorを持つControl Planeのままとする．ASRはsession型adapterとして交換可能にし，Apple固有のprocess protocolや認識APIをConversation domainへ持ち込まない．Frontendはprovider共通の状態・更新・capabilityを扱う．重いASRを将来別process／serviceへ移すための境界を維持する．

最初のproviderは現在のApple Speechであり，partial resultを有効にする．Qwen3-ASRへの置換，model download，VAD，barge-in，filler，TTS provider変更は行わない．既存のApple Speech／Kyoko／text fallbackと，LLM／Karte／Reasoner経路を維持する．

## Session契約

`VoiceStreamingASR.OpenSession(ctx, request, onUpdate)`は`VoiceASRSession`を返す．sessionは`Append(ctx, sequence, pcm)`，`Finish(ctx)`，`Cancel()`を持つ．Runtimeの`BeginASR`／`AppendASRAudio`／`EndASR`が，UIからの操作をこのadapterへ橋渡しする．生PCMは宣言したsample rateのmono signed PCM16 little endianであり，一時memoryとprocess pipeだけを通る．

| 情報 | 契約 |
| --- | --- |
| operation／session／turn／segment ID | session開始時のidentityをすべての更新へ保持する |
| revision | 同じsegment内で単調増加する．重複・逆順を採用しない |
| phase | `partial`／`stable`／`final`と，cancel／timeout／failureを区別する |
| transcript／stable prefix | 表示用の一時本文．stable prefixはproviderが保証できる範囲だけ |
| provider／model revision | 本文やlocal pathを含まないadapter identity |
| monotonic timestamp | 更新の順序とlatencyを表す．異なるprocessのclock originを混同しない |
| cancel／timeout／failure | sessionを終端にし，固定error codeを使う．自由文の例外を渡さない |

PCMのsequenceは取り込み順を表し，ASR hypothesisのrevisionとは独立する．ASR revisionをC0.1の`generation_revision`へ流用しない．PCMは1 chunk 64 KiB／総量8 MiB／60秒以内，ASR revisionは2，048以内とする．cancel，会話切替，operationの終端後に届いた更新はidentityとsessionの生存状態で破棄する．

Native sessionは最初のPCMまでの準備を30秒に制限する．capture timerは最初のPCM書込み成功から60秒＋有限のdrain margin 5秒，finalizationは15秒とし，全体のguardは115秒である．Runtimeの外側のguardは125秒である．マイク権限待ちによってcaptureの60秒を先に消費せず，音声sample数の60秒上限も独立して検証する．Frontendの未送信・送信中PCMは合計1秒分以内で，bridge呼出しは直列化する．

`OpenSession`の成功はhelper processの起動とstart frameの送信成功を表す．初回のSpeech権限決定を待つready ACKではない．非prompting readinessで既知の開始不可を除外した後，未決定の権限はhelper内で要求し，失敗・timeoutを同operationへ通知する．その間の音声は有限memoryだけで保持し，late failureでもマイクとoperationを停止する．

Apple providerは，途中の文字列一致やconfidenceだけを根拠にstable prefixを推測しない．partialのstable prefixは空であり，最初に保証されたstable結果はfinalとなる．他providerがstableを保証できる場合も，stableは表示専用である．

一度提示したstable prefixは後のrevisionで短縮・書換えできず，finalのstable prefixは全文と一致させる．native helperは終端frameの改行，追加frameがないこと，processの正常終了を検証してからfinalを公開する．

## 表示と会話への確定

`asr_update`はtransient eventである．UIは同じsegmentの新revisionで既存のhypothesisを置き換え，確定範囲と変更され得る範囲を視覚的に区別する．partialを文字列として追記せず，古いsegment／operationのeventを新しい会話へ表示しない．

partialとstableは，Conversation history，Karte，tool実行，LLM requestへ入れない．録音のendpoint後に受理したfinalだけを既存の確定transcript経路へ1回渡す．providerがendpointより先にfinalを返しても，そのcallbackだけでLLMを起動しない．同じfinalがcallbackと`Finish`戻り値の双方へ届く場合にも，user turnとLLM requestを重複させない．

cancel／timeout／failureでは途中のhypothesisを最終transcriptとして扱わない．text入力は引き続きASRを経由せずに会話できる．録音前のreadinessとuser gesture内のAudioContext resumeはC0.1安定化の契約を維持する．

## Traceとprivacy

ASRを含むtraceはschema version 3とし，旧version 1／2の読取りを維持する．ASR専用metadataは，provider／model revision，revision数，文字数，first audio／first partial／first stable／final／finalizationまでのlatencyだけを持つ．未観測のlatencyはnullまたは省略で表し，0 msの観測値と区別する．metadataはASRと終端・cancelのeventに限定し，生成・再生の全eventへ複製しない．

first partial／first stable／finalは`BeginASR`からの時間，finalizationは`EndASR`からfinal確定までの非負の時間として報告する．最初のPCM到着をfirst audioとして同じ起点から記録するため，認識のlatencyを調べる際はfirst partialとの差分も確認できる．計測は同じRuntime monotonic clockで行い，provider updateのclock値をRuntime起点の時間と誤認しない．Appleのfirst stableは最初の保証済みfinalまで観測不能であり，partialがないrunはfirst partialを0 msとして成功扱いにしない．

空文字・空白だけのhypothesisもrevision数には含めるが，first partialは最初の非空本文が届いた時刻とする．表示できる認識結果がないままlatency目標を達成したことにはしない．

durable traceは最初のpartial／stable，final，終端summary等の有限なeventに限定する．revisionごとの本文やeventを保存しない．UI向けpartial配信を集約する場合も，観測したrevision数・最初のlatencyは集約前に計測し，finalと終端eventを失わない．C0.1のaudio trace集約を維持し，operationの上限128 eventsとreaderの256 KiB上限の両方を満たす．

raw audio，transcript，partial本文，prompt，reasoning，参照音声，speaker embeddingをGit，trace，評価exportへ含めない．例外の自由文，provider実行fileの絶対pathもtraceのmetadataへ含めない．実機計測の記録は匿名run ID，設定，状態，文字数，revision数，latencyに限定する．

## 自動testの計画

| 境界 | 検証内容 |
| --- | --- |
| adapter session | PCM順序と上限，partial更新，final，cancel，timeout，failure，pipe終了とcleanup |
| UI | 同segmentのrevision置換，stable表示，録音停止後のfinal待ち，古いcallback破棄，text fallback |
| Conversation | partial／stable中のLLM呼出し0回，endpoint finalで1回，履歴とKarteへfinalのみ |
| trace | 128を超えるpartial revisionsでもevent数が増え続けず，revision数・文字数・観測latencyを保持 |
| C0.1との組合せ | ASR更新の後に8 generation revisions／24 segments／16 automatic continuations／64 audio chunksを実Engineで実行し，critical eventと最終assistant1件を保持 |
| durable保存 | 各生成revisionのtraceを新しいEngineから再読込し，128 events／256 KiB以内と本文非保存を確認 |
| 終端 | cancel／failure／timeoutの固定metadata，未観測latencyのnull，遅延finalが次turnへ入らないこと |

合成PCMと合成transcriptを使い，通常suiteは権限，実録音，実modelを必要としない．Go focused testとrace，Go全suite，Frontend suite／production build，Python正規suite，repository validation，shell syntax，生成bindingsの再生成一致で回帰を確認する．

`interaction_asr_trace_test.go`の合成統合testでは，partial 1，536 revisionsとendpoint前のfinal，同じfinalのcallback／Finish重複通知を通し，endpoint以前のLLM呼出し0回と，ASR finalの採用1回を確認した．その後の8 generation revisions／24 segments／64 audio chunksでは，公開trace 184件が保存128件へ集約され，critical event 56件をすべて保持した．各generation revisionの保存・再読込は成功し，最終保存サイズは約150 kBでreader上限以内だった．cancel／failure／timeout，次turnへ届く遅延final，startup／Appendの自由文error非公開も含め，focused testとrace 10回反復に合格した．これは合成fixtureによる自動test結果であり，実音声latencyの測定値ではない．

## target Macの実機計測計画

短い日本語の合成test文を複数回発話し，録音dataを保存せずにfirst partialとfinalizationのp50／p95を報告する．最初のrunとreadinessがwarmなrunを区別し，試行数，成功数，partial未観測数，失敗／cancel数も添える．分位点は成功runの観測値を昇順に並べたnearest-rank法とし，小標本である場合はその制約を明示する．partialなしのrunを0 msとして分布へ混ぜない．

OS／hardware／locale／provider revision，on-device可否，権限状態を記録する．録音開始からfirst partialまでのlatencyと，停止操作からfinal受理までのlatencyを混同しない．initial targetはfirst partial p50 500 ms以内とするが，未達なら実測値と，capture送信／helper起動／認識／finalizationのどの区間が支配的かを報告する．実測前にこの目標を達成済みと記載しない．

実機結果が未取得または権限未許可の場合は，自動testの成功と実音声認識の未検証を分けて報告する．p50／p95を推定値やmock値で代用しない．

`TestC02InstalledStreamingASR`は明示opt-inで既存Kyokoが生成した合成PCMを実Apple Speechへ50 msずつ送る．5回のfirst partial／first stable／finalization，revision数，文字数と，cancel後の新sessionを検証する．マイクを開かず，合成WAVは既存TTSのprivate一時領域から読取り直後に削除し，本文・音声をlogへ出さない．これは人がマイクへ話した実測とは区別する．

```sh
cd desktop
EPHY_C02_INSTALLED=1 go test -run '^TestC02InstalledStreamingASR$' -count=1 -v .
```

未許可の場合はskipする．`EPHY_C02_ALLOW_PERMISSION=1`で明示的なOS許可要求を有効にできるが，responsible applicationにSpeech usage descriptionが必要であり，headless agentや通常のtest runnerからの起動が動作するとは限らない．TCCを迂回したり，権限databaseを書き換えたりしない．通常appは既存の`build_conversation_app.sh`がSpeech／Microphoneのusage descriptionを設定する．実機結果と制約は本文なしの作業報告へ記録する．

## 次Gateへの境界

C0.1の確定した`GenerationProgress.SpeechUnits`を引き続き既存TTSへ渡す．ASR sessionはC0.3のVoiceProfile／SpeechRequestを導入せず，ReasonerとInteraction／Realizationの責務を変更しない．C0.2完了時点で停止し，C0.3以降の実装・model導入は別Gateの承認を待つ．
