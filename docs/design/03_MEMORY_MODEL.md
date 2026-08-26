# Memory Model設計

## 目的

Memoryは，Ephyが何を経験し，何を知っているかを管理する．Identity及びProfileから分離し，訂正，削除，忘却，監査及び同意管理を可能にする．

## 分類

| type | 内容 |
|---|---|
| `fact` | 明示的又は検証可能な事実 |
| `preference` | ユーザー又はEphyの好み |
| `relationship` | 呼称，関係性及び距離感 |
| `project` | project固有情報 |
| `event` | 過去に発生した出来事 |
| `interpretation` | 推測又は解釈 |
| `instruction` | ユーザーが指定した継続的設定 |
| `temporary_state` | 期限付きの一時状態 |

## Memory object

```yaml
memory_id: "example-memory-id"
instance_id: "019c0000-0000-7000-8000-000000000000"
subject: "user"
type: "preference"
content: "句読点は「，」「．」を使用する"
source:
  type: "conversation"
  reference: "private://conversation/example"
confidence: 1.0
created_at: "2026-08-05T00:00:00+09:00"
updated_at: "2026-08-05T00:00:00+09:00"
valid_from: "2026-08-05T00:00:00+09:00"
valid_until: null
retention_policy: "until_deleted"
consent:
  storage_allowed: true
  training_allowed: false
status: "active"
supersedes: null
```

## Governance

- Memory候補を無断で永続保存しない
- 保存可否をPolicy又はユーザー承認で決定する
- 出典，confidence及び有効期間を保持する
- 推測を事実として保存しない
- 訂正，削除及び忘却を可能にする
- 削除を検索index等の派生dataへ反映する
- 変更及び削除のaudit logを残す
- storage consentとtraining consentを分離する
- fork時に元個体のprivate Memoryを原則として複製しない

Memory GrowthはIdentity Changeではない．Memoryの追加，訂正又は削除によって`instance_id`を変更してはならない．
