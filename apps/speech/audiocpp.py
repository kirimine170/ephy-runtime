"""Pinned audio.cpp C ABI，loaded only inside the cancelable speech worker．"""

from __future__ import annotations

import array
import ctypes as C
import hashlib
import json
from pathlib import Path
import re
import sys
import threading
import wave

from .irodori import (
    IrodoriAdapter,
    normalize_speech_text,
    style_caption,
    _waveform_to_wav,
)
from .schemas import SpeechError, SpeechRequest

SOURCE_REVISION = "ff1bcc4555ff99c4383329b0b21b52a18cc8b3cd"
MODEL_REVISION = "1509fa38c945c13ffd645d8def423f0c4cb21254"
MODEL_SHA256 = "cef628aa1ce784993025789c24d05b4b9e60fa039d40453bc8bdea32f4cb99e7"
MODEL_SIZE = 1112547264
ABI_VERSION = 0x000100
STYLE_MAPPING_REVISION = "irodori-finite-v1"


class ModelConfig(C.Structure):
    _fields_ = [
        (key, C.c_char_p)
        for key in ("family_hint", "config_id", "weight_id", "model_spec_override")
    ]


class BackendConfig(C.Structure):
    _fields_ = [("backend", C.c_char_p), ("device", C.c_int), ("threads", C.c_int)]


def verified_file(path_value, expected: str, size: int | None = None) -> Path:
    path = Path(path_value)
    if (
        not path.is_absolute()
        or not path.is_file()
        or path.is_symlink()
        or any(parent.is_symlink() for parent in path.parents)
        or not isinstance(expected, str)
        or not re.fullmatch(r"[a-f0-9]{64}", expected)
        or (size is not None and path.stat().st_size != size)
    ):
        raise SpeechError("tts_model_revision_mismatch")
    # The existing isolated Irodori inference venv can use Python 3.10．
    # Stream large weights without requiring hashlib.file_digest (3.11+)．
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1 << 20), b""):
            digest.update(block)
    if digest.hexdigest() != expected:
        raise SpeechError("tts_model_revision_mismatch")
    return path


class AudioCppABI:
    """Only documented ABI 0.1 symbols．Never load this in the Gateway process．"""

    def __init__(self, path: Path, build_version: str):
        self.lib = C.CDLL(str(path))
        void, string, integer = C.c_void_p, C.c_char_p, C.c_int
        out = C.POINTER(void)
        signatures = {
            "abi_version": (C.c_uint32, []),
            "build_version": (string, []),
            "registry_create": (integer, [string, out]),
            "registry_free": (None, [void]),
            "model_load": (integer, [void, string, C.POINTER(ModelConfig), void, out]),
            "model_free": (None, [void]),
            "model_family": (string, [void]),
            "model_supports": (integer, [void, string, string]),
            "options_create": (void, []),
            "options_set": (integer, [void, string, string]),
            "options_free": (None, [void]),
            "session_create": (
                integer,
                [void, string, string, C.POINTER(BackendConfig), void, out],
            ),
            "session_free": (None, [void]),
            "session_run": (integer, [void, void, out]),
            "request_create": (void, []),
            "request_free": (None, [void]),
            "request_set_text": (integer, [void, string, string]),
            "request_set_option": (integer, [void, string, string]),
            "request_set_voice_audio": (
                integer,
                [void, C.POINTER(C.c_float), C.c_size_t, integer, integer],
            ),
            "result_free": (None, [void]),
            "result_audio": (
                integer,
                [
                    void,
                    C.POINTER(C.POINTER(C.c_float)),
                    C.POINTER(C.c_size_t),
                    C.POINTER(integer),
                    C.POINTER(integer),
                ],
            ),
        }
        for name, (restype, argtypes) in signatures.items():
            function = getattr(self.lib, "audiocpp_" + name)
            function.restype, function.argtypes = restype, argtypes
            setattr(self, name, function)
        if (
            self.abi_version() != ABI_VERSION
            or self.build_version() != build_version.encode()
        ):
            raise SpeechError("tts_model_revision_mismatch")

    @staticmethod
    def check(status):
        # Native error strings can contain text and reference paths．
        if status != 0:
            raise SpeechError("tts_failed")


