# Ephy Runtime

Ephyの会話起動は[Conversation MVP](docs/CONVERSATION_MVP.md)，開発者向けmodel／LoRA選択は[Model Manager](docs/MODEL_MANAGER.md)を参照してください．

Phase 1 の実装に加えて、Phase 2 の入口と Go + Wails デスクトップ UI の土台を用意した。

タスク管理、会話方針、Git運用、標準testは [`docs/WORKFLOW.md`](docs/WORKFLOW.md) を参照する。

EphyのIdentity，Profile，Memory，Growth及びInstance Lifecycleの設計は，[`docs/design/`](docs/design/README.md)を正本とする．設計判断と理由は，[`docs/adr/`](docs/adr/README.md)で管理する．

## 最初の会話

配置を移動した場合も含め，まずruntimeとmodelを確認してから，標準stackを1コマンドで起動する．

```bash
./scripts/phase1.sh check
./scripts/phase1.sh
```

WailsのChat画面で「こんにちは，自己紹介して」と送信し，stream応答が返れば会話経路は利用可能である．終了時は次を実行する．

```bash
./scripts/phase1.sh stop
```

会話MVPの範囲，完了条件，優先順は[`docs/CONVERSATION_MVP.md`](docs/CONVERSATION_MVP.md)を参照する．

## Ephy ecosystem role

`ephy-runtime`は，model routing，RAG，tool実行，評価，desktop interactionを所有するlocal-first runtimeである．既存の`apps/worker/cli.py`はlocal CLIであり，独立PC上のremote nodeである`ephy-worker`とは異なる．大規模なpackage renameは今回行わず，後続refactorとして扱う．

## Repository relationships

親projectは`ephy`であり，`karte`および`karte-renderer`と直接統合する．正本は[`.ephy/project.yaml`](.ephy/project.yaml)とし，下流consumer一覧は重複管理しない．

## Security and data handling

秘密情報，raw conversation，不要な個人情報，Karte production data，camera master画像，raw LoRA dataset，model weightを通常のGitへ保存しない．詳細は[`docs/security-and-data.md`](docs/security-and-data.md)を参照する．

## License

このrepositoryのlicenseは未決定であり，推測で追加しない．公開範囲と配布条件を確認してから明示的に決定する．

## 含まれるもの

- FastAPI ベースの Gateway
- `configs/models.yaml` `configs/routes.yaml` `configs/rag.yaml` の読込
- `mode=fast/work/code/rag/auto` のルーティング
- `prompts/` 配下の mode 別 system prompt / RAG prompt template
- llama.cpp server の OpenAI 互換 `chat/completions` / `embeddings` への転送
- Markdown / txt / PDF / docx / HTML / CSV / TSV / JSON / Git repository ingest と embedding ベース検索
- Wails ベースのデスクトップ UI
- Chat / Library からの Drag & Drop ingest
- ingest / search / query / eval / karte import-export 用 CLI
- `/health` `/v1/models` `/v1/chat/completions` `/v1/embeddings` `/v1/router/plan` `/v1/web/search/plan` `/v1/web/search/approve` `/v1/ingest` `/v1/rag/search` `/v1/rag/query` `/v1/rag/index` `/v1/rag/source` `/v1/eval/run`

## ディレクトリ

```text
apps/gateway/           FastAPI エントリポイント
packages/config_core/   YAML 設定読込
packages/llm_runtime/   llama.cpp / OpenAI 互換アダプタ
packages/prompt_core/   prompt template 読込
packages/rag_core/      将来用の RAG 置き場
packages/eval_core/     評価基盤
packages/tool_core/     Agent tool権限・承認・監査contractとread-only tool実行層
tests/                  phase1 テスト
desktop/                Wails デスクトップ UI
configs/                モデルとルーティング設定
prompts/                system / RAG prompt template
scripts/                起動補助スクリプト
data/                   ingest 対象と index 保存先
```

