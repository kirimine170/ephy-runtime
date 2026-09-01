from __future__ import annotations

import hashlib
import json
import threading
import time
from pathlib import Path

import pytest

from packages.karte_core.context import (
    ContextRequest,
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


def _wait_for_request(directory: Path) -> Path:
    deadline = time.monotonic() + 1
    while time.monotonic() < deadline:
        files = list(directory.glob("*.json"))
        if files:
            return files[0]
        time.sleep(0.005)
    raise AssertionError("context request was not published")
