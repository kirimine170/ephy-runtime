from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path

import pytest

from packages.karte_core.conversation import KarteConversationRequest, KarteConversationService
from packages.karte_core.context import (
    ContextDocument,
    ContextResponse,
    ContextSearchResult,
    KarteContextGroundingSource,
    KarteContextSelection,
    KarteContextTimeout,
)
from packages.karte_core.source import KarteSourceAdapter


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


class _StubContextClient:
    def __init__(self, root: Path) -> None:
        self.adapter = KarteSourceAdapter(root)
        self.calls: list[tuple[str, str, dict]] = []

    def search_and_read(self, query: str, **kwargs) -> KarteContextSelection:
        self.calls.append(("search_and_read", query, kwargs))
        documents = [_context_document(document) for document in self.adapter.scan().documents]
        results = [
            _context_result(document, score=max(1, 10 - index))
            for index, document in enumerate(documents[: kwargs.get("top_k", 5)])
        ]
        search_response = ContextResponse(
            request_id="stub-search-request",
            request_sha256="a" * 64,
            operation="search",
            status="ok",
            results=results,
            processed_at=NOW,
        )
        by_doc_id = {document.doc_id: document for document in documents}
        sources = [
            KarteContextGroundingSource(
                result=result,
                document=by_doc_id[result.doc_id],
                excerpt=by_doc_id[result.doc_id].body[:6_000],
                read_status="read",
            )
            for result in results[: kwargs.get("max_documents", 3)]
        ]
        return KarteContextSelection(search_response=search_response, sources=sources, read_failed_count=0)

    def read(self, doc_id: str, **kwargs) -> ContextResponse:
        self.calls.append(("read", doc_id, kwargs))
        document = next(
            (
                _context_document(candidate)
                for candidate in self.adapter.scan().documents
                if candidate.doc_id == doc_id
            ),
            None,
        )
        return ContextResponse(
            request_id="stub-read-request",
            request_sha256="b" * 64,
            operation="read",
            status="ok" if document is not None else "not_found",
            document=document,
            processed_at=NOW,
        )


def _context_document(document) -> ContextDocument:
    parts = document.relative_path.split("/")
    return ContextDocument(
        doc_id=document.doc_id,
        title=document.title,
        project=str(document.frontmatter.get("project") or parts[2]),
        kind=str(document.frontmatter.get("kind") or parts[3]),
        tags=document.tags,
        sensitivity=str(document.frontmatter.get("sensitivity") or "internal"),
        relative_path=document.relative_path,
        updated_at=document.updated_at,
        sha256=document.sha256,
        body=document.body,
        provenance=[],
    )


def _context_result(document: ContextDocument, *, score: float) -> ContextSearchResult:
    return ContextSearchResult(
        **document.model_dump(exclude={"body"}),
        snippet=document.body[:2_048],
        score=score,
    )


def _service(root: Path) -> KarteConversationService:
    return KarteConversationService(root, context_client=_StubContextClient(root))


def _synthetic_document(*, body: str, sha256: str = "c" * 64) -> ContextDocument:
    return ContextDocument(
        doc_id="doc:context-owned",
        title="EphyとKarteの連携方針を決定したい",
        project="ephy",
        kind="decision",
        tags=["ephy", "integration"],
        sensitivity="internal",
        relative_path="content/projects/ephy/decision/2026-09/context-owned.md",
        updated_at=NOW,
        sha256=sha256,
        body=body,
        provenance=[],
    )


class _StaticContextClient:
    def __init__(self, *, selection=None, read_response=None, error: Exception | None = None) -> None:
        self.selection = selection
        self.read_response = read_response
        self.error = error
        self.calls: list[tuple[str, str, dict]] = []

    def search_and_read(self, query: str, **kwargs):
        self.calls.append(("search_and_read", query, kwargs))
        if self.error is not None:
            raise self.error
        return self.selection

    def read(self, doc_id: str, **kwargs):
        self.calls.append(("read", doc_id, kwargs))
        if self.error is not None:
            raise self.error
        return self.read_response


def _empty_selection() -> KarteContextSelection:
    return KarteContextSelection(
        search_response=ContextResponse(
            request_id="empty-search-request",
            request_sha256="d" * 64,
            operation="search",
            status="ok",
            results=[],
            processed_at=NOW,
        )
    )


def test_conversation_plan_builds_publishable_create_with_full_summary(tmp_path: Path) -> None:
    service = _service(_data_root(tmp_path))

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


