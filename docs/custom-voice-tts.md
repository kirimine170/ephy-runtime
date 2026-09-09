# Custom voice TTS：準備と起動

C0.3の設計判断は[ADR-0012](adr/ADR-0012-custom-voice-tts.md)に記録する．この手順は，Qwen3-TTSの推論環境をRuntimeから独立して準備し，許諾を確認した参照素材から再利用可能なclone promptを作るためのものである．参照素材はまだ提供されておらず，実際のclone品質・声の一致度・実機latencyの受入れは未完了である．合成fixtureのtest合格を実声の合格とみなさない．

C0.3.2の第3 providerであるIrodori Animeは，[Irodori-TTS experimental provider](irodori-tts-provider.md)の別手順を使う．QwenとIrodoriのreference identity形式やstyle capabilityを混在させない．

## 1．推論用のPython環境を準備する

以下のpathはすべてplaceholderである．`EPHY_VOICE_ROOT`にはGit repository外の絶対pathを指定する．Runtime repositoryのignored directoryも使用できない．symlinkを含むpathはprivate storeが拒否する．既存素材を移動・削除する必要はない．

```bash
export EPHY_RUNTIME_REPO='/absolute/path/to/ephy-runtime'
export EPHY_VOICE_ROOT='/absolute/private/ephy-voice'
umask 077
install -d -m 700 "$EPHY_VOICE_ROOT"
python3.12 -m venv "$EPHY_VOICE_ROOT/venv"
"$EPHY_VOICE_ROOT/venv/bin/python" -m pip install -e "${EPHY_RUNTIME_REPO}[speech]"
"$EPHY_VOICE_ROOT/venv/bin/python" -m pip install \
  'qwen-tts @ git+https://github.com/QwenLM/Qwen3-TTS.git@022e286b98fbec7e1e916cb940cdf532cd9f488e'
```

