# Ephy Identity・Profile・Instance Lifecycle 設計

このディレクトリは，Ephyの人格，同一性，会話Profile，個体識別，記憶，成長，モデル交換及び姉妹個体への派生に関する設計の正本を管理する．

Ephyの同一性を，単一のLLM，system prompt，LoRA，記憶DB又は実行環境へ閉じ込めないことを基本原則とする．

## 文書一覧

- [01_IDENTITY.md](01_IDENTITY.md)：Identityと個体識別
- [02_PROFILE_AND_CONVERSATION.md](02_PROFILE_AND_CONVERSATION.md)：会話Profileとpromptへの反映
- [03_MEMORY_MODEL.md](03_MEMORY_MODEL.md)：Memoryの分類とgovernance
- [04_GROWTH_PROTOCOL.md](04_GROWTH_PROTOCOL.md)：変更の提案，評価，承認及びrollback
- [05_INSTANCE_LIFECYCLE.md](05_INSTANCE_LIFECYCLE.md)：個体の作成，復元，fork及び状態遷移
- [06_PACKAGE_BOUNDARIES.md](06_PACKAGE_BOUNDARIES.md)：共有実装とprivate dataの境界
- [07_FINE_TUNING_POLICY.md](07_FINE_TUNING_POLICY.md)：Model及びLoRAの位置付けと学習方針

設計判断の理由は[Architecture Decision Records](../adr/README.md)，開発手順は[設計及び実装Workflow](../WORKFLOW.md)，machine-readableな制約は`schemas/`，公開可能な設定例は`configs/examples/`で管理する．議論中の草案は，判断が確定した時点で設計資料，ADR，schema及びtestへ反映する．

## 設計原則

```text
Identity
    個体が誰であるか

Profile
    どのように話し，振る舞うか

Memory
    何を経験し，何を知っているか

Runtime Policy
    何を実行でき，どの承認が必要か

Model／LoRA
    文章生成及び推論をどの実装で行うか

Growth Protocol
    変更をどのように提案，評価，承認，適用，rollbackするか
```

Runtimeは各層の正本を読み込み，Policyを適用した上でModel又はLoRAへ推論を委譲する．Identityから特定model，LoRA，prompt実装又はstorage実装への依存を禁止する．

## 公開範囲

repositoryには，設計，schema，example値及びtestのみを置く．実際の`instance_id`，owner identifier，秘密鍵，0号固有のProfile，Memory，会話履歴，未匿名化dataset，access token，local path及びprivate model artifactはcommitしない．

## 初期実装の完了条件

- Identity及びProfileのschemaとloaderが存在する
- example YAMLがschema validationを通る
- immutableなIdentity fieldの変更をapplication layerで拒否できる
- Profile又はmodelの変更で`instance_id`が変化しない
- restoreでは`instance_id`を維持し，forkでは新規発行する
- Runtimeが構造化Profileからprompt policyを生成する
- private data boundaryがtestで検証される
- 既存のPython，Go及びFrontendの回帰testを破壊しない

初期実装では，blockchain，分散型ID，外部認証局，完全な鍵管理基盤，Memory DBの全面実装，LoRA学習処理，姉妹個体の本番生成，cloud同期及び複数端末間の自動競合解決を対象外とする．