def test_create_summary_keeps_the_complete_conversation_log(tmp_path: Path) -> None:
    service = _service(_data_root(tmp_path))
    request = _request(
        messages=[
            {"role": "user", "content": "最初の相談内容です．"},
            {"role": "assistant", "content": "最初の回答です．"},
            {"role": "user", "content": "追加で確認したい内容です．"},
            {"role": "assistant", "content": "最後の回答です．"},
        ]
    )

    plan = service.plan(request)

    assert "## 会話ログ" in plan.summary_markdown
    assert "### 1．利用者\n\n最初の相談内容です．" in plan.summary_markdown
    assert "### 2．Ephy\n\n最初の回答です．" in plan.summary_markdown
    assert "### 3．利用者\n\n追加で確認したい内容です．" in plan.summary_markdown
    assert "### 4．Ephy\n\n最後の回答です．" in plan.summary_markdown


def test_conversation_plan_requires_project_consultation(tmp_path: Path) -> None:
    service = _service(_data_root(tmp_path))

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
    service = _service(root)

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
    service = _service(root)
    request = _request(resolution="create")
    plan = service.plan(request)
    reviewed_request = request.model_copy(update={"reviewed_plan_sha256": plan.plan_sha256})

    first = service.publish(reviewed_request)
    second = service.publish(reviewed_request)

    assert first.state == "pending"
    assert second.state == "pending"
    assert first.candidate_id == second.candidate_id
    payload = json.loads(Path(first.path).read_text(encoding="utf-8"))
    assert payload["operation"] == "create"
    assert payload["target_relative_path"] is None
    assert service.status(first.candidate_id).state == "pending"


def test_candidate_identity_changes_when_user_changes_resolution(tmp_path: Path) -> None:
    service = _service(_data_root(tmp_path))

    automatic = service.plan(_request())
    explicit = service.plan(_request(kind="note", resolution="create"))

    assert automatic.candidate_id != explicit.candidate_id


def test_candidate_identity_covers_tags_and_sensitivity(tmp_path: Path) -> None:
    service = _service(_data_root(tmp_path))

    baseline = service.plan(_request())
    changed_tags = service.plan(_request(tags=["architecture"]))
    changed_sensitivity = service.plan(_request(sensitivity="confidential"))

    assert baseline.candidate_id != changed_tags.candidate_id
    assert baseline.candidate_id != changed_sensitivity.candidate_id


