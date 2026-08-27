from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


ScenarioSourceKind = Literal["synthetic", "manual", "anonymized_export"]
DatasetSplit = Literal["train", "validation", "holdout"]
DeletionStatus = Literal["active", "deleted"]
PairStatus = Literal["pending", "reviewed", "duplicate_generation"]
CanonicalSelection = Literal["a", "b", "tie", "skip"]
DisplaySelection = Literal["left", "right", "tie", "skip"]
ReviewerType = Literal["human", "llm"]
ModelRole = Literal["fast", "work", "code"]
ReasonTag = Literal[
    "direct",
    "natural_japanese",
    "friendly_polite",
    "good_distance",
    "contextual",
    "concise",
    "good_question",
    "unnecessary_question",
    "too_formal",
    "too_casual",
    "too_long",
    "generic_preamble",
    "excessive_agreement",
    "persona_break",
    "factual_problem",
    "other",
]


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class PreferenceMessage(StrictModel):
    role: Literal["user", "assistant"]
    content: str = Field(min_length=1, max_length=32_000)


class Consent(StrictModel):
    storage: bool
    training: bool


class ConversationScenario(StrictModel):
    scenario_id: str = Field(pattern=r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
    category: str = Field(min_length=1, max_length=120)
    messages: list[PreferenceMessage] = Field(min_length=1, max_length=16)
    source_kind: ScenarioSourceKind
    source_ref: str | None = Field(default=None, max_length=500)
    provenance: str = Field(min_length=1, max_length=1_000)
    transform_history: list[str] = Field(default_factory=list, max_length=50)
    consent: Consent
    deletion_status: DeletionStatus = "active"
    split: DatasetSplit

    @model_validator(mode="after")
    def require_latest_user_turn(self):
        if self.messages[-1].role != "user":
            raise ValueError("A preference scenario must end with a user message")
        return self


class PreferenceDataset(StrictModel):
    schema_version: Literal[1] = 1
    description: str | None = Field(default=None, max_length=1_000)
    scenarios: list[ConversationScenario] = Field(min_length=1, max_length=10_000)


class GenerationParameters(StrictModel):
    temperature: float = Field(default=0.8, gt=0, le=2)
    top_p: float = Field(default=0.95, gt=0, le=1)
    seed: int | None = None
    max_tokens: int = Field(default=512, ge=1, le=8_192)


class CandidateSpec(StrictModel):
    candidate_id: str
    model_role: ModelRole
    model_registration_id: str
    model_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    adapter_registration_id: str | None = None
    adapter_sha256: str | None = Field(default=None, pattern=r"^[0-9a-f]{64}$")
    prompt_revision: str = Field(pattern=r"^[0-9a-f]{64}$")
    generation_parameters: GenerationParameters
    generated_at: datetime

    @model_validator(mode="after")
    def require_complete_adapter_identity(self):
        if (self.adapter_registration_id is None) != (self.adapter_sha256 is None):
            raise ValueError("Adapter registration ID and SHA-256 must be recorded together")
        return self


class PreferencePair(StrictModel):
    pair_id: str
    session_id: str
    scenario_id: str
    candidate_a: CandidateSpec
    candidate_b: CandidateSpec
    response_a: str
    response_b: str
    response_a_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    response_b_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    display_order: Literal["ab", "ba"]
    status: PairStatus
    created_at: datetime


class PreferenceVote(StrictModel):
    vote_id: str
    pair_id: str
    selection: CanonicalSelection
    reason_tags: list[ReasonTag] = Field(default_factory=list)
    note: str | None = Field(default=None, max_length=2_000)
    reviewer_type: ReviewerType = "human"
    approved_for_sft: bool = False
    created_at: datetime
    supersedes_vote_id: str | None = None


class PreferenceSession(StrictModel):
    session_id: str
    dataset_path: str
    model_role: ModelRole = "fast"
    target_pairs: int = Field(default=20, ge=1, le=100)
    prefetch: int = Field(default=4, ge=1, le=10)
    generation_parameters: GenerationParameters = Field(default_factory=GenerationParameters)
    status: Literal["active", "complete"] = "active"
    created_at: datetime


class CreatePreferenceSessionRequest(StrictModel):
    dataset_path: str = Field(min_length=1, max_length=4_096)
    model_role: ModelRole = "fast"
    pair_count: int = Field(default=20, ge=1, le=100)
    prefetch: int = Field(default=4, ge=1, le=10)
    generation_parameters: GenerationParameters = Field(default_factory=GenerationParameters)


class GeneratePreferencePairsRequest(StrictModel):
    limit: int | None = Field(default=None, ge=1, le=10)


class SubmitPreferenceVoteRequest(StrictModel):
    selection: DisplaySelection
    reason_tags: list[ReasonTag] = Field(default_factory=list)
    note: str | None = Field(default=None, max_length=2_000)
    approved_for_sft: bool = False
    supersedes_vote_id: str | None = None


class ExportPreferenceRequest(StrictModel):
    format: Literal["dpo", "sft"]
    output: str = Field(min_length=1, max_length=4_096)


class BlindPreferencePair(StrictModel):
    pair_id: str
    messages: list[PreferenceMessage]
    response_left: str
    response_right: str
    category: str
    progress: dict[str, int]
