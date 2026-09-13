# 常駐実装のmain統合と次の検討

2026-09-14．常駐実装をGitHubへ保存し，検証結果に基づいてPR・mergeまで進める依頼に対応する．実行中の通常版と検証版，既存worktreeのbranchは切り替えない．

## 統合する範囲

基点は`origin/main@211e681`．常駐版の実装基点`042c3a7`までには，未マージの起動管理，log pathの引渡し，署名済みbundleの配置，TTS環境の引渡し，文字会話の読み上げと長い推論の修正5件がある．常駐版が利用する既存の起動・会話経路なので，PRに依存として含める．履歴から削除して通常版の修正を失わせない．

その上に，常駐modeと既存音声sessionの接続，単独feedback，型付きProfile上書き，対象発言とdeliveryの保持，復元・undo・取消，有限のKarte参加，experimental audio.cpp，独立した比較probeを追加する．実装前のADR，機能，統合修正，試験記録をローカルcommitに分けている．詳細は[操作手順](RESIDENT_FEEDBACK.md)と[検証結果](experiments/reuse-core/resident-results.md)を参照する．

## 通常版への影響

- 常駐UI・専用lock・保存領域の選択は`EPHY_RESIDENT=1`で有効にする．通常起動のGatewayは従来の8000で，専用launcherは18900を使う．常駐用Gatewayは`EPHY_RESIDENT_ENABLED=1`で設定上書きを有効にする．
- mainへ入れても自動待受やlogin時起動は有効にならない．audio.cpp，Pipecat，agent SDKを通常の既定経路へ切り替えない．optional比較依存も通常のPython依存へ加えない．
- 共通経路の変更には，文字会話の読み上げと生成完了待ち，Irodoriの参照音声処理の共通化，SQLiteのprivate配置・symlink検査の強化が含まれる．新機能の試験に加え，通常会話・既存A/B・既存speechを含む全体回帰で確認する．
- DB tableの拡張は既存A/Bと分離し，単独feedbackを学習用pairへ変換しない．通常版の個人設定や記憶DBをmigrationする起動処理は加えない．
- 配布・起動済みbundleの差替え・共有serverの再起動は今回のmergeに含めない．検証用branchとworktreeを保持し，mainの変更と日常の実行環境更新を別の操作として扱う．

## 統合前の確認

個人設定のない一時worktreeでPython全体回帰，frontend全件・build，Go race検査と合成Karte receiver，契約照合を実行する．公開対象のrepository validatorと機密pattern検査も行う．PRのGitHub Actionsが成功し，最新mainとの競合がないことを確認してから，repositoryで許可されているsquash mergeを行う．差分に破壊的な懸念が残る場合はmergeせずPRを保持する．

ローカルPythonは731件合格・4件skip，frontendは330件合格，Karte契約は45ファイル一致．外側sandboxで失敗したmacOS sandbox専用試験5件は，同じ5件だけを実行権限のある環境で再確認して合格した．CI結果とmergeの記録は対応するPRを正本とする．実マイク・背景capture・sleep復帰の受入をunit testで代用しない．

## 次の開発方針の検討材料

以下は今後の提案であり，実装済み・自動実行予定ではない．

1. この常駐版を体験比較の基準として保持し，実マイク，最小化，割込み，復帰を一続きで測る．失敗の頻度，停止までの時間，応答待ち，資源使用を個別に記録する．
2. 日常の指摘から，どの発言・声・設定が不快だったかを確かめる．定型表現の拡張とUIの整理は，誤分類の取消と対象保持を維持しながら進める．
3. 記憶の訂正と参加者別の共有制限は，Karteを正本とする契約から検討する．現在の文書ID単位の保守的な抑制を，細かな公開範囲の実装完了とは扱わない．
4. 推論engineや音声frameworkの変更は，体験上の課題と計測結果に結びつく場合に比較する．既存の動く経路を保ち，部品の採用と常駐体験の改善を個別に判断する．
