# 常駐会話と指摘の反映

改訂3の専用実装．主branch：`codex/feat-resident-feedback`．通常の会話画面に常駐操作を追加し，既存Whisper／SpeechService／モデル選択を使用する．設定反映はモデルの重みの学習ではない．

## このMacで試す

専用worktreeで実行する．初期準備後は`start`だけでよい．build成果物が一時領域から削除された場合は`build`を再実行する．

```bash
cd /Users/kirimine170/Desktop/Ephy_Project/ephy-workspace/ephy-runtime-resident
.venv/bin/python scripts/resident_app.py start
```

1. `Ephy Resident`の会話画面を開く．通常版と同時にマイク待受を開始しない．
2. 音声profileを確認し，必要なら既存の声の選択・試聴を使う．初回の選択候補は通常版の起動設定から読み取った`voice-irodori-anime-exp`．以後の声の選択は検証版内で保存する．生成中の発言は開始時の声を保つ．
3. 単独会話なら「この会話の発言者は所有者です」を選ぶ．指摘を保存する場合は「指摘と設定変更の保存を許可」も選ぶ．保存許可と学習許可は異なり，本機能から学習は実行しない．
4. 「待受」を選び，「待受を開始／再開」を押す．マイク許可が未設定ならmacOSの許可操作を行う．一回ごとにsessionを作り直さず会話を続ける．最小化中の実マイク継続は下記の未確認事項を参照する．
5. 「いつも説明が長い」と伝えると，継続設定の返答長を短くする．「もっと短くして」はこのsessionの指定になる．反映状態と有効設定を画面で確認し，次の質問を行う．明示的な「詳しく説明して」は短さの既定設定より優先する．
6. 「今の言い方はよかった」，「今の声が嫌だった」，または画面の「よかった」「嫌だった」で対象の発言を評価する．声への評価だけでspeakerを自動変更しない．聞こえた区間が不明な場合は不明として残す．
7. 「止めて」は現在の発話を停止する．「今は話しかけないで」は停止に加え，この会話の自発発話を控える．直接質問には応答できる．マイク休止は別の操作で，画面から再開する．
8. 「さっきの変更を戻して」または履歴の「この変更を戻す」で設定を戻す．別のfieldへの後続変更は維持する．対象fieldがさらに更新された場合は，新しい変更を確認してから戻す．
9. アプリを閉じるか「アプリを終了」で終了する．専用launcherの監視processが，その起動で所有したGateway・音声service・fixture受信processを回収する．通常版のserverは停止しない．

音声指摘は既存ASRの確定入力を使う．引用文やtool結果を所有者の指示として扱わない．観測・参加modeでは，直接の質問や指摘を「Ephy，…」で始める．話者が不明な会話は所有者として扱わず，継続設定を変更しない．

## 初期準備・再build・停止

```bash
cd /Users/kirimine170/Desktop/Ephy_Project/ephy-workspace/ephy-runtime-resident
../ephy-runtime/.venv/bin/python scripts/resident_app.py prepare
.venv/bin/python scripts/resident_app.py build
.venv/bin/python scripts/resident_app.py start
.venv/bin/python scripts/resident_app.py status
.venv/bin/python scripts/resident_app.py stop
```

`prepare`は通常版の選択済みモデル設定と声の設定，検証したASR helperを専用側へ用意する．記憶・会話DBはコピーしない．既存の検証版設定を上書きせず，Identity／基礎Profile・モデル・許諾済み音声referenceは読み取り参照する．通常版へ戻す場合は検証版を終了後，従来の`Ephyを起動.command`を使う．

保存先は`/Users/kirimine170/Desktop/Ephy_Project/local-data/resident-feedback`．`preferences/preferences.sqlite3`に単独feedbackと型付き設定変更を保存し，`recording/`と`karte/`は通常版から分離する．Gatewayは`127.0.0.1:18900`，音声serviceは`127.0.0.1:18967`．ローカルモデルserverの8081／8082／8083は既存のものを共有し，検証版から切替・停止しない．通常版のポートが稼働していることだけを理由に検証版serviceを代用しない．

継続設定は所有者と個体ごとに復元する．session限定設定はアプリを開き直した新sessionへ持ち越さない．期限付き設定は元の期限を保持する．継続設定や指摘を通常版へ自動反映する処理はない．

## 最小の能動参加を合成資料で試す

```bash
.venv/bin/python scripts/resident_app.py stop
.venv/bin/python scripts/resident_app.py fixture
.venv/bin/python scripts/resident_app.py start
```

`fixture`は架空の共有経験だけを専用Karte領域へ置き，固定revisionの実Karte contextcoreを使う小さな受信helperをbuildする．通常のKarte GUIや通常版の記憶は使わない．fixture markerのない既存記憶領域へは混ぜない．

この合成fixtureでは参加者IDと発言者IDをどちらも`owner`にし，参加者の許可を選ぶ．「観測」で待受を開始し，「公園のピクニック」等の合成例を話す．候補が得られても観測modeでは再生しない．「会話へ参加」では，根拠・参加者・現在の会話revision・期限・発話中か・抑制を確認した候補だけを既存音声経路へ渡す．新しい発言，休止，参加者や設定の変更は古い候補を無効にする．「今は話しかけないで」の後は候補を抑制し，Ephyへの直接質問には応答する．

`participation-grants.json`は検索範囲と共有可能な文書ID／hashを限定する．モデル生成引数から拡張できない．このfixtureでの成功は話者分離や実会話の共有許諾を自動判定できたという意味ではない．実記憶へ移行する際は，参加者と文書の共有範囲を明示的に設定する．

## 指摘の記録・削除・評価との境界

指摘の受付，保存，設定反映，失敗は別に表示する．対象が曖昧な評価は確認待ちとなり，原因や理想回答を捏造しない．フィードバック本文をsystem promptへ継ぎ足さず，有限の設定差分を次の生成に渡す．停止操作は保存の成功を待たない．

履歴には対象発言の参照，聞こえた範囲，指摘，設定差分とundoの来歴を残す．単独評価は既存A/B tableのpairやvoteへ変換しない．訂正・削除されたものと学習同意のないものは学習export対象にしない．既存A/B評価の承認手順を別に行わない限り，DPO／SFTの例を作らない．

事実訂正は対象参照とともにKarte確認待ちとして記録する．この経路はKarte本文を自動上書きせず，訂正の確定は既存Karteで行う．音声clipは本機能では保存しない．本文保持期間の既定値は90日，履歴上限は10,000件で，上限時は保存失敗を表示する．削除・取消のAPIは専用Gatewayの`/v1/resident/feedback/{event_id}/retract`とし，`delete: true`で本文を消去する．元の記録を識別するための最小限の来歴は残る．

## 検証結果と未確認事項

実行結果は`docs/experiments/reuse-core/resident-results.md`に記録する．再現用の設定・計測は専用stateの`logs/`，部品比較のraw結果は`logs/resident-feedback/reuse-probes/`に置く．合成fixture，実モデル／実音声生成，実マイク／speakerによる体験確認を区別する．

実マイクでの会話・背景capture・物理的なspeaker停止，sleep/wake・実device切断後の復帰，長時間連続負荷，声の好みと距離感の主観評価は，実行できた範囲を超えて確認済みとはしない．実際に試す時は，通常版のマイクを休止し，上の会話・指摘・最小化・復元手順を順に確認する．
