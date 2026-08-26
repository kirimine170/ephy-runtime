# Growth Protocol設計

## 目的

成長に伴う変更を分類し，提案，検証，評価，承認，適用及びrollbackを一貫して管理する．Identityの不変条件を通常の成長処理から保護する．

## 変更分類

| change type | 内容 |
|---|---|
| Memory Growth | 新しい事実又は経験の追加 |
| Profile Growth | 会話傾向又は表現方法の変更 |
| Policy Growth | 行動規則又はtool利用方針の変更 |
| Capability Growth | 新しい機能又はtoolの追加 |
| Model Growth | model又はLoRAの更新 |
| Identity Change | 原則禁止 |

## Proposal

変更は直接適用せず，proposalとして記録する．

```yaml
proposal_id: "example-proposal-id"
instance_id: "019c0000-0000-7000-8000-000000000000"
change_type: "profile"
target: "profile.addressing.call_name_frequency"
before: "high"
after: "moderate"
reason: "名前呼びの頻度が高く，会話が不自然だったため"
evidence:
  - "evaluation://example"
risk:
  level: "low"
  description: "呼称が減り，距離感が弱くなる可能性"
requires_approval: true
status: "proposed"
created_at: "2026-08-05T00:00:00+09:00"
```

## 適用手順

```text
propose
→ validate
→ evaluate
→ approve
→ apply
→ verify
→ accept又はrollback
```

`validate`では，schema，変更対象及びIdentityの不変条件を検証する．`evaluate`では，期待する効果，回帰，privacy及びsafetyを検証する．承認が必要なproposalは，承認前に適用しない．

## Rollback

Profile，Policy及びModelの変更は，適用前のversion又はartifactをrollback先として保持する．Memoryの訂正及び削除はaudit可能にするが，削除要求に反して内容を復元してはならない．Identityを通常のrollback対象として扱わず，Growth処理から変更できないようにする．
