"""Pinned experimental Irodori v4.1 Anime adapter．

Runtime owns phrase segmentation．This adapter receives exactly one committed
speech unit，uses automatic duration prediction，and never accepts free-form
caption text from a caller．
"""

from __future__ import annotations

import array
from contextlib import contextmanager
import hashlib
import importlib.metadata
import io
import json
import math
import os
from pathlib import Path
import re
import sys
import tempfile
import unicodedata
import wave
from typing import Any, Callable, Iterator

from .schemas import MAX_WAV_BYTES, SpeechError, SpeechRequest

SOURCE_REVISION = "8224dafb46d0aba89209a8f905f1cb7e3299d9c1"
MODEL_ID = "phasefield-audio/Irodori-TTS-v4.1-Anime"
MODEL_REVISION = "6b259f5baa5e236b3d14cbd1f8555ca87d92b530"
CODEC_ID = "Aratako/Semantic-DACVAE-Japanese-32dim"
CODEC_REVISION = "47376ee24834d7a05a48ebabfe3cde29b3c5e214"
SILENTCIPHER_ID = "Sony/SilentCipher"
SILENTCIPHER_REVISION = "a1c4d021905e0dc5b24be5f68db5fc4dba410ee1"
DACVAE_SOURCE_REVISION = "414c20785fc3a28373073ea8ef7a1316eeeaca6e"
SILENTCIPHER_SOURCE_REVISION = "d46d7d0893a583d8968ab3a6626e2289faec9152"

# Immutable Hugging Face artifact identities．SHA-1 entries are Git blob IDs；
# SHA-256 entries are LFS object IDs．Quantized files are intentionally absent．
MODEL_FILES = {
    "model.safetensors": (3064295596, "5630aa0a661930ff678a1d1f1893876e24520f4776d4fcc25ca54e6ab9f41f37"),
    "tokenizer/tokenizer.json": (6718495, "1b9a8b7e5e8d37174e6b9e9655cdbd0c9b456af7"),
    "tokenizer/tokenizer_config.json": (668, "e59d0be2a7fd2bc7f7b85136e46c65a41c1c3634"),
}
CODEC_FILES = {
    "weights.pth": (429620065, "db120339c5ee7eca1912cdf29bc612b947a0808e69c3cebfb4936b45a762c1d5"),
}
SILENTCIPHER_FILES = {
    "44_1_khz/73999_iteration/enc_c.ckpt": (184765, "ff64f80d2391fdfc888e4103c499be4a2c958e59626587a1eab6db93204814c7"),
    "44_1_khz/73999_iteration/dec_c.ckpt": (2008794, "c23b57635172b2fbd3a8531d15ba76b5885a10d0fe902ccb823c521c56041b33"),
    "44_1_khz/73999_iteration/dec_m_0.ckpt": (9554818, "829540c058270d29788f05294894d45bf436e44add0e1099422242ebb94b7088"),
    "44_1_khz/73999_iteration/hparams.yaml": (1474, "41c69dfaabdf288dd8ecc8f0aa59f73d211537e8"),
    "44_1_khz/73999_iteration/opt.ckpt": (23448174, "f129597d7b5459be0455c43d9df12b6a842cbb95344024ebe297fb2516cfd6d1"),
}

STYLE_CAPTIONS = {
    "neutral": "自然な会話調で，落ち着いた話し方",
    "warm": "柔らかく親しい，温かみのある話し方",
    "cheerful": "明るく嬉しそうな，はきはきした話し方",
    "cute": "可愛らしく，やや高めで親しみのある話し方",
    "sleepy": "眠そうで，ゆっくり柔らかな話し方",
    "concerned": "心配そうに，穏やかに寄り添う話し方",
}

