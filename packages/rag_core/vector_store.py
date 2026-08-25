from __future__ import annotations

import math
import os
from dataclasses import dataclass, field
from pathlib import Path

import httpx

from packages.config_core.loader import ROOT_DIR, VectorDBConfig
from .schemas import IndexedChunk, SearchResult
from .store import JsonChunkStore


class VectorStore:
    def replace_for_paths(self, paths: set[str], chunks: list[IndexedChunk], vector_size: int) -> int:
        raise NotImplementedError

    def search(self, query_vector: list[float], project: str | None, source_path: str | None, tags: list[str], top_k: int) -> list[SearchResult]:
        raise NotImplementedError

    def load_chunks(self, project: str | None = None) -> list[IndexedChunk]:
        raise NotImplementedError


@dataclass
class ResilientVectorStore(VectorStore):
    primary: VectorStore
    fallback: LocalJsonVectorStore

    def replace_for_paths(self, paths: set[str], chunks: list[IndexedChunk], vector_size: int) -> int:
        fallback_count = self.fallback.replace_for_paths(paths=paths, chunks=chunks, vector_size=vector_size)
        try:
            return self.primary.replace_for_paths(paths=paths, chunks=chunks, vector_size=vector_size)
        except (httpx.HTTPError, RuntimeError):
            return fallback_count

    def search(self, query_vector: list[float], project: str | None, source_path: str | None, tags: list[str], top_k: int) -> list[SearchResult]:
        try:
            return self.primary.search(
                query_vector=query_vector,
                project=project,
                source_path=source_path,
                tags=tags,
                top_k=top_k,
            )
        except (httpx.HTTPError, RuntimeError):
            return self.fallback.search(
                query_vector=query_vector,
                project=project,
                source_path=source_path,
                tags=tags,
                top_k=top_k,
            )

    def load_chunks(self, project: str | None = None) -> list[IndexedChunk]:
        try:
            return self.primary.load_chunks(project=project)
        except (httpx.HTTPError, RuntimeError):
            return self.fallback.load_chunks(project=project)


@dataclass
class LocalJsonVectorStore(VectorStore):
    store: JsonChunkStore

    def replace_for_paths(self, paths: set[str], chunks: list[IndexedChunk], vector_size: int) -> int:
        existing = self.store.load()
        retained = [
            chunk
            for chunk in existing
            if not any(_path_is_within(chunk.source_path, path) for path in paths)
        ]
        stored_chunks = retained + chunks
        self.store.save(stored_chunks)
        return len(stored_chunks)

    def search(self, query_vector: list[float], project: str | None, source_path: str | None, tags: list[str], top_k: int) -> list[SearchResult]:
        chunks = self.store.load()
        required_tags = {tag for tag in tags if tag}
        filtered = [
            chunk
            for chunk in chunks
            if (project is None or chunk.project == project)
            and (source_path is None or chunk.source_path == source_path)
            and (not required_tags or required_tags.issubset(set(chunk.tags)))
        ]
        ranked = [
            build_search_result(chunk=chunk, score=cosine_similarity(query_vector, chunk.embedding or []))
            for chunk in filtered
        ]
        ranked = [item for item in ranked if item.score > 0]
        ranked.sort(key=lambda item: item.score, reverse=True)
        return ranked[:top_k]

    def load_chunks(self, project: str | None = None) -> list[IndexedChunk]:
        chunks = self.store.load()
        if project is None:
            return chunks
        return [chunk for chunk in chunks if chunk.project == project]


