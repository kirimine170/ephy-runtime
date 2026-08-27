# Ephy Preference A/B評価

## 目的

Preference A/B評価は，同一の会話履歴に対して生成した2応答から，よりEphyらしく自然な応答を人が選ぶための評価基盤である．結果はSQLiteへ追記し，将来のDPO／ORPO及び明示承認されたSFT用JSONLへ変換できる．LoRA学習自体は現在の実装範囲に含まれない．

`same_prompt`方式では，Model Managerで対象roleに現在選択されている同一model，同一LoRA，同一system promptを使い，`temperature > 0`かつ異なるseedで2回生成する．`prompt_v1_v2`及び`prompt_v2_v3`方式では，同一model，同一LoRA，同一seedを保ち，`warm_polite` promptだけを指定した2版で切り替える．これにより，sampling差とprompt改善を分けて評価できる．`base_vs_adapter`方式では，Model Managerで選択中のLoRAをllama-serverのrequest単位scale 0／指定値で切り替え，同一model，同一v3 prompt，同一seedのbaseとLoRAを比較する．scaleはcandidate metadataへ記録し，session途中で変更できない．

## データ境界

永続化を伴う操作には，Git管理外にある絶対パスを明示する．

```bash
export EPHY_PREFERENCE_DATA_ROOT=/absolute/path/to/ephy-preference-data
```

保存先は次のとおりである．APIからDBパスを変更することはできない．

```text
${EPHY_PREFERENCE_DATA_ROOT}/preferences.sqlite3
```

生成結果，投票及びexportは`EPHY_PREFERENCE_DATA_ROOT`配下に限定される．環境変数が未指定，相対パス，又はGit repositoryと包含関係にある場合，永続化を拒否する．生の会話，DB，JSONL及びmodel weightをGitへ追加してはならない．repository内のsample datasetは，実在人物，実プロジェクト，Karte及びPersonal Contextに由来しない合成例だけで構成する．

## Consent，provenance及びsplit

各scenarioは，`source_kind`，`source_ref`，`provenance`，`transform_history`，`consent.storage`，`consent.training`及び`deletion_status`を保持する．保存への同意と学習への同意は別の判断である．学習exportへ含めるには，`consent.training=true`かつ`deletion_status=active`でなければならない．

splitはscenario単位で`train`，`validation`又は`holdout`へ固定する．学習用DPO／SFT exportに含まれるのは`train`だけであり，validation及びholdoutは評価用に保持する．同一会話のturnを異なるsplitへ分割しない．

## 生成とblind review

1. `configs/eval.preference.sample.yaml`，`configs/eval.preference.v3.yaml`又はdata root配下の許可されたYAMLからsessionを作成する．
2. Model Managerの現在のrole選択からmodel及びLoRAを解決する．Qwenのversionやmodel名は固定しない．
3. Prompt比較では，同じ会話とseedから指定した2版の候補を順次生成する．`same_prompt`では，同じ会話とpromptから異なるseedで2候補を生成する．同時推論数は1である．
4. 正規化後に同一なら，Prompt比較では両versionを同じ新seedで，`same_prompt`では候補Bを再生成する．最大試行後も同一のpairは`duplicate_generation`として保存し，review queueから除外する．
5. 表示時だけ左右をランダム化し，UIへはpair ID，会話履歴，左右の応答，category及び進捗だけを返す．
6. `left`又は`right`はserver側でcanonicalな`a`又は`b`へ変換する．

生成metadataにはmodel role，model registration ID，base model SHA-256，adapter registration ID及びSHA-256，prompt variant，prompt revision，temperature，top_p，seed，生成日時及び応答SHA-256を保存する．review画面にはmodel名，LoRA名，prompt version，seed，生成順，生成時間，token数及び文字数を表示しない．prompt別の勝敗はsession完了後だけ表示する．

## Wails UI

