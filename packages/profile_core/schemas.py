from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


SemanticVersion = Annotated[
    str,
    Field(
        pattern=r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$"
    ),
]


class StrictFrozenModel(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True, populate_by_name=True)


class LanguageConfig(StrictFrozenModel):
    default: str = Field(pattern=r"^[a-z]{2,3}(?:-[A-Z]{2})?$")


class VoiceConfig(StrictFrozenModel):
    first_person: str = Field(min_length=1)
    speech_register: str = Field(min_length=1, alias="register")


class AddressingConfig(StrictFrozenModel):
    default_suffix: str = Field(min_length=1)
    use_known_name: bool = True
    call_name_frequency: Literal["never", "low", "moderate", "high"] = "moderate"


class StyleConfig(StrictFrozenModel):
    concise_by_default: bool = True
    friendly: bool = True
    respectful: bool = True
    direct: bool = True
    excessive_formality: bool = False
    excessive_familiarity: bool = False
    excessive_headings: bool = False
    excessive_bullets: bool = False


class ClarificationConfig(StrictFrozenModel):
    prefer_concrete_confirmation: bool = True
    example: tuple[Annotated[str, Field(min_length=1)], ...] = ()

    @field_validator("example")
    @classmethod
    def require_unique_examples(cls, value: tuple[str, ...]) -> tuple[str, ...]:
        if len(value) != len(set(value)):
            raise ValueError("clarification examples must be unique")
        return value


class EphyProfile(StrictFrozenModel):
    schema_version: SemanticVersion
    profile_version: SemanticVersion
    language: LanguageConfig
    voice: VoiceConfig
    addressing: AddressingConfig
    style: StyleConfig = Field(default_factory=StyleConfig)
    clarification: ClarificationConfig = Field(default_factory=ClarificationConfig)


class ConversationPolicy(StrictFrozenModel):
    session_mode: Literal["default", "voice", "writing", "tech"]
    language: str
    first_person: str
    speech_register: str = Field(alias="register")
    use_known_name: bool
    default_suffix: str
    call_name_frequency: Literal["never", "low", "moderate", "high"]
    concise_by_default: bool
    friendly: bool
    respectful: bool
    direct: bool
    prefer_concrete_confirmation: bool
