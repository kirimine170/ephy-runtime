# Ephy Schema

このディレクトリは，Ephyの公開可能なmachine-readable contractを管理する．

| Schema | 対象 | Example |
|---|---|---|
| `identity_manifest.schema.json` | 個体の不変識別情報，genesis及び検証metadata | `configs/examples/identity.example.yaml` |
| `ephy_profile.schema.json` | 会話言語，voice，呼称及びstyle | `configs/examples/profile.example.yaml` |
| `memory_object.schema.json` | Memoryの分類，出典，有効期間，保持及びconsent | `configs/examples/memory.example.yaml` |
| `karte-ephy/v1/*.schema.json` | Karte–Ephy V1.1 placement／create／append／receipt filesystem contract | `karte-ephy/v1/fixtures/*.json` |
| `karte-context/v1/*.schema.json` | Karte Context V1 request／response／policy／metadata-only audit contract | `karte-context/v1/fixtures/*.json` |

すべてJSON Schema Draft 2020-12を使用し，schema version 1では未知fieldを拒否する．UUID及びdate-time等の`format`は，format checkerを有効にして検証する．

JSON Schemaは単一documentの構造を検証する．既存Identity Manifestからのimmutable field変更，timestamp間の前後関係，`instance_id`の重複稼働及びhashの再計算は，application layerで検証する．
