from __future__ import annotations

import threading
import time
from collections import deque
from dataclasses import asdict, dataclass
from datetime import UTC, datetime

from packages.rag_core.service import RagService

from .source import KarteDocument, KarteScanResult, KarteSourceAdapter


@dataclass(frozen=True)
class KarteWatchEvent:
    operation: str
    doc_id: str | None
    relative_path: str | None
    previous_relative_path: str | None
    sha256: str | None
    observed_at: str


class KarteWatchService:
    def __init__(
        self,
        *,
        adapter: KarteSourceAdapter,
        rag_service: RagService,
        project: str = "karte",
        tags: list[str] | None = None,
        interval_seconds: float = 1.0,
        debounce_seconds: float = 0.25,
        max_events: int = 256,
    ) -> None:
        if interval_seconds <= 0:
            raise ValueError("interval_seconds must be positive")
        if debounce_seconds < 0:
            raise ValueError("debounce_seconds cannot be negative")
        if max_events <= 0:
            raise ValueError("max_events must be positive")
        self.adapter = adapter
        self.rag_service = rag_service
        self.project = project
        self.tags = tags or []
        self.interval_seconds = interval_seconds
        self.debounce_seconds = debounce_seconds
        self._events: deque[KarteWatchEvent] = deque(maxlen=max_events)
        self._snapshot: dict[str, KarteDocument] | None = None
        self._pending_scan: KarteScanResult | None = None
        self._pending_fingerprint: tuple | None = None
        self._pending_since: float | None = None
        self._last_scan_at: str | None = None
        self._last_error: str | None = None
        self._last_issues: list[dict] = []
        self._generation = 0
        self._running = False
        self._stop_event = threading.Event()
        self._thread: threading.Thread | None = None
        self._lock = threading.RLock()

    def start(self) -> None:
        with self._lock:
            if self._running:
                return
            self._stop_event.clear()
            self._running = True
            self._generation += 1
            self._thread = threading.Thread(target=self._run, name="karte-watch", daemon=True)
            self._thread.start()

    def stop(self, timeout_seconds: float = 5.0) -> None:
        if timeout_seconds < 0:
            raise ValueError("timeout_seconds cannot be negative")
        with self._lock:
            thread = self._thread
            self._stop_event.set()
        if thread is not None:
            thread.join(timeout=timeout_seconds)
        with self._lock:
            if self._thread is thread and (thread is None or not thread.is_alive()):
                self._running = False
                self._thread = None

    cancel = stop

    def restart(self, timeout_seconds: float = 5.0) -> None:
        self.stop(timeout_seconds=timeout_seconds)
        self.start()

    def full_rescan(self) -> dict:
        scan = self.adapter.scan()
        result = self.rag_service.sync_karte_documents(
            documents=scan.documents,
            replace_paths={str(self.adapter.content_root)},
            project=self.project,
            tags=self.tags,
        )
        event = KarteWatchEvent(
            operation="full_rescan",
            doc_id=None,
            relative_path="content",
            previous_relative_path=None,
            sha256=None,
            observed_at=_utc_now(),
        )
        with self._lock:
            self._snapshot = {document.doc_id: document for document in scan.documents}
            self._pending_scan = None
            self._pending_fingerprint = None
            self._pending_since = None
            self._last_scan_at = event.observed_at
            self._last_error = None
            self._last_issues = [asdict(issue) for issue in scan.issues]
            self._events.append(event)
        return {**result, "event": asdict(event), "issues": self._last_issues}

    def poll_once(self, *, force: bool = False) -> list[dict]:
        with self._lock:
            has_snapshot = self._snapshot is not None
        if not has_snapshot:
            return [self.full_rescan()["event"]]

        scan = self.adapter.scan()
        fingerprint = _fingerprint(scan)
        with self._lock:
            baseline = self._snapshot or {}
            baseline_fingerprint = _document_fingerprint(baseline.values())
            self._last_scan_at = _utc_now()
            self._last_issues = [asdict(issue) for issue in scan.issues]
            if fingerprint == baseline_fingerprint:
                self._pending_scan = None
                self._pending_fingerprint = None
                self._pending_since = None
                return []

            now = time.monotonic()
            if not force and self.debounce_seconds > 0:
                if self._pending_fingerprint != fingerprint:
                    self._pending_scan = scan
                    self._pending_fingerprint = fingerprint
                    self._pending_since = now
                    return []
                if self._pending_since is not None and now - self._pending_since < self.debounce_seconds:
                    self._pending_scan = scan
                    return []
                scan = self._pending_scan or scan

        return self._apply_scan(scan)

    def health(self) -> dict:
        with self._lock:
            return {
                "running": self._running,
                "generation": self._generation,
                "last_scan_at": self._last_scan_at,
                "last_error": self._last_error,
                "event_count": len(self._events),
                "event_capacity": self._events.maxlen,
                "pending": self._pending_scan is not None,
                "issues": list(self._last_issues),
            }

    def events(self) -> list[dict]:
        with self._lock:
            return [asdict(event) for event in self._events]

    def _run(self) -> None:
        try:
            if self._snapshot is None:
                self.full_rescan()
            while not self._stop_event.wait(self.interval_seconds):
                self.poll_once()
        except Exception as exc:  # background health must retain the failure for explicit recovery
            with self._lock:
                self._last_error = f"{type(exc).__name__}: {exc}"
        finally:
            with self._lock:
                self._running = False

    def _apply_scan(self, scan: KarteScanResult) -> list[dict]:
        with self._lock:
            previous = dict(self._snapshot or {})
        current = {document.doc_id: document for document in scan.documents}
        changed_documents: list[KarteDocument] = []
        replace_paths: set[str] = set()
        events: list[KarteWatchEvent] = []
        observed_at = _utc_now()

        for doc_id in sorted(previous.keys() & current.keys()):
            old = previous[doc_id]
            new = current[doc_id]
            if old.relative_path != new.relative_path:
                changed_documents.append(new)
                replace_paths.update({str(old.absolute_path), str(new.absolute_path)})
                events.append(KarteWatchEvent("rename", doc_id, new.relative_path, old.relative_path, new.sha256, observed_at))
            elif old.sha256 != new.sha256:
                changed_documents.append(new)
                replace_paths.add(str(new.absolute_path))
                events.append(KarteWatchEvent("update", doc_id, new.relative_path, None, new.sha256, observed_at))

        for doc_id in sorted(current.keys() - previous.keys()):
            document = current[doc_id]
            changed_documents.append(document)
            replace_paths.add(str(document.absolute_path))
            events.append(KarteWatchEvent("create", doc_id, document.relative_path, None, document.sha256, observed_at))

        for doc_id in sorted(previous.keys() - current.keys()):
            document = previous[doc_id]
            replace_paths.add(str(document.absolute_path))
            events.append(KarteWatchEvent("delete", doc_id, document.relative_path, None, document.sha256, observed_at))

        if replace_paths:
            self.rag_service.sync_karte_documents(
                documents=changed_documents,
                replace_paths=replace_paths,
                project=self.project,
                tags=self.tags,
            )

        with self._lock:
            self._snapshot = current
            self._pending_scan = None
            self._pending_fingerprint = None
            self._pending_since = None
            self._last_error = None
            self._last_scan_at = observed_at
            self._last_issues = [asdict(issue) for issue in scan.issues]
            self._events.extend(events)
        return [asdict(event) for event in events]


def _fingerprint(scan: KarteScanResult) -> tuple:
    return _document_fingerprint(scan.documents)


def _document_fingerprint(documents) -> tuple:
    return tuple(sorted((document.doc_id, document.relative_path, document.sha256) for document in documents))


def _utc_now() -> str:
    return datetime.now(tz=UTC).isoformat()
