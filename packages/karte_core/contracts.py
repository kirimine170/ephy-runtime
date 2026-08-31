from __future__ import annotations

import re
from datetime import datetime
from pathlib import PurePosixPath
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


SCHEMA_VERSION = "1.0"
_CANDIDATE_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_SHA256_RE = re.compile(r"^[a-f0-9]{64}$")


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


class KarteChangeProposal(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: Literal["1.0"] = SCHEMA_VERSION
    candidate_id: str
    operation: Literal["create", "update"]
    target_doc_id: str | None
    target_relative_path: str
    base_sha256: str | None
    proposed_frontmatter: dict[str, Any]
    proposed_body: str = Field(max_length=1_048_576)
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
    def validate_target_relative_path(cls, value: str) -> str:
        return validate_content_relative_path(value)

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
            if self.target_doc_id is not None or self.base_sha256 is not None:
                raise ValueError("create requires null target_doc_id and base_sha256")
            return self

        if not self.target_doc_id:
            raise ValueError("update requires target_doc_id")
        if self.base_sha256 is None or not _SHA256_RE.fullmatch(self.base_sha256):
            raise ValueError("update requires a lowercase base_sha256")
        proposed_doc_id = self.proposed_frontmatter.get("doc_id")
        if proposed_doc_id is not None and proposed_doc_id != self.target_doc_id:
            raise ValueError("proposed doc_id must match target_doc_id")
        return self


class KarteReceipt(BaseModel):
    model_config = ConfigDict(extra="forbid")

    schema_version: Literal["1.0"] = SCHEMA_VERSION
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
