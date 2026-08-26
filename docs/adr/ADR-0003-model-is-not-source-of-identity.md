# ADR-0003：ModelをIdentityの正本にしない

- Status：Accepted
- Date：2026-08-05

## Context

base model及びLoRAは，交換，量子化，蒸留，再学習又は廃止の対象になる．これらをIdentityの正本にすると，artifactを変更するたびに個体の同一性が失われる．

## Decision

base model，LoRA，system prompt及びMemory DBを，EphyのIdentityの正本にしない．これらは，構造化されたIdentity，Profile，Policy及びMemoryを利用する交換可能な実装として扱う．

## Consequences

- model交換後も同じ`instance_id`を維持できる
- Runtimeは推論backendから独立してIdentity及びProfileを読み込む必要がある
- model又はLoRAの更新時に，Identity継続性及びProfile再現性の評価が必要になる
- model固有の能力差はIdentityではなくCapability又はModel Growthとして管理する
