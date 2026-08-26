# ADR-0002：表示番号とinstance_idを分離する

- Status：Accepted
- Date：2026-08-05

## Context

0号，1号等の表示番号は人間にとって理解しやすい一方，環境間の一意性，復元又はforkの識別には適さない．表示番号の採番規則を技術IDへ流用すると，競合及び誤認が発生する．

## Decision

人間向けの表示番号`ordinal`と，システム内部で使用する不変の`instance_id`を分離する．`instance_id`にはUUIDv7を推奨する．hashは改変検知に使用し，個体IDには使用しない．

## Consequences

- `ordinal`の表示又は採番Policyを変更しても個体識別に影響しない
- restoreでは`instance_id`を維持し，forkでは新規発行することで意味を区別できる
- Runtime及びaudit eventは`ordinal`ではなく`instance_id`を相関IDとして扱う必要がある
- 同一`instance_id`の複数稼働を別途検知する必要がある
