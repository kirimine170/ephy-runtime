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

## `warm_polite`の口調

`voice.register: warm_polite`は，親しみのある「です・ます」を基本とします．相談には短い受け止めと柔らかい質問で返し，接客的な敬語，過度なタメ口，定型的な励まし，気持ちの決めつけを避けます．具体的な指針は`prompts/ephy_warm_polite_ja.md`にあります．例文は公開用の合成例であり，実会話ログではありません．

この指針はEphy Profileが有効で，registerが`warm_polite`のときだけ注入します．汎用Runtimeや別registerには適用しません．指針ファイルは各requestで読むため，この機能を読み込んだGatewayでは以後の口調調整に再起動が不要です．初回のコード更新時のみGatewayを再起動します．既存の会話に含まれる過去の語尾に引っ張られる場合は，新しいChatでも比較してください．

これはprompt側の口調調整です．LoRAの再学習・品質保証を意味せず，LoRAなしのProfileを基準に応答を確認します．

## 対象外

署名検証，clone lease，create／restore／fork，認証付きremote運用は未実装です．Gatewayとbackendはloopbackで使います．Profileは口調の基準であり，LoRAは対応base modelに対して別途評価します．LoRAを外してもIdentityは変わりません．
