"""Pinned Qwen Base adapter．The official API streams committed phrases only．"""

from __future__ import annotations

import array
import hashlib
import importlib.metadata
import io
import json
import math
import os
from pathlib import Path
import sys
import wave
from typing import Any, Callable

from .schemas import MAX_WAV_BYTES, SpeechError, SpeechRequest

SOURCE_REVISION = "022e286b98fbec7e1e916cb940cdf532cd9f488e"
MODEL_REVISION = "fd4b254389122332181a7c3db7f27e918eec64e3"
MODEL_ID = "Qwen/Qwen3-TTS-12Hz-1.7B-Base"
QWEN_LANGUAGES = {
    "ja-JP": "Japanese", "en-US": "English", "en-GB": "English", "zh-CN": "Chinese",
    "ko-KR": "Korean", "de-DE": "German", "fr-FR": "French", "ru-RU": "Russian",
    "pt-BR": "Portuguese", "pt-PT": "Portuguese", "es-ES": "Spanish", "it-IT": "Italian",
}
# Hugging Face immutable revision metadata．Ordinary files use Git blob SHA-1；
# safetensors use the published LFS SHA-256，never pickle checkpoints．
MODEL_FILES = {
    "config.json": (4494, "81b57e8e790c07e8fa7f82d8bdfd7574d485c396"),
    "generation_config.json": (245, "1872b16d4535564abf2db5d6debe6cddd82b7f2e"),
    "merges.txt": (1671839, "20024bfe7c83998e9aeaf98a0cd6a2ce6306c2f0"),
    "model.safetensors": (3857413744, "38fc7fc51c5e776e840414b6fd443962e9411b9654888fd7913e4da643cb857c"),
    "preprocessor_config.json": (127, "0525dd953bb9241912f7147666f0d535165d5d4f"),
    "speech_tokenizer/config.json": (2336, "06cc8dc4c5ec8a1929086b71b98c313020d9268b"),
    "speech_tokenizer/configuration.json": (76, "ab58e2eaf53cd14a1a2a7527d9261ceea93a24cd"),
    "speech_tokenizer/model.safetensors": (682293092, "836b7b357f5ea43e889936a3709af68dfe3751881acefe4ecf0dbd30ba571258"),
    "speech_tokenizer/preprocessor_config.json": (234, "ba40914f4f49ab98a8ca545d4892ef7291a39592"),
    "tokenizer_config.json": (7344, "6ff9fd60cc623bb54bbd603cbd418c97a11528d7"),
    "vocab.json": (2776833, "4783fe10ac3adce15ac8f358ef5462739852c569"),
}


def verify_model_files(root: Path, files: dict[str, tuple[int, str]] | None = None) -> None:
    if not root.is_absolute() or not root.is_dir():
        raise SpeechError("tts_unavailable")
    for name, (size, expected) in (MODEL_FILES if files is None else files).items():
        path = root / name
        if not path.is_file() or path.stat().st_size != size:
            raise SpeechError("tts_model_revision_mismatch")
        digest = hashlib.sha256() if len(expected) == 64 else hashlib.sha1()
        if len(expected) == 40:
            digest.update(f"blob {size}\0".encode())
        with path.open("rb") as source:
            for piece in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(piece)
        if digest.hexdigest() != expected:
            raise SpeechError("tts_model_revision_mismatch")


def verify_source_revision() -> None:
    try:
        distribution = importlib.metadata.distribution("qwen-tts")
        direct = json.loads(distribution.read_text("direct_url.json") or "{}")
        revision = direct.get("vcs_info", {}).get("commit_id")
        if distribution.version != "0.1.1" or revision != SOURCE_REVISION:
            raise SpeechError("tts_model_revision_mismatch")
    except SpeechError:
        raise
    except Exception:
        raise SpeechError("tts_unavailable") from None


