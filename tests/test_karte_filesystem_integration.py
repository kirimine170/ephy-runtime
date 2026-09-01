from __future__ import annotations

import hashlib
import json
import os
import time
from pathlib import Path

import jsonschema
import pytest

from packages.config_core.loader import AppConfig, RagConfig, VectorDBConfig
from packages.karte_core.contracts import KarteChangeProposal, KarteReceipt
from packages.karte_core.outbox import KarteOutbox
from packages.karte_core.source import KarteSourceAdapter
from packages.karte_core.watcher import KarteWatchService
from packages.rag_core.service import RagService


def _write_document(
    data_root: Path,
    relative_path: str,
    *,
    doc_id: str,
    title: str = "Synthetic note",
    tags: str = "fixture, karte",
    body: str = "# Body\n\nSearchable synthetic text.",
) -> Path:
    path = data_root / relative_path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        f'---\ntitle: "{title}"\ntags: "{tags}"\ndoc_id: "{doc_id}"\nprivate_hint: "frontmatter only"\n---\n{body}\n',
        encoding="utf-8",
    )
    return path


def _service(tmp_path: Path) -> RagService:
    return RagService(
        config=AppConfig(
            rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
            vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
        )
    )


def test_contract_fixtures_validate_against_versioned_schemas() -> None:
    root = Path("schemas/karte-ephy/v1")
    proposal_schema = json.loads((root / "proposal.schema.json").read_text(encoding="utf-8"))
    receipt_schema = json.loads((root / "receipt.schema.json").read_text(encoding="utf-8"))
    create_payload = json.loads((root / "fixtures/create-proposal.json").read_text(encoding="utf-8"))
    append_payload = json.loads((root / "fixtures/append-proposal.json").read_text(encoding="utf-8"))
    consultation_payload = json.loads((root / "fixtures/consultation-proposal.json").read_text(encoding="utf-8"))
    receipt_payload = json.loads((root / "fixtures/accepted-receipt.json").read_text(encoding="utf-8"))
    invalid_payload = json.loads((root / "fixtures/invalid-traversal-proposal.json").read_text(encoding="utf-8"))

    jsonschema.validate(create_payload, proposal_schema, format_checker=jsonschema.FormatChecker())
    jsonschema.validate(append_payload, proposal_schema, format_checker=jsonschema.FormatChecker())
    jsonschema.validate(consultation_payload, proposal_schema, format_checker=jsonschema.FormatChecker())
    jsonschema.validate(receipt_payload, receipt_schema, format_checker=jsonschema.FormatChecker())
    KarteChangeProposal.model_validate(create_payload)
    KarteChangeProposal.model_validate(append_payload)
    consultation = KarteChangeProposal.model_validate(consultation_payload)
    with pytest.raises(ValueError, match="consultation"):
        consultation.require_publishable()
    KarteReceipt.model_validate(receipt_payload)
    with pytest.raises((jsonschema.ValidationError, ValueError)):
        jsonschema.validate(invalid_payload, proposal_schema, format_checker=jsonschema.FormatChecker())


