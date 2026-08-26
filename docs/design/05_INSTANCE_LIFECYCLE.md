# Instance Lifecycle設計

## 状態

個体は，次の状態のいずれかを持つ．

```text
provisioning
active
suspended
archived
revoked
```

状態遷移はaudit logへ記録する．`revoked`からの再有効化は通常の状態遷移として扱わず，明示的な復旧Policyを必要とする．

## 新規作成

新規個体の作成時は，次を実施する．

1. 新しい`instance_id`を発行する
2. 利用可能な`ordinal`を割り当てる
3. `parent_instance_id`を設定する
4. 個体名を設定する
5. private Identity Manifestを生成する
6. genesis hashを生成する
7. 初期Profileを作成する
8. Memory storageを空で初期化する
9. audit logへ記録する

## 復元

backupから同一個体を復元する場合は，元の`instance_id`を維持する．復元前に，Manifest hash，backup source，schema version，最終audit sequence，Memory index，Profile version及びRuntime versionを検証する．同じ`instance_id`が別環境でactiveになっている場合は，競合を解消するまで起動を停止する．

## Fork

既存個体を基に別個体を作成する場合は，新しい`instance_id`を発行し，元個体のIDを`parent_instance_id`へ記録する．

```yaml
instance_id: "new UUID"
parent_instance_id: "source UUID"
```

forkで複製できるものは，公開された一般Profile template，共通Runtime設定，公開可能なskill，個人情報を含まない学習済みadapter及びsystem共通Policyに限定する．元個体のprivate Memory，実Profile及びowner情報は，原則として複製しない．

## Clone検知

同じ`instance_id`が複数のactive環境で起動した場合は，警告又は起動停止を行う．少なくとも次のlease情報を記録する．

```yaml
instance_id: "019c0000-0000-7000-8000-000000000000"
runtime_id: "example-runtime-id"
host_fingerprint_hash: "sha256:example"
started_at: "2026-08-05T00:00:00+09:00"
lease_expires_at: "2026-08-05T00:05:00+09:00"
```

host fingerprintの生値は保存せず，秘密情報を含まない安定した入力からhashを生成する．leaseは期限切れ，grace period及び時計ずれを考慮して判定する．

## 検証要件

- createは新しい`instance_id`を発行する
- restoreは元の`instance_id`を維持する
- forkは新しい`instance_id`と元個体の`parent_instance_id`を設定する
- forkへprivate Memoryが混入しない
- 同じ`instance_id`の重複active leaseを検知する
- archive及びrevokeをauditできる