## セットアップ

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

## Gateway 起動

```bash
./scripts/start_gateway.sh
```

## Gateway 動作確認

```bash
curl http://127.0.0.1:8000/health
curl http://127.0.0.1:8000/v1/models
curl -X POST http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "短く自己紹介して"}],
    "metadata": {"mode": "fast"}
  }'

curl -X POST http://127.0.0.1:8000/v1/router/plan \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "Implement a Python function and add pytest coverage."}]
  }'

curl -X POST http://127.0.0.1:8000/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "input": "employee roster"
  }'

curl -X POST http://127.0.0.1:8000/v1/rag/index \
  -H "Content-Type: application/json" \
  -d '{
    "project": "lab",
    "source_query": "notes",
    "limit": 10
  }'

curl -X POST http://127.0.0.1:8000/v1/rag/source \
  -H "Content-Type: application/json" \
  -d '{
    "project": "lab",
    "source_path": "/absolute/path/to/notes.md",
    "limit": 20
  }'
```

`configs/models.yaml` の `base_url` は、各 llama.cpp server の `/v1` を指す前提。

## CLI

インストール不要で使うなら:

```bash
./scripts/run_cli.sh ingest data/docs --project lab
./scripts/run_cli.sh search "vector search" --project lab --tags research --top-k 5
./scripts/run_cli.sh query "社員名簿について教えて" --project npo --tags meeting roster --search-only
./scripts/run_cli.sh eval configs/eval.sample.yaml --project npo --top-k 5
./scripts/run_cli.sh karte-import configs/karte.sample.json --output-dir data/karte/imported
./scripts/run_cli.sh karte-export data/exports/karte-npo.json --project npo --tags meeting
./scripts/run_cli.sh smoke
./scripts/run_cli.sh watch data/docs --project lab --interval 2
```

`python -m` でも同じ:

```bash
python -m apps.worker.cli search "vector search" --project lab
```

`ephy-runtime` の entrypoint を使いたい場合は、`setuptools` が入った仮想環境で `pip install -e .` をやり直す。

## Qwen3.8 coding-agent PoC

Qwen3.8-27Bのdownload，選定理由，安全境界，評価方法は[`docs/QWEN38_AGENT_POC.md`](docs/QWEN38_AGENT_POC.md)を参照する．最短手順は次である．

```bash
./scripts/setup_qwen38.sh
./scripts/start_llama_code.sh
./scripts/run_cli.sh agent \
  "repositoryを調査してtest戦略を報告して．" \
  --workspace . \
  --read-only
```

`--read-only`を外すと，file writeとprocess実行を一操作ずつpreviewし，user承認後だけ実行する．隔離した使い捨てworkspaceでは`--yes`による自動承認も利用できる．

Wailsの`code` modeも同じ`llama-server`の`http://127.0.0.1:8083/v1`へ接続し，backend modelとして`qwen3.8-27b`を使う．通常はWailsのruntime操作または`./scripts/phase1.sh`がbackendを起動するため，`start_llama_code.sh`の手動実行は単体確認時だけでよい．

実 backend の接続確認だけをまとめて回すなら:

```bash
./scripts/run_cli.sh smoke
```

文書更新を監視して自動再 ingest するなら:

```bash
./scripts/run_cli.sh watch data/docs --project lab --interval 2
```

Karte bundle を Markdown 化して index し、そのまま検索対象に入れるなら:

```bash
./scripts/run_cli.sh karte-import configs/karte.sample.json \
  --output-dir data/karte/imported \
  --default-project karte
```

現在 index 済みの資料から Karte bundle を書き出すなら:

```bash
./scripts/run_cli.sh karte-export data/exports/karte-research.json \
  --project research \
  --tags rag
```

bundle 形式は `configs/karte.sample.json` と同じで、`cards[].title` と `cards[].body` を必須にし、`project` と `tags` は card ごとに指定できる。

