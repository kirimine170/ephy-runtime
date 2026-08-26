# ADR-0005：0号固有dataをrepositoryへ保存しない

- Status：Accepted
- Date：2026-08-05

## Context

0号の実Identity，Memory，Profile，関係情報及び学習datasetには，個人情報，privateな会話及び秘密情報が含まれ得る．共有Runtimeと同じrepositoryへ保存すると，公開，fork又はtest fixtureを通じて漏洩する可能性がある．

## Decision

0号の実Identity，Memory，Profile実値，関係情報及びprivate datasetをrepositoryへcommitしない．repositoryには，非実在のexample値，schema，設計及びtestのみを置く．private dataはrepository外，又は明示的にignoreされたprivate rootで管理する．

## Consequences

- repository単体では実個体を起動できず，private rootの明示的な設定が必要になる
- example，test fixture及びlogに実データが混入しないことを継続的に検査する必要がある
- backup，鍵管理，access control及び削除処理はprivate領域側で実装する必要がある
- 姉妹個体又は研究Profileへ共有できるdataを明示的に分類する必要がある
