from __future__ import annotations

import re
from datetime import datetime
from pathlib import PurePosixPath
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


SCHEMA_VERSION = "1.1"
_CANDIDATE_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_SHA256_RE = re.compile(r"^[a-f0-9]{64}$")
_PROJECT_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,63}$")
_FILENAME_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,127}\.md$")
_YEAR_MONTH_RE = re.compile(r"^[0-9]{4}-(0[1-9]|1[0-2])$")

KarteDocumentKind = Literal[
    "note",
    "meeting",
    "decision",
    "plan",
    "task",
    "research",
    "reference",
    "report",
    "person",
    "organization",
    "journal",
]


def validate_content_relative_path(value: str) -> str:
    if not value or "\\" in value:
        raise ValueError("target_relative_path must use forward slashes")
    path = PurePosixPath(value)
    if path.is_absolute() or not path.parts or path.parts[0] != "content":
        raise ValueError("target_relative_path must be below content/")
    if any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError("target_relative_path contains traversal")
    if path.suffix.lower() != ".md":
        raise ValueError("target_relative_path must name a Markdown file")
    return str(path)


class SourceRef(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: str = Field(min_length=1, max_length=64)
    reference: str = Field(min_length=1, max_length=2048)
    sha256: str | None = None

    @field_validator("sha256")
    @classmethod
    def validate_sha256(cls, value: str | None) -> str | None:
        if value is not None and not _SHA256_RE.fullmatch(value):
            raise ValueError("sha256 must be a lowercase SHA-256 digest")
        return value


class PlacementCandidate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    project: str
    kind: KarteDocumentKind
    confidence: float = Field(ge=0, le=1)
    reason: str = Field(min_length=1, max_length=512)

    @field_validator("project")
    @classmethod
    def validate_project(cls, value: str) -> str:
        if not _PROJECT_RE.fullmatch(value):
            raise ValueError("project must be a lowercase path-safe slug")
        return value


class PlacementHint(BaseModel):
    model_config = ConfigDict(extra="forbid")

    project: str
    kind: KarteDocumentKind
    year_month: str
    confidence: float = Field(ge=0, le=1)
    preferred_filename: str
    candidates: list[PlacementCandidate] = Field(min_length=1, max_length=3)
    consultation_required: bool
    consultation_question: str | None = Field(default=None, max_length=1024)

    @field_validator("project")
    @classmethod
    def validate_project(cls, value: str) -> str:
        if not _PROJECT_RE.fullmatch(value):
            raise ValueError("project must be a lowercase path-safe slug")
        return value

    @field_validator("year_month")
    @classmethod
    def validate_year_month(cls, value: str) -> str:
        if not _YEAR_MONTH_RE.fullmatch(value):
            raise ValueError("year_month must use YYYY-MM")
        return value

    @field_validator("preferred_filename")
    @classmethod
    def validate_preferred_filename(cls, value: str) -> str:
        if not _FILENAME_RE.fullmatch(value):
            raise ValueError("preferred_filename must be a lowercase path-safe Markdown filename")
        return value

    @model_validator(mode="after")
    def validate_consultation(self) -> "PlacementHint":
        if self.consultation_required and not (self.consultation_question or "").strip():
            raise ValueError("consultation_required placement needs a consultation_question")
        if not self.consultation_required and self.consultation_question is not None:
            raise ValueError("resolved placement cannot retain a consultation_question")
        if not any(candidate.project == self.project and candidate.kind == self.kind for candidate in self.candidates):
            raise ValueError("placement candidates must include the selected project and kind")
        return self


class KarteChangeProposal(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: Literal["1.1"] = SCHEMA_VERSION
    candidate_id: str
    operation: Literal["create", "append"]
    target_doc_id: str | None
    target_relative_path: str | None
    base_sha256: str | None
    append_position: Literal["document_end"] | None
    proposed_frontmatter: dict[str, Any]
    proposed_body: str = Field(max_length=1_048_576)
    placement: PlacementHint
    source_refs: list[SourceRef] = Field(min_length=1, max_length=64)
    sensitivity: Literal["public", "internal", "confidential", "restricted"]
    created_at: datetime

    @field_validator("candidate_id")
    @classmethod
    def validate_candidate_id(cls, value: str) -> str:
        if not _CANDIDATE_ID_RE.fullmatch(value):
            raise ValueError("candidate_id contains unsupported characters")
        return value

    @field_validator("target_relative_path")
    @classmethod
    def validate_target_relative_path(cls, value: str | None) -> str | None:
        return validate_content_relative_path(value) if value is not None else None

    @field_validator("proposed_frontmatter")
    @classmethod
    def validate_frontmatter(cls, value: dict[str, Any]) -> dict[str, Any]:
        if len(value) > 64:
            raise ValueError("proposed_frontmatter has too many fields")
        if any(not isinstance(key, str) or not key.strip() for key in value):
            raise ValueError("proposed_frontmatter keys must be non-empty strings")
        return value

    @model_validator(mode="after")
    def validate_operation_fields(self) -> "KarteChangeProposal":
        if self.operation == "create":
            if self.target_doc_id is not None or self.target_relative_path is not None or self.base_sha256 is not None:
                raise ValueError("create lets Karte choose the path and requires null target identity")
            if self.append_position is not None:
                raise ValueError("create cannot set append_position")
            return self

        if not self.target_doc_id:
            raise ValueError("append requires target_doc_id")
        if self.target_relative_path is None:
            raise ValueError("append requires target_relative_path")
        if self.base_sha256 is None or not _SHA256_RE.fullmatch(self.base_sha256):
            raise ValueError("append requires a lowercase base_sha256")
        if self.append_position != "document_end":
            raise ValueError("append currently supports document_end only")
        if not self.proposed_body.strip() and not self.proposed_frontmatter:
            raise ValueError("append must propose a body fragment or frontmatter patch")
        proposed_doc_id = self.proposed_frontmatter.get("doc_id")
        if proposed_doc_id is not None and proposed_doc_id != self.target_doc_id:
            raise ValueError("proposed doc_id must match target_doc_id")
        return self

    def require_publishable(self) -> None:
        if self.placement.consultation_required:
            raise ValueError("Ephy must resolve placement consultation before publishing the proposal")


class KarteReceipt(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: Literal["1.1"] = SCHEMA_VERSION
    candidate_id: str
    result: Literal["accepted", "rejected", "conflict", "invalid"]
    doc_id: str | None
    relative_path: str | None
    resulting_sha256: str | None
    processed_at: datetime
    error_code: str | None
    message: str | None

    @field_validator("candidate_id")
    @classmethod
    def validate_candidate_id(cls, value: str) -> str:
        if not _CANDIDATE_ID_RE.fullmatch(value):
            raise ValueError("candidate_id contains unsupported characters")
        return value

    @field_validator("relative_path")
    @classmethod
    def validate_relative_path(cls, value: str | None) -> str | None:
        return validate_content_relative_path(value) if value is not None else None

    @field_validator("resulting_sha256")
    @classmethod
    def validate_resulting_sha256(cls, value: str | None) -> str | None:
        if value is not None and not _SHA256_RE.fullmatch(value):
            raise ValueError("resulting_sha256 must be a lowercase SHA-256 digest")
        return value

    @model_validator(mode="after")
    def validate_accepted_result(self) -> "KarteReceipt":
        if self.result == "accepted":
            if not self.doc_id or not self.relative_path or not self.resulting_sha256:
                raise ValueError("accepted receipt requires document identity and resulting hash")
            if self.error_code is not None:
                raise ValueError("accepted receipt cannot contain error_code")
        return self
