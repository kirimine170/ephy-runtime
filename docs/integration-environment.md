# ローカル統合テスト環境

`scripts/integration_stack.py` が，Karte，Runtime，ASR，TTSと関連サービスのビルド・起動・終了を扱う．過去の検証ログや完了済みworktreeを起動依存にしない．

署名済みアプリの保存先 `home` は，同期対象外の `~/Library/Application Support/Ephy Integration` とする．Desktopや同期サービスの配下では，ビルド後に付加されるFinder情報がmacOSの署名検証を妨げることがある．モデルとログはそれぞれの設定パスに置く．パスの `~` は実行ユーザーのホームへ展開する．

このMac用のパスは `configs/integration.sample.json` をもとに `configs/integration.local.json` へ保存する．秘密の値をこのJSONに書かず，既存のTTS credentialファイルのパスを指定する．Karteの会話データと記録ON/OFFは既存の保存先を引き継ぐ．

```sh
.venv/bin/python scripts/integration_stack.py build --config configs/integration.local.json
.venv/bin/python scripts/integration_stack.py start --config configs/integration.local.json
.venv/bin/python scripts/integration_stack.py status --config configs/integration.local.json
.venv/bin/python scripts/integration_stack.py stop --config configs/integration.local.json
```

`build` は実APIを使うKarteとRuntimeをビルドし，WhisperとKarte制御helperをRuntimeへ同梱する．Whisper helperのソースとhashが一致しない場合は先に再ビルドする．Karte制御helperの固定commitはGitから一時的に取り出すため，そのcommit専用のworktreeは不要である．ネイティブAPIの代替実装が含まれるKarteや，hashが一致しない実行物は起動しない．

`start` は起動中のサービスを再利用し，Irodoriの認証済み声一覧，初回の実音声生成，Karteの受信プロトコルを確認してからRuntimeを開く．Irodoriの準備確認で生成した音声は再生も保存もしない．同じIrodoriプロセスと声設定での再起動では，確認済みの準備状態を再利用する．Runtime自身が指定のWhisperを起動したことも確認する．初回のモデル読込完了はRuntimeのASR表示で確認する．マイクの開始・記録設定の変更・音声再生は起動コマンドから操作しない．

初回起動では，Karteの文書初期化とRuntimeのWhisper起動をそれぞれ最大180秒待機し，待機中の処理を表示する．アプリが途中で終了した場合は待機を打ち切る．TTSのPythonには仮想環境内の実行パスを指定し，シンボリックリンクの参照先へ置き換えない．

`stop` は通常のアプリ終了を先に行う．未保存の編集があり終了を取り消した場合は，依存サービスの停止へ進まない．起動時に記録したPIDと起動情報が変わったプロセスを停止しない．

統合テストでは，サービスの起動確認に加え，同じ配布アプリでASR認識，LLM応答，TTS生成・再生，会話保存，Karteの一覧更新・読込，未保存編集の保護，終了・再起動を検証する．単体テストやブラウザ用APIだけの成功を，ネイティブ統合テストの完了と扱わない．