# Step 3.6 found that targeted substitutions are safer than whole-text kana．
# This list is deliberately finite，reviewable，and independent of LLM output．
PRONUNCIATIONS = (
    (re.compile(r"(?<![A-Za-z])Ephy(?![A-Za-z])", re.IGNORECASE), "エフィー"),
    (re.compile(r"(?<![A-Za-z])Qwen(?![A-Za-z])", re.IGNORECASE), "クウェン"),
    (re.compile(r"(?<![A-Za-z])GitHub(?![A-Za-z])", re.IGNORECASE), "ギットハブ"),
    (re.compile(r"(?<![A-Za-z])ASR(?![A-Za-z])", re.IGNORECASE), "エーエスアール"),
    (re.compile(r"(?<![A-Za-z])TTS(?![A-Za-z])", re.IGNORECASE), "ティーティーエス"),
    (re.compile(r"(?<![A-Za-z])LLM(?![A-Za-z])", re.IGNORECASE), "エルエルエム"),
    (re.compile(r"(?<![A-Za-z])API(?![A-Za-z])", re.IGNORECASE), "エーピーアイ"),
)


def _verify_files(root: Path, files: dict[str, tuple[int, str]]) -> None:
    if not root.is_absolute() or not root.is_dir():
        raise SpeechError("tts_unavailable")
    for name, (size, expected) in files.items():
        path = root / name
        if not path.is_file() or path.is_symlink() or path.stat().st_size != size:
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
        for package, version, revision in (
            ("irodori-tts", "0.1.0", SOURCE_REVISION),
            ("dacvae", "1.0.0", DACVAE_SOURCE_REVISION),
            ("silentcipher", "1.0.5", SILENTCIPHER_SOURCE_REVISION),
        ):
            distribution = importlib.metadata.distribution(package)
            direct = json.loads(distribution.read_text("direct_url.json") or "{}")
            if distribution.version != version or direct.get("vcs_info", {}).get("commit_id") != revision:
                raise SpeechError("tts_model_revision_mismatch")
    except SpeechError:
        raise
    except Exception:
        raise SpeechError("tts_unavailable") from None


def normalize_speech_text(text: str) -> tuple[str, tuple[str, ...]]:
    result = unicodedata.normalize("NFKC", text)
    changes: list[str] = []
    for pattern, reading in PRONUNCIATIONS:
        updated, count = pattern.subn(reading, result)
        if count:
            changes.append(pattern.pattern)
            result = updated
    # Emoji is control metadata，not spoken text．Preserve Japanese punctuation．
    result = "".join(char for char in result if not (
        0x1F1E6 <= ord(char) <= 0x1FAFF or 0x2600 <= ord(char) <= 0x27BF
        or ord(char) in {0x200D, 0xFE0E, 0xFE0F}
    ))
    result = re.sub(r"[ \t]+", " ", result).strip()
    if not result or len(result) > 180:
        raise SpeechError("invalid_speech_text")
    return result, tuple(changes)


def style_caption(request: SpeechRequest) -> str:
    try:
        parts = [STYLE_CAPTIONS[request.affect]]
    except KeyError:
        raise SpeechError("unsupported_voice_control") from None
    if request.intensity == 0.75:
        parts.append("感情表現は控えめ")
    elif request.intensity == 1.25:
        parts.append("感情表現はやや強め")
    elif request.intensity != 1.0:
        raise SpeechError("unsupported_voice_control")
    if request.pace == 0.85:
        parts.append("少しゆっくり")
    elif request.pace == 1.15:
        parts.append("少しテンポよく")
    elif request.pace != 1.0:
        raise SpeechError("unsupported_voice_control")
    pause = {"natural": "自然な間", "short": "間を短めに", "deliberate": "句読点で丁寧に間を置く"}
    if request.pause_style not in pause:
        raise SpeechError("unsupported_voice_control")
    parts.append(pause[request.pause_style])
    return "．".join(parts) + "．"


def _waveform_to_wav(samples: Any, sample_rate: int, volume: float) -> bytes:
    try:
        if type(sample_rate) is not int or not 8000 <= sample_rate <= 48000 or not math.isfinite(volume) or not 0 <= volume <= 1:
            raise SpeechError("tts_invalid_audio")
        if hasattr(samples, "detach"):
            samples = samples.detach().float().cpu().squeeze().tolist()
        if not isinstance(samples, (list, tuple)) or not 0 < len(samples) <= sample_rate * 60:
            raise SpeechError("tts_invalid_audio")
        pcm = array.array("h")
        for sample in samples:
            value = float(sample)
            if not math.isfinite(value):
                raise SpeechError("tts_invalid_audio")
            pcm.append(round(max(-1.0, min(1.0, value)) * volume * 32767))
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


