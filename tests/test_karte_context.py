from __future__ import annotations

import hashlib
import json
import threading
import time
from datetime import UTC, datetime
from pathlib import Path

import pytest

from packages.karte_core.context import (
    ContextDocument,
    ContextProvenance,
    ContextRequest,
    ContextResponse,
    ContextSearchResult,
    KarteContextClient,
    KarteContextProtocolError,
    KarteContextTimeout,
    _atomic_write_bytes,
    _serialize_json,
)


def test_context_client_search_exchanges_atomic_protocol(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=1, poll_interval=0.01)

    def respond() -> None:
        request_path = _wait_for_request(client.requests_dir)
        data = request_path.read_bytes()
        request = ContextRequest.model_validate_json(data)
        response = {
            "protocol_version": "1.0",
            "request_id": request.request_id,
            "request_sha256": hashlib.sha256(data).hexdigest(),
            "operation": "search",
            "status": "ok",
            "results": [
                {
                    "doc_id": "doc:synthetic-001",
                    "title": "Synthetic context",
                    "project": "ephy",
                    "kind": "decision",
                    "tags": ["architecture"],
                    "sensitivity": "internal",
                    "relative_path": "content/projects/ephy/decision/2026-09/context.md",
                    "updated_at": "2026-09-01T00:00:00Z",
                    "sha256": "a" * 64,
                    "snippet": "Karte owns Personal Context.",
                    "score": 12,
                    "provenance": [
                        {
                            "type": "canonical",
                            "reference": "content/projects/ephy/decision/2026-09/context.md",
                            "sha256": "a" * 64,
                        }
                    ],
                }
            ],
            "document": None,
            "diagnostics": [],
            "error": None,
            "processed_at": "2026-09-01T00:00:01Z",
        }
        _atomic_write_bytes(client.responses_dir / request_path.name, _serialize_json(response))

    worker = threading.Thread(target=respond)
    worker.start()
    response = client.search("personal context", projects=["ephy"], tags=["architecture"], top_k=5)
    worker.join(timeout=1)

    assert response.status == "ok"
    assert response.results[0].doc_id == "doc:synthetic-001"
    assert response.results[0].provenance[0].type == "canonical"


def test_context_client_times_out_without_karte_processor(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=0.05, poll_interval=0.01)

    with pytest.raises(KarteContextTimeout, match="timed out"):
        client.search("missing processor")

    request_files = list(client.requests_dir.glob("*.json"))
    assert len(request_files) == 1
    payload = json.loads(request_files[0].read_text(encoding="utf-8"))
    assert payload["operation"] == "search"


def test_context_client_rejects_response_hash_mismatch(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=1, poll_interval=0.01)

    def respond() -> None:
        request_path = _wait_for_request(client.requests_dir)
        request = ContextRequest.model_validate_json(request_path.read_bytes())
        response = {
            "protocol_version": "1.0",
            "request_id": request.request_id,
            "request_sha256": "b" * 64,
            "operation": "search",
            "status": "ok",
            "results": [],
            "document": None,
            "diagnostics": [],
            "error": None,
            "processed_at": "2026-09-01T00:00:01Z",
        }
        _atomic_write_bytes(client.responses_dir / request_path.name, _serialize_json(response))

    worker = threading.Thread(target=respond)
    worker.start()
    with pytest.raises(KarteContextProtocolError, match="does not match"):
        client.search("hash mismatch")
    worker.join(timeout=1)