def test_explicit_append_can_confirm_a_low_similarity_doc_id(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    existing = _write_document(root, body="# 別件\n\nまったく異なる既存内容です．")
    service = _service(root)

    resolved = service.plan(_request(resolution="append", intended_doc_id="doc:existing"))

    assert resolved.recommendation == "append"
    assert resolved.publishable is True
    assert resolved.proposal.target_relative_path == existing.relative_to(root).as_posix()


def test_auto_recommendation_uses_karte_context_instead_of_direct_canonical_scan(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    _write_document(
        root,
        body="# EphyとKarteの連携方針を決定したい\n\nproject優先で保存する方針です．",
    )
    context_client = _StaticContextClient(selection=_empty_selection())
    service = KarteConversationService(root, context_client=context_client)

    plan = service.plan(_request())

    assert plan.recommendation == "create"
    assert plan.similar_documents == []
    assert plan.context_status.status == "ok"
    assert context_client.calls[0][0] == "search_and_read"
    assert context_client.calls[0][2] == {
        "projects": ["ephy"],
        "tags": [],
        "sensitivity_ceiling": "internal",
        "top_k": 5,
        "max_documents": 3,
    }


def test_auto_recommendation_consults_when_karte_context_is_unavailable(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    context_client = _StaticContextClient(error=KarteContextTimeout("synthetic timeout"))
    service = KarteConversationService(root, context_client=context_client)

    plan = service.plan(_request())

    assert plan.recommendation == "consult"
    assert plan.publishable is False
    assert plan.context_status.status == "unavailable"
    assert "could not be checked" in " ".join(plan.reasons)


def test_partial_context_read_keeps_disclosed_candidate_but_requires_choice(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    document = _synthetic_document(
        body="# EphyとKarteの連携方針を決定したい\n\nproject優先で保存し，曖昧な場合は相談します．"
    )
    result = _context_result(document, score=10)
    search_response = ContextResponse(
        request_id="partial-search-request",
        request_sha256="e" * 64,
        operation="search",
        status="ok",
        results=[result],
        processed_at=NOW,
    )
    selection = KarteContextSelection(
        search_response=search_response,
        sources=[
            KarteContextGroundingSource(
                result=result,
                excerpt=result.snippet,
                read_status="snippet_fallback",
            )
        ],
        read_failed_count=1,
    )
    service = KarteConversationService(root, context_client=_StaticContextClient(selection=selection))

    plan = service.plan(_request())

    assert plan.recommendation == "consult"
    assert plan.context_status.status == "partial"
    assert plan.context_status.searched_count == 1
    assert plan.context_status.read_count == 0
    assert plan.context_status.read_failed_count == 1
    assert plan.similar_documents[0].doc_id == "doc:context-owned"


def test_explicit_append_uses_context_owned_current_hash_without_direct_file(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    document = _synthetic_document(body="# 別件\n\n現在のKarte本文です．", sha256="f" * 64)
    read_response = ContextResponse(
        request_id="context-read-request",
        request_sha256="1" * 64,
        operation="read",
        status="ok",
        document=document,
        processed_at=NOW,
    )
    context_client = _StaticContextClient(read_response=read_response)
    service = KarteConversationService(root, context_client=context_client)

    plan = service.plan(_request(resolution="append", intended_doc_id=document.doc_id))

    assert plan.recommendation == "append"
    assert plan.publishable is True
    assert plan.proposal.target_doc_id == document.doc_id
    assert plan.proposal.target_relative_path == document.relative_path
    assert plan.proposal.base_sha256 == "f" * 64
    assert plan.proposal.source_refs[-1].model_dump() == {
        "type": "karte-context",
        "reference": f"doc_id:{document.doc_id}",
        "sha256": "f" * 64,
    }
    assert plan.context_status.status == "ok"
    assert context_client.calls == [
        (
            "read",
            document.doc_id,
            {"projects": ["ephy"], "tags": [], "sensitivity_ceiling": "internal"},
        )
    ]


def test_missing_project_and_explicit_create_do_not_search_across_personal_context(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    context_client = _StaticContextClient(error=AssertionError("context must not be called"))
    service = KarteConversationService(root, context_client=context_client)

    missing_project = service.plan(_request(project=None))
    explicit_create = service.plan(_request(resolution="create"))

    assert missing_project.recommendation == "consult"
    assert missing_project.context_status.status == "not_required"
    assert explicit_create.recommendation == "create"
    assert explicit_create.context_status.status == "not_required"
    assert context_client.calls == []


def test_publish_rejects_append_when_context_document_changed_after_review(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    original = _synthetic_document(body="# 方針\n\nレビュー時点の本文です．", sha256="2" * 64)
    context_client = _StaticContextClient(
        read_response=ContextResponse(
            request_id="reviewed-read-request",
            request_sha256="3" * 64,
            operation="read",
            status="ok",
            document=original,
            processed_at=NOW,
        )
    )
    service = KarteConversationService(root, context_client=context_client)
    request = _request(resolution="append", intended_doc_id=original.doc_id)
    reviewed_plan = service.plan(request)
    updated = original.model_copy(update={"body": "# 方針\n\nレビュー後に更新されました．", "sha256": "4" * 64})
    context_client.read_response = ContextResponse(
        request_id="updated-read-request",
        request_sha256="5" * 64,
        operation="read",
        status="ok",
        document=updated,
        processed_at=NOW,
    )
    reviewed_request = request.model_copy(update={"reviewed_plan_sha256": reviewed_plan.plan_sha256})

    with pytest.raises(ValueError, match="changed after review"):
        service.publish(reviewed_request)

    assert list((root / ".mdsys/ephy/outbox/pending").glob("*.json")) == []


def test_publish_rejects_plan_that_was_not_explicitly_reviewed(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    service = KarteConversationService(root, context_client=_StaticContextClient(selection=_empty_selection()))

    with pytest.raises(ValueError, match="requires reviewed_plan_sha256"):
        service.publish(_request())

    assert list((root / ".mdsys/ephy/outbox/pending").glob("*.json")) == []


def test_publish_reports_stale_review_before_new_context_consultation(tmp_path: Path) -> None:
    root = _data_root(tmp_path)
    context_client = _StaticContextClient(selection=_empty_selection())
    service = KarteConversationService(root, context_client=context_client)
    request = _request()
    reviewed_plan = service.plan(request)
    context_client.error = KarteContextTimeout("synthetic timeout after review")

    with pytest.raises(ValueError, match="changed after review"):
        service.publish(request.model_copy(update={"reviewed_plan_sha256": reviewed_plan.plan_sha256}))

    assert list((root / ".mdsys/ephy/outbox/pending").glob("*.json")) == []