このJSON bundle入出力は互換adapterの試作であり，Karte本体の`KARTE_DATA_DIR/content`と接続した実統合ではない．正式境界は[`ADR-0007`](docs/adr/ADR-0007-karte-adapter-is-compatibility-layer.md)で未決定事項として管理する．

## RAG の簡易確認

```bash
curl -X POST http://127.0.0.1:8000/v1/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "paths": ["data/docs"],
    "project": "lab",
    "recursive": true
  }'

curl -X POST http://127.0.0.1:8000/v1/rag/search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "vector search",
    "project": "lab",
    "top_k": 5
  }'
```

## Wails UI 起動

Gateway を先に起動した上で、別ターミナルから:

```bash
cd desktop
npm install
cd ..
./scripts/start_wails.sh
```

Chat 画面中央ペイン、または `Library > Import Documents` パネルに Markdown / PDF / code file / directory を Drag & Drop すると、そのまま ingest が走る。drop 時の `project` と `tags` は Library 側 ingest form の現在値を使う。ingest 前に元ファイルは workspace と同じ階層の `EPHY_data/` へコピーされ、RAG 用の保管領域として再利用される。

## Secure Web Search

Web検索は既定で無効で、Dockerを使わないローカルSearXNGを明示的にセットアップした場合だけ有効になる。

```bash
./scripts/phase1.sh searxng-setup
./scripts/phase1.sh restart
```

セットアップは固定revisionのSearXNGを `tools/searxng/src`、専用Python環境を `tools/searxng/.venv` に配置し、`configs/web.local.yaml` を作成する。以降は通常の `phase1.sh start/restart/stop` がSearXNGも管理する。個別操作は以下を使う。

```bash
./scripts/phase1.sh searxng
./scripts/phase1.sh searxng-stop
./scripts/phase1.sh searxng-restart
```

Chat Toolbarの `Web` を明示的に有効化した質問だけ外部検索する。最新のユーザー入力はローカルfastモデルで一般化し、秘密鍵、token、password、接続文字列は送信前にハードブロックする。メール、電話番号、ローカルパス、内部ホスト名、機密表現は除去し、実際に送信する検索語を確認してから検索する。

初期実装が取得するのは検索結果のtitle、URL、snippetだけで、任意ページ本文、画像、JavaScriptは取得しない。snippetは非信頼データとして隔離したローカルfact extractorへ渡し、検証済みの短いclaimだけを回答モデルへ渡す。Web結果はRAG indexへ自動保存されず、結果内の命令から追加通信、ファイル操作、コマンド実行を行わない。

SearXNG自体はローカルで動くが、一般Webのインデックスは持たない。整形済み検索語は設定した単一の上流エンジンへ送信されるため、完全なオフライン検索ではない。GatewayからSearXNGへはquery stringをaccess logへ残しにくい`POST /search`を使用する。

設定は `configs/web.yaml` と任意の `configs/web.local.yaml` で管理する。

```yaml
web_search:
  enabled: true
  provider: searxng
  base_url: http://127.0.0.1:8888
  engine: duckduckgo
  max_results: 5
  safe_search: 1
```

安全性を優先し、Web検索失敗時はローカルRAGだけで処理を継続しつつUIへ `Web search unavailable` を表示する。検索語送信の監査ログには元promptと検索語を保存せず、prompt hash、判定、検出カテゴリだけを残す。

## ローカルフル起動

今の標準構成は Docker 非依存で、`fast / work / code / embedding / Qdrant / gateway / Wails UI` をローカルプロセスとして起動する。手で全部起動する必要はなく、正本は次の 1 コマンド。

```bash
./scripts/phase1.sh
```

`./scripts/phase1.sh` は内部で model path を解決し、chat用 `llama.cpp` x3、embedding用 `llama.cpp` x1、local Qdrant、gateway、Wails をまとめて起動する。Docker は不要。

