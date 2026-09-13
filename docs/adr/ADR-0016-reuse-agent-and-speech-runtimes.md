# ADR-0016：記憶確認と音声実行部品の分離検証

- 状態：検証中．default採用ではない．
- 日付：2026-09-13．
- base：211e681f9153d0e332ded42f0d9ed2cec0f2ae4e．

## 背景

日常会話の観測から共有経験に基づく候補を作り，Runtimeが話す・待つ・破棄する最小経路を先に検証する．分散実行や学習の完成を前提にしない．既存checkoutのfix/integration-lifecycleは5コミット先行しているため保全し，このworktreeはorigin/mainから分離する．

## 責任と検証順序

Karteは記憶の正本，Runtimeはidentity／profile，会話revision，参加者，scope，発話権，deliveryの正本を所有する．固定の検索→取得→候補生成をbaselineとする．検索語修正で成功率が改善する必要性が確認できた場合だけ，Pydantic AI slimとPi agent-coreを同じlocalモデルと有限予算で比較する．採用部品のrun内historyは一時的であり，記憶保存を意味しない．二重tool loopを置かない．

Pipecatは別の小さなprobeでqueue，frame，割込みを評価する．ASR final／turn epoch／再生ACKを既存契約に対応させ，二重ownerを作らない．委譲可能な処理とadapterの増分を比較して採否を決める．LangGraph，LiveKit，OpenAI Agents SDKはこの狭い単一PCの検証で未充足の要件が出るまで追加しない．

T-130の常駐HTTP案に代え，audio.cppの公式C ABIを既存ProcessWorker内で使用する．HTTP切断をGPU停止と誤認せず，既存terminate→kill→wait→temporary回収を再利用できるためである．Irodori Animeはoffline句生成であり，PCM copy後に既存WAV契約へ戻す．通常Python providerを維持し，新providerはexperimentalかつ明示選択とする．

## 採用条件と縮退

記憶確認は根拠・共有範囲・有限終了・取消後の無効化を満たすこと．音声pipelineはfinal一回，相槌で取消しないこと，遅延audio破棄，実再生区間だけのdeliveryを満たし，保守を減らすこと．TTSは実libraryの連続生成と回収・復帰，品質，計測を分離して判定する．失敗時は既存経路へ戻す．mock合格や資料上の対応を実機合格としない．

実測，依存固定，起動方法と残件は[実験記録](../experiments/reuse-core/README.md)へ記載する．共通設計は横並びephy-reuse-designのADR-0001を参照する．

## 実行後の決定

追加検索を要する合成記録で固定workflowの不足を確認し，Pydantic AIとPiの両方が正しい出典へ到達した．動的検索の実験経路には既存Pythonと同居できるPydantic AIを選び，PiのNode／IPCはprobe内に限定した．通常採用はtimeoutと不要な反復，発話の受け入れやすさの改善後に判断する．各runのhistoryとdispatchだけをSDKへ委譲し，scope・共有許可・候補採否を委譲しない．

Pipecatは実frameと実ASR→Work→TTSで動作したが，Goのdelivery ownerを置き換えずに制御の削減を実証できなかった．通常経路には追加せず，外部turn確定とepoch／ACK対応をprobeに閉じる．audio.cppは参照付きMetalで共通24ケース／29句，取消と次生成を確認したためexperimental経路を残す．詳細値，元モデルとQ8の違い，未確認の聴感・CUDAは[結果](../experiments/reuse-core/results.md)を正本とする．

新providerはdefaultを禁止する．通常Conversationの入力契約を変更せず，検証専用候補を既存Goの生成・再生・C2変換へ渡す内部入口だけを追加する．候補はユーザーの質問やASR finalに変換しない．エラー／根拠不足／古いrevisionは発話せず，利用者がbaselineへ切り替えられる．自動の他provider fallbackは行わない．ランチャーのCtrl-Cだけで通常環境へ戻れる．
