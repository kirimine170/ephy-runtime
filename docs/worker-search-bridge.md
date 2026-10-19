# Discord → Runtime → Worker の検索接続

Runtime は検索計画，外部送信の承認，検索結果の検証，会話生成を担当する．
`provider: worker` を選ぶと，独立した ephy-worker Manager の契約 0.4
`web.collect`（`mode: search`）へ，承認済みの検索語だけを送る．
既定の `provider: searxng` は従来の動作を維持する．
コーディング Job，任意 Tool，PR 作成をチャットから実行する機能は含まない．

## 設定と起動

1. ephy-worker 側で Manager と Worker を別プロセスで起動する．
   Manager の requester credential と Runtime の credential を一致させ，
   `target_worker_id` を登録済み Worker の ID と一致させる．
2. `configs/web-worker.example.yaml` を `configs/web.local.yaml` へコピーする．
   接続先，Worker ID を設定し，`manager_credential_env` で指定した環境変数へ
   credential を設定する．credential の値は YAML，URL，コマンド履歴へ保存しない．
   Runtime の通常起動で local override が読み込まれる．
3. Runtime Gateway を通常どおり起動する．Manager は HTTPS または明示的な
   loopback HTTP のみ許可する．credential は `X-Ephy-Credential` ヘッダーで送り，
   redirect，環境 proxy は利用しない．Gateway 自体はこの MVP では loopback に置く．
4. ephy-worker の opt-in `discord-runtime` transport で Runtime の loopback URL を指定する．
   Discord token と Manager credential は別の秘密情報として扱う．

モデル設定は Runtime の `configs/models.yaml` と既存の local override を使う．
計画と隔離した事実抽出には `fast` モデルが必要で，会話応答には既存のモデル routing を使う．
Worker の検索 provider は Worker 側で設定する．この adapter は有料 provider を自動選択しない．
1 Job は検索語 1 件，検索リクエスト予算 2 件で，Tavily の quota 確認と検索にも対応する．

## HTTP の流れ

`POST /v1/web-search/plan` に `{ "query": "公開情報についての質問" }` を送る．
応答は `plan_id`，`decision`（`allow`／`confirm`／`block`），`outbound_query`，
`risk_categories`，`expires_at` を含む．`confirm` を自動承認してはいけない．
既存 UI 向けの `/v1/web/search/plan` と `/v1/web/search/approve` も維持する．
承認 UI からのみ `POST /v1/web-search/approve` に `{ "plan_id": "..." }` を送れる．
Discord transport は承認 API を呼び出さない．

`allow` または明示的承認の後，同じ最新 user message を含む
`POST /v1/chat/completions` に metadata を付ける．

```json
{
  "model": "auto",
  "messages": [{"role": "user", "content": "公開情報についての質問"}],
  "metadata": {"web_search": true, "web_search_plan_id": "取得した plan_id"}
}
```

通常の chat client は，検索が待機中でも同じ Gateway request 内で完了を待ち，
従来どおりの JSON completion または SSE を受け取る．既存の Wails UI と
Discord transport はこの経路を使い，ユーザーの追加返信は必要ない．

非同期の再照会に対応した client が明示的に `Prefer: respond-async` ヘッダーを送ると，
短い待機時間 `worker_wait_seconds` を超えた場合に HTTP 202 と
`{ "status": "pending", "job_id": "...", "plan_id": "...", ... }` を返す．
呼び出し側は同じ chat payload，plan，ヘッダーを再送して自動的に完了を受け取る．
ユーザーの追加返信を待つ必要はない．この待機時間は Job の中止条件ではない．
Manager の `worker_deadline_seconds` が検索実行の予算を制限する．
Manager が `partial`／`failed`／`cancelled`／`lost` を返した場合は HTTP 502 の
`detail.code` と Job identity を返し，調査成功として会話を生成しない．

`stream: true` の場合も，HTTP 202 は `Prefer: respond-async` の指定時だけ返す．検索が終わると
既存の SSE を使い，`web_search_status` と `sources` の後に回答 delta が続く．
会話生成に全体の経過時間上限は設けず，モデルとの streaming 通信が 600 秒間
途絶えた場合に失敗とする．検索計画と事実抽出の非 streaming 通信は従来の
120 秒の read timeout を維持する．

同じ plan は不変の検索語と `submit_key` に結び付ける．同時呼び出しも直列化し，
POST 応答を受け取れなかった場合も同じ key で再試行するため，Manager の冪等な投入を使える．
plan はメモリ内の有限キャッシュで，未着手は `plan_ttl_seconds` で失効する．
開始済み plan は検索 deadline と再照会猶予分だけ保持する．Gateway 再起動／設定 reload 後の
自動再開は実装していない．Manager の Job は残るため，必要なら Manager から状態を照会する．
`POST /v1/web-search/cancel` に `{ "plan_id": "..." }` を送ると，紐付く Job のキャンセルを要求できる．

## 結果と信頼境界

完了応答の `web_search_status` には provider，contract，plan ID，Job ID，固定 Worker ID，
input hash，Manager state と source count を含める．Manager の投入・状態・結果 API の
identity，入力 hash，query，attempt，成功状態の整合性を検証する．これらは認証済み Manager
との契約検証であり，Worker の内容が正しいことを暗号署名で証明するものではない．

応答サイズを制限し，検索候補には既存の公開 URL 検証と本文サニタイズを適用する．
外部文章は会話履歴を持たない隔離した LLM で短い事実へ抽出し，検証済み source ID に結び付ける．
元の検索 snippet を会話モデルの命令として渡さない．安全な事実を抽出できなければ明示的に失敗する．
credential，外部エラー本文，元の会話は Manager エラーや結果 provenance に含めない．
