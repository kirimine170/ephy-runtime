from __future__ import annotations

from pathlib import Path
from uuid import UUID

from packages.identity_core import IdentityService


ROOT = Path(__file__).resolve().parents[1]
IDENTITY_EXAMPLE = ROOT / "configs" / "examples" / "identity.example.yaml"


def test_identity_service_loads_example_manifest() -> None:
    manifest = IdentityService().load(IDENTITY_EXAMPLE)

    assert manifest.identity.lineage_name == "Ephy"
    assert manifest.identity.individual_name == "エフィ"
    assert manifest.identity.ordinal == 0
    assert manifest.identity.instance_id == UUID("019c0000-0000-7000-8000-000000000000")


def test_compare_immutable_reports_changed_instance_id() -> None:
    service = IdentityService()
    before = service.load(IDENTITY_EXAMPLE)
    payload = before.model_dump(mode="json")
    payload["identity"]["instance_id"] = "019c0000-0002-7000-8000-000000000000"
    after = service.validate(payload)

    violations = service.compare_immutable(before, after)

    assert [violation.field for violation in violations] == ["identity.instance_id"]
    assert violations[0].before == "019c0000-0000-7000-8000-000000000000"
    assert violations[0].after == "019c0000-0002-7000-8000-000000000000"


def test_compare_immutable_allows_status_change() -> None:
    service = IdentityService()
    before = service.load(IDENTITY_EXAMPLE)
    payload = before.model_dump(mode="json")
    payload["identity"]["status"] = "suspended"
    after = service.validate(payload)

    assert service.compare_immutable(before, after) == []
