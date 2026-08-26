from __future__ import annotations

from pathlib import Path
from typing import Any, Literal

import yaml

from .schemas import ConversationPolicy, EphyProfile


SessionMode = Literal["default", "voice", "writing", "tech"]


class ProfileService:
    def load(self, path: Path) -> EphyProfile:
        return self.validate(_read_yaml(path))

    def validate(self, profile: EphyProfile | dict[str, Any]) -> EphyProfile:
        if isinstance(profile, EphyProfile):
            return profile
        return EphyProfile.model_validate(profile)

    def resolve_conversation_policy(
        self,
        profile: EphyProfile,
        session_mode: SessionMode = "default",
    ) -> ConversationPolicy:
        return ConversationPolicy(
            session_mode=session_mode,
            language=profile.language.default,
            first_person=profile.voice.first_person,
            speech_register=profile.voice.speech_register,
            use_known_name=profile.addressing.use_known_name,
            default_suffix=profile.addressing.default_suffix,
            call_name_frequency=profile.addressing.call_name_frequency,
            concise_by_default=profile.style.concise_by_default,
            friendly=profile.style.friendly,
            respectful=profile.style.respectful,
            direct=profile.style.direct,
            prefer_concrete_confirmation=profile.clarification.prefer_concrete_confirmation,
        )


def _read_yaml(path: Path) -> dict[str, Any]:
    payload = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    if not isinstance(payload, dict):
        raise ValueError(f"Expected mapping in {path}")
    return payload
