# ADR-0004：Restoreとforkを分ける

- Status：Accepted
- Date：2026-08-05

## Context

backupから同じ個体を復旧する操作と，既存個体を基に新しい個体を生成する操作では，同一性及びprivate dataの扱いが異なる．両者を同じ複製操作として扱うと，同じIDの重複稼働又はprivate Memoryの意図しない継承が起こる．

## Decision

restoreでは元の`instance_id`を維持する．forkでは新しい`instance_id`を発行し，元個体のIDを`parent_instance_id`へ記録する．fork時は，元個体のprivate Memoryを原則として複製しない．

## Consequences

- 同一個体の復旧と新個体の生成を明確に区別できる
- restore時は，同じ`instance_id`が別環境で稼働していないことを検証する必要がある
- fork時は，複製対象を公開可能なtemplate，Policy，skill及び匿名化済みartifactへ限定する必要がある
- lifecycle testでrestoreとforkのID規則を検証する必要がある
