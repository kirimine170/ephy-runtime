# Developer Model Manager

Settingsの`Model & LoRA`で開発者モードを有効にすると，Fast／Work／Codeごとにローカルモデルを選択できます．Embeddingの変更は再indexが必要なため，この画面の対象外です．

## 操作

1. `手元のGGUFを追加`に登録IDを入力し，既存ファイルを選択します．コピーや再ダウンロードはしません．checksum計算中は操作を無効にします．
   Qwen3-8B又はQwen3.8-27B相当の別IDを登録する場合は，Runtimeプロファイルも選択します．IDがプロファイルIDと一致する場合は自動判定します．
2. 役割とモデルを選び，必要なら対応するLoRAを選択します．LoRAなしでもProfileは適用されます．
3. `選択を適用`で保存します．Desktopが起動したモデルなら対象だけを停止し，新モデルの応答準備を確認してGatewayをreloadします．停止中なら次回起動時から反映します．

外部ターミナルで起動したモデルは勝手に停止しません．そのlauncherで停止した後，Runtime画面のStart Fast／Work／Codeから起動してください．Gatewayは`127.0.0.1:8000`を使います．

## 保存と安全性

- `configs/model-registry.local.json`に絶対path，SHA-256，size，source，revision，量子化情報を記録します．登録IDの上書きは拒否します．
- `configs/runtime-selection.local.json`はatomic renameと更新revisionの比較で保存します．既存の`models.local.yaml`やIdentityを変更しません．
- LoRAはbase modelの登録IDとSHA-256の両方へ結び付けます．登録時のbase指定が正しいことは作成者が確認してください．重みの意味的互換性まで自動で証明するものではありません．
- 切替前にGGUFとchecksumを検証します．失敗した選択は保存しません．新モデルの起動／Gateway reloadに失敗した場合は，元の選択と稼働状態の復元を試み，復元結果を表示します．
- Gatewayは実行中のchat／stream／RAG／embedding／evalがあると切替を拒否します．切替中の新規推論には503を返します．異常終了で残った切替leaseは10分で失効します．
- Desktop外からbackendへ直接送られるrequestは管理対象外です．切替中の直接アクセスは避けてください．
- GPU memoryの自動配分や複数modelのschedulerは未実装です．同時稼働数を抑え，大きなモデルを使う前に不要な役割を停止してください．

## Runtime model profile

モデル固有の方針は`configs/model-profiles.yaml`へ集約します．モデルの学習コードやKarte連携へ条件分岐を散らさず，次の情報をRuntimeが読み取ります．

- モデル本来のcapabilityと，現在のRuntimeで有効なcapability
- thinking mode，reasoning effort，thinking保持方針
- native／maximum／local default context
- 起動待ち時間，resource class，host memoryの目安
- llama-serverへ渡すGPU layer数

Qwen3.8-27BはVision capabilityを持ちますが，現在のRuntimeはprojectorと画像入力経路を接続していないため，UIでは`未接続：vision`と表示します．メモリ判定は量子化方式やcontextで変動するため，強制拒否ではなくadvisory warningです．既存registryに保存済みのcontext値は暗黙に変更せず，新規登録時だけプロファイルのlocal defaultを使います．

現行プロファイルは次の2種類です．

- `qwen3-8b`：local default 32K，起動待ち180秒
- `qwen3.8-27b`：local default 32K，native 262K，maximum 1M，起動待ち420秒

モデル開発／fine-tuningはこの仕組みの主経路ではありません．RuntimeとKarteは，profile contractを満たすモデルを差し替えて利用する側として実装します．

## CLI

```sh
.venv/bin/python -m packages.model_registry list
.venv/bin/python -m packages.model_registry import /absolute/path/model.gguf --id my-model --quantization Q6_K --profile qwen3-8b
.venv/bin/python -m packages.model_registry import-adapter /absolute/path/style.gguf --id style-v0 --base-model my-model
.venv/bin/python -m packages.model_registry check --role fast --model-id my-model --adapter-id style-v0
```

`check`はdry-runで，設定やprocessを変更しません．CLIの`select`は設定保存だけで再起動は行わないため，稼働中はDesktopの適用操作を使います．既存selectionのrevisionを`list`から取得し，`--expected-revision`へ指定してください．

downloadは`download --id ID --url HTTPS_URL --sha256 SHA256 --size-bytes BYTES --revision REVISION`で行います．保存先は`models/registry/`です．空き容量を事前確認し，size／SHA-256／GGUF headerが一致した場合だけ公開します．既存ファイルは上書きせず，中断時の一時ファイルを破棄します．再開ではなく再取得する方針です．認証情報やquery parameterを含むURLは受け付けません．

`download`へ`--dry-run`を付けると，ネットワーク通信やファイル作成なしで，必要容量・空き容量・保存先・再開方針を確認できます．取得後も空き容量を再検査します．

## 検証用UI

`desktop/frontend/model-manager-preview.html`は合成catalogだけを扱う画面fixtureです．実設定やprocessは変更せず，production buildのentryにも含めません．

## 実機検証

2026-08-26に既存Qwen3-8B Q6_KとQwen3.8-27B Q4_K_Mを使い，Fast＋Gateway起動，Profile付き通常／stream応答，8B→27B→8B切替，不完全GGUFの起動失敗後の旧モデル復元と会話再開を確認しました．UI fixtureの開発者モード，model選択，互換LoRA絞り込みも確認済みです．口調の自然さやLoRAの改善効果を保証する検証ではありません．

```sh
cd desktop
EPHY_MODEL_INTEGRATION=1 EPHY_RUNTIME_ASSET_ROOT=/absolute/runtime/assets go test -run TestLiveLocalModelSwitch -v -timeout 15m
```

このopt-in testは一時workspaceへsourceを複製し，既存weightを参照します．portが使用中なら拒否し，起動したprocessだけを終了します．通常CIではskipします．
