"""Authenticated bounded collection; private questions never enter a job．"""
from __future__ import annotations

import asyncio
import hashlib
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from time import monotonic

from pydantic import ValidationError

from packages.web_search_core.worker_provider import (
    WorkerSearchError,
    WorkerSearchPending,
    WorkerSearchProvider,
    WorkerSearchSession,
    _hash,
)

from .schemas import ObservePayload, ObserveRequest, ObserveResult, canonical_json


@dataclass
class ObserveSession(WorkerSearchSession):
    accepted_payload: ObservePayload | None = None

    def identity(self):
        identity = super().identity()
        identity["request_id"] = identity.pop("plan_id")
        identity["target_worker_id"] = self.submit["target_worker_id"]
        return identity


class DiscordObserveProvider(WorkerSearchProvider):
    def new_observation(self, request: ObserveRequest) -> ObserveSession:
        milliseconds = (int(request.request_id) >> 22) + 1420070400000
        before = datetime.fromtimestamp(milliseconds / 1000, UTC)
        job_input = {"mode": "observe", "guild_id": self._config.guild_id,
                     "channel_ids": list(self._config.channel_ids),
                     "observed_before": before.isoformat().replace("+00:00", "Z"),
                     "lookback_days": 30, "max_messages_per_channel": self._config.max_messages_per_channel}
        submit = {"contract": "0.4", "submit_key": f"runtime-discord-observe-{request.request_id}",
                  "parent_job_id": None, "kind": "discord.observe", "input": job_input,
                  "target_worker_id": self._config.target_worker_id,
                  "budget": {"max_queries": 1, "search_requests": 1, "max_sources": 1,
                             "fetch_requests": 100, "model_requests": 1, "html_bytes": 8 * 1024**2,
                             "text_chars": 1000000, "job_seconds": self._config.worker_deadline_seconds,
                             "request_seconds": min(self._config.worker_deadline_seconds, 120)},
                  "deadline_seconds": self._config.worker_deadline_seconds}
        return ObserveSession(request.request_id, submit, _hash(job_input),
                              collection_deadline=monotonic() + submit["deadline_seconds"])

    async def observe_job(self, session: ObserveSession) -> ObservePayload:
        try:
            await self._check_cancellation(session)
            if session.accepted_payload is not None:
                return session.accepted_payload.model_copy(deep=True)
            if session.job_id is None:
                view = await self._collection_request(session, "POST", "/jobs", body=session.submit)
                self._validate_view(session, view)
            end = asyncio.get_running_loop().time() + self._config.worker_wait_seconds
            while True:
                await self._check_cancellation(session)
                view = await self._collection_request(session, "GET", f"/jobs/{session.job_id}")
                self._validate_view(session, view)
                if session.state in {"completed", "partial"}:
                    payload = await self._completed_observation(session, view)
                    self._collection_remaining(session)
                    session.accepted_payload = payload.model_copy(deep=True)
                    return payload
                if session.state in {"failed", "cancelled", "lost"}:
                    raise WorkerSearchError("discord_observe_failed", session=session)
                remaining = end - asyncio.get_running_loop().time()
                collection_remaining = self._collection_remaining(session)
                if remaining <= 0:
                    raise WorkerSearchPending(session)
                await asyncio.sleep(min(remaining, collection_remaining, self._config.worker_poll_seconds))
        except WorkerSearchError as exc:
            raise WorkerSearchError(exc.code, session=session) from None

    async def _completed_observation(self, session: ObserveSession, view: dict) -> ObservePayload:
        response = await self._collection_request(session, "GET", f"/jobs/{session.job_id}/result")
        try:
            result = ObserveResult.model_validate(response.get("result"))
            usage = result.usage
            expected_status = "succeeded" if session.state == "completed" else "partial"
            if (response.get("job_id") != session.job_id or response.get("state") != session.state
                    or result.job_id != session.job_id or result.attempt_id != view.get("attempt_id")
                    or result.model_dump(mode="json") != view.get("result")
                    or result.status != expected_status or response.get("stop_reason") != result.stop_reason
                    or result.stop_reason != view.get("stop_reason")
                    or usage.model_dump() != response.get("usage") or response.get("usage") != view.get("usage")
                    or usage.search_requests != 0 or usage.search_credits != 0 or usage.model_requests != 0
                    or usage.fetch_requests > session.submit["budget"]["fetch_requests"]
                    or usage.document_bytes > session.submit["budget"]["html_bytes"]
                    or result.elapsed_seconds > session.submit["deadline_seconds"]):
                raise ValueError()
            payload = result.payload
            expected = session.submit["input"]
            coverage = payload.coverage
            before = datetime.fromisoformat(expected["observed_before"])
            if (coverage.guild_id != expected["guild_id"] or coverage.channel_ids != expected["channel_ids"]
                    or coverage.window_end != before or coverage.window_start != before - timedelta(days=30)
                    or (coverage.status == "complete") != (result.status == "succeeded")
                    or result.status == "succeeded" and (payload.failures or result.stop_reason is not None)
                    or result.status == "succeeded" and (not coverage.content_requested
                        or any(c.status != "complete" for c in coverage.channels)
                        or any(m.content_status != "available" or m.content_truncated for m in payload.messages))
                    or len({c.channel_id for c in coverage.channels}) != len(coverage.channels)
                    or {c.channel_id for c in coverage.channels} != set(expected["channel_ids"])
                    or payload.snapshot.guild is not None and payload.snapshot.guild.id != expected["guild_id"]
                    or any(c.guild_id != expected["guild_id"] for c in payload.snapshot.channels)
                    or any(f.channel_id is not None and f.channel_id not in expected["channel_ids"]
                           for f in payload.failures)):
                raise ValueError()
            timestamps = [coverage.window_start, coverage.window_end, coverage.observed_at]
            timestamps.extend(m.created_at for m in payload.messages)
            timestamps.extend(m.edited_at for m in payload.messages if m.edited_at is not None)
            timestamps.extend(c.oldest_observed_at for c in coverage.channels if c.oldest_observed_at is not None)
            if any(value.utcoffset() != timedelta(0) for value in timestamps):
                raise ValueError()
            for channel in coverage.channels:
                messages = [m for m in payload.messages if m.channel_id == channel.channel_id]
                if (len(messages) != channel.message_count or len(messages) > expected["max_messages_per_channel"]
                        or channel.status == "complete" and channel.history_permission != "granted"):
                    raise ValueError()
            if result.manifest:
                if len(result.manifest) != 1:
                    raise ValueError()
                artifact = result.manifest[0]
                original = result.model_dump(mode="json")
                original["manifest"] = []
                content = canonical_json(original).encode("utf-8")
                if ((artifact.name, artifact.media_type, artifact.schema_version)
                        != ("result-pack.json", "application/json", 1)
                        or artifact.bytes != len(content) or artifact.sha256 != hashlib.sha256(content).hexdigest()):
                    raise ValueError()
            return payload
        except (ValidationError, ValueError, TypeError, KeyError, OverflowError):
            raise WorkerSearchError("discord_observe_result_invalid", session=session) from None