def test_context_client_rejects_search_content_outside_requested_scope(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=1, poll_interval=0.01)

    def respond() -> None:
        request_path = _wait_for_request(client.requests_dir)
        data = request_path.read_bytes()
        request = ContextRequest.model_validate_json(data)
        response = {
            "protocol_version": "1.0",
            "request_id": request.request_id,
            "request_sha256": hashlib.sha256(data).hexdigest(),
            "operation": "search",
            "status": "ok",
            "results": [
                {
                    "doc_id": "doc:restricted-001",
                    "title": "Restricted document",
                    "project": "other-project",
                    "kind": "decision",
                    "tags": ["unrelated"],
                    "sensitivity": "restricted",
                    "relative_path": "content/projects/other-project/decision/2026-09/restricted.md",
                    "updated_at": "2026-09-01T00:00:00Z",
                    "sha256": "a" * 64,
                    "snippet": "This must not be disclosed to the caller.",
                    "score": 10,
                    "provenance": [],
                }
            ],
            "document": None,
            "diagnostics": [],
            "error": None,
            "processed_at": "2026-09-01T00:00:01Z",
        }
        _atomic_write_bytes(client.responses_dir / request_path.name, _serialize_json(response))

    worker = threading.Thread(target=respond)
    worker.start()
    with pytest.raises(KarteContextProtocolError, match="outside the requested scope"):
        client.search(
            "scope violation",
            projects=["ephy"],
            tags=["architecture"],
            sensitivity_ceiling="internal",
        )
    worker.join(timeout=1)


def test_context_client_rejects_successful_read_without_document(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=1, poll_interval=0.01)

    def respond() -> None:
        request_path = _wait_for_request(client.requests_dir)
        data = request_path.read_bytes()
        request = ContextRequest.model_validate_json(data)
        response = {
            "protocol_version": "1.0",
            "request_id": request.request_id,
            "request_sha256": hashlib.sha256(data).hexdigest(),
            "operation": "read",
            "status": "ok",
            "results": [],
            "document": None,
            "diagnostics": [],
            "error": None,
            "processed_at": "2026-09-01T00:00:01Z",
        }
        _atomic_write_bytes(client.responses_dir / request_path.name, _serialize_json(response))

    worker = threading.Thread(target=respond)
    worker.start()
    with pytest.raises(KarteContextProtocolError, match="response is invalid"):
        client.read("doc:synthetic-001")
    worker.join(timeout=1)


def test_context_client_rejects_read_document_for_a_different_doc_id(tmp_path: Path) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path, timeout_seconds=1, poll_interval=0.01)

    def respond() -> None:
        request_path = _wait_for_request(client.requests_dir)
        data = request_path.read_bytes()
        request = ContextRequest.model_validate_json(data)
        response = {
            "protocol_version": "1.0",
            "request_id": request.request_id,
            "request_sha256": hashlib.sha256(data).hexdigest(),
            "operation": "read",
            "status": "ok",
            "results": [],
            "document": {
                "doc_id": "doc:other-002",
                "title": "Wrong document",
                "project": "ephy",
                "kind": "decision",
                "tags": ["architecture"],
                "sensitivity": "internal",
                "relative_path": "content/projects/ephy/decision/2026-09/wrong.md",
                "updated_at": "2026-09-01T00:00:00Z",
                "sha256": "d" * 64,
                "body": "This body belongs to a different document.",
                "provenance": [],
            },
            "diagnostics": [],
            "error": None,
            "processed_at": "2026-09-01T00:00:01Z",
        }
        _atomic_write_bytes(client.responses_dir / request_path.name, _serialize_json(response))

    worker = threading.Thread(target=respond)
    worker.start()
    with pytest.raises(KarteContextProtocolError, match="requested doc_id"):
        client.read("doc:synthetic-001")
    worker.join(timeout=1)


