"""Bounded participation policy，independent of inference and pipeline libraries．"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
import json
import time
from typing import Callable

from pydantic import BaseModel, ConfigDict, Field
from packages.karte_core.context import (
    ContextScope,
    ContextDocument,
    _item_matches_requested_scope,
)


class Evidence(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    doc_id: str = Field(min_length=1, max_length=256)
    sha256: str = Field(pattern=r"^[a-f0-9]{64}$")
    quote: str = Field(min_length=1, max_length=600)


class Candidate(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    text: str = Field(default="", max_length=180)
    evidence: tuple[Evidence, ...] = Field(default=(), max_length=3)
    reason: str = Field(default="insufficient_evidence", max_length=128)


@dataclass(frozen=True)
class RunScope:
    """Runtime grants，never model-generated tool arguments．"""

    projects: tuple[str, ...]
    tags: tuple[str, ...]
    participants: frozenset[str]
    shareable: frozenset[tuple[str, str]]
    sensitivity_ceiling: str = "internal"

    def context(self):
        return ContextScope(
            projects=list(self.projects),
            tags=list(self.tags),
            sensitivity_ceiling=self.sensitivity_ceiling,
        )


@dataclass
class Run:
    operation_id: str
    session_id: str
    turn_id: str
    revision: int
    scope: RunScope
    expires_at: float
    deadline: float
    valid: Callable[[], bool]
    max_tools: int = 6
    max_models: int = 4
    max_result_bytes: int = 16384
    tools: int = 0
    models: int = 0
    canceled: bool = False
    terminal: bool = False
    seen: set[str] = field(default_factory=set)
    documents: dict[str, ContextDocument] = field(default_factory=dict)
    events: list[dict] = field(default_factory=list)

    def check(self):
        if self.canceled or self.terminal or not self.valid():
            raise RuntimeError("stale_run")
        if time.monotonic() >= min(self.deadline, self.expires_at):
            raise RuntimeError("deadline")

    def model_call(self):
        self.check()
        if self.models >= self.max_models:
            raise RuntimeError("model_budget")
        self.models += 1

    def tool_call(self, name, arguments):
        self.check()
        if self.tools >= self.max_tools:
            raise RuntimeError("tool_budget")
        self.tools += 1  # Includes invalid and repeated calls．
        self.events.append(
            {"event": "tool_attempt", "name": name, "attempt": self.tools}
        )
        if name not in {"karte_search", "karte_read"}:
            raise RuntimeError("tool_denied")
        signature = json.dumps([name, arguments], sort_keys=True)
        if signature in self.seen:
            raise RuntimeError("repeated_tool")
        self.seen.add(signature)
        # Reject scope expansion before an SDK can discard unknown arguments．
        key = "query" if name == "karte_search" else "doc_id"
        if (
            not isinstance(arguments, dict)
            or set(arguments) != {key}
            or not isinstance(arguments[key], str)
            or not 0 < len(arguments[key]) <= (2048 if key == "query" else 256)
        ):
            raise RuntimeError("invalid_arguments")


class MemoryTools:
    """Async boundary to the existing bounded synchronous Karte client．"""

    def __init__(self, run: Run, client):
        self.run, self.client = run, client

    async def call(self, name, arguments):
        self.run.tool_call(name, arguments)
        return await self._execute(name, arguments)

    async def _execute(self, name, arguments):
        # SDK callers count raw tool attempts before argument validation．
        run = self.run
        run.check()
        if name not in {"karte_search", "karte_read"}:
            raise RuntimeError("tool_denied")
        key = "query" if name == "karte_search" else "doc_id"
        if (
            set(arguments) != {key}
            or not isinstance(arguments[key], str)
            or not 0 < len(arguments[key]) <= (2048 if key == "query" else 256)
        ):
            raise RuntimeError("invalid_arguments")
        scope = run.scope.context()
        method = self.client.search if name == "karte_search" else self.client.read
        kwargs = scope.model_dump()
        if name == "karte_search":
            kwargs["top_k"] = 3
        # Cancellation invalidates this run immediately．The bounded Karte client
        # thread can finish later；its result never mutates run state after check．
        async with asyncio.timeout(
            max(0.001, min(run.deadline, run.expires_at) - time.monotonic())
        ):
            response = await asyncio.to_thread(method, arguments[key], **kwargs)
        run.check()
        if response.status != "ok":
            return {"status": response.status, "results": []}
        items = response.results if name == "karte_search" else [response.document]
        safe = []
        for item in items:
            if item is None:
                continue
            if not _item_matches_requested_scope(item, scope):
                raise RuntimeError("scope_violation")
            if (item.doc_id, item.sha256) not in run.scope.shareable:
                continue
            if isinstance(item, ContextDocument):
                # Do not silently truncate evidence or promote a search snippet．
                value = {
                    "doc_id": item.doc_id,
                    "sha256": item.sha256,
                    "body": item.body,
                }
            else:
                value = {
                    "doc_id": item.doc_id,
                    "sha256": item.sha256,
                    "snippet": item.snippet,
                }
            safe.append(value)
        result = {"status": "ok", "results": safe}
        if len(json.dumps(result, ensure_ascii=False).encode()) > run.max_result_bytes:
            raise RuntimeError("result_budget")
        if name == "karte_read" and safe:
            run.documents[items[0].doc_id] = items[0]
        return result

    def validate_candidate(self, candidate: Candidate):
        self.run.check()
        if not candidate.text:
            return candidate
        if not candidate.evidence:
            raise RuntimeError("missing_evidence")
        for evidence in candidate.evidence:
            document = self.run.documents.get(evidence.doc_id)
            if (
                document is None
                or document.sha256 != evidence.sha256
                or evidence.quote not in document.body
            ):
                raise RuntimeError("invalid_evidence")
            if (evidence.doc_id, evidence.sha256) not in self.run.scope.shareable:
                raise RuntimeError("sharing_denied")
        return candidate


async def fixed_workflow(tools: MemoryTools, query: str, generate):
    results = await tools.call("karte_search", {"query": query})
    for source in results.get("results", [])[:3]:
        await tools.call("karte_read", {"doc_id": source["doc_id"]})
    if not tools.run.documents:
        return Candidate()
    tools.run.model_call()
    async with asyncio.timeout(
        max(0.001, min(tools.run.deadline, tools.run.expires_at) - time.monotonic())
    ):
        result = await generate(query, list(tools.run.documents.values()))
    return tools.validate_candidate(result)
