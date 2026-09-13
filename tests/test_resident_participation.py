"""Synthetic observations verify scope，priority，and late candidate rejection．"""

import asyncio
import copy
import json
from types import SimpleNamespace

import pytest

from apps.gateway.model_transition import InferenceGate
from apps.gateway.resident_participation import CandidateCancel, CandidateCheck, CandidateRequest, ResidentParticipation
from packages.config_core.loader import ModelConfig
from packages.prompt_core.loader import PromptManager
from scripts.reuse_core.memory_probe import FixtureClient


class Resident:
    def __init__(self):
        self.snapshot = {"revision": 1, "policy": {"proactive": "allowed", "restricted_memory_ids": []}}

    def state(self, session):
        return copy.deepcopy(self.snapshot)

    def apply_policy(self, request, snapshot):
        return request


def setup(tmp_path, monkeypatch):
    client = FixtureClient()
    document = client.documents["fixture-picnic"]
    path = tmp_path / "grants.json"
    path.write_text(json.dumps({"scope": {"projects": ["reuse-fixture"], "tags": ["synthetic"]},
        "participants": ["fixture-hana"], "documents": [{"doc_id": document.doc_id,
        "sha256": document.sha256, "participants": ["fixture-hana"]}]}))
    monkeypatch.setenv("EPHY_RESIDENT_PARTICIPATION_GRANTS", str(path))
    payload = CandidateRequest(session_id="session", operation_id="observation", revision=1,
        observation="公園のピクニック", participants={"fixture-hana"}, speaker_id="fixture-hana", observation_allowed=True)
    state = SimpleNamespace(resident_service=Resident(), karte_context_client=client, inference_gate=InferenceGate(),
        model_router=SimpleNamespace(route_chat=lambda _: SimpleNamespace(selected_model=ModelConfig(
            provider="llama_cpp", base_url="http://127.0.0.1:8082/v1", model="local", max_context=8192))),
        prompt_manager=PromptManager())
    called = []

    async def generate(config, request):
        called.append(request)
        return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps({
            "text": "わたしも，桜の下のおにぎりを覚えています．", "evidence": [{
                "doc_id": document.doc_id, "sha256": document.sha256, "quote": document.body}], "reason": "grounded"})}}]}

    state.chat_adapter = SimpleNamespace(create_chat_completion=generate)
    return ResidentParticipation(), payload, state, path, called


async def connected():
    return False


def test_candidate_uses_scoped_karte_and_preserves_observation_ids(tmp_path, monkeypatch):
    service, payload, state, _, calls = setup(tmp_path, monkeypatch)
    result = asyncio.run(service.generate(payload, state, connected))
    assert result["status"] == "candidate" and result["revision"] == 1
    assert result["policy_revision"] == 1 and result["tool_attempts"] == 2
    assert len(calls) == 1 and result["model_requests"] == 1
    assert "PRIVATE-FIXTURE" not in str(calls[0])
    assert [call[0] for call in state.karte_context_client.calls] == ["search", "read"]
    assert state.karte_context_client.calls[0][2]["projects"] == ["reuse-fixture"]
    assert service.current is None and state.inference_gate.active == 0


@pytest.mark.parametrize("change", ["cancel", "policy", "grants", "foreground"])
def test_late_generation_cannot_escape_invalidation(tmp_path, monkeypatch, change):
    async def run():
        service, payload, state, path, _ = setup(tmp_path, monkeypatch)
        entered = asyncio.Event()
        cancelled = asyncio.Event()

        async def slow(*args):
            entered.set()
            try:
                await asyncio.sleep(10)
            finally:
                cancelled.set()

        state.chat_adapter.create_chat_completion = slow
        task = asyncio.create_task(service.generate(payload, state, connected))
        await asyncio.wait_for(entered.wait(), 1)
        if change == "cancel":
            service.cancel(CandidateCancel(session_id="session", operation_id="observation"))
        elif change == "foreground":
            service.cancel_current()
        elif change == "grants":
            path.write_text("{}")
        else:
            state.resident_service.snapshot["revision"] += 1
        result = await asyncio.wait_for(task, 1)
        assert result["status"] == "cancelled" and not result["candidate"]["text"]
        assert cancelled.is_set() and service.current is None and state.inference_gate.active == 0

    asyncio.run(run())


@pytest.mark.parametrize("reason", ["unknown_participant", "suppressed", "restricted", "busy", "missing"])
def test_denied_or_busy_candidate_never_calls_model(tmp_path, monkeypatch, reason):
    service, payload, state, path, calls = setup(tmp_path, monkeypatch)
    if reason == "unknown_participant":
        payload = payload.model_copy(update={"participants": frozenset({"stranger"})})
    elif reason == "suppressed":
        state.resident_service.snapshot["policy"]["proactive"] = "suppressed"
    elif reason == "restricted":
        state.resident_service.snapshot["policy"]["restricted_memory_ids"] = ["fixture-picnic"]
    elif reason == "busy":
        state.inference_gate.active = 1
    else:
        path.unlink()
    result = asyncio.run(service.generate(payload, state, connected))
    assert result["status"] != "candidate" and not calls
    assert not state.karte_context_client.calls


def test_empty_search_does_not_invent_memory(tmp_path, monkeypatch):
    service, payload, state, _, calls = setup(tmp_path, monkeypatch)
    payload = payload.model_copy(update={"observation": "存在しない雪山の記憶"})
    result = asyncio.run(service.generate(payload, state, connected))
    assert result["status"] == "insufficient_evidence" and not calls
    assert result["tool_attempts"] == 1 and result["model_requests"] == 0


@pytest.mark.parametrize("change", ["none", "policy", "grants", "document", "participants", "expired", "cancel"])
def test_final_ticket_rereads_version_and_consumes_once(tmp_path, monkeypatch, change):
    async def run():
        service, payload, state, path, _ = setup(tmp_path, monkeypatch)
        result = await service.generate(payload, state, connected)
        check = CandidateCheck(session_id=payload.session_id, operation_id=payload.operation_id,
            revision=payload.revision, participants=payload.participants, ticket_id=result["ticket_id"])
        if change == "policy":
            state.resident_service.snapshot["revision"] += 1
        elif change == "grants":
            path.write_text("{}")
        elif change == "document":
            state.karte_context_client.documents["fixture-picnic"] = state.karte_context_client.documents["fixture-picnic"].model_copy(update={"sha256": "0" * 64})
        elif change == "participants":
            check = check.model_copy(update={"participants": frozenset({"stranger"})})
        elif change == "expired":
            service.ticket["expires"] = 0
        elif change == "cancel":
            service.cancel_current()
        status = await service.check(check, state, connected)
        assert status["status"] == ("ready" if change == "none" else "discarded")
        assert (await service.check(check, state, connected))["status"] == "discarded"

    asyncio.run(run())
