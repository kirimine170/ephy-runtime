"""Body-free errors and provider-independent speech wire contracts．"""

from __future__ import annotations

from copy import deepcopy
import math
import re
from typing import Any

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
    "tts_asset_invalid", "tts_stream_eof", "invalid_voice_config",
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


class SpeechStyle(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    affect: str = "neutral"
    intensity: float = 1.0
    pace: float = 1.0
    pitch_hint: float = 0.0
    volume: float = Field(default=1.0, ge=0.0, le=1.0)
    pause_style: str = "natural"
    interruptible: bool = True

    @model_validator(mode="after")
    def finite_style(self) -> SpeechStyle:
        if not all(math.isfinite(v) for v in (self.intensity, self.pace, self.pitch_hint, self.volume)):
            raise ValueError("invalid_speech_text")
        return self


class SpeechRequest(SpeechStyle):
    request_id: str = Field(pattern=ID_PATTERN)
    operation_id: str = Field(pattern=ID_PATTERN)
    session_id: str = Field(pattern=ID_PATTERN)
    turn_id: str = Field(pattern=ID_PATTERN)
    generation_revision: int = Field(ge=1, le=64)
    speech_unit_sequence: int = Field(ge=1, le=64)
    voice_profile_id: str = Field(pattern=ID_PATTERN)
    model_revision: str = Field(pattern=ID_PATTERN)
    clone_prompt_digest: str = ""
    reference_group_digest: str = ""
    speech_text: str = Field(min_length=1, max_length=180)

    @field_validator("clone_prompt_digest", "reference_group_digest")
    @classmethod
    def validate_optional_digest(cls, value: str) -> str:
        if value and not re.fullmatch(DIGEST_PATTERN, value):
            raise ValueError("invalid_speech_text")
        return value

    @field_validator("speech_text")
    @classmethod
    def validate_text(cls, value: str) -> str:
        if not value.strip() or len(value.encode("utf-8", errors="strict")) > 720:
            raise ValueError("invalid_speech_text")
        if any(ord(c) < 32 and c not in "\n\t" for c in value):
            raise ValueError("invalid_speech_text")
        return value


DEFAULT_STYLE: dict[str, Any] = SpeechStyle().model_dump()
QWEN_CAPABILITIES: dict[str, Any] = {
    "controls": {"volume": {"type": "number", "min": 0.0, "max": 1.0, "step": 0.05}},
    "streaming": True, "streaming_mode": "phrase", "interruptible": True,
}
IRODORI_CAPABILITIES: dict[str, Any] = {
    "controls": {
        "affect": {"type": "enum", "values": ["neutral", "warm", "cheerful", "cute", "sleepy", "concerned"]},
        "intensity": {"type": "number", "min": 0.75, "max": 1.25, "step": 0.25},
        "pace": {"type": "number", "min": 0.85, "max": 1.15, "step": 0.15},
        "volume": {"type": "number", "min": 0.0, "max": 1.0, "step": 0.05},
        "pause_style": {"type": "enum", "values": ["natural", "short", "deliberate"]},
    },
    "streaming": True, "streaming_mode": "phrase", "interruptible": True,
}


def _matches_capabilities(value: Any, expected: Any) -> bool:
    if isinstance(expected, dict):
        return (isinstance(value, dict) and value.keys() == expected.keys()
                and all(_matches_capabilities(value[key], item) for key, item in expected.items()))
    if isinstance(expected, float):
        # JSON numbers may be integral，but booleans must not impersonate numbers．
        return type(value) in (int, float) and value == expected
    return type(value) is type(expected) and value == expected


def _control_value(style: SpeechStyle, name: str) -> Any:
    return getattr(style, name)


def validate_style_for_capabilities(style: SpeechStyle, capabilities: dict[str, Any]) -> None:
    controls = capabilities["controls"]
    defaults = SpeechStyle()
    for name in ("affect", "intensity", "pace", "pitch_hint", "volume", "pause_style"):
        value = _control_value(style, name)
        control = controls.get(name)
        if control is None:
            if value != _control_value(defaults, name):
                raise SpeechError("unsupported_voice_control")
        elif control["type"] == "enum":
            if value not in control["values"]:
                raise SpeechError("unsupported_voice_control")
        elif type(value) not in (int, float) or not control["min"] <= value <= control["max"]:
            raise SpeechError("unsupported_voice_control")
        elif control.get("step"):
            steps = (value - control["min"]) / control["step"]
            if abs(steps - round(steps)) > 1e-7:
                raise SpeechError("unsupported_voice_control")
    if not style.interruptible or not capabilities["interruptible"]:
        raise SpeechError("unsupported_voice_control")


def public_profile(value: dict[str, Any], *, available: bool) -> dict[str, Any]:
    fields = ("voice_profile_id", "display_name", "provider", "model_revision", "language",
              "clone_prompt_digest", "reference_group_digest", "provenance_id")
    # Availability and error codes are service facts，never configuration claims．
    required = set(fields) - {"clone_prompt_digest", "reference_group_digest"}
    if (not isinstance(value, dict) or type(available) is not bool
            or not required <= set(value)
            or set(value) - set(fields) - {"default_style", "capabilities"}):
        raise SpeechError("invalid_speech_text")
    result = {key: value.get(key, "") for key in fields}
    for key in ("voice_profile_id", "provider", "model_revision", "language", "provenance_id"):
        if not isinstance(result[key], str) or not re.fullmatch(ID_PATTERN, result[key]):
            raise SpeechError("invalid_speech_text")
    provider_capabilities = {"qwen3-tts": QWEN_CAPABILITIES, "irodori-tts": IRODORI_CAPABILITIES}
    expected_capabilities = provider_capabilities.get(result["provider"])
    clone = result["clone_prompt_digest"]
    group = result["reference_group_digest"]
    if (expected_capabilities is None or not all(isinstance(item, str) for item in (clone, group))
            or (result["provider"] == "qwen3-tts" and (not re.fullmatch(DIGEST_PATTERN, clone) or group))
            or (result["provider"] == "irodori-tts" and (clone or not re.fullmatch(DIGEST_PATTERN, group)))):
        raise SpeechError("invalid_speech_text")
    if not isinstance(result["display_name"], str) or not 1 <= len(result["display_name"]) <= 80:
        raise SpeechError("invalid_speech_text")
    try:
        style = SpeechStyle.model_validate(value.get("default_style", {})).model_dump()
    except ValidationError:
        raise SpeechError("unsupported_voice_control") from None
    if "capabilities" in value and not _matches_capabilities(value["capabilities"], expected_capabilities):
        raise SpeechError("unsupported_voice_control")
    validate_style_for_capabilities(SpeechStyle.model_validate(style), expected_capabilities)
    result.update(default_style=style, capabilities=deepcopy(expected_capabilities), available=available)
    if not available:
        result["error_code"] = "tts_unavailable"
    return result
