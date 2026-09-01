from __future__ import annotations

import hashlib
import json
import os
import re
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Literal, get_args

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from .contracts import KarteChangeProposal, KarteDocumentKind, KarteReceipt, PlacementCandidate, SourceRef
from .outbox import KarteOutbox
from .planning import ExistingDocumentMatch, KarteProposalPlanner
from .source import KarteDocument, KarteSourceAdapter


SUPPORTED_KINDS = tuple(get_args(KarteDocumentKind))
_PROJECT_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,63}$")
_CONVERSATION_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
_SPACE_RE = re.compile(r"\s+")
_WORD_RE = re.compile(r"[a-z0-9_]{2,}|[ぁ-んァ-ヶ一-龠々]{1,}", re.IGNORECASE)
_TITLE_TRIM_RE = re.compile(r"^[#>*\-\s]+|[。．.!！?？、，,:：;；\s]+$")


class ConversationMessage(BaseModel):
    model_config = ConfigDict(extra="forbid")

    role: Literal["user", "assistant"]
    content: str = Field(min_length=1, max_length=100_000)

    @field_validator("content")
    @classmethod
    def normalize_content(cls, value: str) -> str:
        normalized = value.strip()
        if not normalized:
            raise ValueError("conversation message content cannot be blank")
        return normalized


class KarteConversationRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    conversation_id: str
    messages: list[ConversationMessage] = Field(min_length=2, max_length=30)
    occurred_at: datetime
    project: str | None = None
    kind: KarteDocumentKind | None = None
    sensitivity: Literal["public", "internal", "confidential", "restricted"] = "internal"
    tags: list[str] = Field(default_factory=list, max_length=16)
    resolution: Literal["auto", "create", "append"] = "auto"
    intended_doc_id: str | None = None

    @field_validator("conversation_id")
    @classmethod
    def validate_conversation_id(cls, value: str) -> str:
        if not _CONVERSATION_ID_RE.fullmatch(value):
            raise ValueError("conversation_id contains unsupported characters")
        return value

    @field_validator("project")
    @classmethod
    def validate_project(cls, value: str | None) -> str | None:
        if value is None:
            return None
        normalized = value.strip().lower()
        if not _PROJECT_RE.fullmatch(normalized):
            raise ValueError("project must be a lowercase path-safe slug")
        return normalized

    @field_validator("tags")
    @classmethod
    def validate_tags(cls, value: list[str]) -> list[str]:
        normalized = list(dict.fromkeys(tag.strip() for tag in value if tag.strip()))
        if any(len(tag) > 64 for tag in normalized):
            raise ValueError("tags cannot exceed 64 characters")
        return normalized

    @model_validator(mode="after")
    def validate_resolution(self) -> "KarteConversationRequest":
        if self.occurred_at.tzinfo is None or self.occurred_at.utcoffset() is None:
            raise ValueError("occurred_at must include a timezone")
        if self.resolution == "append" and not (self.intended_doc_id or "").strip():
            raise ValueError("append resolution requires intended_doc_id")
        if self.resolution == "create" and self.intended_doc_id is not None:
            raise ValueError("create resolution cannot retain intended_doc_id")
        if self.messages[-1].role != "assistant":
            raise ValueError("the latest conversation message must be an assistant response")
        if sum(len(message.content) for message in self.messages) > 300_000:
            raise ValueError("conversation is too large to summarize")
        return self


class SimilarDocument(BaseModel):
    model_config = ConfigDict(extra="forbid")

    doc_id: str
    title: str
    relative_path: str
    project: str | None
    kind: str | None
    similarity: float = Field(ge=0, le=1)


class KarteConversationPlanResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidate_id: str
    recommendation: Literal["consult", "create", "append"]
    publishable: bool
    needs_project: bool
    reasons: list[str]
    summary_title: str
    summary_markdown: str
    similar_documents: list[SimilarDocument]
    proposal: KarteChangeProposal


class KarteConversationPublishResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidate_id: str
    state: Literal["pending", "accepted", "rejected", "processed"]
    path: str
    plan: KarteConversationPlanResponse


class KarteConversationStatusResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidate_id: str
    state: Literal["missing", "pending", "accepted", "rejected", "processed"]
    receipt: KarteReceipt | None = None


@dataclass(frozen=True)
class _ClassifiedKind:
    kind: str
    confidence: float
    candidates: list[tuple[str, float, str]]


