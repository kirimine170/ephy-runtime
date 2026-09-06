# ADR-0010：Generation Completion Safety

- Status：Accepted for C0.1 implementation．実機受入れ結果は作業報告で区別する．
- Base：`feat/interaction-loop-c0`，`79b88a677def2d43ad5d3c125c497abbb33843cb`．
- Scope：C0.1のみ．B0を変更せず，C0.2以降は開始しない．

## 問題と診断

長い音声応答がFrontendで途中確定した．コード変更前に同じfast／voice／temperature 0.2／max_tokens 512で実Qwen3-8Bを再実行し，現行Gateway経路，実Go parser／InteractionEngine，実Frontend completion mergeを通して文字数とdigestだけを比較した．上流・Gateway・Runtimeは420文字，Frontendは182文字であった．providerは`stop`と最終SSEおよび`[DONE]`を返していた．Engineの128回preview制限と，最終全文より非空previewを優先するFrontend処理が欠落を起こす．分類は4である．元のsession historyとprovider terminalは保存されていないため，報告された末尾の語そのものを遡及的に証明したわけではない．

同一prompt context，seed 42，temperature 0.2，上限512でthinkingを比較した．有効時は総生成512／reasoning content再tokenize 396／visible再tokenize 112で`length`，無効時は総生成338／reasoning 0／visible再tokenize 337で`stop`となった．有効時は四季すべてを説明できず，無効時は春夏秋冬を含み自然な境界で終わった．request単位のboolean `chat_template_kwargs.enable_thinking`は，稼働templateと生成結果の双方で有効性を確認した．

稼働llama.cppはbuild 9872，revision `665892536dfb1b7532161e3182304bd35c33e768`，Qwen3-8B-Q6_K，各slotのcontextは8192である．既存LoRAを含む稼働model条件を維持した．model設定ファイルのcontext宣言と実slot上限は異なるため，宣言値だけで残容量を推定しない．診断の本文，reasoning，raw audioは保存せず，終了理由，文字数，token数，digest，時刻のみを保全した．

## 決定

Frontendでは最終snapshotの全文をauthoritativeに反映する．確定本文と進行中previewを区別し，正常完了したassistantだけをConversation historyとKarte入力へ採用する．`INCOMPLETE`，cancel，failureの安全な確定本文は画面に残せるが，通常の完了履歴には昇格させない．

Go transportは`stop`，`length`，`tool_calls`，`timeout`，`transport_eof`，`canceled`，`unknown`を保持する．最終SSEと`[DONE]`の両方を必要とし，欠落，不正frame，終端後のdeltaは正常完了にしない．例外の自由文は音声／chatのerror境界を越えず，固定codeを返す．

Interactionのassemblerは確定prefixと未確定suffixを分離する．既定はsegment 512 tokens，最大3 segments，最大2 automatic continuations，総上限1536 tokens，soft target 75％である．`VoiceTurnRequest.generation_limits`で有限の範囲を設定できる．usage未観測のsegmentは上限分を予算に課金し，観測したtoken数とは区別する．既定のhard上限は増やさない．

`length`では最後の安全な境界までrollbackし，同operation contextで元のuser要求と確定assistant prefixから続行する．新しいuser turnを保存しない．末尾と先頭の完全一致overlapのみを除く．`stop`は原則完了とし，明白なMarkdown／括弧／引用の破損だけに1回のrepairを許す．句点のない短い回答は正常に扱う．

上限到達時は`INCOMPLETE`となり，「続きを生成」で同operation／turnの次の`generation_revision`を開始する．1回の明示再開にも同じ有限生成予算を適用する．operationの明示再開は最大7回まで，元の7日保持期限を延長しない．audio sequenceはoperation内で単調増加し，古いrevisionのeventを破棄する．最終履歴は同じassistant entryに統合する．

`generationBoundaries`と`generationSpeechText`が確定した句／文を`GenerationProgress.SpeechUnits`へ渡す．未完語，未閉鎖Markdown，未確定continuationはこの境界を越えない．閉じたcode fenceは表示用に保持し，code本文を音声へ渡さない．確定unitはLLM継続中も既存VoiceTTSで再生できる．TTS failureはoperationを停止する．TTS予算は推論待機時間を含めず，合成処理の累積時間で消費する．

## Metadataとprivacy

traceとgeneration metadataはschema version 2である．provider finish reason，segment上限と総予算，生成token数と観測flag，reasoning token数とsource，segment数，continuation数，最初のraw delta／visible content／terminal SSEの時刻を本文なしで記録する．旧version 1 traceの読取りは維持する．`llm_completed`は最終assembled responseの完了時だけ，`turn_completed`はさらに必要な音声再生がすべてACKされた時だけ発行する．音声化対象のないcode-only回答は明示的な`tts_skipped`とする．

reasoningのprovider実測値は`provider`，reasoning contentを既存tokenizerで数えた値は`retokenized`，観測不能は`unavailable`とnullで区別する．複数segmentに未観測値や異なるsourceが混在した場合，全体のreasoning数を既知として報告しない．raw本文をexportしない．評価tagには`incomplete_response`を追加し，再開前後の評価は`generation_revision`で区別する．

## Architectureとの関係と次Gate

RuntimeはInteraction／Conversation／Policy／Orchestratorを持つControl Planeとして維持する．Qwen固有のthinking／reasoning指定はPython LLM adapterに置き，Go assembler，Conversation domain，UIへ埋め込まない．既存macOS Speech，Kyoko，text fallbackを維持する．重いモデルは既存の独立Inference service境界に留める．

C0.2開始時は`VoiceASR`の一括Transcribe契約をsession型へ拡張し，ASR専用のsegment ID／revision／partial・stable・final eventとUIを追加する必要がある．現在のonTranscriptはfinal専用であり，partialをhistory，Karte，LLMへ渡してはならない．LLM再開用のgeneration revisionをASR revisionへ流用しない．

C0.3は上記SpeechUnits境界を再利用できる．そのGateで`VoiceTTS.Stream`をVoiceProfile ID／capabilities／SpeechRequestへ拡張し，独立provider adapterと再生timelineを定義する．今回のResponsePlanとVoiceHintは既存互換のままである．参照音声やclone promptを保持するstoreは実装していない．

ASR／TTSモデル導入，download，音声sample取込み，streaming ASR，VAD，barge-in，filler，Avatar，LoRA変更は実装していない．push，PR，merge，Sheets更新，次Gate開始には別の承認を必要とする．

## 検証とrollback

Python正規suite，Go全suiteとrace，Frontend suite，production build，repository validation，shell syntax，bindingsの2回生成一致を確認する．`TestGenerationInstalledSeasonsAndPlayback`は明示opt-inで既存modelとKyokoおよび実afplayを使用し，prompt cache cold／warm，再実行，cancel後の新session，一時WAV cleanupを検証する．raw音声を記録するASR試験ではない．再生開始はOS processの開始時刻であり，音響的な発音開始の計測とは区別する．

```sh
EPHY_C01_INSTALLED=1 go test -run '^TestGenerationInstalledSeasonsAndPlayback$' -count=1 -v .
```

独立したC0.1 commitをrevertすればC0基点へ戻せる．B0 commit，既存model，recovery保全物を変更する必要はない．