class IrodoriAdapter:
    def __init__(self, config: dict[str, Any], *, runtime: Any = None,
                 group_loader: Callable[[str], Any] | None = None,
                 sampling_request_factory: Callable[..., Any] | None = None):
        self.config = config
        self.runtime = runtime
        self.group_loader = group_loader
        self.sampling_request_factory = sampling_request_factory
        self.groups: dict[str, Any] = {}
        self.seed = config.get("seed", 420)
        self.num_steps = config.get("num_steps", 40)
        self.cfg = config.get("cfg", {"text": 3.0, "caption": 3.0, "speaker": 5.0})
        if (type(self.seed) is not int or not 0 <= self.seed <= 2**31 - 1
                or type(self.num_steps) is not int or not 1 <= self.num_steps <= 100
                or not isinstance(self.cfg, dict) or set(self.cfg) != {"text", "caption", "speaker"}
                or not all(type(value) in (int, float) and math.isfinite(value) and 0 <= value <= 10 for value in self.cfg.values())):
            raise SpeechError("invalid_speech_text")

    def _load(self) -> None:
        if self.runtime is not None:
            return
        if (self.config.get("source_revision") != SOURCE_REVISION
                or self.config.get("model_revision") != MODEL_REVISION
                or self.config.get("codec_revision") != CODEC_REVISION
                or self.config.get("silentcipher_revision") != SILENTCIPHER_REVISION):
            raise SpeechError("tts_model_revision_mismatch")
        verify_source_revision()
        model_path = Path(self.config["model_path"])
        model_root = model_path.parent if model_path.name == "model.safetensors" else model_path
        _verify_files(model_root, MODEL_FILES)
        _verify_files(Path(self.config["codec_path"]), CODEC_FILES)
        silent_root = Path(self.config["silentcipher_path"])
        _verify_files(silent_root, SILENTCIPHER_FILES)
        if self.config.get("precision", "fp32") != "fp32":
            raise SpeechError("invalid_speech_text")
        device = self.config.get("device", "mps")
        if device not in {"cpu", "mps", "cuda"}:
            raise SpeechError("invalid_speech_text")
        os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", HF_HUB_DISABLE_TELEMETRY="1")
        import silentcipher
        original_get_model = silentcipher.get_model
        iteration = silent_root / "44_1_khz" / "73999_iteration"

        def local_get_model(*, model_type: str, device: str):
            if model_type != "44.1k":
                raise SpeechError("tts_model_revision_mismatch")
            return original_get_model(model_type=model_type, device=device,
                                      ckpt_path=str(iteration), config_path=str(iteration / "hparams.yaml"))

        silentcipher.get_model = local_get_model
        try:
            from irodori_tts.inference_runtime import InferenceRuntime, RuntimeKey
            from irodori_tts.tokenizer import PretrainedTextTokenizer
            original_tokenizer_descriptor = PretrainedTextTokenizer.__dict__["from_pretrained"]
            original_tokenizer = PretrainedTextTokenizer.from_pretrained

            def local_tokenizer(cls, repo_id: str, add_bos: bool = True,
                                local_files_only: bool = False, revision: str | None = None):
                return original_tokenizer(repo_id=str(model_root / "tokenizer"),
                                          add_bos=add_bos, local_files_only=True)

            PretrainedTextTokenizer.from_pretrained = classmethod(local_tokenizer)
            try:
                self.runtime = InferenceRuntime.from_key(RuntimeKey(
                    checkpoint=str(model_root / "model.safetensors"), model_device=device,
                    codec_repo=str(Path(self.config["codec_path"]) / "weights.pth"), model_precision="fp32",
                    codec_device=device, codec_precision="fp32", compile_model=False,
                ))
            finally:
                PretrainedTextTokenizer.from_pretrained = original_tokenizer_descriptor
        finally:
            silentcipher.get_model = original_get_model

    def _group(self, digest: str, provenance_id: str) -> Any:
        if digest not in self.groups:
            if self.group_loader is not None:
                group = self.group_loader(digest)
            else:
                from packages.speech_assets import SpeechAssetStore
                group = SpeechAssetStore(Path(self.config["asset_store_path"])).load_reference_group(digest)
            if group.provenance_id != provenance_id or len(self.groups) >= 16:
                raise SpeechError("tts_asset_invalid")
            self.groups[digest] = group
        group = self.groups[digest]
        if group.provenance_id != provenance_id:
            raise SpeechError("tts_asset_invalid")
        return group

    @contextmanager
    def _reference_wav(self, group: Any) -> Iterator[str]:
        # Irodori v4.1 accepts one ref_wav．An ordered profile group is rendered
        # to one short，private，request-scoped WAV and unlinked immediately．
        try:
            target_rate = max(item.metadata["sample_rate"] for item in group.references)
            if not 8000 <= target_rate <= 48000:
                raise SpeechError("tts_asset_invalid")
            pieces: list[list[float]] = []
            for item in group.references:
                with wave.open(io.BytesIO(item.audio_bytes), "rb") as source:
                    pcm = array.array("h", source.readframes(source.getnframes()))
                    if sys.byteorder != "little":
                        pcm.byteswap()
                    samples = [sample / 32768.0 for sample in pcm]
                    rate = source.getframerate()
                if rate != target_rate:
                    count = max(1, round(len(samples) * target_rate / rate))
                    if len(samples) == 1:
                        samples = samples * count
                    else:
                        scale = (len(samples) - 1) / max(1, count - 1)
                        resampled = []
                        for index in range(count):
                            position = index * scale
                            left = min(int(position), len(samples) - 1)
                            right = min(left + 1, len(samples) - 1)
                            fraction = position - left
                            resampled.append(samples[left] * (1 - fraction) + samples[right] * fraction)
                        samples = resampled
                pieces.append(samples)
                pieces.append([0.0] * round(target_rate * 0.05))
            combined = [sample for piece in pieces[:-1] for sample in piece]
            if not 0 < len(combined) <= target_rate * 30:
                raise SpeechError("tts_asset_invalid")
            root = Path(self.config["temporary_path"])
            if not root.is_absolute() or not root.is_dir() or root.is_symlink():
                raise SpeechError("tts_asset_invalid")
            handle = tempfile.NamedTemporaryFile(prefix="ref-", suffix=".wav", dir=root, delete=False)
            path = Path(handle.name)
            os.fchmod(handle.fileno(), 0o600)
            with wave.open(handle, "wb") as output:
                output.setnchannels(1)
                output.setsampwidth(2)
                output.setframerate(target_rate)
                values = array.array("h", (round(max(-1.0, min(1.0, float(v))) * 32767) for v in combined))
                if sys.byteorder != "little":
                    values.byteswap()
                output.writeframes(values.tobytes())
            handle.close()
        except SpeechError:
            raise
        except Exception:
            raise SpeechError("tts_asset_invalid") from None
        try:
            yield str(path)
        finally:
            try:
                path.unlink(missing_ok=True)
            except Exception:
                pass

    def synthesize(self, request: SpeechRequest, profile: dict[str, Any]) -> bytes:
        self._load()
        if request.model_revision != MODEL_REVISION or request.clone_prompt_digest:
            raise SpeechError("voice_profile_changed")
        group = self._group(request.reference_group_digest, profile["provenance_id"])
        text, _ = normalize_speech_text(request.speech_text)
        caption = style_caption(request)
        if self.sampling_request_factory is None:
            from irodori_tts.inference_runtime import SamplingRequest
            request_factory = SamplingRequest
        else:
            request_factory = self.sampling_request_factory
        with self._reference_wav(group) as reference:
            result = self.runtime.synthesize(request_factory(
                text=text, caption=caption, ref_wav=reference, no_ref=False,
                seconds=None, duration_scale=1.0 / request.pace, min_seconds=0.5,
                max_seconds=30.0, max_ref_seconds=30.0, num_candidates=1,
                decode_mode="sequential", num_steps=self.num_steps,
                cfg_scale_text=float(self.cfg["text"]),
                cfg_scale_caption=float(self.cfg["caption"]),
                cfg_scale_speaker=float(self.cfg["speaker"]), seed=self.seed,
                trim_tail=True, lora_adapter=None,
            ))
        return _waveform_to_wav(result.audio, result.sample_rate, request.volume)
