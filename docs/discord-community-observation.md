# Discordコミュニティ観測MVP

Runtimeは設定したDiscordの範囲を`discord.observe` Jobで取得し，ローカルモデルで議論・明示的な決定・taskを整理する．自動投稿，管理操作，担当者の割当，期限の設定，Karteへの保存は行わない．

`configs/discord-observe.example.yaml`を参考に，非追跡の`configs/discord-observe.local.yaml`へ`discord_observe`設定を置く．既定は無効である．guildとsource channelを固定し，Worker側にも同じ観測scopeを設定する．Manager credentialは環境変数からだけ読む．`target_worker_id: auto`ではManagerが対応scopeを持つWorkerを選ぶ．Runtimeは最初に検証した実Workerを固定し，途中での変更を拒否する．明示的なWorker指定の既存検索検証は維持する．

`POST /v1/discord/observe`は`request_id`，`guild_id`，`channel_id`，`operation`（`report`，`snapshot`，`activity`，`discussion`，`unresolved`），`period_days`（7または30），`query`，任意の`previous_answer`を受け取る．Discord transportは設定されたguildの自然なコミュニティ質問を，公開Web検索計画より先にこの経路へ送る．受領・途中経過・段落分割・返信の既存deliveryを使う．

観測上限時刻は元のDiscord request snowflakeから固定する．Managerへ送るのはguild，source channel，30日間の期間と件数上限だけで，質問と過去回答は送らない．submit keyは`runtime-discord-observe-<request_id>`である．202は収集と本文分析のどちらを待つ間も返し，同じrequest，Job，input hashで再照会する．初回の投入待ちだけJobとWorkerがnullになり，検証後のIDを変更しない．収集期限は収集にだけ適用し，受理した本文の分析へ延長して適用しない．200は通常の`choices`と`discord_observe_status`を返す．`completed`と`partial`，`analysis_status: verified`と`unverified`を区別する．無効な結果や期限切れを成功へ置き換えない．

結果の型はWorkerのadditive contract 0.4 `ObservePayload`と対応する．schema参照SHA-256は`c3fc81f07a02fbc14573828a7b00e50385a33dceedab7355de0e85ea6377f02d`である．入力，Job，attempt，実Worker，usage，結果packのcanonical bytesとSHA-256，期間とscopeを検証する．`snapshot.channels`はguildのチャンネルmetadata全体で，本文の取得範囲は設定したsource channelだけである．

推論はliteral loopback HTTP `/v1`の`llama_cpp`だけを許可し，環境proxyとredirectを使わない専用接続でSSEを読む．既存のmodelやprofile，ロード状態は変更しない．全体の生成timeoutは追加せず，600秒の読み取りidle timeoutを使う．生成中も202への再照会ができ，InferenceGateの保持によりmodel切替と競合させない．質問・投稿・前の回答は非信頼データであり，toolや新しい通信の権限にならない．

本文は新しい順に，設定した件数・文字数内の全文を選ぶ．欠損または途中省略された本文は分析へ使わず，省略件数を回答に示す．要約の根拠は既知の投稿IDと完全一致する引用へ結び付ける．これは参照整合性の検証であり，要約の意味の正確性を証明するものではない．明示的な決定・taskにだけ確認表現を要求し，担当者・期限は根拠の引用に完全一致する場合に限る．未回答・未完了は観測範囲の推測として示す．

回答は日本語，一人称「わたし」で，測定事実，議論の要点，確認した決定・task，推測，意見，提案を区別する．本文分析を検証できない場合も，実測値と慎重な見方・次の整理案を返す．履歴の欠損，本文の未取得，省略，期間，取得時点，スレッド対象外を示す．投稿件数は観測できた範囲の値で，コミュニティ全体の完全な活動量ではない．

Runtimeはraw投稿をdiskへ保存せず，件数を制限した一時的なsession/cacheだけを持つ．Managerの共有保存と30日retentionが収集データの正本であり，Workerのローカル履歴には依存しない．サーバーmetadataと投稿集計の高度な分析，個人の専門性推定，Karte保存，管理操作は将来の別scopeである．
