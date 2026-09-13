"""Synthetic Karte protocol owner＋real local model through resident fixed flow．"""

import argparse
import asyncio
from datetime import datetime, UTC
import hashlib
import json
import os
from pathlib import Path
import subprocess
import threading
import time
from types import SimpleNamespace

import httpx

from apps.gateway.model_transition import InferenceGate
from apps.gateway.resident_participation import CandidateCheck, CandidateRequest, ResidentParticipation
from packages.config_core.loader import AppConfig, ModelConfig, RouteConfig
from packages.eval_core.preference_store import PreferenceStore
from packages.eval_core.resident_schemas import ResidentSessionRequest
from packages.eval_core.resident_service import ResidentService
from packages.karte_core.context import KarteContextClient, ContextRequest, _atomic_write_bytes, _serialize_json
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.prompt_core.loader import PromptManager
from packages.router_core.router import ModelRouter
from scripts.reuse_core.memory_probe import FixtureClient, local_endpoint


async def run(args):
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False, mode=0o700)
    native = Path(args.native_karte_state).resolve() if args.native_karte_state else None
    karte = native / "karte" if native else output / "karte"
    if not native:
        karte.mkdir(mode=0o700)
    client = KarteContextClient(karte, timeout_seconds=2, poll_interval=0.01)
    fixture = FixtureClient()
    done = threading.Event()
    calls = []

    def serve():
        seen = set()
        while not done.wait(0.01):
            for path in sorted(client.requests_dir.glob("*.json")):
                if path.name in seen:
                    continue
                raw = path.read_bytes()
                request = ContextRequest.model_validate_json(raw)
                if request.operation == "search":
                    response = fixture.search(request.query.text, **request.scope.model_dump(), top_k=request.query.top_k)
                else:
                    response = fixture.read(request.doc_id, **request.scope.model_dump())
                calls.append(request.operation)
                response = response.model_copy(update={"request_id": request.request_id,
                    "request_sha256": hashlib.sha256(raw).hexdigest(), "processed_at": datetime.now(UTC)})
                _atomic_write_bytes(client.responses_dir / path.name, _serialize_json(response.model_dump(mode="json")))
                seen.add(path.name)

    thread = threading.Thread(target=serve, daemon=True)
    participants = frozenset({"owner"}) if native else fixture.scope().participants
    grants = native / "participation-grants.json" if native else output / "grants.json"
    if not native:
        grants.write_text(json.dumps({"scope": fixture.scope().context().model_dump(),
            "participants": sorted(participants), "documents": [{"doc_id": doc, "sha256": sha,
            "participants": sorted(participants)} for doc, sha in sorted(fixture.scope().shareable)]}))
    os.environ["EPHY_RESIDENT_PARTICIPATION_GRANTS"] = str(grants)
    root = Path(__file__).resolve().parents[2]
    resident = ResidentService(store=PreferenceStore(output / "feedback"), instance_id="resident-synthetic",
                              owner_key="fixture-hana", repository_root=root)
    resident.create_session(ResidentSessionRequest(session_id="synthetic-session", owner_selected=True, storage_consent=False))
    config = AppConfig(models={"work": ModelConfig(provider="llama_cpp", model=args.model,
        base_url=local_endpoint(args.endpoint), max_context=8192, default_temperature=0.3,
        thinking_mode="always", preserve_thinking=True)}, routes={"work": RouteConfig(model="work")})
    adapter = LlamaCppChatAdapter(timeout=60)
    await adapter._client.aclose()
    adapter._client = httpx.AsyncClient(timeout=60, trust_env=False, follow_redirects=False)
    state = SimpleNamespace(resident_service=resident, karte_context_client=client,
        inference_gate=InferenceGate(), model_router=ModelRouter(config), prompt_manager=PromptManager(), chat_adapter=adapter)
    results = []

    async def connected():
        return False

    receiver = None
    if native:
        receiver = subprocess.Popen([str(native / "karte-context-fixture"), "--data-root", str(karte)],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        thread.start()
    try:
        for case in fixture.fixture["cases"]:
            if case["id"] not in args.cases.split(","):
                continue
            payload = CandidateRequest(session_id="synthetic-session", operation_id="probe-" + case["id"],
                revision=1, observation=case["query"], participants=participants,
                speaker_id="owner" if native else "fixture-hana",
                observation_allowed=True)
            service = ResidentParticipation()
            result = await service.generate(payload, state, connected)
            if result["status"] == "candidate":
                result["final_check"] = await service.check(CandidateCheck(session_id=payload.session_id,
                    operation_id=payload.operation_id, revision=payload.revision, participants=payload.participants,
                    ticket_id=result["ticket_id"]), state, connected)
            result["case"] = case["id"]
            result["expected_doc"] = case["expected_doc"]
            result["passed"] = (not result["candidate"]["text"] and result["status"] == "insufficient_evidence"
                if case["expected_doc"] is None else any(e["doc_id"] == case["expected_doc"] for e in result["candidate"]["evidence"]))
            results.append(result)
            print(json.dumps(result, ensure_ascii=False), flush=True)
    finally:
        done.set()
        if receiver:
            receiver.terminate()
            try:
                await asyncio.to_thread(receiver.wait, 3)
            except subprocess.TimeoutExpired:
                receiver.kill()
                await asyncio.to_thread(receiver.wait, 3)
        else:
            await asyncio.to_thread(thread.join, 3)
        await adapter.aclose()
        (output / "metrics.json").write_text(json.dumps({"synthetic": True, "real_local_model": True,
            "karte_owner": "pinned Karte contextcore 90c69c58945e4eb00ec5f13c73b444ff298443d2" if native else "synthetic protocol responder using real KarteContextClient exchange",
            "receiver_reaped": receiver.returncode is not None if receiver else None,
            "model": args.model, "endpoint": args.endpoint, "tool_operations": calls, "results": results}, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("--endpoint", default="http://127.0.0.1:8082/v1")
    parser.add_argument("--model", default="qwen3.8-27b")
    parser.add_argument("--cases", default="simple,missing")
    parser.add_argument("--native-karte-state", help="Prepared isolated fixture state；owns a real Karte receiver for this run")
    asyncio.run(run(parser.parse_args()))
