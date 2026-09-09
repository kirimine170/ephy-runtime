# Irodori-TTS experimental provider

C0.3.2ではIrodoriを明示選択専用のexperimental providerとして使う．設計理由は[ADR-0013](adr/ADR-0013-irodori-experimental-provider.md)を参照する．実音声，transcript，permission evidence，reference group，生成音声はGit外のprivate storeにだけ置く．

## 固定revisionとartifact

| Component | Revision | License確認 | 固定artifact |
| --- | --- | --- | --- |
| `Aratako/Irodori-TTS` | `8224dafb46d0aba89209a8f905f1cb7e3299d9c1` | repository MIT | `irodori-tts==0.1.0`とVCS commitを照合 |
| `phasefield-audio/Irodori-TTS-v4.1-Anime` | `6b259f5baa5e236b3d14cbd1f8555ca87d92b530` | model card MIT | `model.safetensors`，3，064，295，596 bytes，SHA-256 `5630aa0a661930ff678a1d1f1893876e24520f4776d4fcc25ca54e6ab9f41f37` |
| tokenizer | Anime revisionと同じ | modelと同じ | `tokenizer.json`，6，718，495 bytes，Git blob `1b9a8b7e5e8d37174e6b9e9655cdbd0c9b456af7`；`tokenizer_config.json`，668 bytes，Git blob `e59d0be2a7fd2bc7f7b85136e46c65a41c1c3634` |
| `Aratako/Semantic-DACVAE-Japanese-32dim` | `47376ee24834d7a05a48ebabfe3cde29b3c5e214` | model card MIT | `weights.pth`，429，620，065 bytes，SHA-256 `db120339c5ee7eca1912cdf29bc612b947a0808e69c3cebfb4936b45a762c1d5` |
| `Sony/SilentCipher` | `a1c4d021905e0dc5b24be5f68db5fc4dba410ee1` | weight licenseは未確定 | 44.1 kHzの5 filesを`apps/speech/irodori.py`のsize/hash manifestで照合 |

Irodori-TTS-ServerはStep 3の比較対象だが，Runtime統合では使用しない．disconnectで実synthesisを停止できないためである．Animeのtraining data詳細と客観評価も公開情報だけでは不足している．技術検証の採用と公開製品への採用を分ける．

推論環境ではIrodoriに加え，`dacvae==1.0.0`をsource commit `414c20785fc3a28373073ea8ef7a1316eeeaca6e`，`silentcipher==1.0.5`をsource commit `d46d7d0893a583d8968ab3a6626e2289faec9152`へ固定する．adapterは3 distributionのversionと`direct_url.json`を起動時に照合する．通常のRuntime起動中にpackageやweightをdownloadしない．

```bash
"$EPHY_VOICE_ROOT/venv/bin/python" -m pip install \
  'irodori-tts @ git+https://github.com/Aratako/Irodori-TTS.git@8224dafb46d0aba89209a8f905f1cb7e3299d9c1' \
  'dacvae @ git+https://github.com/facebookresearch/dacvae@414c20785fc3a28373073ea8ef7a1316eeeaca6e' \
  'silentcipher @ git+https://github.com/SesameAILabs/silentcipher.git@d46d7d0893a583d8968ab3a6626e2289faec9152'
```

## private reference group

既存の`import-reference`で各clipを登録する．すべてに正確なtranscriptと所有・明示許諾recordが必要である．1〜4個の登録済みreferenceを一つのordered groupにする．同じIDの重複，symlink，hardlink，unsafe permission，上書きを拒否する．

```bash
"$EPHY_VOICE_ROOT/venv/bin/python" -m apps.speech group-references \
  --asset-store "$EPHY_VOICE_ROOT/assets" \
  --reference-id 'ref_<first 64 hex>' \
  --reference-id 'ref_<second 64 hex>'
```

成功時に公開してよいのは`reference_group_digest`，`provenance_id`，`reference_count`だけである．現在のIrodori APIはtranscriptをspeaker conditionへ渡さないが，storeの許諾・正確性条件は弱めない．

## service config

Qwen互換の旧configも維持するが，複数providerには次の形を使う．pathはすべてGit外の絶対pathで，実fileへ置き換える．IrodoriはFP32だけを受け付ける．

```json
{
  "asset_store_path": "/absolute/private/ephy-voice/assets",
  "default_profile_id": "voice-qwen-local",
  "providers": {
    "qwen3-tts": {
      "source_revision": "022e286b98fbec7e1e916cb940cdf532cd9f488e",
      "model_revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
      "model_path": "/absolute/private/qwen-model",
      "device": "mps", "dtype": "float32", "output_budget": 512
    },
    "irodori-tts": {
      "source_revision": "8224dafb46d0aba89209a8f905f1cb7e3299d9c1",
      "model_revision": "6b259f5baa5e236b3d14cbd1f8555ca87d92b530",
      "model_path": "/absolute/private/irodori-anime/model.safetensors",
      "codec_revision": "47376ee24834d7a05a48ebabfe3cde29b3c5e214",
      "codec_path": "/absolute/private/semantic-dacvae",
      "silentcipher_revision": "a1c4d021905e0dc5b24be5f68db5fc4dba410ee1",
      "silentcipher_path": "/absolute/private/silentcipher",
      "device": "mps", "precision": "fp32", "seed": 420, "num_steps": 40,
      "cfg": {"text": 3.0, "caption": 3.0, "speaker": 5.0}
    }
  },
  "profiles": [
    {
      "voice_profile_id": "voice-qwen-local", "display_name": "Qwen Local Voice",
      "provider": "qwen3-tts", "model_revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
      "language": "ja-JP", "clone_prompt_digest": "<64 hex>", "provenance_id": "prov_<32 hex>"
    },
    {
      "voice_profile_id": "voice-irodori-anime-exp", "display_name": "Irodori Anime（実験）",
      "provider": "irodori-tts", "model_revision": "6b259f5baa5e236b3d14cbd1f8555ca87d92b530",
      "language": "ja-JP", "reference_group_digest": "<64 hex>", "provenance_id": "prov_<32 hex>"
    }
  ]
}
```

`default_profile_id`をIrodori profileへ設定するとserviceは`invalid_voice_config`で起動を拒否する．Runtimeもuntrusted catalogからのIrodori defaultを拒否する．loopback bind，bearer secret，起動方法は[custom voice TTS手順](custom-voice-tts.md)と同じである．

## 自動検証と実UATの境界

自動検証はsynthetic WAVだけを使う．provider contract，capability UI，有限style mapping，reference grouping，bearer認証，timeout，cancel，disconnect，stale audio，長文continuation，200-turn stress，trace retention，temporary WAV削除を検証する．

実UATでは許諾済みlocal referenceだけを使用し，読み，途中切れ，identity，style，感情の過剰さ，長文安定性を人間が判定する．Step 3.6の暫定policyはauto duration＋Runtimeの句分割＋対象語辞書である．自動test合格だけでvoice品質，production-ready，default voice採用を宣言しない．
