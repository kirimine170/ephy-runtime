from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import pytest

from scripts import karte_conversation_mutation_uat as mutation_uat

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


def test_review_bridge_returns_the_revision_it_checked_out(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    repository = tmp_path / "karte"
    repository.mkdir()
    (repository / "go.mod").write_text("module test\n", encoding="utf-8")
    (repository / "frontend/dist").mkdir(parents=True)
    data_root = tmp_path / "data"
    data_root.mkdir()
    revision_calls: list[Path] = []
    commands: list[list[str]] = []

    def fake_revision(path: Path) -> str:
        revision_calls.append(path)
        return "revision-used-by-bridge"

    def fake_run(argv: list[str], **_kwargs) -> SimpleNamespace:
        commands.append(argv)
        if argv[:2] == ["git", "clone"]:
            Path(argv[-1]).mkdir(parents=True)
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr(mutation_uat, "_git_revision", fake_revision)
    monkeypatch.setattr(mutation_uat.subprocess, "run", fake_run)

    revision = mutation_uat._run_karte_review_bridge(repository, data_root)

    assert revision == "revision-used-by-bridge"
    assert revision_calls == [repository.resolve()]
    assert any(command[:3] == ["git", "checkout", "--quiet"] and command[-1] == revision for command in commands)