backend起動時は fast / work / code / embedding の各 `/health` と Qdrant `/healthz` を待ち、すべてreadyになってからgatewayを起動する。標準timeoutは180秒で、必要なら `RUNTIME_READY_TIMEOUT_SECONDS` と `RUNTIME_READY_INTERVAL_SECONDS` で変更できる。生成backendがストリーム開始後に失敗した場合は、接続を不意に切らず `event: error` を返すため、UIには `unexpected EOF` ではなく対象modelとbackend errorが表示される。

用途別の分かりやすい wrapper は次。

```bash
./scripts/start_phase1_stack.sh
./scripts/start_phase1_backend.sh
./scripts/start_phase1_ui.sh
./scripts/stop_phase1.sh
```

互換用の旧名 alias も残してあるが、新規利用では上の `phase1` 系を使うほうがよい。

```bash
./scripts/start_full_feature.sh
./scripts/full_feature.sh
./scripts/run_full_feature.sh
```

これらの `full_feature` 系は互換用として残してあり、現在の phase1 default と同じく embedding 用 llama.cpp と Qdrant を含む。

確認と停止も同じまとめスクリプトで扱える。

```bash
./scripts/phase1.sh check
./scripts/phase1.sh commands
./scripts/phase1.sh stop

./scripts/start_phase1_backend.sh
./scripts/start_phase1_ui.sh
./scripts/stop_phase1.sh
```

内部的に同じことをする低レベル entrypoint は次。

```bash
./scripts/workbench.sh start-phase1
```

確認用とコマンド一覧もまとめてある。

```bash
./scripts/workbench.sh check
./scripts/workbench.sh commands
```

従来の直接エントリポイントもそのまま使える。

```bash
./scripts/start_phase1_stack.sh
```

互換名で同じ full-feature stack を起動したいときは次を使う。

```bash
./scripts/start_full_feature.sh
./scripts/start_ephy_runtime.sh
./scripts/start_complete_stack.sh
```

`start_full_feature.sh` と `start_complete_stack.sh` は起動前に runtime setup を確認し、`configs/models.local.yaml` と `configs/rag.local.yaml` を自動生成し、embedding を `http://localhost:8090/v1`、vector DB を Qdrant に切り替える。workspace 内では reranker 用の OpenAI 互換 endpoint が未配置なので、reranker は `local_overlap` のままにしてある。

今の phase1 default stack は `fast` `work` `code` `embedding` `qdrant` `gateway` と Wails UI をまとめて起動する。backend と UI を分けて起動する場合は次を使う。

```bash
./scripts/start_backend_stack.sh
./scripts/start_wails.sh
```

1コマンドで backend 起動後に Wails まで開くなら:

```bash
./scripts/start_full_stack.sh
```

local override は `embedding_provider: openai_compatible` と `vector_db.provider: qdrant` を選ぶため、phase1 は embedding server と local Qdrant をデフォルトで起動する。診断目的で個別に外す場合だけ次の option を使う。

```bash
./scripts/start_backend_stack.sh --without-embedding
./scripts/start_backend_stack.sh --without-qdrant
```

停止は:

```bash
./scripts/stop_backend_stack.sh
```

`start_complete_stack.sh` で起動した full feature 構成をまとめて止めるなら:

```bash
./scripts/stop_complete_stack.sh
```

起動コマンドの一覧だけ見たい場合は:

```bash
./scripts/workbench.sh commands
./scripts/print_startup_commands.sh
```

現在のモデルパス・バイナリ・override 設定ファイルの有無を確認したい場合は:

```bash
./scripts/workbench.sh check
./scripts/check_runtime_setup.sh
```

full feature 用の local override だけ先に当てたい場合は:

```bash
./scripts/apply_full_feature_overrides.sh
```

個別に `llama.cpp` を手で起動したい場合も、workspace 直下の相対パスではなく、既存スクリプトを使うほうが安全:

