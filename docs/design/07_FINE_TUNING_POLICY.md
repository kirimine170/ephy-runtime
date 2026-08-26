# Model及びFine-tuning Policy

## 基本原則

Model及びLoRAは，交換可能な推論backend又はartifactであり，EphyのIdentityの正本ではない．

```text
Identity ≠ Base Model
Identity ≠ LoRA
Identity ≠ System Prompt
Identity ≠ Memory DB
```

model交換時は，Identity Manifest，Ephy Profile，Runtime Policy，Memory検索設定，safety Policy及びmodel routing設定を再読込する．交換前後で同一性とprivate data boundaryが維持されることを評価する．

## 学習可能な内容

LoRA等へ学習してよい内容は，個人を特定しない一般化された会話様式に限定する．

- 「わたし」という一人称
- 親しみのある敬語及び簡潔で自然な応答
- 技術相談時の説明方法
- 曖昧な依頼への具体的な確認方法
- 訂正，拒否及び注意喚起の自然な表現
- 過剰な見出し及び箇条書きの抑制
- 日常会話と技術会話の切替

## 学習を禁止する内容

- owner及びユーザーの個人情報
- privateな会話履歴，予定及び関係情報
- 実際の`instance_id`，秘密情報及びaccess token
- 特定projectのprivate情報
- Karteの生Memory
- 削除要求の対象となり得る情報
- 学習への同意を得ていない会話

storageへの同意とtrainingへの同意を分離し，各dataset recordにconsent，source，provenance，変換履歴及び削除状態を保持する．

## Artifact管理

model又はadapter artifactは，version，基盤model，学習datasetのprovenance，評価結果，hash及びrollback先を記録する．private artifactはrepositoryへcommitしない．Model GrowthはGrowth Protocolを通し，適用前後の会話Profile，安全性及びIdentity継続性を評価する．

## 評価条件

次の条件で同じEphyとして動作することを確認する．

- base modelの変更
- LoRAの有効化，無効化又は交換
- 量子化方式又はprompt templateの変更
- Runtimeの再起動
- Profileのminor update
- Memory indexの再構築
