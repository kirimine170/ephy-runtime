from __future__ import annotations

from datetime import UTC, datetime
import json
from pathlib import Path

import pytest

from packages.karte_core.context import ContextDocument, ContextResponse
from scripts.karte_context_permission_retry_uat import (
    DOC_ID,
    RELATIVE_PATH,
    SYNTHETIC_BODY,
    build_report,
    prepare_workspace,
    validate_denied_response,
    validate_retry_response,
    write_ephy_policy,
)


NOW = datetime(2026, 9, 2, tzinfo=UTC)


def _response(*, status: str, document: ContextDocument | None = None) -> ContextResponse:
    return ContextResponse(
        request_id=f"uat-{status}",
        request_sha256="a" * 64,
        operation="read",
        status=status,
        document=document,
        processed_at=NOW,
    )


def _document() -> ContextDocument:
    return ContextDocument(
        doc_id=DOC_ID,
        title="Permission retry fixture",
        project="ephy",
        kind="note",
        tags=["synthetic-uat"],
        sensitivity="restricted",
        relative_path=RELATIVE_PATH.as_posix(),
        updated_at=NOW,
        sha256="b" * 64,
        body=SYNTHETIC_BODY,
        provenance=[],
    )


def test_workspace_starts_denied_and_policy_update_is_atomic(tmp_path: Path) -> None:
    prepare_workspace(tmp_path)

    policy_path = tmp_path / ".mdsys/context/v1/policy.json"
    policy = json.loads(policy_path.read_text(encoding="utf-8"))
    assert policy["actors"]["ephy"]["sensitivity_ceiling"] == "internal"
    assert SYNTHETIC_BODY in (tmp_path / RELATIVE_PATH).read_text(encoding="utf-8")

    write_ephy_policy(tmp_path, sensitivity_ceiling="restricted")
    policy = json.loads(policy_path.read_text(encoding="utf-8"))
    assert policy["actors"]["ephy"]["sensitivity_ceiling"] == "restricted"
    assert list(policy_path.parent.glob("*.tmp")) == []


def test_response_checks_prevent_denied_disclosure_and_wrong_retry_identity() -> None:
    validate_denied_response(_response(status="denied"))
    validate_retry_response(_response(status="ok", document=_document()))

    with pytest.raises(RuntimeError, match="disclosed"):
        validate_denied_response(_response(status="ok", document=_document()))
    wrong = _document().model_copy(update={"doc_id": "doc:other"})
    with pytest.raises(RuntimeError, match="different document identity"):
        validate_retry_response(_response(status="ok", document=wrong))


def test_report_is_metadata_only(tmp_path: Path) -> None:
    report = build_report(
        data_root=tmp_path,
        executable=tmp_path / "Karte.app/Contents/MacOS/karte",
        process_id=123,
        denied=_response(status="denied"),
        retried=_response(status="ok", document=_document()),
        started_at=NOW,
    )

    serialized = json.dumps(report, ensure_ascii=False)
    assert report["steps"] == [
        {"name": "default_ephy_policy", "status": "denied", "content_disclosed": False},
        {"name": "restricted_policy_retry", "status": "ok", "content_disclosed": True},
    ]
    assert report["document"]["sha256"] == "b" * 64
    assert SYNTHETIC_BODY not in serialized