def waveform_to_wav(samples: Any, sample_rate: int, volume: float) -> bytes:
    if sample_rate != 24000 or not math.isfinite(volume) or not 0 <= volume <= 1:
        raise SpeechError("tts_invalid_audio")
    try:
        if getattr(samples, "ndim", 1) != 1 or not 0 < len(samples) <= sample_rate * 60:
            raise SpeechError("tts_invalid_audio")
        pcm = array.array("h")
        for sample in samples:
            sample = float(sample)
            if not math.isfinite(sample):
                raise SpeechError("tts_invalid_audio")
            pcm.append(round(max(-1.0, min(1.0, sample)) * volume * 32767))
        if sys.byteorder != "little":
            pcm.byteswap()
        output = io.BytesIO()
        with wave.open(output, "wb") as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(sample_rate)
            wav.writeframes(pcm.tobytes())
        body = output.getvalue()
        if len(body) > MAX_WAV_BYTES:
            raise SpeechError("tts_invalid_audio")
        return body
    except SpeechError:
        raise
    except Exception:
        raise SpeechError("tts_invalid_audio") from None


class QwenAdapter:
    def __init__(self, config: dict[str, Any], *, model: Any = None,
                 prompt_loader: Callable[[str], Any] | None = None):
        self.config = config
        self.model = model
        self.prompt_loader = prompt_loader
        self.prompts: dict[str, Any] = {}
        self.prompt_provenance: dict[str, str] = {}
        self.output_budget = config.get("output_budget", 512)
        if type(self.output_budget) is not int or not 16 <= self.output_budget <= 1024:
            raise SpeechError("invalid_speech_text")

    def _load(self) -> None:
        if self.model is not None:
            return
        if self.config.get("model_revision") != MODEL_REVISION:
            raise SpeechError("tts_model_revision_mismatch")
        verify_source_revision()
        verify_model_files(Path(self.config["model_path"]))
        # Applied only to the independent worker．No implicit weight downloads．
        os.environ["HF_HUB_OFFLINE"] = "1"
        os.environ["TRANSFORMERS_OFFLINE"] = "1"
        os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
        import torch
        from qwen_tts import Qwen3TTSModel
        device = self.config.get("device", "cpu")
        if device not in {"cpu", "mps", "cuda:0"}:
            raise SpeechError("invalid_speech_text")
        if device == "mps" and not torch.backends.mps.is_available():
            raise SpeechError("tts_unavailable")
        dtype_name = self.config.get("dtype", "float32")
        if dtype_name not in {"float32", "float16", "bfloat16"}:
            raise SpeechError("invalid_speech_text")
        self.model = Qwen3TTSModel.from_pretrained(
            self.config["model_path"], device_map=device, dtype=getattr(torch, dtype_name),
            attn_implementation="sdpa", local_files_only=True, trust_remote_code=False,
        )
        if self.model.model.tts_model_type != "base":
            raise SpeechError("tts_model_revision_mismatch")

    def _validate_prompt_arrays(self, arrays: dict[str, Any]) -> None:
        import numpy as np
        try:
            codes, embedding = arrays["ref_code"], arrays["ref_spk_embedding"]
            config = self.model.model.config
            groups = int(config.talker_config.num_code_groups)
            vocabulary = int(config.talker_config.code_predictor_config.vocab_size)
            dimension = int(config.speaker_encoder_config.enc_dim)
            if (groups != 16 or vocabulary != 2048 or dimension != 2048 or
                    codes.dtype != np.dtype("int64") or codes.ndim != 2 or
                    not 0 < codes.shape[0] <= 750 or codes.shape[1] != groups or
                    int(codes.min()) < 0 or int(codes.max()) >= vocabulary or
                    embedding.dtype != np.dtype("float32") or embedding.shape != (dimension,) or
                    not np.isfinite(embedding).all()):
                raise SpeechError("tts_asset_invalid")
        except SpeechError:
            raise
        except Exception:
            raise SpeechError("tts_asset_invalid") from None

    def _prompt(self, digest: str, provenance_id: str | None) -> Any:
        if digest in self.prompts:
            if self.prompt_loader is None and self.prompt_provenance.get(digest) != provenance_id:
                raise SpeechError("tts_asset_invalid")
            return self.prompts[digest]
        if self.prompt_loader is not None:
            prompt = self.prompt_loader(digest)
        else:
            import torch
            from qwen_tts.inference.qwen3_tts_model import VoiceClonePromptItem
            from packages.speech_assets import SpeechAssetStore
            asset = SpeechAssetStore(Path(self.config["asset_store_path"])).load_clone_prompt(digest)
            metadata = asset.metadata
            if (metadata.get("model_revision") != MODEL_REVISION or metadata.get("provider") != "qwen3-tts" or
                    metadata.get("tokenizer_revision") != MODEL_REVISION or asset.provenance_id != provenance_id):
                raise SpeechError("tts_asset_invalid")
            self._validate_prompt_arrays(asset.arrays)
            prompt = [VoiceClonePromptItem(
                ref_code=torch.from_numpy(asset.arrays["ref_code"].copy()).long(),
                ref_spk_embedding=torch.from_numpy(asset.arrays["ref_spk_embedding"].copy()).float(),
                x_vector_only_mode=False, icl_mode=True, ref_text=metadata["ref_text"],
            )]
            self.prompt_provenance[digest] = asset.provenance_id
        # A configured service has a finite profile catalog．Never cache per text．
        if len(self.prompts) >= 16:
            raise SpeechError("tts_asset_invalid")
        self.prompts[digest] = prompt
        return prompt

    def synthesize(self, request: SpeechRequest, profile: dict[str, Any]) -> bytes:
        self._load()
        if request.model_revision != MODEL_REVISION or request.reference_group_digest:
            raise SpeechError("voice_profile_changed")
        language = QWEN_LANGUAGES.get(profile["language"])
        if language is None:
            raise SpeechError("invalid_speech_text")
        prompt = self._prompt(request.clone_prompt_digest, profile.get("provenance_id"))
        talker = self.model.model.talker
        original = talker.generate
        eos = int(self.model.model.config.talker_config.codec_eos_token_id)
        terminal: list[bool] = []

        def observe(*args: Any, **kwargs: Any) -> Any:
            result = original(*args, **kwargs)
            sequences = result.sequences
            if hasattr(sequences, "detach"):
                sequences = sequences.detach().cpu().tolist()
            if not isinstance(sequences, list) or len(sequences) != 1:
                raise SpeechError("tts_incomplete")
            tokens = sequences[0]
            terminal.append(bool(tokens) and len(tokens) <= self.output_budget and tokens[-1] == eos and eos not in tokens[:-1])
            return result

        talker.generate = observe
        try:
            waves, sample_rate = self.model.generate_voice_clone(
                text=request.speech_text, language=language, voice_clone_prompt=prompt,
                non_streaming_mode=False, max_new_tokens=self.output_budget,
            )
        finally:
            talker.generate = original
        if terminal != [True]:
            raise SpeechError("tts_incomplete")
        if not isinstance(waves, list) or len(waves) != 1:
            raise SpeechError("tts_invalid_audio")
        return waveform_to_wav(waves[0], sample_rate, request.volume)

    def prepare(self, reference_id: str) -> Any:
        self._load()
        import soundfile
        from packages.speech_assets import SpeechAssetStore
        store = SpeechAssetStore(Path(self.config["asset_store_path"]))
        reference = store.load_reference(reference_id)
        # Decode the approved private asset ourselves；never pass a URL to the SDK．
        samples, sample_rate = soundfile.read(io.BytesIO(reference.audio_bytes), dtype="float32", always_2d=False)
        prompts = self.model.create_voice_clone_prompt(
            ref_audio=(samples, sample_rate), ref_text=reference.transcript, x_vector_only_mode=False,
        )
        if len(prompts) != 1:
            raise SpeechError("tts_asset_invalid")
        item = prompts[0]
        arrays = {"ref_code": item.ref_code.detach().long().cpu().numpy(),
                  "ref_spk_embedding": item.ref_spk_embedding.detach().float().cpu().numpy()}
        self._validate_prompt_arrays(arrays)
        return store.store_clone_prompt(reference_id, provider="qwen3-tts", model_revision=MODEL_REVISION,
            tokenizer_revision=MODEL_REVISION, arrays=arrays,
            metadata={"x_vector_only_mode": False, "icl_mode": True, "ref_text": reference.transcript})
