# Identity設計

## 目的

Identityは，Ephyの個体としての同一性を表す．基盤model，LoRA，Profile，Memory，prompt又は実行環境を変更しても，同じ個体として継続できなければならない．

## 0号

0号の公開可能な識別情報は，次の通りとする．

```yaml
lineage_name: "Ephy"
individual_name: "エフィ"
ordinal: 0
```

`Ephy`は系統又はproject全体，`エフィ`は個体名，`ordinal`は人間向けの表示番号として，それぞれ分離して扱う．0号の「エフィ」は原則として変更不可とする．別名が必要な場合は，既存名を削除せずaliasとして追加する．

## 個体識別

個体識別には，`ordinal`と`instance_id`を使用する．

- `ordinal`は，0号，1号などの人間向け表示番号であり，一意性の保証には使用しない
- `instance_id`は，生成時に一度だけ発行する不変の技術IDであり，UUIDv7を推奨する

`instance_id`はmodel，LoRA，Profile又はMemoryの変更，Runtimeの再起動及びbackupからの復元で維持する．別個体へのforkでは新規発行する．

hashは，Manifest，backup，dataset及びmodel artifactの改変検知に使用し，個体IDには使用しない．Identity Manifestの更新によって値が変わるため，Manifest全体のhashを`instance_id`としてはならない．推奨algorithmはSHA-256とする．

## Identity Manifest

公開repositoryにはexample値のみを置く．

```yaml
schema_version: "1.0.0"

identity:
  lineage_name: "Ephy"
  individual_name: "エフィ"
  ordinal: 0
  instance_id: "019c0000-0000-7000-8000-000000000000"
  parent_instance_id: null
  created_at: "2026-08-05T00:00:00+09:00"
  status: "active"

genesis:
  runtime_version: "0.1.0"
  profile_version: "0.1.0"
  genesis_manifest_hash: "sha256:example"
  created_by: "owner"

ownership:
  owner_reference: "private://owner"
  owner_data_embedded: false

verification:
  signature_algorithm: null
  public_key_id: null
  signature: null
```

必須項目は，`schema_version`，`identity.lineage_name`，`identity.individual_name`，`identity.ordinal`，`identity.instance_id`，`identity.created_at`，`identity.status`及び`genesis.genesis_manifest_hash`とする．

## 不変条件

次のfieldは，通常のProfile変更又はGrowth処理では変更できない．

```text
schema_version
identity.lineage_name
identity.individual_name
identity.ordinal
identity.instance_id
identity.parent_instance_id
identity.created_at
genesis.genesis_manifest_hash
```

JSON Schemaは単一documentの形式を検証し，application layerは更新前後を比較して不変条件を検証する．

```python
IMMUTABLE_IDENTITY_FIELDS = {
    "schema_version",
    "identity.lineage_name",
    "identity.individual_name",
    "identity.ordinal",
    "identity.instance_id",
    "identity.parent_instance_id",
    "identity.created_at",
    "genesis.genesis_manifest_hash",
}
```

## Service境界

Identity serviceは，少なくとも次の操作を提供する．

```python
class IdentityService:
    def load(self, path: Path) -> IdentityManifest: ...
    def validate(self, manifest: IdentityManifest) -> ValidationResult: ...
    def verify_hash(self, manifest: IdentityManifest) -> bool: ...
    def compare_immutable(
        self,
        before: IdentityManifest,
        after: IdentityManifest,
    ) -> list[IdentityViolation]: ...
    def create_instance(
        self,
        request: CreateInstanceRequest,
    ) -> IdentityManifest: ...
    def restore_instance(self, backup_path: Path) -> IdentityManifest: ...
    def fork_instance(
        self,
        source: IdentityManifest,
        request: ForkInstanceRequest,
    ) -> IdentityManifest: ...
```

## 検証要件

- schemaに適合するManifestを受理する
- 不正なUUID及び負の`ordinal`を拒否する
- immutable fieldの変更を拒否する
- Profile又はmodelの変更で`instance_id`が変化しない
- restoreでは`instance_id`を維持し，forkでは新規発行する
- 同じ`instance_id`の複数稼働を検知できる