class AudioCppAdapter(IrodoriAdapter):
    """Reuse pure controls/reference helpers；never call the Python inference loader．"""

    def __init__(self, config: dict, **kwargs):
        super().__init__(config, **kwargs)
        self.abi = None
        self.registry, self.model, self.session = (
            C.c_void_p(),
            C.c_void_p(),
            C.c_void_p(),
        )
        self.busy = threading.Lock()

    def _load(self):
        if self.abi is not None:
            return
        cfg = self.config
        if (
            cfg.get("source_revision") != SOURCE_REVISION
            or cfg.get("model_revision") != MODEL_REVISION
            or cfg.get("model_sha256") != MODEL_SHA256
            or cfg.get("abi_version") != ABI_VERSION
            or cfg.get("style_mapping_revision") != STYLE_MAPPING_REVISION
            or cfg.get("backend") not in {"cpu", "metal", "cuda"}
            or cfg.get("codec_backend", "same") not in {"same", "cpu"}
            or type(cfg.get("threads", 4)) is not int
            or not 1 <= cfg.get("threads", 4) <= 32
        ):
            raise SpeechError("tts_model_revision_mismatch")
        model_path = verified_file(cfg["model_path"], MODEL_SHA256, MODEL_SIZE)
        library = verified_file(cfg["library_path"], cfg["library_sha256"])
        manifest_path = verified_file(
            cfg["build_manifest_path"], cfg["build_manifest_sha256"]
        )
        manifest = json.loads(manifest_path.read_text())
        if (
            manifest.get("source_revision") != SOURCE_REVISION
            or manifest.get("library_sha256") != cfg["library_sha256"]
            or manifest.get("build_version") != cfg["build_version"]
            or manifest.get("abi_version") != ABI_VERSION
        ):
            raise SpeechError("tts_model_revision_mismatch")
        for dependency in manifest.get("dependencies", []):
            verified_file(dependency["path"], dependency["sha256"])
        abi = AudioCppABI(library, cfg["build_version"])
        options = None
        self.abi = abi
        try:
            abi.check(abi.registry_create(None, C.byref(self.registry)))
            model_config = ModelConfig(b"irodori_tts", None, None, None)
            abi.check(
                abi.model_load(
                    self.registry,
                    str(model_path).encode(),
                    C.byref(model_config),
                    None,
                    C.byref(self.model),
                )
            )
            if abi.model_family(self.model) != b"irodori_tts" or not abi.model_supports(
                self.model, b"tts", b"offline"
            ):
                raise SpeechError("tts_model_revision_mismatch")
            options = abi.options_create()
            if not options:
                raise SpeechError("tts_unavailable")
            for key, value in {
                "irodori_tts.codec_backend": cfg.get("codec_backend", "same"),
                "irodori_tts.reference_cache_slots": "1",
            }.items():
                abi.check(abi.options_set(options, key.encode(), value.encode()))
            backend = BackendConfig(cfg["backend"].encode(), 0, cfg.get("threads", 4))
            abi.check(
                abi.session_create(
                    self.model,
                    b"tts",
                    b"offline",
                    C.byref(backend),
                    options,
                    C.byref(self.session),
                )
            )
        except BaseException:
            self.close()
            raise
        finally:
            if options:
                abi.options_free(options)

    def close(self):
        if self.abi is not None:
            for kind in ("session", "model", "registry"):
                getattr(self.abi, kind + "_free")(getattr(self, kind))
                setattr(self, kind, C.c_void_p())
            self.abi = None

    def synthesize(self, request: SpeechRequest, profile: dict) -> bytes:
        if not self.busy.acquire(blocking=False):
            raise SpeechError("tts_busy")
        try:
            if request.model_revision != MODEL_REVISION or request.clone_prompt_digest:
                raise SpeechError("voice_profile_changed")
            group = self._group(
                request.reference_group_digest, profile["provenance_id"]
            )
            text, _ = normalize_speech_text(request.speech_text)
            caption = style_caption(request)
            self._load()
            with self._reference_wav(group) as reference:
                return self._generate(text, caption, reference, request)
        finally:
            self.busy.release()

    def _generate(self, text, caption, reference, speech):
        abi = self.abi
        request, result = abi.request_create(), C.c_void_p()
        if not request:
            raise SpeechError("tts_unavailable")
        try:
            abi.check(abi.request_set_text(request, text.encode(), b"ja"))
            with wave.open(reference, "rb") as source:
                samples = array.array("h", source.readframes(source.getnframes()))
                if sys.byteorder != "little":
                    samples.byteswap()
                pcm = (C.c_float * len(samples))(
                    *(value / 32768.0 for value in samples)
                )
                abi.check(
                    abi.request_set_voice_audio(
                        request, pcm, len(samples), source.getframerate(), 1
                    )
                )
            options = {
                "instruction": caption,
                "no_ref": "false",
                "duration_scale": str(1 / speech.pace),
                "min_duration_sec": "0.5",
                "max_duration_sec": "30",
                "num_inference_steps": str(self.num_steps),
                "text_guidance_scale": str(self.cfg["text"]),
                "caption_guidance_scale": str(self.cfg["caption"]),
                "speaker_guidance_scale": str(self.cfg["speaker"]),
                "seed": str(self.seed),
                "trim_tail": "true",
                "text_chunk_mode": "endline",
                "text_chunk_size": str(max(1, len(text))),
            }
            for key, value in options.items():
                abi.check(abi.request_set_option(request, key.encode(), value.encode()))
            abi.check(abi.session_run(self.session, request, C.byref(result)))
            samples, frames, rate, channels = (
                C.POINTER(C.c_float)(),
                C.c_size_t(),
                C.c_int(),
                C.c_int(),
            )
            abi.check(
                abi.result_audio(
                    result,
                    C.byref(samples),
                    C.byref(frames),
                    C.byref(rate),
                    C.byref(channels),
                )
            )
            if (
                not samples
                or channels.value != 1
                or not 8000 <= rate.value <= 48000
                or not 0 < frames.value <= rate.value * 60
            ):
                raise SpeechError("tts_invalid_audio")
            # Copy borrowed PCM before result_free；converter verifies finite values and size．
            return _waveform_to_wav(
                list(samples[: frames.value]), rate.value, speech.volume
            )
        finally:
            abi.result_free(result)
            abi.request_free(request)
