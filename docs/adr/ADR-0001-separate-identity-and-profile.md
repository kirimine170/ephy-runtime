# ADR-0001：IdentityとProfileを分離する

- Status：Accepted
- Date：2026-08-05

## Context

個体の同一性と会話上の表現を同じ設定へ格納すると，口調，呼称又は応答傾向の変更が，別個体の生成と同じ意味になってしまう．また，model又はprompt実装を交換した際に，同一性を継続して扱えない．

## Decision

個体が誰であるかを表すIdentityと，どのように話し，振る舞うかを表すProfileを別々に管理する．Profileの変更では`instance_id`を変更しない．

## Consequences

- Profileを独立してversioning，評価及びrollbackできる
- model及びprompt実装を交換してもIdentityを維持できる
- RuntimeはIdentityとProfileを別々に読み込み，整合した会話Policyを生成する必要がある
- application layerでIdentityのimmutable fieldを保護する必要がある
