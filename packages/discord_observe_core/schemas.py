"""Pinned additive 0.4 Discord observation wire models and local request．"""
from __future__ import annotations

import json
from typing import Annotated, Literal

from pydantic import (
    AwareDatetime,
    BaseModel,
    ConfigDict,
    Field,
    StrictStr,
    field_validator,
    model_validator,
)

from packages.web_search_core.worker_provider import _Artifact, _Usage, _Wire

MAX_OBSERVE_BYTES = 8 * 1024**2
SnowflakeString = Annotated[StrictStr, Field(pattern=r"^[1-9][0-9]{0,19}$")]


def canonical_json(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


class ContractModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class ObserveRequest(ContractModel):
    request_id: SnowflakeString
    guild_id: SnowflakeString
    channel_id: SnowflakeString
    operation: Literal["report", "snapshot", "activity", "discussion", "unresolved"]
    period_days: Literal[7, 30]
    query: str = Field(min_length=1, max_length=4000, strict=True)
    previous_answer: str | None = Field(default=None, max_length=2400, strict=True)

    @field_validator("request_id", "guild_id", "channel_id")
    @classmethod
    def valid_snowflake(cls, value):
        if int(value) > 2**64 - 1:
            raise ValueError("invalid Discord identity")
        return value


class ObservedGuild(ContractModel):
    id: SnowflakeString
    name: str = Field(max_length=200)
    description: str | None = Field(default=None, max_length=4000)
    approximate_member_count: int | None = Field(default=None, ge=0)


class ObservedChannel(ContractModel):
    id: SnowflakeString
    guild_id: SnowflakeString
    name: str = Field(max_length=200)
    type: int = Field(ge=0)
    parent_id: SnowflakeString | None = None


class ObservedRole(ContractModel):
    id: SnowflakeString
    name: str = Field(max_length=200)
    permissions: str = Field(pattern=r"^[0-9]{1,32}$")


class ObserveSnapshot(ContractModel):
    guild: ObservedGuild | None = None
    channels: list[ObservedChannel] = Field(default_factory=list, max_length=500)
    roles: list[ObservedRole] = Field(default_factory=list, max_length=500)


class ObservedMessage(ContractModel):
    guild_id: SnowflakeString
    channel_id: SnowflakeString
    message_id: SnowflakeString
    author_id: SnowflakeString
    author_bot: bool
    operator: bool
    created_at: AwareDatetime
    edited_at: AwareDatetime | None = None
    reply_to_message_id: SnowflakeString | None = None
    permalink: str = Field(max_length=200)
    content: str | None = Field(default=None, max_length=4000)
    content_status: Literal["available", "empty_or_unavailable", "not_requested"]
    content_truncated: bool = False

    @model_validator(mode="after")
    def grounded_content(self):
        if self.permalink != f"https://discord.com/channels/{self.guild_id}/{self.channel_id}/{self.message_id}":
            raise ValueError("message permalink identity mismatch")
        if (self.content_status == "available") != bool(self.content):
            raise ValueError("content availability mismatch")
        return self


class ObserveChannelActivity(ContractModel):
    channel_id: SnowflakeString
    message_count: int = Field(ge=0)
    unique_authors: int = Field(ge=0)


class ObserveDailyActivity(ContractModel):
    date: str = Field(pattern=r"^\d{4}-\d{2}-\d{2}$")
    message_count: int = Field(ge=0)


class ObserveActivity(ContractModel):
    period_days: Literal[7, 30]
    message_count: int = Field(ge=0)
    unique_authors: int = Field(ge=0)
    human_messages: int = Field(ge=0)
    bot_messages: int = Field(ge=0)
    operator_messages: int = Field(ge=0)
    nonoperator_messages: int = Field(ge=0)
    reply_messages: int = Field(ge=0)
    operator_reply_messages: int = Field(ge=0)
    nonoperator_reply_messages: int = Field(ge=0)
    operator_conversation_starts: int = Field(ge=0)
    nonoperator_conversation_starts: int = Field(ge=0)
    channels: list[ObserveChannelActivity] = Field(max_length=30)
    daily: list[ObserveDailyActivity] = Field(max_length=31)


class ObserveChannelCoverage(ContractModel):
    channel_id: SnowflakeString
    status: Literal["complete", "partial", "unavailable"]
    reason: str | None = Field(default=None, max_length=128)
    message_count: int = Field(ge=0)
    oldest_observed_at: AwareDatetime | None = None
    history_permission: Literal["granted", "denied", "unknown"]


class ObserveCoverage(ContractModel):
    guild_id: SnowflakeString
    channel_ids: list[SnowflakeString] = Field(min_length=1, max_length=30)
    window_start: AwareDatetime
    window_end: AwareDatetime
    observed_at: AwareDatetime
    threads_included: Literal[False] = False
    content_requested: bool
    status: Literal["complete", "partial", "unavailable"]
    channels: list[ObserveChannelCoverage] = Field(max_length=30)
    limitations: list[str] = Field(default_factory=list, max_length=32)


class ObservationFailure(ContractModel):
    stage: Literal["snapshot", "history", "worker"]
    code: str = Field(min_length=1, max_length=128)
    channel_id: SnowflakeString | None = None


class ObservePayload(ContractModel):
    mode: Literal["observe"] = "observe"
    snapshot: ObserveSnapshot
    activity: list[ObserveActivity] = Field(min_length=2, max_length=2)
    messages: list[ObservedMessage] = Field(max_length=2000)
    coverage: ObserveCoverage
    failures: list[ObservationFailure] = Field(default_factory=list, max_length=100)

    @model_validator(mode="after")
    def bounded_evidence(self):
        if len(canonical_json(self.model_dump(mode="json")).encode("utf-8")) > MAX_OBSERVE_BYTES:
            raise ValueError("observation payload too large")
        if {a.period_days for a in self.activity} != {7, 30}:
            raise ValueError("observation requires both activity windows")
        identities = [(m.channel_id, m.message_id) for m in self.messages]
        if len(set(identities)) != len(identities):
            raise ValueError("duplicate observed message")
        for message in self.messages:
            if (message.guild_id != self.coverage.guild_id or message.channel_id not in self.coverage.channel_ids
                    or not self.coverage.window_start <= message.created_at < self.coverage.window_end):
                raise ValueError("message outside observation scope or window")
        return self


class ObserveResult(_Wire):
    contract: Literal["0.4"]
    job_id: str = Field(pattern=r"^[A-Za-z0-9._-]{8,128}$")
    attempt_id: str = Field(pattern=r"^[A-Za-z0-9._-]{8,128}$")
    status: Literal["succeeded", "partial", "failed", "cancelled"]
    stop_reason: str | None = Field(max_length=128)
    usage: _Usage
    elapsed_seconds: float = Field(ge=0, le=86400, allow_inf_nan=False)
    payload: ObservePayload
    manifest: list[_Artifact] = Field(max_length=128)
