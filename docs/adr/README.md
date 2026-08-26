# Architecture Decision Records

このディレクトリは，Ephyの設計判断とその理由を記録する．設計の現在の状態は`docs/design/`，machine-readableな制約は`schemas/`を正本とする．

| ADR | 判断 | Status |
|---|---|---|
| [ADR-0001](ADR-0001-separate-identity-and-profile.md) | IdentityとProfileを分離する | Accepted |
| [ADR-0002](ADR-0002-separate-ordinal-and-instance-id.md) | 表示番号と`instance_id`を分離する | Accepted |
| [ADR-0003](ADR-0003-model-is-not-source-of-identity.md) | ModelをIdentityの正本にしない | Accepted |
| [ADR-0004](ADR-0004-restore-and-fork-are-different.md) | restoreとforkを分ける | Accepted |
| [ADR-0005](ADR-0005-private-instance-data-is-not-committed.md) | 0号固有dataをrepositoryへ保存しない | Accepted |
| [ADR-0006](ADR-0006-local-worker-cli-is-not-remote-node.md) | local worker CLIとremote nodeを分離する | Accepted |
| [ADR-0007](ADR-0007-karte-adapter-is-compatibility-layer.md) | Karte JSON adapterを互換layerとして扱う | Accepted |

新しい判断は，連番のADRとして追加する．既存ADRの判断を変更する場合は本文を上書きせず，新しいADRで置換関係を示す．
