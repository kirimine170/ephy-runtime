from __future__ import annotations

import json
import os
import tempfile
from pathlib import Path

from .schemas import IndexedChunk
from packages.karte_core.protected import protected_chunk, remove_managed_copy


class JsonChunkStore:
    def __init__(self, path: Path, *, prune_missing_sources: bool = False) -> None:
        self._path = path
        self._prune_missing_sources = prune_missing_sources
        self._path.parent.mkdir(parents=True, exist_ok=True)

    def load(self) -> list[IndexedChunk]:
        if not self._path.exists():
            return []
        if self._path.is_symlink():
            raise ValueError("unsafe_chunk_cache")
        payload = json.loads(self._path.read_text(encoding="utf-8"))
        chunks = [IndexedChunk.model_validate(item) for item in payload]
        blocked = [chunk for chunk in chunks if protected_chunk(chunk)]
        if blocked:
            for chunk in blocked:
                remove_managed_copy(chunk)
            chunks = [chunk for chunk in chunks if chunk not in blocked]
            self.save(chunks)
        if not self._prune_missing_sources:
            return chunks
        return [chunk for chunk in chunks if Path(chunk.source_path).is_file()]

    def save(self, chunks: list[IndexedChunk]) -> None:
        chunks = [chunk for chunk in chunks if not protected_chunk(chunk)]
        payload = [chunk.model_dump() for chunk in chunks]
        if self._path.is_symlink():
            raise ValueError("unsafe_chunk_cache")
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=self._path.parent, prefix=".chunk-cache-", delete=False) as stream:
                temporary = Path(stream.name)
                json.dump(payload, stream, ensure_ascii=False, indent=2)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, self._path)
            descriptor = os.open(self._path.parent, os.O_RDONLY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
