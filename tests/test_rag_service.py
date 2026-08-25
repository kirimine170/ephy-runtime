import asyncio
from pathlib import Path
from zipfile import ZipFile

import pytest

from packages.config_core.loader import AppConfig, RagConfig, VectorDBConfig
import packages.rag_core.service as rag_service_module
from packages.rag_core.schemas import IndexBrowseRequest, IndexSourceRequest, IngestRequest, RAGQueryRequest, SearchRequest
from packages.rag_core.service import RagService


def test_rag_service_ingest_and_search(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Research\n\nQdrant stores vector search data.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="lab"))
    results = service.search(SearchRequest(query="vector search", project="lab", top_k=5))

    assert ingest["indexed_documents"] == 1
    assert ingest["indexed_chunks"] >= 1
    assert results["results"]
    assert results["results"][0]["project"] == "lab"


def test_rag_service_search_can_filter_by_source_path(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Research\n\nQdrant stores vector search data.", encoding="utf-8")
    plan = docs_dir / "plan.md"
    plan.write_text("# Plan\n\nAnother unrelated note.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(docs_dir)], project="lab"))

    results = service.search(SearchRequest(query="vector search", project="lab", source_path=str(notes.resolve()), top_k=5))

    assert results["results"]
    assert all(item["original_source_path"] == str(notes.resolve()) for item in results["results"])


def test_rag_service_search_can_filter_by_tags(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Research\n\nQdrant stores vector search data.", encoding="utf-8")
    memo = docs_dir / "memo.md"
    memo.write_text("# Memo\n\nEmployee roster memo.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(notes)], project="lab", recursive=False, tags=["research", "vector"]))
    service.ingest(IngestRequest(paths=[str(memo)], project="lab", recursive=False, tags=["npo"]))

    results = service.search(SearchRequest(query="vector search", project="lab", tags=["research"], top_k=5))

    assert results["results"]
    assert all("research" in item["tags"] for item in results["results"])
    assert all(item["original_source_path"] == str(notes.resolve()) for item in results["results"])


def test_rag_service_rejects_ingest_when_remote_embedding_backend_is_unavailable(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Research\n\nChain-of-thought prompting improves complex reasoning.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(
            embedding_provider="openai_compatible",
            embedding_model_alias="embedding",
            embedding_dimensions=32,
        ),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
        models={
            "embedding": {
                "provider": "openai_compatible",
                "model": "qwen3-embedding",
                "base_url": "http://127.0.0.1:9/v1",
            }
        },
    )
    service = RagService(config=config)
    with pytest.raises(RuntimeError, match="Embedding backend request failed"):
        service.ingest(IngestRequest(paths=[str(docs_dir)], project="fallback"))


def test_rag_service_can_find_chain_of_thought_paper_content(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    paper = docs_dir / "cot-paper.md"
    paper.write_text(
        (
            "# Chain-of-Thought Prompting Elicits Reasoning in Large Language Models\n\n"
            "We explore how generating a chain of thought, a series of intermediate reasoning steps, "
            "improves the ability of large language models to perform complex reasoning."
        ),
        encoding="utf-8",
    )

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(docs_dir)], project="papers"))
    results = service.search(SearchRequest(query="chain-of-thought prompting paper", project="papers", top_k=5))

    assert results["results"]
    top = results["results"][0]
    assert top["original_source_path"] == str(paper.resolve())
    assert "Chain-of-Thought Prompting Elicits Reasoning in Large Language Models" in " > ".join(top["heading_path"])


def test_rag_service_expands_cot_abbreviation_for_retrieval() -> None:
    assert RagService._expand_retrieval_query("CoTについて教えて") == (
        "CoTについて教えて\nchain of thought prompting"
    )
    assert RagService._expand_retrieval_query("通常の質問") == "通常の質問"


def test_rag_service_ingests_pdf_via_loader(monkeypatch, tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    paper = docs_dir / "paper.pdf"
    paper.write_bytes(b"%PDF-1.4\n")

    def fake_load_document_sections(file_path):
        assert file_path.name == paper.name
        return [([], "PDF based meeting notes about employee roster.")]

    monkeypatch.setattr(rag_service_module, "load_document_sections", fake_load_document_sections)

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="pdf"))
    results = service.search(SearchRequest(query="employee roster", project="pdf", top_k=5))

    assert ingest["indexed_documents"] == 1
    assert ingest["indexed_chunks"] >= 1
    assert results["results"]
    assert results["results"][0]["original_source_path"] == str(paper.resolve())


def test_rag_service_ingests_directory_with_markdown_and_pdf(monkeypatch, tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Meeting\n\nEmployee roster was confirmed.", encoding="utf-8")
    paper = docs_dir / "paper.pdf"
    paper.write_bytes(b"%PDF-1.4\n")

    original_loader = rag_service_module.load_document_sections

    def wrapped_loader(file_path):
        if file_path.name == paper.name:
            return [([], "Board paper covering employee roster confirmation.")]
        return original_loader(file_path)

    monkeypatch.setattr(rag_service_module, "load_document_sections", wrapped_loader)

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="mixed-docs"))
    results = service.search(SearchRequest(query="employee roster confirmation", project="mixed-docs", top_k=10))

    source_paths = {item["original_source_path"] for item in results["results"]}

    assert ingest["indexed_documents"] == 2
    assert ingest["indexed_chunks"] >= 2
    assert str(notes.resolve()) in source_paths
    assert str(paper.resolve()) in source_paths