class KarteConversationService:
    """Turn a reviewed Ephy conversation into a Karte outbox proposal."""

    def __init__(self, karte_data_dir: str | Path) -> None:
        self.adapter = KarteSourceAdapter(karte_data_dir)
        self.outbox = KarteOutbox(karte_data_dir)
        self.planner = KarteProposalPlanner()

    @classmethod
    def from_environment(cls) -> "KarteConversationService | None":
        configured = os.environ.get("KARTE_DATA_DIR", "").strip()
        if not configured:
            return None
        try:
            return cls(configured)
        except (OSError, ValueError):
            return None

    def plan(self, request: KarteConversationRequest) -> KarteConversationPlanResponse:
        scan = self.adapter.scan()
        summary_title, create_body, append_body = _summarize_conversation(request.messages, request.occurred_at)
        similar_pairs = _similar_documents(scan.documents, summary_title, create_body)
        similar_documents = [_public_similar_document(document, similarity) for document, similarity in similar_pairs]
        matches = [_existing_match(document, similarity) for document, similarity in similar_pairs]

        selected_document = next(
            (document for document in scan.documents if request.intended_doc_id and document.doc_id == request.intended_doc_id),
            None,
        )
        if selected_document is not None and not any(match.doc_id == selected_document.doc_id for match in matches):
            similarity = _document_similarity(selected_document, summary_title, create_body)
            matches.append(_existing_match(selected_document, similarity))
        classified = _classify_kind(request.messages, request.kind)
        project = request.project or "master"
        kind = classified.kind
        confidence = classified.confidence
        additional_consultation_reasons: list[str] = []
        if request.project is None:
            confidence = min(confidence, 0.5)
            additional_consultation_reasons.append("project is required before publication")

        if request.resolution == "append" and selected_document is not None:
            selected_project, selected_kind = _document_project_kind(selected_document)
            if selected_project is not None:
                project = selected_project
            if selected_kind is not None:
                kind = selected_kind
            if selected_project is None or selected_kind is None:
                additional_consultation_reasons.append("the selected document needs project and kind metadata before append")
            confidence = 1.0

        placement_candidates = _placement_candidates(project, classified, selected_kind=kind)
        candidate_id = _candidate_id(request)
        tags = list(dict.fromkeys([*request.tags, "ephy", "conversation"]))
        proposed_frontmatter: dict[str, object]
        proposed_body: str
        if request.resolution == "append" and selected_document is not None:
            proposed_frontmatter = {"tags": list(dict.fromkeys([*selected_document.tags, *tags]))}
            proposed_body = append_body
        else:
            proposed_frontmatter = {
                "title": summary_title,
                "project": project,
                "kind": kind,
                "tags": tags,
                "created_at": request.occurred_at.isoformat(),
            }
            proposed_body = create_body

        effective_matches = [] if request.resolution == "create" else matches
        plan = self.planner.plan(
            candidate_id=candidate_id,
            project=project,
            kind=kind,
            year_month=request.occurred_at.strftime("%Y-%m"),
            confidence=confidence,
            preferred_filename=_preferred_filename(kind, candidate_id, request.occurred_at),
            placement_candidates=placement_candidates,
            proposed_frontmatter=proposed_frontmatter,
            proposed_body=proposed_body,
            source_refs=[SourceRef(type="ephy-conversation", reference=f"conversation:{request.conversation_id}#{candidate_id}")],
            sensitivity=request.sensitivity,
            created_at=request.occurred_at,
            intended_doc_id=request.intended_doc_id if request.resolution != "create" else None,
            document_matches=effective_matches,
            additional_consultation_reasons=additional_consultation_reasons,
            content_match_confirmed=request.resolution == "append",
        )
        return KarteConversationPlanResponse(
            candidate_id=candidate_id,
            recommendation=plan.recommendation,
            publishable=not plan.proposal.placement.consultation_required,
            needs_project=request.project is None,
            reasons=list(plan.reasons),
            summary_title=summary_title,
            summary_markdown=proposed_body,
            similar_documents=similar_documents[:5],
            proposal=plan.proposal,
        )

    def publish(self, request: KarteConversationRequest) -> KarteConversationPublishResponse:
        plan = self.plan(request)
        plan.proposal.require_publishable()
        result = self.outbox.publish(plan.proposal)
        return KarteConversationPublishResponse(
            candidate_id=result.candidate_id,
            state=result.state,
            path=result.path,
            plan=plan,
        )

    def status(self, candidate_id: str) -> KarteConversationStatusResponse:
        KarteChangeProposal.validate_candidate_id(candidate_id)
        receipt = self.outbox.read_receipt(candidate_id)
        if receipt is not None:
            state = "processed" if receipt.result in {"conflict", "invalid"} else receipt.result
            return KarteConversationStatusResponse(candidate_id=candidate_id, state=state, receipt=receipt)
        for state, directory in (
            ("pending", self.outbox.pending_dir),
            ("accepted", self.outbox.accepted_dir),
            ("rejected", self.outbox.rejected_dir),
        ):
            if (directory / f"{candidate_id}.json").is_file():
                return KarteConversationStatusResponse(candidate_id=candidate_id, state=state)
        return KarteConversationStatusResponse(candidate_id=candidate_id, state="missing")