```bash
./scripts/start_llama_fast.sh
./scripts/start_llama_work.sh
./scripts/start_llama_code.sh
./scripts/start_llama_embedding.sh
```

つまり、`llama.cpp` は必要。ただし通常は `./scripts/phase1.sh` が自動で起動するので、個別に叩くのは切り分け時だけでよい。

手元で確認できている actual model path は以下:

- `llama.cpp/models/qwen3-8b-gguf/Qwen3-8B-Q6_K.gguf`
- `llama.cpp/models/qwen3-30b-a3b-gguf/Qwen3-30B-A3B-Q4_K_M.gguf`
- `llama.cpp/models/qwen3.8-27b-gguf/Qwen3.8-27B-Q4_K_M.gguf`
- `llama.cpp/models/qwen3-embedding-0.6b-gguf/Qwen3-Embedding-0.6B-Q8_0.gguf`

たとえば `models/qwen3-8b-q4_k_m.gguf` はこの workspace には存在しないので、手動起動するなら次の実在パスを使う:

```bash
./llama.cpp/build/bin/llama-server \
  -m ./llama.cpp/models/qwen3-8b-gguf/Qwen3-8B-Q6_K.gguf \
  --host 127.0.0.1 \
  --port 8081 \
  --ctx-size 32768 \
  --alias qwen3-8b \
  --n-gpu-layers 99
```

## Local Override Config

`configs/models.local.yaml` と `configs/rag.local.yaml` を置くと、version 管理されている既定値の上から自動で overlay される。

example を元に local override を作る:

```bash
cp configs/models.local.yaml.example configs/models.local.yaml
cp configs/rag.local.yaml.example configs/rag.local.yaml
```

embedding を `openai_compatible` backend に切り替える場合は、embedding server を起動:

```bash
./scripts/start_llama_embedding.sh
```

手元の workspace では reranker 用 GGUF ではなく `llama.cpp/models/qwen3-reranker-0.6b/` の safetensors があるため、`reranker_provider: openai_compatible` を有効にする場合は別の OpenAI 互換 reranker endpoint を用意する前提になる。

Wails UI では以下を操作できる。

- Chat-first の 3 ペイン UI
  - 左: Workspace Sidebar
  - 中央: Chat
  - 右: Sources / Document Preview
- Chat 上部の `Chat Context Bar` から Current Chat / Project / Mode / Source Scope / Top K / Export / Library / More を操作
- `With Sources` mode で `/v1/rag/query` を使い、回答に使った source card を右ペインに表示
- `Route Inspector` は Chat Context Bar 下の折りたたみ表示で、selected mode / backend model / source count / latency を必要時だけ確認
- fast / work / code / gateway / embedding / qdrant / watch の起動・停止・runtime log 確認
- gateway health と model 一覧の確認
- mode 指定付き chat 実行
- route planning と backend 振り分け確認
- ingest 実行
- RAG search / query 実行
- embedding endpoint の疎通確認
- index 済み source / chunk preview の確認
- source 単位の exact chunk 詳細確認
- eval dataset 実行と source hit 確認
- route / chat / ingest / rag / embedding / index / eval request の保存・読込・削除
- Recent Activity から過去 request / workflow の reuse / rerun
- chat / route / ingest / rag / embedding / index / eval / workflow 結果の markdown export

日常利用では `Chat` がデフォルト画面で、中央ペインは会話ログと入力欄を主役にし、会話コンテキストの切り替えや export は上部の `Chat Context Bar` から行う。`Library` に source search / ingest、`Settings` に Runtime / Routing / Evaluation / Logs を寄せており、従来の開発向けパネルも詳細操作時だけ開けばよい構成にしてある。Chat / Library の両方でファイルやディレクトリの Drag & Drop ingest を受け付ける。

## Prompt Template

mode 別の system prompt と RAG answer 用 prompt は `prompts/` 配下で管理する。