def test_rag_service_copies_ingested_files_into_ephy_data(monkeypatch, tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    note = docs_dir / "note.md"
    note.write_text("# Note\n\nEmployee roster", encoding="utf-8")
    ephy_data_root = tmp_path / "EPHY_data"
    monkeypatch.setenv("EPHY_RUNTIME_DATA_ROOT", str(ephy_data_root))

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="archive"))

    copied_files = ingest["copied_files"]
    assert ingest["ephy_data_root"] == str(ephy_data_root.resolve())
    assert ingest["indexed_documents"] == 1
    assert len(copied_files) == 1
    assert copied_files[0].startswith(str(ephy_data_root.resolve()))
    assert Path(copied_files[0]).read_text(encoding="utf-8") == note.read_text(encoding="utf-8")


def test_rag_service_ingests_docx(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    report = docs_dir / "report.docx"
    with ZipFile(report, "w") as archive:
        archive.writestr(
            "word/document.xml",
            """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Employee roster was reviewed in the board meeting.</w:t></w:r></w:p>
  </w:body>
</w:document>""",
        )

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="docx"))
    results = service.search(SearchRequest(query="employee roster", project="docx", top_k=5))

    assert ingest["indexed_documents"] == 1
    assert ingest["indexed_chunks"] >= 1
    assert results["results"]
    assert results["results"][0]["original_source_path"] == str(report.resolve())


def test_rag_service_ingests_html_csv_tsv_and_json(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()

    page = docs_dir / "notes.html"
    page.write_text(
        "<html><body><h1>Meeting</h1><p>Employee roster review was completed.</p></body></html>",
        encoding="utf-8",
    )
    csv_file = docs_dir / "roster.csv"
    csv_file.write_text("name,role\nNagao,board member\n", encoding="utf-8")
    tsv_file = docs_dir / "tasks.tsv"
    tsv_file.write_text("task\tstatus\nreview roster\tdone\n", encoding="utf-8")
    json_file = docs_dir / "summary.json"
    json_file.write_text('{"topic":"employee roster","status":"confirmed"}', encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(docs_dir)], project="mixed"))
    results = service.search(SearchRequest(query="employee roster confirmed", project="mixed", top_k=10))

    source_paths = {item["original_source_path"] for item in results["results"]}

    assert ingest["indexed_documents"] == 4
    assert ingest["indexed_chunks"] >= 4
    assert str(page.resolve()) in source_paths
    assert str(csv_file.resolve()) in source_paths
    assert str(tsv_file.resolve()) in source_paths
    assert str(json_file.resolve()) in source_paths


