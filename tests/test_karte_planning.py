from __future__ import annotations

from datetime import UTC, datetime

import pytest

from packages.karte_core.contracts import PlacementCandidate, SourceRef
from packages.karte_core.planning import ExistingDocumentMatch, KarteProposalPlanner


def _candidate(project: str = "ephy", kind: str = "decision", confidence: float = 0.9) -> PlacementCandidate:
    return PlacementCandidate(project=project, kind=kind, confidence=confidence, reason="Synthetic classification.")


def _plan(**overrides):
    values = {
        "candidate_id": "candidate-plan-001",
        "project": "ephy",
        "kind": "decision",
        "year_month": "2026-09",
        "confidence": 0.9,
        "preferred_filename": "synthetic-plan.md",
        "placement_candidates": [_candidate()],
        "proposed_frontmatter": {"title": "Synthetic plan"},
        "proposed_body": "# Synthetic plan",
        "source_refs": [SourceRef(type="synthetic-test", reference="fixture://planning/001")],
        "sensitivity": "internal",
        "created_at": datetime(2026, 9, 1, tzinfo=UTC),
    }
    values.update(overrides)
    return KarteProposalPlanner().plan(**values)


def test_planner_recommends_create_without_exact_or_similar_document() -> None:
    plan = _plan()

    assert plan.recommendation == "create"
    assert plan.proposal.operation == "create"
    assert plan.proposal.target_relative_path is None
    plan.proposal.require_publishable()


def test_planner_recommends_append_only_for_exact_identity_and_content_classification() -> None:
    match = ExistingDocumentMatch(
        doc_id="doc:exact",
        relative_path="content/projects/ephy/decision/2026-09/existing.md",
        sha256="a" * 64,
        project="ephy",
        kind="decision",
        similarity=1,
    )
    plan = _plan(intended_doc_id="doc:exact", document_matches=[match])

    assert plan.recommendation == "append"
    assert plan.proposal.operation == "append"
    assert plan.proposal.target_doc_id == "doc:exact"
    assert plan.proposal.base_sha256 == "a" * 64
    assert plan.proposal.append_position == "document_end"


@pytest.mark.parametrize(
    ("overrides", "reason"),
    [
        ({"confidence": 0.5, "placement_candidates": [_candidate(confidence=0.5)]}, "confidence"),
        (
            {"placement_candidates": [_candidate(confidence=0.55), _candidate("master", "reference", 0.45)]},
            "too close",
        ),
        (
            {
                "document_matches": [
                    ExistingDocumentMatch("doc:similar", "content/similar.md", "b" * 64, "ephy", "decision", 0.9)
                ]
            },
            "similar document",
        ),
    ],
)
def test_planner_requires_consultation_for_uncertainty(overrides, reason: str) -> None:
    plan = _plan(**overrides)

    assert plan.recommendation == "consult"
    assert reason in " ".join(plan.reasons)
    with pytest.raises(ValueError, match="consultation"):
        plan.proposal.require_publishable()


def test_planner_lightly_checks_exact_document_content_before_automatic_append() -> None:
    match = ExistingDocumentMatch(
        doc_id="doc:exact",
        relative_path="content/projects/ephy/note/2026-09/existing.md",
        sha256="c" * 64,
        project="ephy",
        kind="decision",
        similarity=0.01,
    )

    plan = _plan(intended_doc_id="doc:exact", document_matches=[match])
    assert plan.recommendation == "consult"
    assert "not similar enough" in " ".join(plan.reasons)

    confirmed = KarteProposalPlanner().plan(
        candidate_id="candidate-plan-confirmed",
        project="ephy",
        kind="decision",
        year_month="2026-09",
        confidence=1,
        preferred_filename="confirmed.md",
        placement_candidates=[_candidate()],
        proposed_frontmatter={},
        proposed_body="Confirmed append.",
        source_refs=[SourceRef(type="synthetic-test", reference="fixture://planning/confirmed")],
        sensitivity="internal",
        created_at=datetime(2026, 9, 1, tzinfo=UTC),
        intended_doc_id="doc:exact",
        document_matches=[match],
        content_match_confirmed=True,
    )
    assert confirmed.recommendation == "append"