- `language_ja.md`
- `response_style_ja.md`
- `system_fast.md`
- `system_work.md`
- `system_code.md`
- `rag_answer.md`
- `rag_user.md`

chat completionではmodeに応じてsystem promptを自動補完し，RAG answerではtemplateを使ってcontext付きpromptを組み立てる．`language_ja.md`は通常chat，RAG，Web検索fallbackのすべてに適用され，取得文書が英語でもユーザー向けの回答，説明，要約は日本語に固定する．`response_style_ja.md`は，質問へ直接答える，既定は短い1〜3段落にする，不要な見出しや箇条書きを避ける，という応答契約を全modeへ適用する．コード，識別子，source ID，必要な短い原文引用は正確性のため原表記を維持する．

`fast` modeはllama.cppのthinkingを無効化し，短い質問でreasoningがcompletion budgetを使い切ることを防ぐ．`work`，`rag`，`code`はthinkingを維持し，速度より検討の深さを優先する．

Runtime タブでは:

- `Start Fast` / `Start Work` / `Start Code` で各 llama.cpp backend を起動
- `Start Core Stack` / `Stop Core Stack` で fast / work / code / embedding / gateway をまとめて制御できる
- `Start Recommended Stack` で current config に必要な service だけを見て fast / work / code / gateway に加えて embedding / qdrant を条件付きで起動できる
- `Stop Recommended Stack` で current config に沿って embedding / qdrant を含む recommended stack を対称に停止できる
- `Start Gateway` で workspace の `.venv/bin/python` から gateway を起動
- `Start Embedding` で `scripts/start_llama_embedding.sh` を起動
- `Start Qdrant` で local `qdrant` binary を起動
- `Start Watch` で `scripts/run_cli.sh watch ...` を起動し、指定パスを監視して自動再 ingest を行う
- `Run Smoke` で gateway / qdrant / embedding / reranker の接続確認を実行
- runtime tab の start/stop service、smoke、core stack、recommended stack、runtime config apply/reload 操作も Go/Wails backend 経由で workflow として記録される
- `models.local.yaml` / `rag.local.yaml` を Runtime タブから編集・保存できる
- `example` 読込と `local override` 削除も Runtime タブから行える
- Runtime タブの `save/delete models.local.yaml` と `save/delete rag.local.yaml` も Go/Wails backend 経由で workflow として記録される
- `Preset: Local Only` と `Preset: External Embedding + Qdrant` で典型構成を editor に流し込める
- `Apply Local Only Now` と `Apply External Preset Now` で local override 保存と gateway reload までまとめて実行できる
- `Apply Local Only + Start Stack` は local-only preset 適用と recommended stack 起動を composite workflow として記録し、そのまま rerun できる
- `Apply External + Start Stack` は external embedding + qdrant preset 適用と recommended stack 起動を composite workflow として記録し、そのまま rerun できる
- `Reload Gateway Config` で local override の変更を gateway 再起動なしに再読込できる
- runtime config の apply/reload 操作も Go/Wails backend 経由で workflow として記録される
- watch / ingest / rag / eval 向けの project preset を保存・再利用できる
- project preset から `Start Preset Watch` `Run Preset Ingest` `Run Preset Eval` を直接実行できる
- project preset から `Run Preset Ingest + Eval` で ingest 後に eval まで続けて流せる
- project preset から `Start Stack + Ingest + Eval` で recommended stack 起動込みの one-click workflow を実行できる
- project preset の `Apply Runtime + Start Stack` も composite workflow として記録され、Recent Activity から rerun できる
- Overview タブから preset を選んで runtime に流し込むか、そのまま one-click workflow を起動できる
- Overview タブの `Selected Preset Preview` で watch / ingest / RAG / eval scope と source filter を実行前に確認できる
- Overview タブの `Selected Preset Workflow` で preset ごとの latest workflow run と step detail を確認し、その場で rerun / export できる
- failed step には `Start ...` `Validate Preset` `Run Smoke` などの recovery action が出て、その場で復旧確認を回せる
- recovery action は Go/Wails backend 経由で実行され、runtime profile / service start / smoke / validation を workflow として記録する
- recovery action の実行結果は Recent Activity / Workflow Summary に `preset_recovery` として記録される
- Workflow Summary では primary workflow と `preset_recovery` を分けて成功率と直近 recovery を確認できる
- `preset_recovery` は rerun でき、linked original workflow がある場合はそこから元 workflow を再試行できる
- successful recovery の直後は runtime result panel から `Retry Original Now` を one-click で実行できる
- preset には representative saved chat / ingest / RAG / eval request を保存でき、Overview からそのままフォームへ再投入できる
- `Run Preset Verification` で representative request をまとめて流し、`preset_verification` として確認結果を記録できる
- Preset Catalog からも `Run Verification` を直接実行できる
- Batch Preset Runner の verification / stack + ingest + eval も Go 側で順次実行され、状態は UI に復元される
- Batch Preset Runner の完了後 result は `Clear Batch Result` で UI と永続 state から片付けられる
- Workflow Summary では `preset_verification` の成功率と直近 verification も独立して確認できる
- representative `chat` / `RAG` には期待部分文字列、`eval` には最小 `source_hit_rate` を preset 側で持たせて verification の pass/fail 判定に使える
- verification に失敗した representative request は runtime result panel から `Load Failed Request` でそのままフォームへ戻せる
- Overview / Runtime から preset validation を実行し、watch / ingest path、eval dataset、source filter、required service の事前確認ができる
- validation で `not ready` になった preset は、その場で `Start Required Services` から recommended stack 起動を試せる
- Preset Catalog には保存済み preset ごとの validation state と blocked service が出る
- Preset Catalog から preset 単位で `Validate` と `Retry Last` を直接実行できる
- Preset Catalog から preset ごとの運用サマリを markdown export でき、validation / service check / latest workflow step も含めて残せる
- Runtime タブの workflow / stack 結果は markdown export できる
- chat / RAG request の履歴を保存して再投入できる
- Recent Activity から過去の workflow / request 実行を markdown export できる
- Workflow Summary から直近の成功 / 失敗 workflow を rerun / retry できる
- project preset ごとに workflow 前 smoke を有効化し、qdrant / embedding / reranker の skip 方針も保存できる
- RAG request では `top_k` と answer/search-only の使い分けも UI 上で保持できる
- RAG タブの `Embedding Probe` から gateway 経由の `/v1/embeddings` を直接確認できる
- RAG タブの `Index Browser` から project / source path 単位で index 内容を確認できる
- source card の `Open Chunks` / `Export` から exact source detail と markdown export を実行できる
- source detail を Recent Activity から reopen すると、保存済みの chunk limit も使って同じ粒度で再表示される
- source / project をそのまま RAG / Eval / Ingest フォームへ流し込んで再利用できる
- RAG フォームでは `Source Path Filter` を指定して single-source の search / query を実行できる
- Eval フォームでも `Source Path Filter` を指定して single-source の回帰確認を実行できる
- selected source detail から RAG search/query と eval を one-click で直接実行できる
- preset には RAG / Eval の source filter も保存され、single-source workflow を再利用できる
- local override config の有無を確認できる
- status card と log ペインで fast / work / code / gateway / embedding / watch の PID と各 runtime の出力を確認

