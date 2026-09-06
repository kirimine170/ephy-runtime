# Interaction Loop B0 baseline

2026-09-06 に main `4eae29f2bc6789a299ed848c343e69dfdbe34aef` を基準として検証した．

- PR #37 と同一の旧版コピー7件は，利用者の承認後，元path／regular file／SHA-256／mtimeを再検証して Git worktree 外へ退避した．元ファイルは削除していない．完全一致コピー1件は先に退避済み．source の重複は0件．ignored dependency／bytecode 内の類似名は変更していない．
- repository tooling tests の fixture は26個の必要入力のみをコピーし，model，private設定，dependencyを複製しない．許可入力のsymlinkや非regular fileを拒否する．
- bindings は `GOCACHE=/private/tmp/ephy-runtime-go-cache python3 scripts/generate_desktop_bindings.py` で Wails の正規generatorを実行し，class順序／空白を正規化する．2回の生成がbyte単位で一致した．
- 実checkoutの `.venv/bin/python -m pytest -q` は366 passed，3 skipped．`go test ./...`，Frontend 63 tests，production build，repository validation，shell syntax，diff check は成功した．macOSのprocess sandboxを検証するtestは通常ホスト環境で実行する．
- repo 所有のscriptでGatewayを更新し，healthを確認した．4／8 messagesのplanning履歴を各3回検証した．planのみを使用し，publishは呼び出していない．更新後の参考中央値は0.93／0.80 ms．C0のASR／LLM／TTS latencyではない．
- baseline修復とC0の実装commitを分ける．Git外のrecovery内に各path，hash，mtime，検証logを保全している．

B0時点ではC0の音声一周と実マイク試験は未実装・未検証．C0の実装と受入証拠は別途記録する．
