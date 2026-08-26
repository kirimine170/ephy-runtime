# Ephy-runtime 開発運用

## 正本

- タスク、優先度、フェーズ、依存関係、完了条件は [Ephyタスク・バックログ管理](https://docs.google.com/spreadsheets/d/1b6-QifgaXWl3TeMMEf7yTq3VxduIrLewyVVlGhiVudo/edit) を正本とする。
- Identity，Profile，Memory，Growth及びInstance Lifecycleの設計仕様は，[`docs/design/`](design/README.md)を正本とする．
- 設計判断と理由は，[`docs/adr/`](adr/README.md)を正本とする．
- machine-readableな制約は`schemas/`，公開可能な設定例は`configs/examples/`で管理する．未実装の間は，対応する設計資料を正本とする．
- 実装開始前に対象Task ID、依存、完了条件を確認する。
- 新しい要件や実装中に判明した課題は、既存Taskへ追記するか新しいTaskとして登録する。
- 実装完了時は状態、進捗、根拠、リスク、最終更新日を更新し、更新履歴を残す。

## 会話と報告

- 会話、設計説明、進捗報告、test結果は日本語を基本とする。
- RAG文書やWeb sourceが英語でも、事実を日本語で説明する。コード、識別子、URL、必要な原文引用は原表記を維持する。
- 実装上の制約、未検証部分、model依存挙動、security上の懸念は完了扱いにせず共有する。
- 作業報告では対象Task ID、変更結果、検証command、残課題を対応付ける。

## Git

- source codeは [kirimine170/Ephy-runtime](https://github.com/kirimine170/Ephy-runtime) の`main`を基準に管理する。
- model、binary、vector index、RAG原本、runtime log、PID、secret、local overrideはcommitしない。
- commit前に`git status`とstaged diffを確認し、実行したtestを記録する。
- 変更は完了条件を満たす単位でcommitし、無関係な変更を混ぜない。

## Verification

標準の完了確認は次を基準にする。

```bash
.venv/bin/python -m pytest -q
(cd desktop && go test ./...)
(cd desktop/frontend && npm test && npm run build)
for f in scripts/*.sh; do bash -n "$f" || exit 1; done
```

model、RAG、runtime lifecycleに関わる変更では、mock testだけでなくlocal stackを使ったsmoke testも行う。
