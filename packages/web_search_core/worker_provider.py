"""Authenticated contract 0.4 search adapter; remote text is always untrusted．"""
from __future__ import annotations

import asyncio
import hashlib
import ipaddress
import json
import math
import os
import re
from copy import deepcopy
from dataclasses import dataclass, field
from time import monotonic
from typing import Literal
from urllib.parse import urlsplit

import httpx
from pydantic import BaseModel, ConfigDict, Field, ValidationError

from packages.config_core.loader import WebSearchConfig
from .provider import WebResultSanitizer


def _hash(value: dict) -> str:
    data = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(data.encode("utf-8")).hexdigest()


class WorkerSearchError(RuntimeError):
    """Only fixed error codes and validated identity may cross the API boundary．"""

    def __init__(self, code: str, *, session: WorkerSearchSession | None = None) -> None:
        super().__init__(code)
        self.code = code
        self.identity = session.identity() if session else {"provider": "worker", "contract": "0.4"}


class WorkerSearchPending(RuntimeError):
    def __init__(self, session: WorkerSearchSession) -> None:
        super().__init__("worker_search_pending")
        self.identity = session.identity()


@dataclass
class WorkerSearchSession:
    plan_id: str
    submit: dict
    input_hash: str
    job_id: str | None = None
    state: str = "queued"
    cancel_requested: bool = False
    cancel_acknowledged: bool = False
    cancel_lock: asyncio.Lock = field(default_factory=asyncio.Lock)
    collection_deadline: float | None = None
    collection_timed_out: bool = False
    accepted_sources: list[dict] | None = None
    assigned_worker_id: str | None = None

    def identity(self) -> dict:
        identity = {"provider": "worker", "contract": "0.4", "plan_id": self.plan_id,
                "job_id": self.job_id, "worker_id": self.submit["target_worker_id"],
                "input_hash": self.input_hash, "state": self.state}
        if self.submit["target_worker_id"] == "auto":
            identity.update(target_worker_id="auto", worker_id=self.assigned_worker_id)
        return identity


