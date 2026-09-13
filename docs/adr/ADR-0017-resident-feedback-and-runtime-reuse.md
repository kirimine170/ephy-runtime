# ADR-0017：常駐会話と指摘の設定反映

日付：2026-09-14．状態：実装済み，実機受入の残件は検証記録を参照．添付の改訂3に対応する．

## 判断

通常のWails会話画面とInteractionEngine，Whisper ASR，既存SpeechService／ProcessWorker，選択済みモデル・声を再利用する．常駐のsessionと取消・deliveryのownerはDesktopのままとし，別daemonや別会話UIを作らない．初回は休止し，利用者が待受を開始する．最小化では継続し，閉じる操作では終了・回収する．背景captureと実マイクの確認結果はfixture試験と分ける．

継続設定はProfile層の型付きprivate上書き，単独feedbackは既存PreferenceStoreのSQLite接続を再利用した専用tableとする．同一transaction内でfeedback・設定差分・revisionを保存し，二重適用と中途半端な反映を防ぐ．session設定はsessionに限定し，永続設定と混同しない．生成開始時に有効policyをsnapshotし，現在の明示的な依頼を優先する．任意のfeedback本文をsystem promptへ追加しない．undoは対象fieldの変更を新revisionとして記録し，無関係な変更を維持する．保存失敗時も停止は実行し，保存済みと表示しない．

feedbackは取消前のturn／generation／SpeechUnitと既存deliveryへ結びつける．単独評価から架空のA/B pairを作らず，training consentは既定で無効とする．事実訂正はKarteの訂正方針へ渡す対象として扱い，設定反映や重み更新の成功に読み替えない．IdentityとKarte共有権限を上書きしない．本実装はRLHF trainingではない．

最小の能動参加は許可された観測eventに対する有限の検索→取得→候補と，Runtimeのspeak／wait／discard判断に限る．Pi／Pydantic AIの追加loopは既存比較の必要性と結果を確認して選定する．Pipecatは隔離probeで比較し，Goと二重にturnを所有させない．分散実行や学習の完成を最小会話検証の前提にしない．

audio.cppは既存比較branchのC API adapterを差分確認して再利用し，experimental profileを維持する．共有HTTP serverの停止で取消を代用せず，既存child workerのterminate／kill／waitを使う．通常の声は変更しない．外部依存の固定情報と実測・未確認事項は実験記録へ残す．

## 分離とbaseline

- origin/mainをfetchして確認したbaseは`211e681f9153d0e332ded42f0d9ed2cec0f2ae4e`．
- 実装baseは通常版`fix/integration-lifecycle`の`042c3a7`．main以降の5 commitには署名済みアプリの配置，起動管理，音声環境の引渡し，typed chatの読み上げと長い推論の修正があり，動作する既存経路を保つため依存として引き継ぐ．開始時の通常checkoutとreuse checkoutはいずれもclean．
- 主branchは`codex/feat-resident-feedback`，新worktreeは`ephy-runtime-resident`．既存checkoutと`codex/spike-ephy-reuse-core@c509dfc`を変更しない．
- Gateway，port，設定，PID，log，session保存，Preference DB，profile上書き，Wails lockを専用領域へ分離する．共有モデル・referenceは読み取りだけ，既存推論serverは必要時に明記して共有する．所有しないprocessは停止しない．
- rollbackは専用アプリの終了と通常launcherの起動で行う．通常版のProfile，Identity，保存先をmigrationしない．push／PR／merge／releaseは行わない．

## 受入

取消後のfeedback保持，重複・遅延の拒否，設定差分のpromptへの反映，再起動復元，field単位undo，同意と学習exportの境界を自動検証する．関連Python・frontend・Go race検査，repository validatorとアプリbuildを実行する．実機起動・背景化・復帰・実音声の到達点と残件は`docs/RESIDENT_FEEDBACK.md`および実験結果へ追記する．
