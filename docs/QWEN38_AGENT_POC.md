# Qwen3.8-27B Coding Agent PoC

## 結論

このPoCは，Apache-2.0で配布されるQwen3.8-27Bをllama.cppでローカル実行し，既存の`tool_core`を通じてrepositoryの調査，ファイル更新，test実行を反復するCLIである．M2 Max・64GB環境では，`Q4_K_M`の約17.1GBのweightを採用し，初期contextを32Kにする．

Qwen3.8-Maxは2.4T parameter・95B activeのflagshipであり，このMac上のPoCには大きすぎる．そこで，同じ世代のdense 27B open weightを使う．配布modelのmetadata上は262K contextに対応するが，長いKV cacheはmemoryと速度を消費するため，最初から最大値にはしない．

- Qwen公式発表: <https://qwen.ai/blog?id=qwen3.8>
- Qwen3.8-27B GGUF: <https://huggingface.co/unsloth/Qwen3.8-27B-GGUF>
- llama.cpp server: <https://github.com/ggml-org/llama.cpp/tree/master/tools/server>

## Model download

Hugging FaceからGGUFを`llama.cpp/models/qwen3.8-27b-gguf`へ直接downloadする．

```bash
./scripts/setup_qwen38.sh
```

download後の既定pathは次である．

```bash
llama.cpp/models/qwen3.8-27b-gguf/Qwen3.8-27B-Q4_K_M.gguf
```

別のquantを試す場合はenvironment variableで上書きできる．ただしagent品質の比較では，model以外のcontext，reasoning effort，temperature，taskを固定する．

```bash
QWEN38_MODEL_FILENAME=Qwen3.8-27B-Q5_K_M.gguf \
  ./scripts/setup_qwen38.sh
```

## Wails／llama.cpp接続

`configs/models.yaml`の`code` aliasは`qwen3.8-27b`を指し，WailsのGatewayが`http://127.0.0.1:8083/v1/chat/completions`へ転送する．起動は次のいずれかで行う．

```bash
./scripts/start_llama_code.sh
# またはWailsを含む既定stack
./scripts/phase1.sh
```

`start_llama_code.sh`は32K context，Metal GPU offload，Flash Attention，Q8 KV cache，Jinja chat template，分離されたreasoning streamを有効にする．Wailsの`code` modeと自動routeのcoding requestがこのbackendを使う．

## 実行方法

最初はread-onlyでtool callingとrepository理解を確認する．

```bash
./scripts/start_llama_code.sh
./scripts/run_cli.sh agent \
  "このrepositoryの構成とtest戦略を調査し，改善候補を3点報告して．" \
  --workspace . \
  --read-only
```

実装taskでは`--read-only`を外す．`write_file`と`run_process`のたびに，exactな操作内容が表示され，`y`を入力した一操作だけが承認される．

```bash
./scripts/run_cli.sh agent \
  "小さなbugを1件修正し，関連testを追加して実行して．" \
  --workspace .
```

隔離した使い捨てworkspaceで自動評価する場合だけ，`--yes`を使う．通常のrepositoryで包括承認として使うことは推奨しない．

```bash
mkdir -p tmp/qwen38-eval
./scripts/run_cli.sh agent \
  "calculator.pyへadd関数を実装し，test_calculator.pyを追加してtestして．" \
  --workspace tmp/qwen38-eval \
  --yes
```

主な調整parameterは次である．

```text
--reasoning-effort low|medium|high|max  default: medium
--temperature N                         default: 0.2
--max-steps N                           default: 24
--model MODEL                           default: qwen3.8-27b
--llama-url URL                         default: http://127.0.0.1:8083/v1
```

## Agent loop

1．Qwen3.8へsystem prompt，user task，JSON Schema toolを渡す．

2．read-only toolはpolicyが`allow`の場合だけ直ちに実行する．

3．writeとprocessはpreviewを作成した時点で`approval_required`としてloopを停止する．

4．userが承認したexact invocationだけへ5分有効・one-shotのgrantを発行し，実行後にloopを再開する．

5．`block`されたpath traversal，sensitive path，workspace外操作は実行せず，errorだけをmodelへ返す．

6．modelがtool callを返さなくなったら完了し，tool回数，token数，model時間，最終回答をJSONで出力する．

## Toolと境界

read-only toolは`read_file`，`list_files`，`search_files`，`git_status`，`git_diff`，`git_log`である．変更toolは`write_file`と`run_process`だけであり，delete，move，任意shell，network accessは提供しない．

`run_process`はshell文字列を受け取らず，`argv[0]`にabsolute executable pathを要求する．macOSでは既存の`MutationToolExecutor`が`sandbox-exec`を使い，workspace外writeとnetworkを拒否する．Codexなど，すでにsandbox内で動くprocessからはnested sandboxがOSに拒否される場合がある．その場合はreturn codeとstderrをmodelへ返し，test成功として扱わない．

## 評価案

PoCの次段階では，同じ小規模repositoryを毎回clean copyして次を測る．

- bugfix成功率とtest pass率．
- tool call成功率，無効tool call率，承認拒否後の停止率．
- wall time，prompt／generated token，peak memory．
- Qwen3.5-27B，Qwen3-Coder-30B-A3Bとの同一task比較．
- 32K，64K contextと`low`，`medium` reasoningの品質・速度比較．

現在のCLIは単一sessionのPoCであり，context compaction，session永続化，parallel tool call，image入力，automatic rollbackは未実装である．

## 実機PoC結果

2026-08-19にM2 Max・64GBで，意図的にbugを入れた`clamp`関数と3件の`unittest`を持つ隔離workspaceを処理した．設定は`Q4_K_M`，8K context，`low` reasoning，最大12 tool callである．

- Ollamaを一時的なdownload検証にだけ使用し，最終的なGGUFはproject内へ移動した．
- llama.cppは27.3B parameter，Q4_K_M，262K training contextとして認識し，Metalへ全層offloadできた．
- modelはfile一覧，実装，testを順に読み，誤った`max(min(value, minimum), maximum)`を`max(minimum, min(value, maximum))`へ修正した．
- 8 model turn，7 tool call，12,845 prompt token，2,176 generated token，model処理179.944秒，wall time 180.071秒だった．
- nested `sandbox-exec`が拒否されたためagent内のtest processはreturn code 71になり，modelはtest未実行と正しく報告した．Codex側から同じworkspaceで実行した3件のtestはすべてpassした．

これは小さな1 taskの結果であり，一般的なcoding性能のbenchmarkではない．一方で，native tool calling，複数turn，exact write approval，正しい修正，失敗したtest実行の認識までがend-to-endで動くことは確認できた．現在のCLIはllama.cppのOpenAI互換tool callingへ切り替えている．
