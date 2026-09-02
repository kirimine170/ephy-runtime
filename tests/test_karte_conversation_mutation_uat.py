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


def test_artifact_revision_requires_signed_embedded_provenance(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    executable = tmp_path / "Karte.app/Contents/MacOS/karte"
    executable.parent.mkdir(parents=True)
    executable.write_bytes(b"synthetic executable")
    executable.chmod(0o700)
    provenance = executable.parent.parent / "Resources/karte-build-provenance.json"
    provenance.parent.mkdir(parents=True)
    provenance.write_text(
        json.dumps(
            {
                "schema_version": "1.0",
                "source_revision": "a" * 40,
                "target": "darwin-arm64",
            }
        ),
        encoding="utf-8",
    )
    commands: list[list[str]] = []

    def fake_run(argv: list[str], **_kwargs) -> SimpleNamespace:
        commands.append(argv)
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr(mutation_uat.sys, "platform", "darwin")
    monkeypatch.setattr(mutation_uat.subprocess, "run", fake_run)

    revision = mutation_uat._verify_karte_artifact_revision(executable)

    assert revision == "a" * 40
    assert commands == [["codesign", "--verify", "--deep", "--verbose=2", str(executable.parents[2])]]


def test_reject_canonical_check_requires_an_unchanged_tree(tmp_path: Path) -> None:
    candidate_id = "ephy-chat-1234567890abcdef1234"
    evidence = tmp_path / ".mdsys/ephy/mutation-uat-canonical-check.json"
    evidence.parent.mkdir(parents=True)
    evidence.write_text(
        json.dumps(
            {
                "schema_version": "1.0",
                "candidate_id": candidate_id,
                "before_count": 3,
                "after_count": 3,
                "before_sha256": "b" * 64,
                "after_sha256": "b" * 64,
                "tree_unchanged": True,
            }
        ),
        encoding="utf-8",
    )

    check = mutation_uat._verify_reject_canonical_check(tmp_path, candidate_id)

    assert check["tree_unchanged"] is True
    assert check["before_sha256"] == "b" * 64

    payload = json.loads(evidence.read_text(encoding="utf-8"))
    payload["after_sha256"] = "c" * 64
    evidence.write_text(json.dumps(payload), encoding="utf-8")
    with pytest.raises(RuntimeError, match="changed canonical Markdown"):
        mutation_uat._verify_reject_canonical_check(tmp_path, candidate_id)


def test_review_bridge_checks_out_the_artifact_revision(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    repository = tmp_path / "karte"
    repository.mkdir()
    (repository / "go.mod").write_text("module test\n", encoding="utf-8")
    (repository / "frontend/dist").mkdir(parents=True)
    data_root = tmp_path / "data"
    data_root.mkdir()
    commands: list[list[str]] = []

    revision = "d" * 40

    def fake_run(argv: list[str], **_kwargs) -> SimpleNamespace:
        commands.append(argv)
        if argv[:2] == ["git", "clone"]:
            Path(argv[-1]).mkdir(parents=True)
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr(mutation_uat.subprocess, "run", fake_run)

    checked_out_revision = mutation_uat._run_karte_review_bridge(repository, data_root, revision)

    assert checked_out_revision == revision
    assert any(command[:3] == ["git", "checkout", "--quiet"] and command[-1] == revision for command in commands)
