"""Body-free errors and provider-independent speech wire contracts．"""

from __future__ import annotations

import math
import re
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, ValidationError, field_validator, model_validator

ID_PATTERN = r"^[A-Za-z0-9][A-Za-z0-9_./:@-]{0,127}$"
DIGEST_PATTERN = r"^[a-f0-9]{64}$"
MAX_REQUEST_BYTES = 16 * 1024
MAX_WAV_BYTES = 8 * 1024 * 1024
MAX_WORKER_FRAME_BYTES = 12 * 1024 * 1024
ERROR_CODES = frozenset({
    "tts_unavailable", "invalid_speech_text", "unsupported_voice_control",
    "voice_profile_changed", "tts_invalid_audio", "tts_incomplete", "tts_failed",
    "tts_timeout", "tts_canceled", "tts_busy", "tts_model_revision_mismatch",
    "tts_asset_invalid", "tts_stream_eof",
})


class SpeechError(Exception):
    def __init__(self, code: str):
        self.code = code if code in ERROR_CODES else "tts_failed"
        super().__init__(self.code)


def error_code(error: BaseException) -> str:
    return error.code if isinstance(error, SpeechError) else "tts_failed"


def parse_speech_request(value: Any) -> SpeechRequest:
    try:
        return SpeechRequest.model_validate_json(value) if isinstance(value, (bytes, bytearray, str)) else SpeechRequest.model_validate(value)
    except ValidationError as exc:
        # Inspect only our fixed validator marker，never expose Pydantic's input．
        for detail in exc.errors(include_input=False):
            marker = detail.get("ctx", {}).get("error")
            if isinstance(marker, ValueError) and marker.args == ("unsupported_voice_control",):
                raise SpeechError("unsupported_voice_control") from None
        raise SpeechError("invalid_speech_text") from None
    except Exception:
        raise SpeechError("invalid_speech_text") from None


class SpeechRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    request_id: str = Field(pattern=ID_PATTERN)
    voice_profile_id: str = Field(pattern=ID_PATTERN)
    model_revision: str = Field(pattern=ID_PATTERN)
    clone_prompt_digest: str = Field(pattern=DIGEST_PATTERN)
    speech_text: str = Field(min_length=1, max_length=180)
    affect: str = "neutral"
    intensity: float = 1.0
    pace: float = 1.0
    pitch_hint: float = 0.0
    volume: float = Field(default=1.0, ge=0.0, le=1.0)
    pause_style: str = "natural"
    interruptible: bool = True

    @field_validator("speech_text")
    @classmethod
    def validate_text(cls, value: str) -> str:
        if not value.strip() or len(value.encode("utf-8", errors="strict")) > 720:
            raise ValueError("invalid_speech_text")
        if any(ord(c) < 32 and c not in "\n\t" for c in value):
            raise ValueError("invalid_speech_text")
        return value

    @model_validator(mode="after")
    def supported_style(self) -> SpeechRequest:
        if not all(math.isfinite(v) for v in (self.intensity, self.pace, self.pitch_hint, self.volume)):
            raise ValueError("invalid_speech_text")
        if (self.affect, self.intensity, self.pace, self.pitch_hint, self.pause_style, self.interruptible) != (
            "neutral", 1.0, 1.0, 0.0, "natural", True
        ):
            raise ValueError("unsupported_voice_control")
        return self


DEFAULT_STYLE: dict[str, Any] = {
    "affect": "neutral", "intensity": 1.0, "pace": 1.0, "pitch_hint": 0.0,
    "volume": 1.0, "pause_style": "natural", "interruptible": True,
}
QWEN_CAPABILITIES: dict[str, Any] = {
    "controls": {"volume": {"type": "number", "min": 0.0, "max": 1.0, "step": 0.05}},
    "streaming": True, "streaming_mode": "phrase", "interruptible": True,
}


def public_profile(value: dict[str, Any], *, available: bool) -> dict[str, Any]:
    fields = ("voice_profile_id", "display_name", "provider", "model_revision", "language", "clone_prompt_digest", "provenance_id")
    if set(value) - set(fields) - {"default_style", "capabilities", "available", "error_code"}:
        raise SpeechError("invalid_speech_text")
    result = {key: value[key] for key in fields}
    for key in ("voice_profile_id", "provider", "model_revision", "language", "provenance_id"):
        if not isinstance(result[key], str) or not re.fullmatch(ID_PATTERN, result[key]):
            raise SpeechError("invalid_speech_text")
    if result["provider"] != "qwen3-tts" or not re.fullmatch(DIGEST_PATTERN, result["clone_prompt_digest"]):
        raise SpeechError("invalid_speech_text")
    if not isinstance(result["display_name"], str) or not 1 <= len(result["display_name"]) <= 80:
        raise SpeechError("invalid_speech_text")
    result.update(default_style=dict(DEFAULT_STYLE), capabilities=QWEN_CAPABILITIES, available=available)
    if not available:
        result["error_code"] = "tts_unavailable"
    return result
