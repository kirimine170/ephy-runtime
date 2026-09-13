from __future__ import annotations

from datetime import datetime, timezone
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

from packages.profile_core.resident import ProfileChange

Reference = Annotated[str, Field(min_length=1, max_length=200)]


class ResidentModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class ResidentSessionRequest(ResidentModel):
    session_id: Reference
    owner_selected: bool = False
    storage_consent: bool = False


class FeedbackTarget(ResidentModel):
    turn_id: str = Field(default="", max_length=200)
    run_id: str = Field(default="", max_length=200)
    generation_id: str = Field(default="", max_length=200)
    speech_unit_ids: list[Reference] = Field(default_factory=list, max_length=64)
    delivery_status: Literal["unknown", "unplayed", "partial", "played"] = "unknown"
    played_text: str | None = Field(default=None, max_length=2000)
    played_ms: int | None = Field(default=None, ge=0)
    memory_ids: list[Reference] = Field(default_factory=list, max_length=32)


class FeedbackRequest(ResidentModel):
    session_id: Reference
    dedupe_id: Reference
    expected_revision: int = Field(ge=0)
    text: str = Field(default="", max_length=2000)
    kind: Literal["positive", "negative", "neutral", "correction"] | None = None
    input_source: Literal["ui", "voice"] = "ui"
    speaker: Literal["owner", "unknown", "other"] = "unknown"
    target: FeedbackTarget | None = None
    mode: Literal["assistant", "observe", "companion"] = "assistant"
    participant_scope: list[Reference] = Field(default_factory=list, max_length=32)
    observed_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    model_id: str | None = Field(default=None, max_length=200)
    prompt_id: str | None = Field(default=None, max_length=200)
    voice_id: str | None = Field(default=None, max_length=200)
    configuration_id: str | None = Field(default=None, max_length=200)
    # Explicit trusted UI controls may choose finite fields and expiry．
    change: ProfileChange | None = None
    training_consent: bool = False

    @field_validator("observed_at")
    @classmethod
    def require_timezone(cls, value):
        if value.utcoffset() is None:
            raise ValueError("Feedback timestamps require a timezone")
        return value


class UndoRequest(ResidentModel):
    session_id: Reference
    dedupe_id: Reference
    expected_revision: int = Field(ge=0)


class RetractRequest(UndoRequest):
    delete: bool = False
