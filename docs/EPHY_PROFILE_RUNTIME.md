# Ephy Profileのruntime接続

Gatewayは`configs/ephy.local.yaml`で有効にしたときだけ，非公開のIdentity／Profileを読み込みます．public defaultは無効のままです．

```yaml
ephy:
  enabled: true
  private_root: /absolute/path/to/private-data
  instance_id: <instance UUID>
```

`EPHY_PRIVATE_ROOT`と`EPHY_INSTANCE_ID`は同名設定より優先します．ファイル配置は`<private_root>/instances/<UUID>/identity.yaml`と`profile.yaml`です．公開exampleをそのまま実個体として使わず，個体の識別子とgenesisを用意してください．

## 実装済み

- 起動時にSchema，instance ID一致，active状態，private directory内への包含を検証します．壊れた設定では起動しません．
- 通常chat／stream chat／RAG query／stream RAGへ同じProfileを適用します．owner参照，署名，UUID，private pathはmodel promptとhealthへ含めません．
- `/health`は`ephy_enabled`だけを追加公開します．
- chatの`metadata.session_mode`で`default`，`voice`，`writing`，`tech`を選択できます．一人称と個体名は維持します．
- `/v1/admin/reload`は全設定の検証成功後に入れ替えます．失敗時は直前の有効な状態を維持し，private documentの内容をエラーへ出しません．稼働中のimmutable Identity変更は拒否します．
- Profileの変更はreloadまで反映されません．Identityの停止・失効を即座に強制する場合はGatewayを停止してください．

## 対象外

署名検証，clone lease，create／restore／fork，認証付きremote運用は未実装です．Gatewayとbackendはloopbackで使います．Profileは口調の基準であり，LoRAは対応base modelに対して別途評価します．LoRAを外してもIdentityは変わりません．