@dataclass
class QdrantVectorStore(VectorStore):
    config: VectorDBConfig
    client: httpx.Client = field(default_factory=lambda: httpx.Client(timeout=30.0))

    def replace_for_paths(self, paths: set[str], chunks: list[IndexedChunk], vector_size: int) -> int:
        self._ensure_collection(vector_size=vector_size)
        existing_ids = self._find_point_ids_by_path_prefix(paths)
        if existing_ids:
            self._delete_points(existing_ids)
        if chunks:
            self._upsert_chunks(chunks)
        return self._count_points()

    def search(self, query_vector: list[float], project: str | None, source_path: str | None, tags: list[str], top_k: int) -> list[SearchResult]:
        body: dict = {
            "query": query_vector,
            "limit": top_k,
            "with_payload": True,
        }
        filter_must = []
        if project:
            filter_must.append({"key": "project", "match": {"value": project}})
        if source_path:
            filter_must.append({"key": "source_path", "match": {"value": source_path}})
        for tag in tags:
            if tag:
                filter_must.append({"key": "tags", "match": {"value": tag}})
        if filter_must:
            body["filter"] = {"must": filter_must}

        response = self.client.post(self._url("/points/query"), json=body)
        response.raise_for_status()
        result = response.json().get("result", {})
        results = result.get("points", []) if isinstance(result, dict) else result
        return [
            build_search_result_from_payload(
                payload=item.get("payload", {}),
                score=float(item.get("score", 0.0)),
            )
            for item in results
        ]

    def load_chunks(self, project: str | None = None) -> list[IndexedChunk]:
        body: dict = {
            "limit": 256,
            "with_payload": True,
            "with_vector": False,
        }
        if project:
            body["filter"] = {"must": [{"key": "project", "match": {"value": project}}]}

        chunks: list[IndexedChunk] = []
        offset = None
        while True:
            request_body = dict(body)
            if offset is not None:
                request_body["offset"] = offset

            response = self.client.post(self._url("/points/scroll"), json=request_body)
            response.raise_for_status()
            result = response.json().get("result", {})
            points = result.get("points", [])
            chunks.extend(build_indexed_chunk_from_payload(point.get("payload", {})) for point in points)
            offset = result.get("next_page_offset")
            if offset is None:
                break

        return chunks

    def _ensure_collection(self, vector_size: int) -> None:
        response = self.client.get(self._url(""))
        if response.status_code == 200:
            return
        if response.status_code != 404:
            response.raise_for_status()
        create_response = self.client.put(
            self._url(""),
            json={"vectors": {"size": vector_size, "distance": "Cosine"}},
        )
        create_response.raise_for_status()

    def _find_point_ids_by_path_prefix(self, paths: set[str]) -> list[str]:
        if not paths:
            return []

        point_ids: list[str] = []
        offset = None
        while True:
            body: dict = {
                "limit": 256,
                "with_payload": ["source_path"],
                "with_vector": False,
            }
            if offset is not None:
                body["offset"] = offset

            response = self.client.post(self._url("/points/scroll"), json=body)
            response.raise_for_status()
            result = response.json().get("result", {})
            points = result.get("points", [])
            for point in points:
                source_path = str(point.get("payload", {}).get("source_path", ""))
                if any(_path_is_within(source_path, path) for path in paths):
                    point_ids.append(str(point.get("id")))
            offset = result.get("next_page_offset")
            if offset is None:
                break

        return point_ids

    def _delete_points(self, point_ids: list[str]) -> None:
        response = self.client.post(
            self._url("/points/delete?wait=true"),
            json={"points": point_ids},
        )
        response.raise_for_status()

    def _upsert_chunks(self, chunks: list[IndexedChunk]) -> None:
        points = []
        for chunk in chunks:
            points.append(
                {
                    "id": chunk.chunk_id,
                    "vector": chunk.embedding or [],
                    "payload": chunk.model_dump(exclude={"embedding"}),
                }
            )
        response = self.client.put(
            self._url("/points?wait=true"),
            json={"points": points},
        )
        response.raise_for_status()

    def _count_points(self) -> int:
        response = self.client.post(self._url("/points/count"), json={"exact": True})
        response.raise_for_status()
        return int(response.json().get("result", {}).get("count", 0))

    def _url(self, suffix: str) -> str:
        base = (self.config.url or "http://localhost:6333").rstrip("/")
        collection = self.config.collection
        return f"{base}/collections/{collection}{suffix}"


def build_vector_store(config: VectorDBConfig, client: httpx.Client | None = None) -> VectorStore:
    store_path_override = os.getenv("EPHY_RUNTIME_INDEX_PATH", "").strip()
    use_default_store = config.store_path == "data/index/local_docs.json"
    store_path = Path(store_path_override) if store_path_override and use_default_store else ROOT_DIR / config.store_path
    local_store = JsonChunkStore(store_path, prune_missing_sources=use_default_store)
    if config.provider == "local_json":
        return LocalJsonVectorStore(store=local_store)
    if config.provider == "qdrant":
        return ResilientVectorStore(
            primary=QdrantVectorStore(config=config, client=client or httpx.Client(timeout=30.0)),
            fallback=LocalJsonVectorStore(store=local_store),
        )
    raise ValueError(f"Unsupported vector db provider '{config.provider}'")


def build_search_result(chunk: IndexedChunk, score: float) -> SearchResult:
    return SearchResult(
        chunk_id=chunk.chunk_id,
        source_path=chunk.source_path,
        original_source_path=chunk.original_source_path,
        heading_path=chunk.heading_path,
        project=chunk.project,
        tags=chunk.tags,
        chunk_text=chunk.chunk_text,
        score=round(score, 4),
    )


def build_search_result_from_payload(payload: dict, score: float) -> SearchResult:
    return SearchResult(
        chunk_id=str(payload.get("chunk_id", "")),
        source_path=str(payload.get("source_path", "")),
        original_source_path=payload.get("original_source_path"),
        heading_path=list(payload.get("heading_path", [])),
        project=payload.get("project"),
        tags=list(payload.get("tags", [])),
        chunk_text=str(payload.get("chunk_text", "")),
        score=round(score, 4),
    )


def build_indexed_chunk_from_payload(payload: dict) -> IndexedChunk:
    return IndexedChunk(
        chunk_id=str(payload.get("chunk_id", "")),
        source_path=str(payload.get("source_path", "")),
        original_source_path=payload.get("original_source_path"),
        heading_path=list(payload.get("heading_path", [])),
        project=payload.get("project"),
        tags=list(payload.get("tags", [])),
        chunk_text=str(payload.get("chunk_text", "")),
        hash=str(payload.get("hash", "")),
        embedding=None,
    )


def cosine_similarity(left: list[float], right: list[float]) -> float:
    if not left or not right or len(left) != len(right):
        return 0.0
    numerator = sum(a * b for a, b in zip(left, right))
    left_norm = math.sqrt(sum(a * a for a in left))
    right_norm = math.sqrt(sum(b * b for b in right))
    if left_norm == 0 or right_norm == 0:
        return 0.0
    return numerator / (left_norm * right_norm)


def _path_is_within(candidate: str, root: str) -> bool:
    try:
        Path(candidate).resolve().relative_to(Path(root).resolve())
        return True
    except ValueError:
        return False
