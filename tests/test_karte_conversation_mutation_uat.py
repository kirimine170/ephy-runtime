from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import pytest

from scripts.karte_conversation_mutation_uat import (
    APPEND_DOC_ID,
    APPEND_RELATIVE_PATH,
    OCCUPANT_DOC_ID,
    _conversation_request,
    _write_collision_occupant,
    prepare_workspace,
)


def test_mutation_uat_prepares_scoped_personal_context(tmp_path: Path) -> None:
    prepare_workspace(tmp_path)

    canonical = (tmp_path / APPEND_RELATIVE_PATH).read_text(encoding="utf-8")
    policy = json.loads((tmp_path / ".mdsys/context/v1/policy.json").read_text(encoding="utf-8"))
    assert APPEND_DOC_ID in canonical
    assert policy["actors"]["ephy"] == {
        "sensitivity_ceiling": "internal",
        "projects": ["ephy"],
    }
    with pytest.raises(ValueError, match="must be empty"):
        prepare_workspace(tmp_path)


def test_mutation_uat_requests_cover_append_collision_and_reject() -> None:
    append_request = _conversation_request(
        conversation_id="conversation-uat-append",
        resolution="append",
        kind="decision",
        intended_doc_id=APPEND_DOC_ID,
    )
    collision_request = _conversation_request(
        conversation_id="conversation-uat-collision", resolution="create", kind="decision"
    )
    reject_request = _conversation_request(
        conversation_id="conversation-uat-reject", resolution="create", kind="note"
    )

    assert append_request.intended_doc_id == APPEND_DOC_ID
    assert append_request.resolution == "append"
    assert collision_request.resolution == "create"
    assert reject_request.messages[-1].role == "assistant"


def test_collision_fixture_occupies_only_the_preferred_path(tmp_path: Path) -> None:
    plan = SimpleNamespace(
        proposal=SimpleNamespace(
            placement=SimpleNamespace(
                project="ephy",
                kind="decision",
                year_month="2026-09",
                preferred_filename="same-name.md",
            )
        )
    )

    occupant = _write_collision_occupant(tmp_path, plan)

    assert occupant.relative_to(tmp_path).as_posix() == "content/projects/ephy/decision/2026-09/same-name.md"
    assert OCCUPANT_DOC_ID in occupant.read_text(encoding="utf-8")
