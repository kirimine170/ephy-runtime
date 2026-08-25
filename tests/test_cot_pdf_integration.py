from pathlib import Path

import pytest

from packages.config_core.loader import AppConfig, RagConfig, ROOT_DIR, VectorDBConfig
from packages.rag_core.loaders import load_document_sections
from packages.rag_core.schemas import IngestRequest, SearchRequest
from packages.rag_core.service import RagService


def _managed_cot_paper() -> Path | None:
    matches = sorted((ROOT_DIR.parent / "EPHY_data" / "papers").glob("**/2201.11903v6.pdf"))
    return matches[0] if matches else None


def test_real_cot_pdf_extracts_and_is_searchable(monkeypatch, tmp_path) -> None:
    paper = _managed_cot_paper()
    if paper is None:
        pytest.skip("managed CoT paper is not available in EPHY_data")

    sections = load_document_sections(paper)
    assert len(sections) == 43
    assert sections[0][0] == ["Page 1"]
    assert "Chain-of-Thought Prompting Elicits Reasoning" in sections[0][1]

    monkeypatch.setenv("EPHY_RUNTIME_DATA_ROOT", str(tmp_path / "EPHY_data"))
    service = RagService(
        config=AppConfig(
            rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=64),
            vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
        )
    )
    ingest = service.ingest(IngestRequest(paths=[str(paper)], project="papers", tags=["cot"]))
    result = service.search(SearchRequest(query="CoT chain of thought prompting", project="papers", top_k=5))

    assert ingest["indexed_documents"] == 1
    assert ingest["indexed_chunks"] == 147
    assert result["results"]
    assert any(
        item["heading_path"] == ["Page 1"]
        and "Chain-of-Thought Prompting Elicits Reasoning" in item["chunk_text"]
        for item in result["results"]
    )
    assert result["results"][0]["source_path"].startswith(str((tmp_path / "EPHY_data").resolve()))
    assert result["results"][0]["original_source_path"] == str(paper.resolve())