def test_search_and_read_preserves_scope_bounds_context_and_falls_back_per_document(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path)
    timestamp = datetime(2026, 9, 1, tzinfo=UTC)
    provenance = [ContextProvenance(type="canonical", reference="synthetic", sha256="a" * 64)]
    results = [
        ContextSearchResult(
            doc_id=f"doc:synthetic-{index:03d}",
            title=f"Synthetic {index}",
            project="ephy",
            kind="decision",
            tags=["architecture"],
            sensitivity="internal",
            relative_path=f"content/projects/ephy/decision/2026-09/{index}.md",
            updated_at=timestamp,
            sha256="a" * 64,
            snippet=f"needle-{index}",
            score=10 - index,
            provenance=provenance,
        )
        for index in range(1, 5)
    ]
    search_response = ContextResponse(
        request_id="test-search-request",
        request_sha256="b" * 64,
        operation="search",
        status="ok",
        results=results,
        processed_at=timestamp,
    )
    calls: list[tuple] = []

    def fake_search(query: str, **kwargs) -> ContextResponse:
        calls.append(("search", query, kwargs))
        return search_response

    def fake_read(doc_id: str, **kwargs) -> ContextResponse:
        calls.append(("read", doc_id, kwargs))
        if doc_id == "doc:synthetic-002":
            raise KarteContextTimeout("synthetic per-document timeout")
        index = int(doc_id.rsplit("-", 1)[1])
        body = "x" * 3_500 + f"needle-{index}" + "y" * 3_500
        return ContextResponse(
            request_id=f"test-read-request-{index}",
            request_sha256="c" * 64,
            operation="read",
            status="ok",
            document=ContextDocument(
                doc_id=doc_id,
                title=f"Synthetic {index}",
                project="ephy",
                kind="decision",
                tags=["architecture"],
                sensitivity="internal",
                relative_path=f"content/projects/ephy/decision/2026-09/{index}.md",
                updated_at=timestamp,
                sha256="d" * 64,
                body=body,
                provenance=provenance,
            ),
            processed_at=timestamp,
        )

    monkeypatch.setattr(client, "search", fake_search)
    monkeypatch.setattr(client, "read", fake_read)

    selection = client.search_and_read(
        "Personal Context boundary",
        projects=["ephy"],
        tags=["architecture"],
        sensitivity_ceiling="confidential",
        top_k=4,
        max_documents=3,
        max_total_chars=8_000,
        max_document_chars=4_000,
    )

    assert [source.result.doc_id for source in selection.sources] == [
        "doc:synthetic-001",
        "doc:synthetic-002",
        "doc:synthetic-003",
    ]
    assert selection.read_count == 2
    assert selection.read_failed_count == 1
    assert selection.sources[0].read_status == "read"
    assert "needle-1" in selection.sources[0].excerpt
    assert selection.sources[1].read_status == "snippet_fallback"
    assert selection.sources[1].excerpt == "needle-2"
    assert sum(len(source.excerpt) for source in selection.sources) <= 8_000
    assert all(
        call[2] == {
            "projects": ["ephy"],
            "tags": ["architecture"],
            "sensitivity_ceiling": "confidential",
        }
        for call in calls[1:]
    )
    assert calls[0] == (
        "search",
        "Personal Context boundary",
        {
            "projects": ["ephy"],
            "tags": ["architecture"],
            "sensitivity_ceiling": "confidential",
            "top_k": 4,
        },
    )


def test_search_and_read_propagates_protocol_validation_failures(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    (tmp_path / "content").mkdir()
    client = KarteContextClient(tmp_path)
    timestamp = datetime(2026, 9, 1, tzinfo=UTC)
    result = ContextSearchResult(
        doc_id="doc:synthetic-001",
        title="Synthetic",
        project="ephy",
        kind="decision",
        tags=["architecture"],
        sensitivity="internal",
        relative_path="content/projects/ephy/decision/2026-09/synthetic.md",
        updated_at=timestamp,
        sha256="a" * 64,
        snippet="scoped snippet",
        score=10,
        provenance=[],
    )
    search_response = ContextResponse(
        request_id="test-search-request",
        request_sha256="b" * 64,
        operation="search",
        status="ok",
        results=[result],
        processed_at=timestamp,
    )
    monkeypatch.setattr(client, "search", lambda *args, **kwargs: search_response)

    def invalid_read(*args, **kwargs) -> ContextResponse:
        raise KarteContextProtocolError("synthetic out-of-scope response")

    monkeypatch.setattr(client, "read", invalid_read)

    with pytest.raises(KarteContextProtocolError, match="out-of-scope"):
        client.search_and_read("Personal Context boundary", projects=["ephy"])


def _wait_for_request(directory: Path) -> Path:
    deadline = time.monotonic() + 1
    while time.monotonic() < deadline:
        files = list(directory.glob("*.json"))
        if files:
            return files[0]
        time.sleep(0.005)
    raise AssertionError("context request was not published")
