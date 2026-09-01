from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any

from .contracts import KarteChangeProposal, PlacementCandidate, PlacementHint, SourceRef


@dataclass(frozen=True)
class ExistingDocumentMatch:
    doc_id: str
    relative_path: str
    sha256: str
    project: str
    kind: str
    similarity: float


@dataclass(frozen=True)
class ProposalPlanningPolicy:
    minimum_confidence: float = 0.75
    minimum_candidate_margin: float = 0.15
    similar_document_threshold: float = 0.82


@dataclass(frozen=True)
class ProposalPlan:
    proposal: KarteChangeProposal
    recommendation: str
    reasons: tuple[str, ...]


class KarteProposalPlanner:
    """Recommend create，append，or user consultation before outbox publish."""

    def __init__(self, policy: ProposalPlanningPolicy | None = None) -> None:
        self.policy = policy or ProposalPlanningPolicy()

    def plan(
        self,
        *,
        candidate_id: str,
        project: str,
        kind: str,
        year_month: str,
        confidence: float,
        preferred_filename: str,
        placement_candidates: list[PlacementCandidate],
        proposed_frontmatter: dict[str, Any],
        proposed_body: str,
        source_refs: list[SourceRef],
        sensitivity: str,
        created_at: datetime,
        intended_doc_id: str | None = None,
        document_matches: list[ExistingDocumentMatch] | None = None,
    ) -> ProposalPlan:
        matches = document_matches or []
        consultation_reasons: list[str] = []
        if confidence < self.policy.minimum_confidence:
            consultation_reasons.append("placement confidence is below the publish threshold")
        ranked = sorted((candidate.confidence for candidate in placement_candidates), reverse=True)
        if len(ranked) > 1 and ranked[0] - ranked[1] < self.policy.minimum_candidate_margin:
            consultation_reasons.append("multiple placement candidates are too close")

        exact = next((match for match in matches if intended_doc_id and match.doc_id == intended_doc_id), None)
        operation = "create"
        target_doc_id = None
        target_relative_path = None
        base_sha256 = None
        append_position = None
        reasons: list[str] = []
        if intended_doc_id:
            if exact is None:
                consultation_reasons.append("the intended doc_id was not found in canonical Karte content")
            elif exact.project != project or exact.kind != kind:
                consultation_reasons.append("the exact doc_id has different project or kind metadata")
            else:
                operation = "append"
                target_doc_id = exact.doc_id
                target_relative_path = exact.relative_path
                base_sha256 = exact.sha256
                append_position = "document_end"
                reasons.append("exact doc_id，project，kind，and canonical hash support append")
        elif any(match.similarity >= self.policy.similar_document_threshold for match in matches):
            consultation_reasons.append("a similar document exists without an exact doc_id match")

        if operation == "create" and not consultation_reasons:
            reasons.append("no exact or similar canonical document requires append")
        question = None
        if consultation_reasons:
            question = "Please choose the project or document target before publication: " + "; ".join(consultation_reasons)
        placement = PlacementHint(
            project=project,
            kind=kind,
            year_month=year_month,
            confidence=confidence,
            preferred_filename=preferred_filename,
            candidates=placement_candidates,
            consultation_required=bool(consultation_reasons),
            consultation_question=question,
        )
        proposal = KarteChangeProposal(
            candidate_id=candidate_id,
            operation=operation,
            target_doc_id=target_doc_id,
            target_relative_path=target_relative_path,
            base_sha256=base_sha256,
            append_position=append_position,
            proposed_frontmatter=proposed_frontmatter,
            proposed_body=proposed_body,
            placement=placement,
            source_refs=source_refs,
            sensitivity=sensitivity,
            created_at=created_at,
        )
        return ProposalPlan(
            proposal=proposal,
            recommendation="consult" if consultation_reasons else operation,
            reasons=tuple([*reasons, *consultation_reasons]),
        )