SettingsからEvaluationを開き，`Preference A/B`を利用する．dataset，comparison，model role及びpair数を指定してsessionを開始するか，既存sessionを選択して再開する．v3評価では`configs/eval.preference.v3.yaml`と`Prompt v2 vs v3`を選ぶ．LoRA評価では，先にModel Managerで対象LoRAをroleへ適用し，同じdatasetの`Base vs selected LoRA`を選ぶ．学習への再流入を防ぐため，このmodeはvalidation及びholdout scenarioだけを使い，指定pair数が利用可能件数を超える場合は拒否する．未評価pairは再起動後も先頭から復元される．理由タグとnoteは任意であり，選好だけを素早く保存できる．

キーボード操作は次のとおりである．

- `1`：左の候補．
- `2`：右の候補．
- `0`：同程度．
- `S`：判断不能．
- `Enter`：現在の選択を保存する．未選択時は保存しない．
- `Z`：直前の投票を再表示し，新しいvoteとして訂正する．

保存中は追加送信を無効化する．訂正時も過去のvoteは削除せず，`supersedes_vote_id`を持つ新しいvoteを追記する．

## CLI

生成には，対象roleのruntimeが起動し，Model Managerでmodelが選択されている必要がある．

```bash
./scripts/run_cli.sh preference generate \
  --dataset configs/eval.preference.v3.yaml \
  --role fast \
  --comparison prompt_v2_v3 \
  --count 30

./scripts/run_cli.sh preference generate \
  --dataset configs/eval.preference.v3.yaml \
  --role fast \
  --comparison base_vs_adapter \
  --count 11 \
  --adapter-scale 1

./scripts/run_cli.sh preference stats --session SESSION_ID

./scripts/run_cli.sh preference export \
  --session SESSION_ID \
  --format dpo \
  --output exports/ephy-preference-v3.dpo.jsonl
```

## DPO及びSFT export

DPOは，最新の有効voteが`a`又は`b`であり，training consent，active，train，非重複及びchosenとrejectedが異なる条件をすべて満たすrecordだけをchat形式で出力する．tie及びskipは含めない．metadataにはchosen及びrejectedのprompt variantとprompt revisionを含める．

SFTは同じ条件に加え，review時に`approved_for_sft=true`を明示したrecordだけを出力する．選ばれた応答を自動的に理想応答とはみなさない．export先はdata root配下に限定し，既存ファイルを上書きしない．

## 実装済み範囲と将来構想

現在は，同一の選択済みmodel／LoRAによるsame-prompt sampling，prompt v1／v2，v2／v3及びbase／選択中LoRA比較，blind review，append-only vote，session再開，完了後の比較別統計，DPO／SFT export，CLI，Gateway及びWails UIを実装している．base／LoRA比較はrequest単位のLoRA scaleを使うため，global scaleを変更せず，生成後の復元操作も不要である．生成前にllama-serverが読み込んだadapter pathとModel Manager選択を照合し，session途中のmodel又はadapter変更を拒否する．

LoRA version間又は異なるbase model間の自動比較，LLM reviewer，DPO／ORPO training，LoRA artifact作成及びModel Growth適用は将来構想である．これらを追加する際も，private data boundary，artifact hash，固定split及び評価後のrollback可能性を維持する．

## Opt-in実モデルテスト

通常CIはmock生成だけを使う．現在のFastモデルを使う接続確認は，専用の一時data rootを指定して明示的に実行する．自然さ自体は自動判定せず，生成，blind pair，vote及びDPO exportの成立だけを確認する．

```bash
EPHY_MODEL_INTEGRATION=1 \
EPHY_PREFERENCE_DATA_ROOT=/absolute/path/to/temp-ephy-preference-data \
python3 -m pytest tests/test_preference_live_model.py -v
```

独立worktreeから既存runtimeのModel Manager登録を利用する場合だけ，`EPHY_PREFERENCE_MODEL_REGISTRY_ROOT=/absolute/path/to/ephy-runtime`も指定できる．通常のrepository内実行では指定不要である．
