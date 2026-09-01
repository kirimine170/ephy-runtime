from __future__ import annotations

import hashlib
import os
import re
import shutil
import uuid
from pathlib import Path

from packages.config_core.loader import AppConfig, ROOT_DIR
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, RequestMetadata
from packages.prompt_core.loader import PromptManager
from packages.router_core.router import ModelRouter
from packages.karte_core.source import KarteDocument, KarteSourceAdapter
from .chunker import chunk_text, split_markdown_sections
from .embedding import build_embedder
from .loaders import load_document_sections
from .reranker import build_reranker
from .schemas import IndexBrowseRequest, IndexProjectSummary, IndexSourceRequest, IndexSourceSummary, IndexedChunk, IngestRequest, RAGQueryRequest, SearchRequest
from .vector_store import build_vector_store


class RagService:
    _TEXT_EXTENSIONS = {
        ".md",
        ".markdown",
        ".txt",
        ".pdf",
        ".docx",
        ".html",
        ".htm",
        ".csv",
        ".tsv",
        ".json",
    }
    _CODE_EXTENSIONS = {
        ".py",
        ".js",
        ".ts",
        ".tsx",
        ".jsx",
        ".go",
        ".rs",
        ".java",
        ".c",
        ".cc",
        ".cpp",
        ".cxx",
        ".h",
        ".hpp",
        ".cs",
        ".rb",
        ".php",
        ".swift",
        ".kt",
        ".kts",
        ".scala",
        ".sql",
        ".sh",
        ".bash",
        ".zsh",
        ".yaml",
        ".yml",
        ".toml",
        ".ini",
        ".cfg",
        ".env",
        ".dockerfile",
    }
    _EXCLUDED_DIRS = {
        ".git",
        ".venv",
        "__pycache__",
        "node_modules",
        "dist",
        "build",
        ".next",
        ".pytest_cache",
        ".mypy_cache",
    }
    _LW_DATA_DIRNAME = "LW_data"
    _CHAT_GROUNDING_MIN_SCORE = 0.2
    _CHAT_GROUNDING_MAX_SOURCES = 5
    _EMBED_BATCH_SIZE = 16

    def __init__(self, config: AppConfig, *, prompt_manager: PromptManager | None = None) -> None:
        self._config = config
        self._prompt_manager = prompt_manager or PromptManager()
        self._embedder = build_embedder(config.rag, config.models)
        self._reranker = build_reranker(config.rag, config.models)
        self._vector_store = build_vector_store(config.vector_db)

    def ingest(self, payload: IngestRequest) -> dict:
        if not payload.paths:
            raise ValueError("At least one path is required")

        replace_roots: set[str] = set()
        indexed_chunks: list[IndexedChunk] = []
        indexed_documents = 0
        copied_files: list[str] = []

        for raw_path in payload.paths:
            path = Path(raw_path)
            if not path.exists():
                raise ValueError(f"Path does not exist: {raw_path}")
            copied_pairs = self._copy_ingest_source(path=path, recursive=payload.recursive, project=payload.project)
            replace_roots.add(str(path.resolve()))
            replace_roots.add(str(self._build_lw_data_destination(path.resolve(), payload.project).resolve()))
            for file_path, original_source_path in copied_pairs:
                indexed_documents += 1
                copied_files.append(str(file_path.resolve()))
                indexed_chunks.extend(
                    self._index_file(
                        file_path=file_path,
                        project=payload.project,
                        tags=payload.tags,
                        original_source_path=original_source_path,
                    )
                )

        self._embed_indexed_chunks(indexed_chunks)

        vector_size = len(indexed_chunks[0].embedding or []) if indexed_chunks else self._config.rag.embedding_dimensions
        total_chunks = self._vector_store.replace_for_paths(replace_roots, indexed_chunks, vector_size=vector_size)
        return {
            "indexed_documents": indexed_documents,
            "indexed_chunks": len(indexed_chunks),
            "total_chunks": total_chunks,
            "collection": self._config.vector_db.collection,
            "provider": self._config.vector_db.provider,
            "lw_data_root": str(self._lw_data_root().resolve()),
            "copied_files": copied_files,
        }

    def ingest_karte(
        self,
        adapter: KarteSourceAdapter,
        *,
        project: str = "karte",
        tags: list[str] | None = None,
    ) -> dict:
        scan = adapter.scan()
        previous_paths = {
            chunk.doc_id: chunk.relative_path
            for chunk in self._vector_store.load_chunks(project=project)
            if chunk.doc_id and chunk.relative_path
        }
        result = self.sync_karte_documents(
            documents=scan.documents,
            replace_paths={str(adapter.content_root)},
            project=project,
            tags=tags or [],
        )
        result["issues"] = [issue.__dict__ for issue in scan.issues]
        result["renamed_documents"] = [
            {"doc_id": document.doc_id, "from": previous_paths[document.doc_id], "to": document.relative_path}
            for document in scan.documents
            if document.doc_id in previous_paths and previous_paths[document.doc_id] != document.relative_path
        ]
        result["content_root"] = str(adapter.content_root)
        result["copied_files"] = []
        return result

    def sync_karte_documents(
        self,
        *,
        documents: list[KarteDocument],
        replace_paths: set[str],
        project: str = "karte",
        tags: list[str] | None = None,
    ) -> dict:
        indexed_chunks: list[IndexedChunk] = []
        requested_tags = tags or []
        for document in documents:
            indexed_chunks.extend(self._index_karte_document(document, project=project, tags=requested_tags))
        self._embed_indexed_chunks(indexed_chunks)
        vector_size = len(indexed_chunks[0].embedding or []) if indexed_chunks else self._config.rag.embedding_dimensions
        total_chunks = self._vector_store.replace_for_paths(replace_paths, indexed_chunks, vector_size=vector_size)
        return {
            "indexed_documents": len(documents),
            "indexed_chunks": len(indexed_chunks),
            "total_chunks": total_chunks,
            "collection": self._config.vector_db.collection,
            "provider": self._config.vector_db.provider,
        }

    def search(self, payload: SearchRequest) -> dict:
        retrieval_query = self._expand_retrieval_query(payload.query)
        query_vector = self._embedder.embed(retrieval_query)
        top_k = payload.top_k or self._config.rag.top_k
        candidate_k = max(top_k, self._config.rag.rerank_k, self._config.rag.top_k)
        source_path = self._resolve_managed_source_path(payload.source_path, payload.project)
        ranked = self._vector_store.search(
            query_vector=query_vector,
            project=payload.project,
            source_path=source_path,
            tags=payload.tags,
            top_k=candidate_k,
        )
        reranked = self._reranker.rerank(retrieval_query, ranked, limit=top_k)
        return {"query": payload.query, "results": [result.model_dump() for result in reranked]}

    @staticmethod
    def _expand_retrieval_query(query: str) -> str:
        expansions: list[str] = []
        if re.search(r"(?<![A-Za-z0-9])cot(?![A-Za-z0-9])", query, flags=re.IGNORECASE):
            expansions.append("chain of thought prompting")
        if "思考の連鎖" in query:
            expansions.append("chain of thought prompting")
        if not expansions:
            return query
        return f"{query}\n{' '.join(dict.fromkeys(expansions))}"

    def search_grounding_sources(
        self,
        *,
        query: str,
        project: str | None = None,
        source_path: str | None = None,
        tags: list[str] | None = None,
        top_k: int | None = None,
    ) -> list[dict]:
        response = self.search(
            SearchRequest(
                query=query,
                project=project,
                source_path=source_path,
                tags=tags or [],
                top_k=top_k or self._config.rag.rerank_k,
            )
        )
        results = response.get("results", [])
        grounded = [
            item
            for item in results
            if float(item.get("score", 0.0)) >= self._CHAT_GROUNDING_MIN_SCORE
        ]
        return grounded[: self._CHAT_GROUNDING_MAX_SOURCES]

    def browse_index(self, payload: IndexBrowseRequest) -> dict:
        limit = max(1, min(payload.limit or 20, 200))
        chunks = self._vector_store.load_chunks(project=payload.project)
        filtered = chunks
        source_query = (payload.source_query or "").strip().lower()
        if source_query:
            filtered = [chunk for chunk in filtered if source_query in chunk.source_path.lower()]

        projects: dict[str, dict[str, any]] = {}
        sources: dict[str, dict[str, any]] = {}
        for chunk in filtered:
            project_key = chunk.project or "(default)"
            project_entry = projects.setdefault(project_key, {"project": project_key, "chunk_count": 0, "sources": set()})
            project_entry["chunk_count"] += 1
            project_entry["sources"].add(chunk.source_path)

            source_entry = sources.setdefault(
                chunk.source_path,
                {
                    "source_path": chunk.source_path,
                    "original_source_path": chunk.original_source_path,
                    "doc_id": chunk.doc_id,
                    "title": chunk.title,
                    "relative_path": chunk.relative_path,
                    "updated_at": chunk.updated_at,
                    "source_sha256": chunk.source_sha256,
                    "project": chunk.project,
                    "chunk_count": 0,
                    "sample_heading": " > ".join(chunk.heading_path) if chunk.heading_path else None,
                    "sample_text": chunk.chunk_text[:180],
                },
            )
            source_entry["chunk_count"] += 1

        project_summaries = [
            IndexProjectSummary(
                project=item["project"],
                chunk_count=item["chunk_count"],
                source_count=len(item["sources"]),
            ).model_dump()
            for item in projects.values()
        ]
        project_summaries.sort(key=lambda item: (-item["chunk_count"], item["project"]))

        source_summaries = [
            IndexSourceSummary(
                source_path=item["source_path"],
                original_source_path=item["original_source_path"],
                doc_id=item["doc_id"],
                title=item["title"],
                relative_path=item["relative_path"],
                updated_at=item["updated_at"],
                source_sha256=item["source_sha256"],
                project=item["project"],
                chunk_count=item["chunk_count"],
                sample_heading=item["sample_heading"],
                sample_text=item["sample_text"],
            ).model_dump()
            for item in sources.values()
        ]
        source_summaries.sort(key=lambda item: (-item["chunk_count"], item["source_path"]))

        preview_chunks = [
            chunk.model_dump(exclude={"embedding"})
            for chunk in filtered[:limit]
        ]

        return {
            "project_filter": payload.project,
            "source_query": payload.source_query,
            "total_chunks": len(chunks),
            "filtered_chunks": len(filtered),
            "projects": project_summaries,
            "sources": source_summaries,
            "chunks": preview_chunks,
        }

    def get_source_chunks(self, payload: IndexSourceRequest) -> dict:
        source_path = payload.source_path.strip()
        if not source_path:
            raise ValueError("source_path is required")

        limit = max(1, min(payload.limit or 100, 500))
        chunks = self._vector_store.load_chunks(project=payload.project)
        matching = [
            chunk
            for chunk in chunks
            if chunk.source_path == source_path or chunk.original_source_path == source_path
        ]

        return {
            "project_filter": payload.project,
            "source_path": source_path,
            "total_chunks": len(matching),
            "chunks": [chunk.model_dump(exclude={"embedding"}) for chunk in matching[:limit]],
        }

    def _resolve_managed_source_path(self, source_path: str | None, project: str | None) -> str | None:
        if not source_path:
            return None
        chunks = self._vector_store.load_chunks(project=project)
        for chunk in chunks:
            if chunk.source_path == source_path or chunk.original_source_path == source_path:
                return chunk.source_path
        return source_path

    async def query(
        self,
        payload: RAGQueryRequest,
        router: ModelRouter,
        adapter: LlamaCppChatAdapter,
    ) -> dict:
        search_results = self.search(
            SearchRequest(
                query=payload.query,
                project=payload.project,
                source_path=payload.source_path,
                tags=payload.tags,
                top_k=payload.top_k,
            )
        )
        sources = search_results["results"]
        if not payload.answer:
            return {"answer": None, "sources": sources}

        context = self._build_context(sources)
        chat_request = ChatCompletionRequest(
            model="auto",
            messages=self._prompt_manager.build_rag_messages(payload.query, context),
            metadata=RequestMetadata(mode="rag", project=payload.project),
            temperature=0.2,
        )
        decision = router.route_chat(chat_request)
        response = await adapter.create_chat_completion(model_config=decision.selected_model, request_payload=chat_request)
        return {"answer": self._extract_answer_text(response), "sources": sources, "raw_response": response}

    def _iter_files(self, path: Path, recursive: bool) -> list[Path]:
        if path.is_file():
            return [path] if self._is_supported(path) else []

        if not recursive:
            return [candidate for candidate in path.iterdir() if candidate.is_file() and self._is_supported(candidate)]

        files: list[Path] = []
        for root, dirnames, filenames in os.walk(path):
            dirnames[:] = [dirname for dirname in dirnames if dirname not in self._EXCLUDED_DIRS]
            root_path = Path(root)
            for filename in filenames:
                candidate = root_path / filename
                if self._is_supported(candidate):
                    files.append(candidate)
        return files

    @classmethod
    def _is_supported(cls, path: Path) -> bool:
        suffix = path.suffix.lower()
        name = path.name.lower()
        if suffix in cls._TEXT_EXTENSIONS or suffix in cls._CODE_EXTENSIONS:
            return True
        return name == "dockerfile"

    def _copy_ingest_source(
        self,
        path: Path,
        recursive: bool,
        project: str | None,
    ) -> list[tuple[Path, str]]:
        source_root = path.resolve()
        destination_root = self._build_lw_data_destination(source_root, project)
        if destination_root.exists():
            shutil.rmtree(destination_root)
        destination_root.mkdir(parents=True, exist_ok=True)

        copied_pairs: list[tuple[Path, str]] = []
        if source_root.is_file():
            if not self._is_supported(source_root):
                return copied_pairs
            destination_path = destination_root / source_root.name
            shutil.copy2(source_root, destination_path)
            copied_pairs.append((destination_path, str(source_root)))
            return copied_pairs

        files = self._iter_files(path=source_root, recursive=recursive)
        for file_path in files:
            relative_path = file_path.resolve().relative_to(source_root)
            destination_path = destination_root / relative_path
            destination_path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(file_path, destination_path)
            copied_pairs.append((destination_path, str(file_path.resolve())))
        return copied_pairs

    def _build_lw_data_destination(self, source_root: Path, project: str | None) -> Path:
        root_label = self._slugify_path_component(source_root.name or "root")
        source_hash = hashlib.sha1(str(source_root).encode("utf-8")).hexdigest()[:12]
        project_label = self._slugify_path_component(project or "default")
        return self._lw_data_root() / project_label / f"{root_label}-{source_hash}"

    @classmethod
    def _lw_data_root(cls) -> Path:
        override = os.getenv("LW_DATA_ROOT", "").strip()
        if override:
            return Path(override)
        return ROOT_DIR.parent / cls._LW_DATA_DIRNAME

    @staticmethod
    def _slugify_path_component(value: str) -> str:
        cleaned = []
        for char in value.strip().lower():
            if char.isalnum():
                cleaned.append(char)
            else:
                cleaned.append("-")
        slug = "".join(cleaned).strip("-")
        return slug or "item"

    def _index_file(
        self,
        file_path: Path,
        project: str | None,
        tags: list[str],
        original_source_path: str | None = None,
    ) -> list[IndexedChunk]:
        loaded_sections = load_document_sections(file_path)
        chunks: list[IndexedChunk] = []
        source_path = str(file_path.resolve())
        for section_index, (heading_path, content) in enumerate(loaded_sections):
            for chunk_index, text_chunk in enumerate(
                chunk_text(
                    content,
                    chunk_size=self._config.rag.chunk_size,
                    overlap=self._config.rag.chunk_overlap,
                )
            ):
                source_key = f"{source_path}::{section_index}::{chunk_index}::{text_chunk}"
                chunks.append(
                    IndexedChunk(
                        chunk_id=str(uuid.uuid5(uuid.NAMESPACE_URL, source_key)),
                        source_path=source_path,
                        original_source_path=original_source_path,
                        heading_path=heading_path,
                        project=project,
                        tags=tags,
                        chunk_text=text_chunk,
                        hash=hashlib.sha256(text_chunk.encode("utf-8")).hexdigest(),
                        embedding=None,
                    )
                )
        return chunks

    def _index_karte_document(
        self,
        document: KarteDocument,
        *,
        project: str,
        tags: list[str],
    ) -> list[IndexedChunk]:
        normalized_tags = list(dict.fromkeys([*document.tags, *tags]))
        chunks: list[IndexedChunk] = []
        for section_index, section in enumerate(split_markdown_sections(document.body)):
            for chunk_index, text_chunk in enumerate(
                chunk_text(
                    section.content,
                    chunk_size=self._config.rag.chunk_size,
                    overlap=self._config.rag.chunk_overlap,
                )
            ):
                source_key = f"karte:{document.doc_id}:{section_index}:{chunk_index}:{text_chunk}"
                chunks.append(
                    IndexedChunk(
                        chunk_id=str(uuid.uuid5(uuid.NAMESPACE_URL, source_key)),
                        source_path=str(document.absolute_path),
                        original_source_path=None,
                        doc_id=document.doc_id,
                        title=document.title,
                        relative_path=document.relative_path,
                        updated_at=document.updated_at,
                        source_sha256=document.sha256,
                        heading_path=section.heading_path,
                        project=project,
                        tags=normalized_tags,
                        chunk_text=text_chunk,
                        hash=hashlib.sha256(text_chunk.encode("utf-8")).hexdigest(),
                        embedding=None,
                    )
                )
        return chunks

    def _embed_indexed_chunks(self, chunks: list[IndexedChunk]) -> None:
        for start in range(0, len(chunks), self._EMBED_BATCH_SIZE):
            batch = chunks[start : start + self._EMBED_BATCH_SIZE]
            texts = [self._compose_embedding_text(chunk.heading_path, chunk.chunk_text) for chunk in batch]
            embeddings = self._embedder.embed_many(texts)
            if len(embeddings) != len(batch):
                raise RuntimeError(
                    f"Embedding backend returned {len(embeddings)} vectors for a batch of {len(batch)} chunks"
                )
            for chunk, embedding in zip(batch, embeddings):
                chunk.embedding = embedding

    @staticmethod
    def _compose_embedding_text(heading_path: list[str], chunk_text: str) -> str:
        heading = " > ".join(heading_path)
        if heading:
            return f"{heading}\n{chunk_text}"
        return chunk_text

    @staticmethod
    def _build_context(sources: list[dict]) -> str:
        if not sources:
            return "No matching context found."
        parts: list[str] = []
        for index, source in enumerate(sources, start=1):
            heading = " > ".join(source.get("heading_path", [])) or "(root)"
            parts.append(
                f"[{index}] source_path={source.get('source_path')}\n"
                f"original_source_path={source.get('original_source_path') or '(managed source)'}\n"
                f"heading_path={heading}\ntext={source.get('chunk_text')}"
            )
        return "\n\n".join(parts)

    @staticmethod
    def _extract_answer_text(response: dict) -> str | None:
        choices = response.get("choices")
        if not isinstance(choices, list) or not choices:
            return None
        message = choices[0].get("message", {})
        content = message.get("content")
        if isinstance(content, str):
            return content
        return None