def _candidate_id(request: KarteConversationRequest) -> str:
    identity = {
        "conversation_id": request.conversation_id,
        "occurred_at": request.occurred_at.isoformat(),
        "messages": [message.model_dump() for message in request.messages],
        "project": request.project,
        "kind": request.kind,
        "sensitivity": request.sensitivity,
        "tags": request.tags,
        "resolution": request.resolution,
        "intended_doc_id": request.intended_doc_id,
    }
    digest = hashlib.sha256(json.dumps(identity, ensure_ascii=False, sort_keys=True).encode("utf-8")).hexdigest()
    return f"ephy-chat-{digest[:20]}"


def _summarize_conversation(messages: list[ConversationMessage], occurred_at: datetime) -> tuple[str, str, str]:
    latest_user = next(message.content for message in reversed(messages) if message.role == "user")
    latest_assistant = messages[-1].content
    title = _derive_title(latest_user)
    user_excerpt = _truncate(latest_user, 4_000)
    assistant_excerpt = _truncate(latest_assistant, 16_000)
    create_body = (
        f"# {title}\n\n"
        f"## 会話の要点\n\n{assistant_excerpt}\n\n"
        f"## 背景\n\n{user_excerpt}\n"
    )
    append_body = (
        f"## {occurred_at.strftime('%Y-%m-%d')} Ephy会話からの追記\n\n"
        f"{assistant_excerpt}\n\n"
        f"背景：{user_excerpt}\n"
    )
    return title, create_body, append_body


def _derive_title(latest_user: str) -> str:
    first_line = next((line.strip() for line in latest_user.splitlines() if line.strip()), "Ephyとの会話")
    normalized = _TITLE_TRIM_RE.sub("", first_line)
    normalized = _SPACE_RE.sub(" ", normalized).strip()
    if len(normalized) > 56:
        normalized = normalized[:56].rstrip() + "…"
    return normalized or "Ephyとの会話"


def _truncate(value: str, limit: int) -> str:
    normalized = value.strip()
    return normalized if len(normalized) <= limit else normalized[: limit - 1].rstrip() + "…"


_KIND_KEYWORDS: dict[str, tuple[str, ...]] = {
    "meeting": ("会議", "ミーティング", "議事録", "打ち合わせ", "meeting", "mtg"),
    "decision": ("決定", "決めた", "方針", "採用", "却下", "decision"),
    "plan": ("計画", "ロードマップ", "予定", "段取り", "plan"),
    "task": ("タスク", "todo", "やること", "課題"),
    "research": ("調査", "研究", "比較", "検証", "research"),
    "reference": ("参考", "資料", "引用", "reference"),
    "report": ("報告", "結果", "レポート", "report"),
    "person": ("人物", "さんについて", "プロフィール", "person"),
    "organization": ("組織", "会社", "団体", "organization"),
    "journal": ("日記", "今日", "振り返り", "journal"),
}