このinstallはユーザーが明示的に実行する準備操作であり，Runtimeの起動時には実行しない．公式sourceのversion `0.1.1`とVCS commitを検証するため，別のPyPI版や手でコピーしたsourceへ置き換えない．固定sourceのPython metadataはPython 3.12を対象に含む．推論環境の依存解決と実機動作は，対象machineで確認する．[公式package定義](https://github.com/QwenLM/Qwen3-TTS/blob/022e286b98fbec7e1e916cb940cdf532cd9f488e/pyproject.toml)．

## 2．固定modelを明示的に配置する

最初のproviderは`Qwen/Qwen3-TTS-12Hz-1.7B-Base`で，model revisionを`fd4b254389122332181a7c3db7f27e918eec64e3`に固定する．次のコマンドを実行すると，約4.54 GBのmodel filesを指定directoryへdownloadする．この操作は準備時に一度だけ明示的に実行する．既に同じrevisionの検証済みlocal filesがある場合は，そのmodel directoryを後のconfigで指定できる．[公式model revision](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-Base/tree/fd4b254389122332181a7c3db7f27e918eec64e3)．

```bash
"$EPHY_VOICE_ROOT/venv/bin/python" - <<'PY'
import os
from pathlib import Path
from huggingface_hub import snapshot_download
from apps.speech.qwen import MODEL_FILES, MODEL_ID, MODEL_REVISION, verify_model_files

destination = Path(os.environ["EPHY_VOICE_ROOT"]) / "model"
snapshot_download(
    repo_id=MODEL_ID,
    revision=MODEL_REVISION,
    token=False,
    local_dir=str(destination),
    cache_dir=str(Path(os.environ["EPHY_VOICE_ROOT"]) / "download-cache"),
    allow_patterns=list(MODEL_FILES),
)
verify_model_files(destination)
print("model_files_verified")
PY
```

adapterは11 filesのsizeと公開hashを照合する．推論時は`local_files_only=True`とoffline設定を使い，不足fileを自動downloadしない．sourceとmodelのpinを変更する場合は，adapterの互換性検証が必要になる．

## 3．参照素材と許諾記録を準備する

所有する素材，または複製・voice clone・生成利用について明示的な許諾を得た素材だけを使用する．参照音声，正確なtranscript，許諾根拠，派生clone promptはprivate directoryに置き，Git・trace・recovery log・評価exportへコピーしない．

- 参照WAVはPCM16，mono，8〜48 kHz，60秒以内，8 MiB以内とする．音声のcleanさとtranscriptの正確さは利用者が確認する．storeの形式検証だけでは確認できない．
- transcriptはUTF-8 text file，16 KiB以内とする．内容は改行を含めてそのまま保持し，clone promptにも同じ本文を使用する．
- private directoryは0700，config・consent・保存されたasset filesは0600とする．configとconsentもGit内配置，symlink，hardlinkを拒否する．

`consent.json`のひな型は以下である．個別に確認できた項目だけ`true`へ変更し，placeholderを実際のprivate記録に置き換える．登録には5項目すべての確認が必要である．このひな型は許諾を代わりに与えるものではない．

```json
{
  "authority": "owned",
  "storage_allowed": false,
  "voice_clone_allowed": false,
  "synthesis_allowed": false,
  "transcript_verified": false,
  "clean_reference": false,
  "attested_by": "<private attestor identity>",
  "attested_at": "<ISO 8601 timestamp with timezone>",
  "permission_evidence": "<private permission record>"
}
```

`authority`は`owned`または`explicit_permission`だけを受け付ける．後者は空でない`permission_evidence`が必須である．`attested_by`は1〜200文字，`attested_at`はtimezone付きISO timestamp，`permission_evidence`は4，096文字以内とする．未知のkeyは拒否する．この記録と素材fileを，たとえば`$EPHY_VOICE_ROOT/input/`へ配置する．

```bash
chmod 700 "$EPHY_VOICE_ROOT/input"
chmod 600 "$EPHY_VOICE_ROOT/input/consent.json"
chmod 600 "$EPHY_VOICE_ROOT/input/reference.wav"
chmod 600 "$EPHY_VOICE_ROOT/input/transcript.txt"

"$EPHY_VOICE_ROOT/venv/bin/python" -m apps.speech import-reference \
  --asset-store "$EPHY_VOICE_ROOT/assets" \
  --audio "$EPHY_VOICE_ROOT/input/reference.wav" \
  --transcript "$EPHY_VOICE_ROOT/input/transcript.txt" \
  --consent "$EPHY_VOICE_ROOT/input/consent.json"
```

成功時に出力されるのは`reference_id`と`provenance_id`だけである．`reference_id`は`ref_`＋64桁のhex，`provenance_id`は`prov_`＋32桁のhexになる．元のfileを変更せず，WAVの不要metadataを除いたPCMとtranscriptをprivate storeへコピーする．既存assetは上書きしない．失敗時に削除するのは今回の登録処理が作ったstagingだけである．

## 4．再利用するclone promptを準備する

`$EPHY_VOICE_ROOT/prepare-config.json`を0600のprivate fileとして作成する．下のpath placeholderを実際の絶対pathへ置き換える．`device`は`cpu`，`mps`，`cuda:0`，`dtype`は`float32`，`float16`，`bfloat16`のいずれかを指定できるが，利用可能な組合せと速度は実機で確認する．例はadapterの既定値を使用する．

```json
{
  "source_revision": "022e286b98fbec7e1e916cb940cdf532cd9f488e",
  "model_revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
  "model_path": "/absolute/private/ephy-voice/model",
  "asset_store_path": "/absolute/private/ephy-voice/assets",
  "device": "cpu",
  "dtype": "float32",
  "output_budget": 512,
  "default_profile_id": "",
  "profiles": []
}
```

```bash
chmod 600 "$EPHY_VOICE_ROOT/prepare-config.json"
"$EPHY_VOICE_ROOT/venv/bin/python" -m apps.speech prepare \
  --config "$EPHY_VOICE_ROOT/prepare-config.json" \
  --reference-id 'ref_<64 hex digits returned by import-reference>'
```

このコマンドはmodelを実際に読み込み，検証済み参照音声と正確なtranscriptからzero-shot clone promptを生成する．fine tuningは行わない．成功時は`reference_id`と64桁の`clone_prompt_digest`だけを出力する．同じ登録内容への上書きは行わず，既存のdigestを再利用する．

clone promptのcodeとspeaker embeddingはprivate NPZに保存し，pickleを使わない．読込み時は`allow_pickle=False`，size・dtype・shape・finite値・digest・model/tokenizer pin・provenanceを検証する．検証後に参照WAVのpathを開き直さず，検証済みbytesをadapterへ渡す．

## 5．公開profileを登録してserviceを起動する

`$EPHY_VOICE_ROOT/service-config.json`を新しい0600のprivate fileとして作成する．準備用configと同じmodel設定に，profileを追加する．以下の`clone_prompt_digest`と`provenance_id`を前の手順の出力に置き換える．`voice_profile_id`は素材名やpathを含めない任意のopaque ID，`display_name`はUIに表示してよい名称とする．

```json
{
  "source_revision": "022e286b98fbec7e1e916cb940cdf532cd9f488e",
  "model_revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
  "model_path": "/absolute/private/ephy-voice/model",
  "asset_store_path": "/absolute/private/ephy-voice/assets",
  "device": "cpu",
  "dtype": "float32",
  "output_budget": 512,
  "default_profile_id": "voice-local-001",
  "profiles": [
    {
      "voice_profile_id": "voice-local-001",
      "display_name": "Local Voice",
      "provider": "qwen3-tts",
      "model_revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
      "language": "ja-JP",
      "clone_prompt_digest": "<64 hex digits returned by prepare>",
      "provenance_id": "prov_<32 hex digits returned by import-reference>"
    }
  ]
}
```

default styleとcapabilitiesはadapterが返す．Qwen Baseのこの実装で変更できるdelivery controlはvolume 0〜1だけである．未対応のaffect・intensity・pace・pitch・pause設定を非既定値に変えると`unsupported_voice_control`になる．UIは利用可能なcontrolだけを表示する．profileの声を変更する場合は新しいIDを作り，既存IDの参照先を別のclone promptへ差し替えない．

serviceとRuntimeだけが共有するbearer secretを，Git外のprivate directoryに一度だけ生成する．既存fileは上書きしない．secretを設定JSON，command引数，UI，trace，logへ記載しない．以下はshellのtraceを無効にして実行する．

```bash
set +x
python3 - <<'PYTOKEN'
import os
from pathlib import Path
import secrets
path = Path(os.environ["EPHY_VOICE_ROOT"]) / "bearer-token"
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w") as output:
    output.write(secrets.token_urlsafe(32))
PYTOKEN
export EPHY_TTS_BEARER_TOKEN="$(cat "$EPHY_VOICE_ROOT/bearer-token")"
chmod 600 "$EPHY_VOICE_ROOT/service-config.json"
"$EPHY_VOICE_ROOT/venv/bin/python" -m apps.speech serve \
  --config "$EPHY_VOICE_ROOT/service-config.json" \
  --port 8767
```

このterminalでserviceを継続実行する．別terminalから公開metadataだけを確認できる．

```bash
set +x
export EPHY_TTS_BEARER_TOKEN="$(cat "$EPHY_VOICE_ROOT/bearer-token")"
python3 - <<'PYHTTP'
import os
import urllib.request
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
for route in ("/health", "/v1/voice-profiles"):
    request = urllib.request.Request("http://127.0.0.1:8767" + route,
        headers={"Authorization": "Bearer " + os.environ["EPHY_TTS_BEARER_TOKEN"]})
    with opener.open(request, timeout=5) as response:
        print(response.read().decode())
PYHTTP
```

全routeでbearer認証が必須であり，未認証・不一致・重複Authorizationは401の`tts_unauthorized`になる．Origin付きrequestとloopback IP以外のHostは403の`tts_forbidden`になる．secret未設定・形式不正ならserviceは`invalid_voice_config`で起動を拒否する．secretは32〜128文字の英数字・`_`・`-`だけを受け付ける．access logは無効で，inference子processへsecretを継承しない．bindは引き続き`127.0.0.1`固定である．

この境界はsecretを持たないlocal processやbrowserからの呼出しを拒否する．同一OS userがprivate fileやprocess環境を読み取れる場合と，管理者権限でのアクセスを隔離する仕組みではない．secretを変更した場合はserviceとRuntimeの両方を同じ値で再起動する．

`/health`の`ready`はconfigと依存が存在することを表し，実際のmodel読込み・音声合成・声質の合格を保証しない．実合成時にもpinとassetの整合を検証する．初回合成ではmodel読込みが加わる．serviceは1要求ずつ処理し，句間ではworkerを再利用する．

## 6．Runtimeを起動して声を選ぶ

別terminalでRuntime repositoryの絶対pathを指定し，ビルド済みのconversation appを起動する．既定endpointは`http://127.0.0.1:8767`なので，そのportを使う場合は`EPHY_TTS_ENDPOINT`の追加設定は不要である．

```bash
EPHY_RUNTIME_REPO='/absolute/path/to/ephy-runtime'
EPHY_VOICE_ROOT='/absolute/private/ephy-voice'
set +x
export EPHY_TTS_BEARER_TOKEN="$(cat "$EPHY_VOICE_ROOT/bearer-token")"
bash "$EPHY_RUNTIME_REPO/scripts/start_conversation_app.sh"
```

portを変えた場合だけ，adapterの接続先を指定する．

```bash
EPHY_TTS_ENDPOINT='http://127.0.0.1:8768' \
  bash "$EPHY_RUNTIME_REPO/scripts/start_conversation_app.sh"
```

serviceも同じportで起動する．endpointはloopback IPのHTTPに限定し，redirectとproxyは使用しない．別Inference nodeを使う場合は，明示的なSSH port forwarding等でloopbackへ接続する．Runtimeへ参照音声・transcript・embedding・model pathを設定しない．

serviceの構成済みdefault profileが利用可能なら，UI初期選択とprofile省略時のRuntime選択に反映する．catalog初回確認中は音声開始を保留し，defaultまたは選択中のprofileが利用不能・消失した場合はその選択を保持してUIに明示する．text入力は引き続き利用でき，Kyokoは明示選択できる．custom profileの失敗時にはerrorを表示し，別の声へ暗黙に切り替えない．Kyokoへ戻す場合はUIで明示的に選択する．Apple Speechの利用可否とTCCの確認は，TTS serviceとは別である．

## 再生境界と確認する内容

この実装の`streaming_mode`は`phrase`である．C0.1が確定した発話単位だけを受け取り，句読点または空白の安全な境界で180文字以内に分ける．安全な境界が見つからない180文字超の連続部分は，Qwenへ送信する前に`invalid_speech_text`で拒否する．既存Kyokoの180文字fallbackは維持する．

Qwenの1句のWAVと正常EOSを確認した時点で再生queueへ渡すので，応答全文のWAV完成は待たない．1句の途中のwaveformを先行再生する機能ではない．`non_streaming_mode=False`をcodec単位のstreaming成功として扱わない．

- 短い応答と複数句の応答で，文章を欠落させず，先の句を再生中に後続が準備されるか確認する．Markdownの見出し記号やcode本文を読み上げないことも確認する．
- 再生中cancelで停止し，次turnへ遅延音声が混ざらないことを確認する．「続きを生成」でも選択したprofileとstyleを保つことを確認する．
- Qwenのvolume，Kyokoのpace／volumeを確認する．未対応controlがUIへ表示されないことを確認する．
- 初回とwarm状態のlatencyを区別し，声の一致度，発音，聞き取りやすさを実素材の許諾範囲で評価する．現時点で実測値・合格値は記録されていない．

要求期限とRuntimeの累積TTS予算は60秒である．期限超過は`tts_timeout`，正常EOSを確認できない音声は`tts_incomplete`として失敗する．これらを正常再生と報告せず，対象machine・provider/model pin・本文を含まない計測情報で切り分ける．参照素材や音声をdiagnostic logへ貼り付けない．

Qwenで声の一致度またはstyleが不足すると確認された場合だけ，後続でCosyVoice 3との比較を検討する．この手順にVAD，barge-in，filler，fine tuning，C0.4の開始は含まれない．
