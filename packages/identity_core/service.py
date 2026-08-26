from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel, ConfigDict

from .schemas import IdentityManifest


IMMUTABLE_IDENTITY_FIELDS = (
    "schema_version",
    "identity.lineage_name",
    "identity.individual_name",
    "identity.ordinal",
    "identity.instance_id",
    "identity.parent_instance_id",
    "identity.created_at",
    "genesis.genesis_manifest_hash",
)


class IdentityViolation(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    field: str
    before: Any
    after: Any


class IdentityService:
    def load(self, path: Path) -> IdentityManifest:
        return self.validate(_read_yaml(path))

    def validate(self, manifest: IdentityManifest | dict[str, Any]) -> IdentityManifest:
        if isinstance(manifest, IdentityManifest):
            return manifest
        return IdentityManifest.model_validate(manifest)

    def compare_immutable(
        self,
        before: IdentityManifest,
        after: IdentityManifest,
    ) -> list[IdentityViolation]:
        before_payload = before.model_dump(mode="json")
        after_payload = after.model_dump(mode="json")
        violations: list[IdentityViolation] = []
        for field in IMMUTABLE_IDENTITY_FIELDS:
            before_value = _get_nested(before_payload, field)
            after_value = _get_nested(after_payload, field)
            if before_value != after_value:
                violations.append(
                    IdentityViolation(
                        field=field,
                        before=before_value,
                        after=after_value,
                    )
                )
        return violations


def _read_yaml(path: Path) -> dict[str, Any]:
    payload = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    if not isinstance(payload, dict):
        raise ValueError(f"Expected mapping in {path}")
    return payload


def _get_nested(payload: dict[str, Any], dotted_path: str) -> Any:
    value: Any = payload
    for part in dotted_path.split("."):
        value = value[part]
    return value
