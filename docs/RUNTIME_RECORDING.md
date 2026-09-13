# RuntimeからKarteへの自動記録

C2 Step 4の実装と利用手順．保存契約はKarte `90c69c58945e4eb00ec5f13c73b444ff298443d2`のrecord schema／context protocol **2.0**を使用する．C3の要約・日記Jobは含まない．

## 記録を始める

1. 対応するKarteとGatewayを起動し，Runtimeの会話欄にある「会話の記録」を開く．配布用launcherは3者の保存先を揃える．
2. 「Karteのデータ保存先」，「保存先プロジェクト」，「タイムゾーン」を確認し，「設定して記録ON」を1回押す．初期候補は`ephy-conversations`／`Asia/Tokyo`で，分類は`internal`である．既存Developer Modeの設定を記録同意へ変換しない．
3. textまたは音声で会話する．確定したuser発言とassistantの確定本文・生成／再生状態を，都度の採用操作なしで配送する．新しい会話は新しいUUIDで区別する．

保存先は画面に表示される`<Karte data root>/content/projects/<project>/note/YYYY-MM/`である．Karteが正本のMarkdown・ID・revisionを生成する．Runtimeの「最新区間をKarteから読戻す」で，現在のKarte policyによる読取りを確認できる．長い会話の過去区間はKarteの保存先から確認する．本文を表示している間も4秒ごとに再認可し，閉じる・画面非表示・読取り失敗時は消去する．

初回設定した保存先・project・timezoneはこのinstance内で固定する．既存scopeの取消後に記録ONを押してもgrantを自動復活させない．Karte側の現在設定を確認する．学習・外部送信の許可は付けない．

## 状態と復旧

| 表示 | 根拠と操作 |
|---|---|
| ローカル保全 | private queueへfsync済みの未配送event数．配送待ちと失敗を含む |
| 配送待ち | 未確認event．Karte停止中にも保持し，再開後に再送する |
| Karte保存済み | receiptとcandidate／proposal hash／event ID／doc ID／revisionを照合し，現在policyで読戻した正本文・eventのhashまで一致した累計 |
| 保存失敗／権限／競合 | 固定error codeを表示する．本文をtraceへ含めない．原因を解消後「配送を再試行」する |

記録OFFは以後の本文採取を止める．進行中の回答もOFF前の確定範囲で固定し，終端の生成・再生metadataだけを閉じる．OFF前の配送待ちは現在grantが有効なら配送を続ける．再びONにしてもOFF区間や過去UI履歴を追加しない．過去記録の削除はKarte側の別操作である．

user finalをprivate queueへ保全してからLLMを起動する．保全に失敗した発言ではLLMを開始せずエラーを表示する．再起動時は未完了assistantを保全済みの確定本文と生成・再生の観測から閉じ，未観測の完了を推定しない．同じcandidateと署名済みbytesを再送し，Karte停止・Runtime強制終了・保存後receipt前の停止を跨いで同じ保存結果へ収束させる．

queueは既定で`~/Library/Application Support/Ephy/recording`に置く．directoryは0700，fileは0600，単一writer lock，temp→file fsync→rename→directory fsyncを使う．Git内・symlinkの保存先を拒否する．正本確認後は対応本文を直ちに除去し，IDだけの重複防止情報を24時間／10,000件以内に保持する．queueを検索やLLM入力の代用へ使わない．

容量は256 MiB／未配送10,000 eventsで，超過時は既存eventを捨てず記録をpauseする．単一本文はv2契約の64 KiBまで．assistantが超過した場合はそれまでの確定範囲とuser発言を保持し，失敗を表示する．容量逼迫をUIに表示する．disk writeが不確実になった場合は`recording_restart_required`として再起動による再走査を要求する．

配送は同一会話のseq順に行う．256 events，1 MiBの保守的なencoded-size上限，または設定timezoneの日付変更で次segmentへ進む．UI履歴30件やLLM contextの切捨てとは独立する．一時失敗は5秒から最大300秒の指数backoffとjitter，8連続失敗後は15分休止する．手動再試行と再起動で再開でき，期限で本文を捨てない．権限・競合・schemaの失敗は自動反復せず，権限取消は以後の採取もpauseする．

## アクセスと保存対象

- 保存する本文はuserのtext／ASR finalとassistantのconfirmed本文だけである．raw audio，ASR partial，内部reasoning，token deltaを記録しない．音声生成・表示・SpeechUnitの自然終了／中断／不明を別々に保持する．続きの生成は元turnに結び，新しく確定した部分だけを記録する．
- 初回ON前にGatewayの`/v1/karte/records/prepare`で旧経路を検査する．Karteの所有ledgerとv2 markerから，旧direct reader，generic RAGの既存copy，JSON cache，Qdrantの実pointを除外・失効する．到達不能のstoreや壊れた所有情報を成功扱いにしない．
- Runtimeのfile／process toolsからKarte data rootとqueue・control credentialへの直接アクセスを遮断する．モデル向けtoolに記録同意の変更機能を追加しない．v2本文の読取りは毎回Karteを経由し，取消後に古いcopyやcached snippetへ戻らない．
- v1の既存手動候補は互換維持する．C2設定後または明示OFF後はv1の旧自動候補作成を止め，同意の外で別経路へ本文が流れることを防ぐ．

## 検証とbuild

Goのrecording packageで100 turns／200 events，OFFの各境界，実subprocessのSIGKILL，容量・disk failure，segment切替，同一候補再送を検査する．実Karte CLIを指定する`EPHY_TEST_KARTE_CONTROL` gateでは32 turns／64 eventsを配送し，Runtime停止中の採用，再起動，receipt／hash読戻し，重複なし，確認済み本文除去，grant取消を検査する．receipt／read-backのID・hash・event等を不一致にした7ケースは保存済み0のまま保持する．Karte側のfault gateはprepared／canonical／ledger／saved／receipt／archiveの各停止を注入する．具体的な結果は[STATUS](runtime-diary/STATUS.md)を参照する．

`scripts/build_conversation_app.sh`はcleanな上記Karte sourceから`karte-ephy-control`をbuildしてbundleへ入れ，protocolとhashを検査する．sourceの場所は`EPHY_KARTE_SOURCE_ROOT`で指定する．Go 1.25以上が必要である．Whisper helper・modelは現在のlarge-v3-turbo経路を維持し，build provenanceにASRとKarte control両方のhashを残す．

人による最終確認は，ON後にtextと音声を同じ会話へ送り，中断を1回行い，OFF後の発言が増えないことと両app再起動後の読戻しをまとめて行う．自動fixtureの成功を実マイクの品質受入へ置き換えない．
