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