def _classify_kind(messages: list[ConversationMessage], explicit_kind: str | None) -> _ClassifiedKind:
    if explicit_kind is not None:
        return _ClassifiedKind(explicit_kind, 1.0, [(explicit_kind, 1.0, "user selected this kind")])
    text = "\n".join(message.content for message in messages[-6:]).lower()
    scored: list[tuple[str, int]] = []
    for kind, keywords in _KIND_KEYWORDS.items():
        score = sum(text.count(keyword) for keyword in keywords)
        if score > 0:
            scored.append((kind, score))
    scored.sort(key=lambda item: (-item[1], SUPPORTED_KINDS.index(item[0])))
    if not scored:
        return _ClassifiedKind("note", 0.8, [("note", 0.8, "general conversation defaults to a note")])
    top_kind, top_score = scored[0]
    confidence = min(0.98, 0.82 + 0.04 * top_score)
    candidates = [(top_kind, confidence, f"conversation contains {top_score} {top_kind} signal(s)")]
    if len(scored) > 1:
        second_kind, second_score = scored[1]
        second_confidence = max(0.0, min(0.95, confidence - (0.08 if second_score == top_score else 0.2)))
        candidates.append((second_kind, second_confidence, f"conversation also contains {second_score} {second_kind} signal(s)"))
    return _ClassifiedKind(top_kind, confidence, candidates)


def _placement_candidates(project: str, classified: _ClassifiedKind, *, selected_kind: str) -> list[PlacementCandidate]:
    candidates = [
        PlacementCandidate(project=project, kind=kind, confidence=confidence, reason=reason)
        for kind, confidence, reason in classified.candidates
        if kind in SUPPORTED_KINDS
    ]
    if not any(candidate.kind == selected_kind for candidate in candidates):
        candidates.insert(
            0,
            PlacementCandidate(project=project, kind=selected_kind, confidence=1.0, reason="selected document determines append kind"),
        )
    return candidates[:3]


def _preferred_filename(kind: str, candidate_id: str, occurred_at: datetime) -> str:
    return f"ephy-{kind}-{occurred_at.strftime('%Y%m')}-{candidate_id[-8:]}.md"


def _document_project_kind(document: KarteDocument) -> tuple[str | None, str | None]:
    project_value = document.frontmatter.get("project")
    kind_value = document.frontmatter.get("kind")
    project = project_value.strip().lower() if isinstance(project_value, str) and _PROJECT_RE.fullmatch(project_value.strip().lower()) else None
    kind = kind_value.strip().lower() if isinstance(kind_value, str) and kind_value.strip().lower() in SUPPORTED_KINDS else None
    if project is not None and kind is not None:
        return project, kind
    parts = document.relative_path.split("/")
    if len(parts) >= 6 and parts[:2] == ["content", "projects"]:
        if project is None and _PROJECT_RE.fullmatch(parts[2]):
            project = parts[2]
        if kind is None and parts[3] in SUPPORTED_KINDS:
            kind = parts[3]
    return project, kind


def _existing_match(document: KarteDocument, similarity: float) -> ExistingDocumentMatch:
    project, kind = _document_project_kind(document)
    return ExistingDocumentMatch(
        doc_id=document.doc_id,
        relative_path=document.relative_path,
        sha256=document.sha256,
        project=project or "",
        kind=kind or "",
        similarity=similarity,
    )


def _public_similar_document(document: KarteDocument, similarity: float) -> SimilarDocument:
    project, kind = _document_project_kind(document)
    return SimilarDocument(
        doc_id=document.doc_id,
        title=document.title,
        relative_path=document.relative_path,
        project=project,
        kind=kind,
        similarity=round(similarity, 4),
    )


def _similar_documents(documents: list[KarteDocument], title: str, body: str) -> list[tuple[KarteDocument, float]]:
    scored: list[tuple[KarteDocument, float]] = []
    for document in documents:
        similarity = _document_similarity(document, title, body)
        if similarity >= 0.05:
            scored.append((document, similarity))
    scored.sort(key=lambda item: (-item[1], item[0].relative_path))
    return scored[:20]


def _document_similarity(document: KarteDocument, title: str, body: str) -> float:
    source_tokens = _similarity_tokens(f"{title}\n{body}")
    target_tokens = _similarity_tokens(f"{document.title}\n{document.body}")
    if not source_tokens or not target_tokens:
        return 0.0
    overlap = len(source_tokens & target_tokens)
    return max(
        overlap / len(source_tokens | target_tokens),
        overlap / min(len(source_tokens), len(target_tokens)),
    )


def _similarity_tokens(value: str) -> set[str]:
    normalized = _SPACE_RE.sub("", value.lower())
    words = {match.group(0) for match in _WORD_RE.finditer(value.lower())}
    grams = {normalized[index : index + 2] for index in range(max(0, len(normalized) - 1))}
    return words | grams
