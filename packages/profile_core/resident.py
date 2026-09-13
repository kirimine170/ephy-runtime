"""Finite private profile overrides; feedback text is never executable policy．"""
from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage


class ResidentPolicy(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    response_length: Literal["default", "brief", "detailed"] = "default"
    call_name_frequency: Literal["default", "never", "low", "moderate", "high"] = "default"
    proactive: Literal["allowed", "suppressed"] = "allowed"
    restricted_memory_ids: list[str] = Field(default_factory=list, max_length=32)


class ProfileChange(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    field: Literal["response_length", "call_name_frequency", "proactive", "restricted_memory_ids"]
    value: str | list[str]
    scope: Literal["session", "owner", "expiring"] = "session"
    expires_at: datetime | None = None

    @model_validator(mode="after")
    def validate_change(self):
        ResidentPolicy.model_validate({self.field: self.value})
        if isinstance(self.value, list):
            if len(set(self.value)) != len(self.value) or any(
                not value or len(value) > 200 or any(c in value for c in "\r\n") for value in self.value
            ):
                raise ValueError("Invalid memory references")
        if (self.scope == "expiring") != (self.expires_at is not None):
            raise ValueError("Only expiring overrides require an expiry")
        if self.expires_at is not None and self.expires_at.utcoffset() is None:
            raise ValueError("Expiry requires a timezone")
        return self


MARKER = "Ephy Resident Preference Policy"


def apply_resident_policy(request: ChatCompletionRequest, policy: ResidentPolicy) -> ChatCompletionRequest:
    """A per-generation snapshot, subordinate to explicit current requests．"""
    lines = [MARKER, "以下は会話の既定設定です．現在の利用者の明示的な依頼を優先します．Identityと記憶の共有権限は変更しません．"]
    if policy.response_length == "brief":
        lines.append("通常の返答は結論を先に，原則1〜3文で短くします．詳しい説明や成果物を明示的に求められた場合は，必要な内容を省略せず説明します．")
    elif policy.response_length == "detailed":
        lines.append("通常の返答には理由と具体例を添えます．短い回答を明示的に求められた場合は短くします．")
    frequency = {
        "never": "名前での呼びかけは使いません．",
        "low": "名前での呼びかけは挨拶など必要な場面だけに絞ります．",
        "moderate": "名前での呼びかけは適度に使い，毎回は繰り返しません．",
        "high": "文脈に合う場面で名前を使って呼びかけます．呼称の敬称は既存Profileに従います．",
    }
    if policy.call_name_frequency in frequency:
        lines.append(frequency[policy.call_name_frequency])
    if policy.proactive == "suppressed":
        lines.append("この会話では自発的な話しかけを控えます．直接の質問や依頼には通常どおり答えます．")
    messages = [m for m in request.messages if not (m.role == "system" and MARKER in str(m.content))]
    end = 0
    while end < len(messages) and messages[end].role == "system":
        end += 1
    messages.insert(end, ChatMessage(role="system", content="\n".join(lines)))
    return request.model_copy(update={"messages": messages})
