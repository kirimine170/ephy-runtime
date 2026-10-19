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

自分の回答への追加質問では，新しい質問を `query`，前の回答を任意の `context` に分けて送る．
`context` は文字列または null で，最大 2400 文字とする．Discord 側はさらに 1200 UTF-16 code unit
までに制限し，同じユーザーと channel で公開した直近の回答だけを使う．`query` だけの呼び出しも維持する．
Runtime は新しい質問へ従来の秘密情報検査を適用し，前の回答は機微な値を伏せてから，
命令や Tool 権限を持たない引用データとしてローカルの検索語書き換えにだけ渡す．
前の回答のコード例，公開連絡先，パス表記だけで新しい質問を確認待ちにはしないが，
書き換え後の外部送信語には同じ秘密情報検査と承認を必ず適用する．
生の `context` は Manager へ送らず，計画の hash は新しい `query` だけに結び付く．
chat の最新 user message も同じ新しい質問にする．

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
従来どおりの JSON completion または SSE を受け取る．既存の Wails UI は
この経路を使い，ユーザーの追加返信は必要ない．

非同期の再照会に対応した client が明示的に `Prefer: respond-async` ヘッダーを送ると，
短い待機時間 `worker_wait_seconds` を超えた場合に HTTP 202 と
`{ "status": "pending", "job_id": "...", "plan_id": "...", ... }` を返す．
新しい Discord transport はこのヘッダーを送り，同じ chat payload，plan，ヘッダーを
再送して自動的に完了を受け取る．
ユーザーの追加返信を待つ必要はない．この待機時間は Job の中止条件ではない．
Manager の `worker_deadline_seconds` が検索実行の予算を制限する．
Manager が `partial`／`failed`／`cancelled`／`lost` を返した場合は HTTP 502 の
`detail.code` と Job identity を返し，調査成功として会話を生成しない．

`stream: true` の場合も，HTTP 202 は `Prefer: respond-async` の指定時だけ返す．検索が終わると
既存の SSE を使い，`web_search_status` と `sources` の後に回答 delta が続く．
会話生成に全体の経過時間上限は設けず，モデルとの streaming 通信が 600 秒間
途絶えた場合に失敗とする．600 秒は `web_search: true` と正しい形式の 32 桁 plan ID を持つ
検索回答にだけ適用し，通常の streaming は従来の 120 秒の read timeout を維持する．
形式の検証は待機方針の選択だけであり，plan の承認は Gateway が別途検証する．
検索計画と事実抽出の非 streaming 通信は従来の
120 秒の read timeout を維持する．

同じ plan は不変の検索語と `submit_key` に結び付ける．同時呼び出しも直列化し，
POST 応答を受け取れなかった場合も同じ key で再試行するため，Manager の冪等な投入を使える．
plan はメモリ内の有限キャッシュで，未着手は `plan_ttl_seconds` で失効する．
開始済み plan は検索 deadline と再照会猶予分だけ保持する．Gateway 再起動／設定 reload 後の
自動再開は実装していない．Manager の Job は残るため，必要なら Manager から状態を照会する．
`POST /v1/web-search/cancel` に `{ "plan_id": "..." }` を送ると，紐付く Job のキャンセルを要求できる．

Wails の chat は，`web_search: true` と 32 桁の hexadecimal plan ID を指定した場合だけ，
HTTP 全体と InteractionEngine の LLM stage の経過時間制限を使わず完了を待つ．
これは通信の待機方針の選択であり，承認の証明ではない．plan の承認は常に Runtime が検証する．
通常の chat，検索計画，音声処理などの有限 timeout は維持する．Manager の検索 deadline と
モデルの streaming idle timeout も維持する．

開始済みの上記 chat をユーザーが明示的にキャンセルした場合，Wails は同じ plan の cancel API を
独立した 3 秒上限の context で 1 回だけ試みる．取消通信の失敗によって元のキャンセル結果を変えない．
通信失敗，EOF，deadline 超過，成功時，未指定または不正な plan ID では自動取消をしないため，
一時的な接続障害の後も同じ plan を再利用できる．Runtime は Worker session が未投入の plan を
cancel API だけで承認または投入しない．取消は best effort で，Manager 上の実際の状態は別途確認できる．
取消は進行中の検索 poll の lock を待たずに送る．初回の投入応答が未着の場合も取消意図を保持し，
同じ承認済み Job の idempotency key で ID を回復してから取り消す．新しい key の Job は作らない．
取消を要求した計画からは，その後の事実抽出や成功回答を生成しない．
承認済み Worker 計画の chat は，最初のローカル grounding から最終回答まで同じ取消状態に結び付く．
Worker へ未投入でも，cancel API は HTTP 200 の `state: cancelled` と `job_id: null` を返し，
Manager へ通信せずにその後の投入を止める．未承認の確認待ち計画，blocked 計画，SearXNG 計画は取り消せない．
最終生成中の取消はモデルへの待機を閉じ，JSON では成功回答を返さない．SSE では既に送った文章は戻せないが，
以降の回答 delta と正常終了を送らず，`worker_search_cancelled` の error event で終了する．
実行中の chat は元の plan TTL を超えても取消可能な状態を保持し，成功，失敗，切断時にその保持を解放する．
保持中も計画件数の上限を維持する．通信切断だけでは Manager Job を自動取消せず，同じ plan の再照会を維持する．
実際の HTTP 切断は，grounding や JSON 生成の待機中も ASGI の切断通知で検出し，
そのリクエストのローカル処理とモデル接続を閉じて保持を解放する．取消意図は plan に残さない．
SSE への移行前に受信監視を終了し，移行後は streaming 応答だけが切断通知を受け取る．
再接続は同じ plan と Job を再利用できる．切断済みの未応答リクエストは内部的に
`499 client_disconnected` で終え，調査完了の回答として扱わない．
Worker 計画の通常 RAG 検索も別 thread で待つため，索引や embedding の応答待ちが
取消・切断の監視を止めない．既に実行中の同期的な読み取りそのものは強制停止できないが，
その結果の待機と保持を解放し，後から処理が戻っても Job 投入や回答生成へ進めない．
通常の chat と SearXNG の実行経路は維持する．

Worker の収集期限は session 作成時にローカルの monotonic clock から 1 回だけ設定する．
投入，状態照会，結果取得はすべて残り時間内に閉じ，pending の再照会や再接続，
Manager の時刻変更では延長しない．期限超過は `worker_search_deadline_exceeded` と
保持済みの plan／Job identity を返し，同じ plan を再送しても新しい Job や成功結果にしない．
期限後も明示的な取消 API は使用できる．通信切断によるローカル取消とは区別する．
期限内に identity，予算，結果 pack と候補を検証・サニタイズした時点で収集は完了する．
受理した候補は複製して保持・再利用し，その後の事実抽出や最終回答の生成に収集期限を適用しない．
不正な結果，期限超過，取消で中断した収集結果を受理済みとして保存しない．
既に完了した結果も明示的に取り消した plan からは回答しない．

## 結果と信頼境界

完了応答の `web_search_status` には provider，contract，plan ID，Job ID，固定 Worker ID，
input hash，Manager state と source count を含める．Manager の投入・状態・結果 API の
identity，入力 hash，query，attempt，成功状態の整合性を検証する．これらは認証済み Manager
との契約検証であり，Worker の内容が正しいことを暗号署名で証明するものではない．
成功結果の報告値にも承認済み予算を適用し，検索回数と検索 credit は許可した検索リクエスト数以下，
候補件数は `max_sources` 以下であることを検証する．検索専用 Job の fetch，文書 bytes，model 使用量は
すべて 0 とする．artifact manifest は空，または Worker が必ず付ける `result-pack.json`
（`application/json`，schema 1）の参照 1 件だけを許可する．この参照は，検証済みの元の結果から
manifest を空にした canonical JSON の UTF-8 bytes と SHA256 が一致しなければ失敗とする．
追加の成果物や不一致を含む結果は，事実抽出や成功回答へ進めない．この参照から artifact を取得せず，
検証対象は宣言された metadata の整合性であり，リモートに保存された blob 自体を確認するものではない．
これは報告結果の検証であり，不正な実行先が報告せずに行った処理の防止や使用済み費用の回収を保証しない．

応答サイズを制限し，検索候補には既存の公開 URL 検証と本文サニタイズを適用する．
外部文章は会話履歴を持たない隔離した LLM で短い事実へ抽出し，検証済み source ID に結び付ける．
元の検索 snippet を会話モデルの命令として渡さない．安全な事実を抽出できなければ明示的に失敗する．
credential，外部エラー本文，元の会話は Manager エラーや結果 provenance に含めない．
