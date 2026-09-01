from __future__ import annotations

import hashlib
import json
import os
import re
import time
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


CONTEXT_PROTOCOL_VERSION = "1.0"
_REQUEST_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_PROJECT_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,63}$")
_SHA256_RE = re.compile(r"^[a-f0-9]{64}$")
_MAX_RESPONSE_BYTES = 3 * 1024 * 1024


class ContextActor(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: Literal["ephy", "human", "tool"] = "ephy"
    id: str = Field(default="ephy", min_length=1, max_length=128)


class ContextScope(BaseModel):
    model_config = ConfigDict(extra="forbid")

    projects: list[str] = Field(default_factory=list, max_length=64)
    tags: list[str] = Field(default_factory=list, max_length=64)
    sensitivity_ceiling: Literal["public", "internal", "confidential", "restricted"] = "internal"

    @field_validator("projects")
    @classmethod
    def validate_projects(cls, values: list[str]) -> list[str]:
        if any(value != "*" and not _PROJECT_RE.fullmatch(value) for value in values):
            raise ValueError("context project scope contains an invalid slug")
        return list(dict.fromkeys(values))

    @field_validator("tags")
    @classmethod
    def validate_tags(cls, values: list[str]) -> list[str]:
        normalized = [value.strip() for value in values]
        if any(not value or len(value) > 128 for value in normalized):
            raise ValueError("context tag scope contains an invalid tag")
        return list(dict.fromkeys(normalized))


class ContextSearchQuery(BaseModel):
    model_config = ConfigDict(extra="forbid")

    text: str = Field(min_length=1, max_length=2048)
    top_k: int = Field(default=5, ge=1, le=20)


class ContextRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    protocol_version: Literal["1.0"] = CONTEXT_PROTOCOL_VERSION
    request_id: str
    operation: Literal["search", "read"]
    actor: ContextActor = Field(default_factory=ContextActor)
    scope: ContextScope = Field(default_factory=ContextScope)
    query: ContextSearchQuery | None
    doc_id: str | None
    created_at: datetime

    @field_validator("request_id")
    @classmethod
    def validate_request_id(cls, value: str) -> str:
        if not _REQUEST_ID_RE.fullmatch(value):
            raise ValueError("context request_id is invalid")
        return value

    @model_validator(mode="after")
    def validate_operation_fields(self) -> "ContextRequest":
        if self.operation == "search":
            if self.query is None or self.doc_id is not None:
                raise ValueError("context search requires query and null doc_id")
        elif self.query is not None or not (self.doc_id or "").strip() or len(self.doc_id or "") > 256:
            raise ValueError("context read requires only a bounded doc_id")
        return self


class ContextProvenance(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: str
    reference: str
    sha256: str


class ContextSearchResult(BaseModel):
    model_config = ConfigDict(extra="forbid")

    doc_id: str
    title: str
    project: str
    kind: str
    tags: list[str]
    sensitivity: Literal["public", "internal", "confidential", "restricted"]
    relative_path: str
    updated_at: datetime
    sha256: str
    snippet: str
    score: float = Field(ge=0)
    provenance: list[ContextProvenance]


class ContextDocument(BaseModel):
    model_config = ConfigDict(extra="forbid")

    doc_id: str
    title: str
    project: str
    kind: str
    tags: list[str]
    sensitivity: Literal["public", "internal", "confidential", "restricted"]
    relative_path: str
    updated_at: datetime
    sha256: str
    body: str = Field(max_length=2_097_152)
    provenance: list[ContextProvenance]


class ContextDiagnostic(BaseModel):
    model_config = ConfigDict(extra="forbid")

    code: str
    count: int = Field(ge=1)


class ContextProtocolErrorBody(BaseModel):
    model_config = ConfigDict(extra="forbid")

    code: str
    message: str


class ContextResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    protocol_version: Literal["1.0"] = CONTEXT_PROTOCOL_VERSION
    request_id: str
    request_sha256: str
    operation: Literal["search", "read", "invalid"]
    status: Literal["ok", "invalid", "denied", "not_found", "conflict"]
    results: list[ContextSearchResult] = Field(default_factory=list, max_length=20)
    document: ContextDocument | None = None
    diagnostics: list[ContextDiagnostic] = Field(default_factory=list)
    error: ContextProtocolErrorBody | None = None
    processed_at: datetime

    @field_validator("request_id")
    @classmethod
    def validate_request_id(cls, value: str) -> str:
        if not _REQUEST_ID_RE.fullmatch(value):
            raise ValueError("context response request_id is invalid")
        return value

    @field_validator("request_sha256")
    @classmethod
    def validate_request_sha256(cls, value: str) -> str:
        if not _SHA256_RE.fullmatch(value):
            raise ValueError("context response request_sha256 is invalid")
        return value

    @model_validator(mode="after")
    def validate_operation_payload(self) -> "ContextResponse":
        if self.operation == "search" and self.document is not None:
            raise ValueError("context search response cannot include a document")
        if self.operation == "read" and self.results:
            raise ValueError("context read response cannot include search results")
        if self.status == "ok" and self.operation == "read" and self.document is None:
            raise ValueError("successful context read requires a document")
        if self.status in {"denied", "not_found"} and (self.results or self.document is not None):
            raise ValueError("non-disclosing context response cannot include content")
        if self.status in {"invalid", "conflict"} and self.error is None:
            raise ValueError("failed context response requires an error")
        return self


class KarteContextSearchRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    query: str = Field(min_length=1, max_length=2048)
    projects: list[str] = Field(default_factory=list, max_length=64)
    tags: list[str] = Field(default_factory=list, max_length=64)
    sensitivity_ceiling: Literal["public", "internal", "confidential", "restricted"] = "internal"
    top_k: int = Field(default=5, ge=1, le=20)


class KarteContextReadRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    doc_id: str = Field(min_length=1, max_length=256)
    projects: list[str] = Field(default_factory=list, max_length=64)
    tags: list[str] = Field(default_factory=list, max_length=64)
    sensitivity_ceiling: Literal["public", "internal", "confidential", "restricted"] = "internal"


class KarteContextError(RuntimeError):
    """Base error for a safe Personal Context transport failure."""


class KarteContextTimeout(KarteContextError):
    pass


class KarteContextProtocolError(KarteContextError):
    pass


class KarteContextClient:
    """Typed client for Karte-owned Personal Context search and read."""

    def __init__(self, karte_data_dir: str | Path, *, timeout_seconds: float = 3.0, poll_interval: float = 0.05) -> None:
        self.data_root = Path(karte_data_dir).expanduser().resolve(strict=True)
        if not self.data_root.is_dir():
            raise ValueError("KARTE_DATA_DIR must be a directory")
        self.protocol_root = self._checked_path(Path(".mdsys/context/v1"))
        self.requests_dir = self._checked_path(Path(".mdsys/context/v1/requests"))
        self.responses_dir = self._checked_path(Path(".mdsys/context/v1/responses"))
        self.timeout_seconds = max(0.05, min(float(timeout_seconds), 30.0))
        self.poll_interval = max(0.01, min(float(poll_interval), 1.0))
        for directory in (self.requests_dir, self.responses_dir):
            directory.mkdir(parents=True, exist_ok=True, mode=0o700)
            self._assert_within_root(directory)

    @classmethod
    def from_environment(cls) -> "KarteContextClient | None":
        configured = os.environ.get("KARTE_DATA_DIR", "").strip()
        if not configured:
            return None
        try:
            timeout = float(os.environ.get("EPHY_KARTE_CONTEXT_TIMEOUT_SECONDS", "3"))
            return cls(configured, timeout_seconds=timeout)
        except (OSError, ValueError):
            return None

    def search(
        self,
        query: str,
        *,
        projects: list[str] | None = None,
        tags: list[str] | None = None,
        sensitivity_ceiling: Literal["public", "internal", "confidential", "restricted"] = "internal",
        top_k: int = 5,
    ) -> ContextResponse:
        request = ContextRequest(
            request_id=self._new_request_id("search"),
            operation="search",
            scope=ContextScope(projects=projects or [], tags=tags or [], sensitivity_ceiling=sensitivity_ceiling),
            query=ContextSearchQuery(text=query.strip(), top_k=top_k),
            doc_id=None,
            created_at=datetime.now(tz=UTC),
        )
        return self.exchange(request)

    def read(
        self,
        doc_id: str,
        *,
        projects: list[str] | None = None,
        tags: list[str] | None = None,
        sensitivity_ceiling: Literal["public", "internal", "confidential", "restricted"] = "internal",
    ) -> ContextResponse:
        request = ContextRequest(
            request_id=self._new_request_id("read"),
            operation="read",
            scope=ContextScope(projects=projects or [], tags=tags or [], sensitivity_ceiling=sensitivity_ceiling),
            query=None,
            doc_id=doc_id.strip(),
            created_at=datetime.now(tz=UTC),
        )
        return self.exchange(request)

    def exchange(self, request: ContextRequest) -> ContextResponse:
        payload = request.model_dump(mode="json")
        data = _serialize_json(payload)
        request_sha256 = hashlib.sha256(data).hexdigest()
        request_path = self.requests_dir / f"{request.request_id}.json"
        response_path = self.responses_dir / f"{request.request_id}.json"
        if response_path.exists():
            return self._read_validated_response(response_path, request, request_sha256)
        if request_path.exists():
            existing = self._read_regular_bytes(request_path)
            if existing != data:
                raise KarteContextProtocolError("context request_id already exists with different content")
        else:
            try:
                _atomic_write_bytes(request_path, data)
            except OSError as exc:
                raise KarteContextProtocolError("Karte context request could not be published") from exc

        deadline = time.monotonic() + self.timeout_seconds
        while time.monotonic() < deadline:
            if response_path.exists():
                return self._read_validated_response(response_path, request, request_sha256)
            time.sleep(self.poll_interval)
        raise KarteContextTimeout(f"Karte context request timed out after {self.timeout_seconds:g}s")

    def _read_validated_response(self, path: Path, request: ContextRequest, request_sha256: str) -> ContextResponse:
        try:
            response = ContextResponse.model_validate_json(self._read_regular_bytes(path))
        except (ValueError, OSError) as exc:
            raise KarteContextProtocolError("Karte context response is invalid") from exc
        if response.request_id != request.request_id or response.request_sha256 != request_sha256:
            raise KarteContextProtocolError("Karte context response does not match the request")
        if response.operation not in {request.operation, "invalid"}:
            raise KarteContextProtocolError("Karte context response operation is invalid")
        if response.status in {"invalid", "conflict"}:
            code = response.error.code if response.error is not None else response.status
            raise KarteContextProtocolError(f"Karte context request failed: {code}")
        return response

    def _read_regular_bytes(self, path: Path) -> bytes:
        try:
            if path.is_symlink() or not path.is_file():
                raise KarteContextProtocolError("context protocol JSON must be a regular file")
            self._assert_within_root(path)
            if path.stat().st_size > _MAX_RESPONSE_BYTES:
                raise KarteContextProtocolError("Karte context response exceeds the size limit")
            return path.read_bytes()
        except KarteContextProtocolError:
            raise
        except OSError as exc:
            raise KarteContextProtocolError("context protocol JSON could not be read") from exc

    def _checked_path(self, relative: Path) -> Path:
        candidate = self.data_root / relative
        self._assert_within_root(candidate)
        return candidate

    def _assert_within_root(self, candidate: Path) -> None:
        try:
            candidate.resolve(strict=False).relative_to(self.data_root)
        except ValueError as exc:
            raise ValueError("context protocol path escapes KARTE_DATA_DIR") from exc

    @staticmethod
    def _new_request_id(operation: str) -> str:
        return f"ephy-context-{operation}-{uuid.uuid4().hex}"


def _serialize_json(payload: dict) -> bytes:
    return (json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")


def _atomic_write_bytes(destination: Path, data: bytes) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temp_path = destination.parent / f".{destination.name}.{uuid.uuid4().hex}.tmp"
    try:
        with temp_path.open("xb") as file_obj:
            os.chmod(temp_path, 0o600)
            file_obj.write(data)
            file_obj.flush()
            os.fsync(file_obj.fileno())
        os.replace(temp_path, destination)
        directory_fd = os.open(destination.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass
