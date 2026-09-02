# Ephy Conversation MVP

## 目的

最初のマイルストーンは，ユーザーがローカルのEphyを1コマンドで起動し，WailsのChat画面から日本語で会話できる状態である．RAG，Web検索，coding agent，remote workerを先に完成させることは求めない．

## 完了条件

1．repositoryを別の絶対pathへ移動しても，`./scripts/phase1.sh check`が`llama-server`の実行可能性とmodel pathを検証できる．

2．`./scripts/phase1.sh`により，llama.cpp，Gateway，Wails UIがreadyになってからChat画面を利用できる．

3．Chat画面から「こんにちは，自己紹介して」を送信すると，fast modelのstream応答が最後まで表示される．backend起動失敗時は，無言終了や`unexpected EOF`ではなく原因を表示する．

4．Web検索を有効化しなくても会話でき，prompt，会話内容，private profileを外部へ送信しない．

5．`./scripts/phase1.sh stop`によりmanaged processを停止でき，再起動後も同じ手順を再現できる．

6．Ephy Profileを有効化した場合，一人称，呼称，言語，口調が構造化profileからsystem promptへ一度だけ注入される．無効時は汎用runtimeとして起動できる．

## 最短の実装順

### 1．T-006 Runtime portability regression

移動前のbuild pathを保持したllama.cpp binaryでも，現在の`build/bin`にあるdynamic libraryを解決して起動できるようにする．`phase1.sh check`はfileの存在だけでなく，`llama-server --version`が実行できることまで検証する．

### 2．T-050 Ephy Profile integration

PR #16のschemaとPR #17のloaderをmain基準へ統合し，Gateway起動時にprivate rootからIdentityとProfileを読み込む．`ephy.enabled=true`のときだけ`PromptManager.apply_ephy_profile`をChat requestへ適用する．private値をrepository，log，error responseへ保存しない．

### 3．T-032 Conversation E2E

fast modelを使う最小smokeとWails Chat導線を自動化する．起動，health，stream完了，停止を一つの検証手順にし，移動後のpathでも実行する．

## 今回含めないもの

- RAG精度改善，reranker，Web検索品質．
- coding agentとmutation toolの拡張．
- 音声入出力，自発会話，通知policy．
- LoRA学習，会話dataset収集，preference評価．
- ephy-worker，physical CI，Karteとの本接続．

これらは最初の安定したローカル会話を確認した後に進める．
# 会話用の最小起動

`./scripts/start_conversation.sh`はDesktopを開き，Desktop自身がFastとGatewayだけを起動します．Chatの`Ephyを起動`ボタンからも同じ操作ができます．既定の会話modeはQuickです．Work／Code／embeddingは必要になったときに起動してください．この起動方法なら開発者画面からモデルを切り替えられます．

初回の開発用Profileは`.venv/bin/python scripts/init_local_ephy.py --private-root /absolute/path/to/private-data`で用意できます．既存の`ephy.local.yaml`は上書きしません．生成する個体は署名やclone leaseを持たない開発用であり，正式なlifecycle実装の代わりではありません．

モデル選択は[MODEL_MANAGER.md](MODEL_MANAGER.md)，Profileの境界は[EPHY_PROFILE_RUNTIME.md](EPHY_PROFILE_RUNTIME.md)を参照してください．

Desktopを終了すると，そのDesktop自身が起動したFast／Work／Code／embedding／Gateway／watchへ終了を通知します．他のlauncherが起動したprocessやQdrant／SearXNGは停止しません．終了中の新規起動は拒否します．

ビルド済みappをFinderから開いた場合も，実行ファイルの親directoryからRuntime rootを検出します．開発serverを常駐させず使う場合は，`bash scripts/build_conversation_app.sh`で実行可能fileを作成するか，`desktop`で互換するWails CLIを使って`wails build`を実行してください．`EPHY_START_CONVERSATION=1`を渡して起動すると最小stackも自動起動します．

ローカル開発では，ビルド後に`bash scripts/start_conversation_app.sh`で起動できます．Terminalの実行環境と明示的なRuntime rootを保持してappを直接実行します．`.command`ショートカットからもこのscriptを呼び出せます．終了はappのQuitを使い，起動元のTerminalを先に閉じないでください．

Karteとの会話連携を使う場合は，EphyとKarteの起動前に両方へ同じ`KARTE_DATA_DIR`を設定します．完了したChat応答にはKarte候補が自動表示され，分類相談，Karte Contextによる類似文書確認，create／append選択，Karteへの送信，receipt確認を会話画面内で行えます．Karte確認がpartial／unavailableの場合は自動createせず，カード上で再試行または明示選択を求めます．詳細な受入手順は[KARTE_FILESYSTEM_INTEGRATION.md](KARTE_FILESYSTEM_INTEGRATION.md)を参照してください．

`data/runtime/karte/Karte.app`にKarte bundleがインストールされている場合，`scripts/start_conversation_app.sh`はKarteも自動起動します．同梱方法と無効化設定は[KARTE_FILESYSTEM_INTEGRATION.md](KARTE_FILESYSTEM_INTEGRATION.md)を参照してください．