class _Wire(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class _Usage(_Wire):
    search_requests: int = Field(ge=0)
    search_credits: int = Field(ge=0)
    fetch_requests: int = Field(ge=0)
    document_bytes: int = Field(ge=0)
    model_requests: int = Field(ge=0)


class _Diagnostic(_Wire):
    engine: str = Field(min_length=1, max_length=64)
    unresponsive: bool
    reasons: list[str] = Field(max_length=8)
    result_count: int = Field(ge=0)
    code: str | None


class _QueryResult(_Wire):
    query_id: str
    text: str
    role: Literal["overview", "primary", "limitations", "gap"]
    reason: str
    status: Literal["ok", "failed", "limit"]
    result_count: int = Field(ge=0)
    diagnostic: _Diagnostic


class _Candidate(_Wire):
    url: str
    title: str
    snippet: str
    query_id: str
    engine: str
    rank: int
    discovered_at: str
    supplied: bool


class _Failure(_Wire):
    stage: Literal["search", "fetch", "worker", "artifact"]
    query_id: str | None
    source_id: str | None
    code: str = Field(min_length=1, max_length=128)
    detail: str | None = Field(max_length=1000)


class _Artifact(_Wire):
    artifact_id: str = Field(pattern=r"^[A-Za-z0-9._-]{8,128}$")
    name: str = Field(min_length=1, max_length=200)
    media_type: str = Field(min_length=3, max_length=128)
    bytes: int = Field(ge=1)
    sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    schema_version: int = Field(ge=1, le=16)


class _Payload(_Wire):
    mode: Literal["search"]
    queries: list[_QueryResult] = Field(min_length=1, max_length=10)
    candidates: list[_Candidate] = Field(max_length=1000)
    failures: list[_Failure]


class _Result(_Wire):
    contract: Literal["0.4"]
    job_id: str = Field(pattern=r"^[A-Za-z0-9._-]{8,128}$")
    attempt_id: str = Field(pattern=r"^[A-Za-z0-9._-]{8,128}$")
    status: Literal["succeeded", "partial", "failed", "cancelled"]
    stop_reason: str | None = Field(max_length=128)
    usage: _Usage
    elapsed_seconds: float = Field(ge=0, le=86400, allow_inf_nan=False)
    payload: _Payload
    manifest: list[_Artifact] = Field(max_length=128)


class WorkerSearchProvider:
    def __init__(self, config: WebSearchConfig, client: httpx.AsyncClient | None = None) -> None:
        self._config = config
        # Validate even with injected transports; never put credentials in URLs．
        try:
            parsed = urlsplit(config.manager_url)
            hostname = parsed.hostname
            _ = parsed.port
        except ValueError:
            raise ValueError("Worker Manager requires HTTPS or literal loopback HTTP") from None
        try:
            address = ipaddress.ip_address(hostname or "")
            loopback = address.is_loopback
        except ValueError:
            loopback = hostname == "localhost"
        if (parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username or parsed.password
                or parsed.query or parsed.fragment or parsed.path not in {"", "/"}
                or (parsed.scheme == "http" and not loopback)):
            raise ValueError("Worker Manager requires HTTPS or literal loopback HTTP")
        self._base_url = config.manager_url.rstrip("/")
        self._credential = os.environ.get(config.manager_credential_env, "")
        if not self._credential or any(char in self._credential for char in "\r\n"):
            raise ValueError("Worker Manager credential environment variable is missing or invalid")
        self._client = client or httpx.AsyncClient(timeout=config.timeout_seconds, follow_redirects=False, trust_env=False)
        self._owns_client = client is None
        self._sanitizer = WebResultSanitizer(config)

    def new_session(self, query: str, plan_id: str) -> WorkerSearchSession:
        job_input = {"mode": "search", "topic": query, "round_index": 0,
                     "queries": [{"query_id": "Q1", "text": query, "role": "overview",
                                  "reason": "Approved public web search"}]}
        submit = {"contract": "0.4", "submit_key": f"runtime-search-{plan_id}", "parent_job_id": None,
                  "kind": "web.collect", "input": job_input, "target_worker_id": self._config.target_worker_id,
                  "budget": {"max_queries": 1, "search_requests": 2, "max_sources": min(self._config.max_results, 30),
                             "job_seconds": self._config.worker_deadline_seconds,
                             "request_seconds": min(self._config.worker_deadline_seconds, 120)},
                  "deadline_seconds": self._config.worker_deadline_seconds}
        return WorkerSearchSession(plan_id, submit, _hash(job_input),
                                   collection_deadline=monotonic() + submit["deadline_seconds"])

    async def _request(self, method: str, path: str, *, body: dict | None = None) -> dict:
        try:
            async with self._client.stream(method, self._base_url + path, json=body,
                                           headers={"X-Ephy-Credential": self._credential, "Accept": "application/json"},
                                           follow_redirects=False, timeout=self._config.timeout_seconds) as response:
                if not 200 <= response.status_code < 300:
                    raise WorkerSearchError("worker_manager_http_error")
                if "application/json" not in response.headers.get("content-type", "").lower():
                    raise WorkerSearchError("worker_manager_invalid_response")
                data = bytearray()
                async for chunk in response.aiter_bytes():
                    data.extend(chunk)
                    if len(data) > self._config.worker_response_bytes:
                        raise WorkerSearchError("worker_manager_response_too_large")
                payload = json.loads(data, parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
                if not isinstance(payload, dict):
                    raise ValueError()
                return payload
        except WorkerSearchError:
            raise
        except (httpx.HTTPError, ValueError, UnicodeDecodeError, TypeError):
            raise WorkerSearchError("worker_manager_unavailable") from None

    @staticmethod
    def _collection_remaining(session: WorkerSearchSession) -> float:
        if session.collection_deadline is None:
            session.collection_deadline = monotonic() + session.submit["deadline_seconds"]
        remaining = session.collection_deadline - monotonic()
        if session.collection_timed_out or remaining <= 0:
            session.collection_timed_out = True
            raise WorkerSearchError("worker_search_deadline_exceeded", session=session)
        return remaining

    async def _collection_request(self, session: WorkerSearchSession, method: str, path: str,
                                  *, body: dict | None = None) -> dict:
        deadline = asyncio.timeout(self._collection_remaining(session))
        try:
            async with deadline:
                response = await self._request(method, path, body=body)
        except TimeoutError:
            if not deadline.expired():
                raise WorkerSearchError("worker_manager_unavailable", session=session) from None
            session.collection_timed_out = True
            raise WorkerSearchError("worker_search_deadline_exceeded", session=session) from None
        self._collection_remaining(session)
        return response

    def _validate_view(self, session: WorkerSearchSession, view: dict) -> None:
        expected = session.submit
        job_id = view.get("job_id")
        if not isinstance(job_id, str) or not re.fullmatch(r"[A-Za-z0-9._-]{8,128}", job_id):
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        if session.job_id is not None and session.job_id != job_id:
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        for key in ("contract", "submit_key", "parent_job_id", "kind", "input", "target_worker_id", "deadline_seconds"):
            if view.get(key) != expected[key]:
                raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        budget = view.get("budget")
        if not isinstance(budget, dict) or any(budget.get(key) != value for key, value in expected["budget"].items()):
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        if (view.get("mode") != expected["input"]["mode"] or view.get("input_hash") != session.input_hash
                or _hash(view["input"]) != session.input_hash):
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        state = view.get("state")
        if state not in {"queued", "running", "cancel_requested", "completed", "partial", "failed", "cancelled", "lost"}:
            raise WorkerSearchError("worker_job_invalid_state", session=session)
        actual_worker = view.get("worker_id")
        if expected["target_worker_id"] == "auto":
            if (actual_worker is not None and (not isinstance(actual_worker, str)
                    or not re.fullmatch(r"[A-Za-z0-9._-]{1,128}", actual_worker) or actual_worker == "auto")):
                raise WorkerSearchError("worker_job_identity_mismatch", session=session)
            if (state in {"running", "cancel_requested", "completed", "partial"} and actual_worker is None
                    or session.assigned_worker_id is not None and actual_worker != session.assigned_worker_id):
                raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        elif state in {"running", "cancel_requested", "completed"} and actual_worker != expected["target_worker_id"]:
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        try:
            valid_times = all(type(view.get(key)) in {int, float} and math.isfinite(view[key])
                              for key in ("created_at", "deadline_at"))
        except (OverflowError, ValueError):
            valid_times = False
        if not valid_times:
            raise WorkerSearchError("worker_job_invalid_response", session=session)
        if not math.isclose(view["deadline_at"] - view["created_at"], expected["deadline_seconds"], abs_tol=0.01):
            raise WorkerSearchError("worker_job_identity_mismatch", session=session)
        session.job_id = job_id
        if expected["target_worker_id"] == "auto" and actual_worker is not None:
            session.assigned_worker_id = actual_worker
        if not session.cancel_acknowledged:
            session.state = state

    async def search_job(self, session: WorkerSearchSession) -> list[dict]:
        try:
            await self._check_cancellation(session)
            if session.accepted_sources is not None:
                return deepcopy(session.accepted_sources)
            self._collection_remaining(session)
            if session.job_id is None:
                view = await self._collection_request(session, "POST", "/jobs", body=session.submit)
                self._validate_view(session, view)
            await self._check_cancellation(session)
            end = asyncio.get_running_loop().time() + self._config.worker_wait_seconds
            while True:
                await self._check_cancellation(session)
                view = await self._collection_request(session, "GET", f"/jobs/{session.job_id}")
                self._validate_view(session, view)
                await self._check_cancellation(session)
                if session.state == "completed":
                    sources = await self._completed(session, view)
                    await self._check_cancellation(session)
                    self._collection_remaining(session)
                    session.accepted_sources = deepcopy(sources)
                    return deepcopy(session.accepted_sources)
                if session.state in {"partial", "failed", "cancelled", "lost"}:
                    raise WorkerSearchError("worker_search_failed", session=session)
                remaining = end - asyncio.get_running_loop().time()
                collection_remaining = self._collection_remaining(session)
                if remaining <= 0:
                    raise WorkerSearchPending(session)
                await asyncio.sleep(min(remaining, collection_remaining, self._config.worker_poll_seconds))
        except WorkerSearchError as exc:
            if exc.code == "worker_search_deadline_exceeded" and session.cancel_requested:
                await self._check_cancellation(session)
            raise WorkerSearchError(exc.code, session=session) from None

    async def _completed(self, session: WorkerSearchSession, view: dict) -> list[dict]:
        response = await self._collection_request(session, "GET", f"/jobs/{session.job_id}/result")
        try:
            result = _Result.model_validate(response.get("result"))
            expected_query = session.submit["input"]["queries"][0]
            # Worker executes Query (ProseModel), whose contract projects Japanese
            # punctuation in prose fields．The submitted input/hash remain exact．
            expected_result_query = {**expected_query, "text": expected_query["text"].replace("、", "，").replace("。", "．")}
            query = result.payload.queries[0]
            budget = session.submit["budget"]
            if (response.get("job_id") != session.job_id or response.get("state") != "completed"
                    or response.get("stop_reason") is not None or response.get("usage") != view.get("usage")
                    or result.job_id != session.job_id or result.attempt_id != view.get("attempt_id")
                    or result.model_dump() != view.get("result") or result.status != "succeeded"
                    or result.stop_reason is not None or result.payload.failures or len(result.payload.queries) != 1
                    or any(getattr(query, key) != value for key, value in expected_result_query.items())
                    or query.status != "ok" or query.diagnostic.unresponsive
                    or result.usage.model_dump() != response.get("usage")
                    or result.usage.search_requests > budget["search_requests"]
                    or result.usage.search_credits > budget["search_requests"]
                    or result.usage.fetch_requests != 0 or result.usage.document_bytes != 0
                    or result.usage.model_requests != 0
                    or len(result.payload.candidates) > budget["max_sources"]
                    or query.result_count != len(result.payload.candidates)
                    or any(candidate.query_id != "Q1" for candidate in result.payload.candidates)):
                raise ValueError()
            if result.manifest:
                if len(result.manifest) != 1:
                    raise ValueError()
                pack = result.manifest[0]
                if (pack.name, pack.media_type, pack.schema_version) != ("result-pack.json", "application/json", 1):
                    raise ValueError()
                # WorkerService uploads the original envelope before appending this reference．
                original = result.model_dump(mode="json")
                original["manifest"] = []
                content = json.dumps(original, ensure_ascii=False, sort_keys=True,
                                     separators=(",", ":"), allow_nan=False).encode()
                if pack.bytes != len(content) or pack.sha256 != hashlib.sha256(content).hexdigest():
                    raise ValueError()
        except (ValidationError, ValueError, TypeError, IndexError):
            raise WorkerSearchError("worker_result_invalid", session=session) from None
        self._collection_remaining(session)
        sources = self._sanitizer.sanitize([item.model_dump() for item in result.payload.candidates])
        self._collection_remaining(session)
        return sources

    async def cancel(self, session: WorkerSearchSession) -> dict:
        session.cancel_requested = True
        # Polling may hold the plan lock for seconds．Only cancellation requests
        # share this lock, so a known job can be cancelled independently．
        async with session.cancel_lock:
            if session.cancel_acknowledged:
                return session.identity()
            if session.job_id is None:
                # The first POST may be in flight or its reply lost．Recover the
                # same approved job using its original Manager idempotency key．
                view = await self._request("POST", "/jobs", body=session.submit)
                self._validate_view(session, view)
            view = await self._request("POST", f"/jobs/{session.job_id}/cancel")
            self._validate_view(session, view)
            session.cancel_acknowledged = True
            return session.identity()

    async def _check_cancellation(self, session: WorkerSearchSession) -> None:
        if session.cancel_requested:
            await self.cancel(session)
            raise WorkerSearchError("worker_search_cancelled", session=session)

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()