def test_rag_service_ingests_git_repository_like_tree_and_skips_noise_dirs(tmp_path) -> None:
    repo_dir = tmp_path / "repo"
    repo_dir.mkdir()
    (repo_dir / ".git").mkdir()
    (repo_dir / "src").mkdir()
    (repo_dir / "node_modules").mkdir()
    (repo_dir / "dist").mkdir()

    app_file = repo_dir / "src" / "main.py"
    app_file.write_text(
        "def build_router():\n    return 'router plan and rag query support'\n",
        encoding="utf-8",
    )
    readme_file = repo_dir / "README.md"
    readme_file.write_text("# Repo\n\nThis repository contains gateway and routing code.", encoding="utf-8")
    ignored_js = repo_dir / "node_modules" / "index.js"
    ignored_js.write_text("console.log('ignore me')\n", encoding="utf-8")
    ignored_bundle = repo_dir / "dist" / "bundle.js"
    ignored_bundle.write_text("compiled output\n", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    ingest = service.ingest(IngestRequest(paths=[str(repo_dir)], project="repo"))
    results = service.search(SearchRequest(query="router plan gateway code", project="repo", top_k=10))

    source_paths = {item["original_source_path"] for item in results["results"]}

    assert ingest["indexed_documents"] == 2
    assert str(app_file.resolve()) in source_paths
    assert str(readme_file.resolve()) in source_paths
    assert str(ignored_js.resolve()) not in source_paths
    assert str(ignored_bundle.resolve()) not in source_paths


def test_rag_service_browse_index_summarizes_projects_sources_and_chunks(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Meeting\n\nEmployee roster was confirmed.\n\nNext steps are documented here.", encoding="utf-8")
    plan = docs_dir / "plan.md"
    plan.write_text("# Plan\n\nResearch plan references employee roster work.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(docs_dir)], project="lab"))

    response = service.browse_index(IndexBrowseRequest(project="lab", source_query="notes", limit=5))

    assert response["total_chunks"] >= 2
    assert response["filtered_chunks"] >= 1
    assert response["projects"][0]["project"] == "lab"
    assert response["sources"][0]["original_source_path"] == str(notes.resolve())
    assert all("notes" in item["source_path"] for item in response["chunks"])


def test_rag_service_get_source_chunks_returns_exact_source(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Meeting\n\nEmployee roster was confirmed.\n\nNext steps are documented here.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(docs_dir)], project="lab"))

    response = service.get_source_chunks(IndexSourceRequest(project="lab", source_path=str(notes.resolve()), limit=10))

    assert response["source_path"] == str(notes.resolve())
    assert response["total_chunks"] >= 1
    assert all(item["original_source_path"] == str(notes.resolve()) for item in response["chunks"])


def test_rag_service_query_returns_answer_and_sources(tmp_path) -> None:
    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    notes = docs_dir / "notes.md"
    notes.write_text("# Meeting\n\nEmployee roster was confirmed.\n\nNagao will follow up.", encoding="utf-8")

    config = AppConfig(
        rag=RagConfig(embedding_provider="local_hash", embedding_dimensions=32),
        vector_db=VectorDBConfig(provider="local_json", store_path=str(tmp_path / "index.json")),
    )
    service = RagService(config=config)
    service.ingest(IngestRequest(paths=[str(docs_dir)], project="npo"))

    class StubRouter:
        def route_chat(self, request):
            return type("Decision", (), {"selected_model": object()})()

    class StubAdapter:
        async def create_chat_completion(self, *, model_config, request_payload):
            return {"choices": [{"message": {"content": "The employee roster was confirmed."}}]}

    response = asyncio.run(
        service.query(
            payload=RAGQueryRequest(query="employee roster", project="npo", top_k=3, answer=True),
            router=StubRouter(),
            adapter=StubAdapter(),
        )
    )

    assert "employee roster was confirmed" in response["answer"].lower()
    assert response["sources"]
    assert response["sources"][0]["original_source_path"] == str(notes.resolve())
