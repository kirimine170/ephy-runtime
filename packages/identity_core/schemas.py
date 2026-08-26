from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, field_validator


SemanticVersion = Annotated[
    str,
    Field(
        pattern=r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$"
    ),
]
Sha256Digest = Annotated[str, Field(pattern=r"^sha256:[0-9a-f]{64}$")]


class StrictFrozenModel(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


class IdentityStatus(StrEnum):
    PROVISIONING = "provisioning"
    ACTIVE = "active"
    SUSPENDED = "suspended"
    ARCHIVED = "archived"
    REVOKED = "revoked"


class IdentityRecord(StrictFrozenModel):
    lineage_name: str = Field(min_length=1)
    individual_name: str = Field(min_length=1)
    ordinal: int = Field(ge=0)
    instance_id: UUID
    parent_instance_id: UUID | None = None
    created_at: datetime
    status: IdentityStatus

    @field_validator("created_at")
    @classmethod
    def require_timezone(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must include a timezone")
        return value


class GenesisRecord(StrictFrozenModel):
    genesis_manifest_hash: Sha256Digest
    runtime_version: SemanticVersion | None = None
    profile_version: SemanticVersion | None = None
    created_by: str | None = Field(default=None, min_length=1)


class OwnershipRecord(StrictFrozenModel):
    owner_reference: str = Field(pattern=r"^[A-Za-z][A-Za-z0-9+.-]*://.+$")
    owner_data_embedded: bool = False

    @field_validator("owner_data_embedded")
    @classmethod
    def reject_embedded_owner_data(cls, value: bool) -> bool:
        if value:
            raise ValueError("owner data must not be embedded in a public manifest")
        return value


class VerificationRecord(StrictFrozenModel):
    signature_algorithm: str | None = None
    public_key_id: str | None = None
    signature: str | None = None


class IdentityManifest(StrictFrozenModel):
    schema_version: SemanticVersion
    identity: IdentityRecord
    genesis: GenesisRecord
    ownership: OwnershipRecord | None = None
    verification: VerificationRecord | None = None