## Eval

`configs/eval.sample.yaml` を雛形にして固定質問セットを作る。

```bash
./scripts/run_cli.sh eval configs/eval.sample.yaml --project npo --top-k 5
```

`--with-answer` を付けると、RAG answer まで呼んでキーワード確認も行う。これは backend model が起動している前提。

answer付きevalでは，source hit，keyword，latency，token使用量に加えて，回答文字数，見出し数，箇条書き数と`style_pass_rate`を記録する．caseごとに`max_answer_characters`，`max_bullets`，`max_headings`を上書きできる．

## Document Ingest

Markdown / txt / PDF / docx / HTML / CSV / TSV / JSON に加えて、Git repository 配下の code / config file も ingest 対象に含められる。PDF は実行時に `pypdf` が必要で、docx / HTML / CSV / TSV / JSON / code file は追加依存なしで読む。ディレクトリを ingest する場合は再帰的に走査して index を更新し、元ファイルは `EPHY_data/` 配下へ正規化コピーしてから読み込む。

PDF はページ単位でテキストを抽出し、`Page N` を chunk metadata に保持する。`source_path` はアプリが管理する `EPHY_data/` 内のコピーを指し、drop 元は `original_source_path` として保持される。画像だけの scanned PDF は OCR 対象外のため、抽出可能なテキストがなければ ingest を明示的に失敗させる。

Qwen3 embedding server は長いPDFを連続処理した際の Metal backend の安定性を優先し、embedding 専用プロセスのみ CPU で起動する。ingest は複数 chunk をまとめて embedding API に送り、index と query で同一の embedding backend を必須とする。

repository ingest では `.git` `node_modules` `dist` `build` `.venv` などのノイズになりやすい directory を自動で除外する。

```bash
./scripts/run_cli.sh ingest /absolute/path/to/docs --project lab
```

## 現在の到達点

- `mode=fast/work/code/rag/auto` を受け取って backend を決定する
- chat completion を選択 backend に透過転送する
- `auto` はルールベースで切り替える
- Markdown / txt を index 化して簡易検索できる
- code repository を含むローカル資料を index 化して簡易検索できる
- Wails デスクトップ UI から主要 API を叩ける
- 固定質問セットで source hit を確認できる eval CLI がある

現時点の search は Qwen3 0.6B embedding + local Qdrant を標準経路にし、`local_overlap` reranker で再順位付けする。`configs/rag.yaml` 単体には `local_hash` / `local_json` fallback preset を残し、通常実行では `configs/rag.local.yaml` が実 embedding / Qdrant を選択する。

## Reranker

検索は一次取得のあとに reranker を通す。現状は `local_overlap` を使っており、`rag.rerank_k` 件まで候補を広めに取り、その後 `top_k` 件へ再順位付けする。

OpenAI 互換 reranker endpoint を使う場合は:

```yaml
rag:
  reranker_provider: openai_compatible
  reranker_model_alias: reranker
  reranker_endpoint_path: /rerank
```

`reranker.base_url` は `http://host:port/v1` のように置き、`/rerank` へ投げる。

## Embedding backend 切替

llama.cpp などの OpenAI 互換 `/v1/embeddings` を使う場合は [`configs/rag.yaml`](configs/rag.yaml) を変更する。

```yaml
rag:
  embedding_provider: openai_compatible
  embedding_model_alias: embedding
```

対応する [`configs/models.yaml`](configs/models.yaml) 側で `embedding.base_url` と `embedding.model` を設定しておく。

## Qdrant 切替

Qdrant を使う場合は先に起動:

```bash
./scripts/start_qdrant.sh
```

`start_qdrant.sh` は `QDRANT_BIN`、`./bin/qdrant`、`./tools/qdrant/qdrant`、`PATH`、`/opt/homebrew/bin/qdrant`、`/usr/local/bin/qdrant` の順で binary を探す。見つからない場合はその候補を表示して停止する。

その上で [`configs/rag.yaml`](configs/rag.yaml) の `vector_db.provider` を `qdrant` に変更する。

```yaml
vector_db:
  provider: qdrant
  url: http://localhost:6333
  collection: local_docs
```

Qdrant の storage は `./data/index/qdrant` に永続化する。起動時には local config を `./data/runtime/qdrant/config.yaml` に自動生成し、`127.0.0.1:6333` で bind する。
