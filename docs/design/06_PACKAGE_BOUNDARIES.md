# Package Boundary設計

## 目的

共有Runtime及びpackageから，0号固有のIdentity，Profile，Memory及びowner情報を分離する．共有実装を姉妹個体へ再利用しても，private dataが混入しない境界を定義する．

## 現在の共有package

```text
packages/config_core
packages/prompt_core
packages/karte_core
packages/tool_core
packages/llm_runtime
packages/rag_core
packages/eval_core
packages/router_core
packages/runtime_core
packages/web_search_core
```

共有packageに0号固有の値をhard codeしてはならない．Identity，Profile，Memory，Runtime Policy及びmodel routingはinterfaceを介して注入する．

## 計画するdomain package

```text
packages/identity_core
packages/profile_core
packages/memory_core
packages/growth_core
packages/instance_core
```

初期実装では，既存packageとの責務を次のように接続する．

| domain | 既存packageとの関係 |
|---|---|
| `identity_core` | `runtime_core`の起動処理から読み込み，Runtimeへ不変な個体contextを提供する |
| `profile_core` | Profileを会話Policyへ解決し，`prompt_core`がprompt fragmentへ変換する |
| `memory_core` | Memory objectとgovernanceを定義し，`karte_core`及び`rag_core`をstorage／検索adapterとして利用する |
| `growth_core` | `eval_core`の評価結果を参照し，proposalとrollbackを管理する |
| `instance_core` | create，restore，fork及びleaseを管理し，`tool_core`のaudit境界と連携する |

`karte_core`の現在のbundle import／exportをIdentity又はMemoryの正本とはみなさない．`prompt_core`のtemplateもProfileの正本とはみなさない．

## 依存方向

```text
Runtime
  ├── Identityを読み込む
  ├── Profileを読み込む
  ├── Memoryを検索する
  ├── Runtime Policyを適用する
  └── Model／LoRAへ推論を委譲する
```

次の依存を禁止する．

```text
Identity → 特定model
Identity → 特定LoRA
Identity → 特定prompt implementation
Identity → Karteの特定storage implementation

共有Runtime → 0号固有のprivate data
姉妹個体 → 0号のMemory
研究Profile → Ephy本体のprivate Profile
```

## Private root

private dataはrepository外，又は明示的に`.gitignore`された領域へ保存する．

```text
Ephy_private/
└── instances/
    └── <instance_id>/
        ├── identity.yaml
        ├── profile.yaml
        ├── runtime-policy.yaml
        ├── audit/
        ├── memory/
        ├── datasets/
        └── models/
```

private root及び対象個体は，環境変数又はlocal configで指定する．

```bash
EPHY_PRIVATE_ROOT=/path/to/Ephy_private
EPHY_INSTANCE_ID=example-instance-id
```

repositoryの`.gitignore`には，少なくとも次のpatternを含める．

```gitignore
data/private/
instances/
datasets/private/
models/private/
*.identity.yaml
*.private.yaml
*.key
*.pem
*.secret
```

## Public repositoryへ置かない情報

- 実際の`instance_id`及びowner identifier
- 秘密鍵，署名用private key，access token及びsecret
- 0号固有のProfile実値及び関係情報
- KarteのMemory及び会話履歴
- 未匿名化dataset及びconsentのない学習data
- local path及びprivate model artifact

repositoryには，明示的にexampleと分かる非実在値のみを置く．CIでは，実Identity Manifest，private path，secret及び0号固有dataの混入を検査する．
