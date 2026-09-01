from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path

from packages.karte_core.conversation import KarteConversationRequest, KarteConversationService


NOW = datetime(2026, 9, 1, 10, 30, tzinfo=UTC)


def _data_root(tmp_path: Path) -> Path:
    root = tmp_path / "karte_data"
    (root / "content").mkdir(parents=True)
    return root


def _request(**overrides) -> KarteConversationRequest:
    values = {
        "conversation_id": "conversation-001",
        "occurred_at": NOW,
        "messages": [
            {"role": "user", "content": "EphyとKarteの連携方針を決定したい"},
            {"role": "assistant", "content": "project優先で保存し，曖昧な場合は相談する方針に決定します．"},
        ],
        "project": "ephy",
        "sensitivity": "internal",
    }
    values.update(overrides)
    return KarteConversationRequest.model_validate(values)


def _write_document(root: Path, *, body: str, doc_id: str = "doc:existing") -> Path:
    target = root / "content/projects/ephy/decision/2026-09/existing.md"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        "---\n"
        f"doc_id: {doc_id}\n"
        "title: EphyとKarteの連携方針を決定したい\n"
        "project: ephy\n"
        "kind: decision\n"
        "tags: ephy, integration\n"
        "---\n"
        f"{body}\n",
        encoding="utf-8",
    )
    return target


def test_conversation_plan_builds_publishable_create_with_full_summary(tmp_path: Path) -> None:
    service = KarteConversationService(_data_root(tmp_path))

    plan = service.plan(_request())

    assert plan.recommendation == "create"
    assert plan.publishable is True
    assert plan.needs_project is False
    assert plan.proposal.placement.project == "ephy"
    assert plan.proposal.placement.kind == "decision"
    assert plan.proposal.placement.year_month == "2026-09"
    assert plan.proposal.proposed_frontmatter["tags"] == ["ephy", "conversation"]
    assert "## 会話の要点" in plan.summary_markdown
    assert "曖昧な場合は相談" in plan.summary_markdown


def test_conversation_plan_requires_project_consultation(tmp_path: Path) -> None:
    service = KarteConversationService(_data_root(tmp_path))

    plan = service.plan(_request(project=None))

    assert plan.recommendation == "consult"
    assert plan.publishable is False
    assert plan.needs_project is True
    assert "project is required" in " ".join(plan.reasons)


def test_similar_document_requires_choice_then_builds_append_diff(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    existing = _write_document(
        root,
        body="# EphyとKarteの連携方針を決定したい\n\nproject優先で保存し，曖昧な場合は相談する方針に決定します．",
    )
    service = KarteConversationService(root)

    unresolved = service.plan(_request())
    assert unresolved.recommendation == "consult"
    assert unresolved.similar_documents[0].doc_id == "doc:existing"

    resolved = service.plan(_request(resolution="append", intended_doc_id="doc:existing"))
    assert resolved.recommendation == "append"
    assert resolved.publishable is True
    assert resolved.proposal.target_relative_path == existing.relative_to(root).as_posix()
    assert resolved.proposal.base_sha256 is not None
    assert resolved.proposal.proposed_body.startswith("## 2026-09-01 Ephy会話からの追記")
    assert "# EphyとKarte" not in resolved.proposal.proposed_body


def test_human_create_resolution_ignores_similar_document_and_publishes_idempotently(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    _write_document(
        root,
        body="# EphyとKarteの連携方針を決定したい\n\nproject優先で保存し，曖昧な場合は相談する方針に決定します．",
    )
    service = KarteConversationService(root)
    request = _request(resolution="create")

    first = service.publish(request)
    second = service.publish(request)

    assert first.state == "pending"
    assert second.state == "pending"
    assert first.candidate_id == second.candidate_id
    payload = json.loads(Path(first.path).read_text(encoding="utf-8"))
    assert payload["operation"] == "create"
    assert payload["target_relative_path"] is None
    assert service.status(first.candidate_id).state == "pending"


def test_candidate_identity_changes_when_user_changes_resolution(tmp_path: Path) -> None:
    service = KarteConversationService(_data_root(tmp_path))

    automatic = service.plan(_request())
    explicit = service.plan(_request(kind="note", resolution="create"))

    assert automatic.candidate_id != explicit.candidate_id


def test_candidate_identity_covers_tags_and_sensitivity(tmp_path: Path) -> None:
    service = KarteConversationService(_data_root(tmp_path))

    baseline = service.plan(_request())
    changed_tags = service.plan(_request(tags=["architecture"]))
    changed_sensitivity = service.plan(_request(sensitivity="confidential"))

    assert baseline.candidate_id != changed_tags.candidate_id
    assert baseline.candidate_id != changed_sensitivity.candidate_id


def test_explicit_append_can_confirm_a_low_similarity_doc_id(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    existing = _write_document(root, body="# 別件\n\nまったく異なる既存内容です．")
    service = KarteConversationService(root)

    resolved = service.plan(_request(resolution="append", intended_doc_id="doc:existing"))

    assert resolved.recommendation == "append"
    assert resolved.publishable is True
    assert resolved.proposal.target_relative_path == existing.relative_to(root).as_posix()