def test_read_only_adapter_parses_body_and_reports_invalid_sources(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    content_root = data_root / "content"
    content_root.mkdir(parents=True)
    note = _write_document(data_root, "content/notes/valid.md", doc_id="doc:valid")
    (content_root / "ignored.txt").write_text("not indexed", encoding="utf-8")
    (content_root / "invalid.md").write_text("---\ntitle: [broken\n---\nbody", encoding="utf-8")
    _write_document(data_root, "content/duplicate-a.md", doc_id="doc:duplicate")
    _write_document(data_root, "content/duplicate-b.md", doc_id="doc:duplicate")
    outside = tmp_path / "outside.md"
    outside.write_text("outside", encoding="utf-8")
    os.symlink(outside, content_root / "escape.md")
    os.symlink(content_root / "missing.md", content_root / "broken.md")

    result = KarteSourceAdapter(data_root).scan()

    assert [document.doc_id for document in result.documents] == ["doc:valid"]
    document = result.documents[0]
    assert document.relative_path == "content/notes/valid.md"
    assert document.tags == ["fixture", "karte"]
    assert "frontmatter only" not in document.body
    assert document.sha256 == hashlib.sha256(note.read_bytes()).hexdigest()
    assert {issue.code for issue in result.issues} >= {
        "invalid_frontmatter",
        "duplicate_doc_id",
        "symlink_escape",
        "broken_symlink",
    }


def test_adapter_rejects_traversal_and_karte_ingest_does_not_copy(tmp_path: Path, monkeypatch) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    _write_document(data_root, "content/valid.md", doc_id="doc:valid")
    lw_data = tmp_path / "LW_data"
    monkeypatch.setenv("LW_DATA_ROOT", str(lw_data))
    adapter = KarteSourceAdapter(data_root)
    service = _service(tmp_path)

    with pytest.raises(ValueError):
        adapter.read_document("content/../outside.md")
    result = service.ingest_karte(adapter, project="karte")
    chunks = service._vector_store.load_chunks(project="karte")  # noqa: SLF001

    assert result["copied_files"] == []
    assert not lw_data.exists()
    assert chunks
    assert chunks[0].doc_id == "doc:valid"
    assert chunks[0].relative_path == "content/valid.md"
    assert "frontmatter only" not in chunks[0].chunk_text


def test_stable_doc_id_rename_updates_path_without_new_chunk_identity(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    original = _write_document(data_root, "content/original.md", doc_id="doc:rename")
    adapter = KarteSourceAdapter(data_root)
    service = _service(tmp_path)
    service.ingest_karte(adapter)
    before = service._vector_store.load_chunks(project="karte")  # noqa: SLF001
    renamed = data_root / "content/renamed.md"
    original.rename(renamed)

    result = service.ingest_karte(adapter)
    after = service._vector_store.load_chunks(project="karte")  # noqa: SLF001

    assert result["renamed_documents"] == [
        {"doc_id": "doc:rename", "from": "content/original.md", "to": "content/renamed.md"}
    ]
    assert {chunk.chunk_id for chunk in before} == {chunk.chunk_id for chunk in after}
    assert {chunk.relative_path for chunk in after} == {"content/renamed.md"}


def test_watcher_emits_incremental_changes_with_bounded_history_and_recovers(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    first = _write_document(data_root, "content/first.md", doc_id="doc:first", body="# First\n\none")
    adapter = KarteSourceAdapter(data_root)
    service = _service(tmp_path)
    watcher = KarteWatchService(
        adapter=adapter,
        rag_service=service,
        interval_seconds=0.01,
        debounce_seconds=0,
        max_events=3,
    )
    watcher.full_rescan()

    _write_document(data_root, "content/first.md", doc_id="doc:first", body="# First\n\ntwo")
    assert [event["operation"] for event in watcher.poll_once()] == ["update"]
    renamed = data_root / "content/renamed.md"
    first.rename(renamed)
    assert [event["operation"] for event in watcher.poll_once()] == ["rename"]
    _write_document(data_root, "content/second.md", doc_id="doc:second")
    assert [event["operation"] for event in watcher.poll_once()] == ["create"]
    renamed.unlink()
    assert [event["operation"] for event in watcher.poll_once()] == ["delete"]

    assert len(watcher.events()) == 3
    assert {chunk.doc_id for chunk in service._vector_store.load_chunks(project="karte")} == {"doc:second"}  # noqa: SLF001
    recovered = watcher.full_rescan()
    assert recovered["indexed_documents"] == 1
    watcher.start()
    time.sleep(0.03)
    watcher.stop()
    assert watcher.health()["running"] is False
    assert watcher.health()["generation"] == 1


def test_outbox_atomic_publish_is_idempotent_and_never_touches_content(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    canonical = _write_document(data_root, "content/canonical.md", doc_id="doc:canonical")
    before = canonical.read_bytes()
    proposal = KarteChangeProposal.model_validate_json(
        Path("schemas/karte-ephy/v1/fixtures/create-proposal.json").read_text(encoding="utf-8")
    )
    outbox = KarteOutbox(data_root)

    first = outbox.publish(proposal)
    second = outbox.publish(proposal)

    assert first.state == second.state == "pending"
    assert first.path == second.path
    assert canonical.read_bytes() == before
    assert not list(outbox.pending_dir.glob("*.tmp"))
    changed = proposal.model_copy(update={"proposed_body": "different"})
    with pytest.raises(ValueError):
        outbox.publish(changed)


def test_outbox_refuses_unresolved_placement_consultation(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    proposal = KarteChangeProposal.model_validate_json(
        Path("schemas/karte-ephy/v1/fixtures/consultation-proposal.json").read_text(encoding="utf-8")
    )
    outbox = KarteOutbox(data_root)

    with pytest.raises(ValueError, match="consultation"):
        outbox.publish(proposal)

    assert not list(outbox.pending_dir.glob("*.json"))


def test_outbox_reads_receipts_and_rejects_symlink_escape(tmp_path: Path) -> None:
    data_root = tmp_path / "karte_data"
    (data_root / "content").mkdir(parents=True)
    outbox = KarteOutbox(data_root)
    receipt_path = outbox.receipts_dir / "candidate-append-001.json"
    receipt_path.write_text(
        Path("schemas/karte-ephy/v1/fixtures/accepted-receipt.json").read_text(encoding="utf-8"),
        encoding="utf-8",
    )
    (outbox.receipts_dir / ".partial.tmp").write_text("{", encoding="utf-8")
    assert outbox.read_receipt("candidate-append-001").result == "accepted"
    assert len(outbox.list_receipts()) == 1

    outside_receipt = tmp_path / "outside-receipt.json"
    outside_receipt.write_text("{}", encoding="utf-8")
    os.symlink(outside_receipt, outbox.receipts_dir / "candidate-create-001.json")
    with pytest.raises(ValueError):
        outbox.list_receipts()

    escaped_root = tmp_path / "escaped-data"
    (escaped_root / "content").mkdir(parents=True)
    outside = tmp_path / "outside"
    outside.mkdir()
    os.symlink(outside, escaped_root / ".mdsys")
    with pytest.raises(ValueError):
        KarteOutbox(escaped_root)
