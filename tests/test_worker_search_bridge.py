import asyncio
import copy
import json
import time
from types import SimpleNamespace

import httpx
import pytest
from fastapi import FastAPI

from apps.gateway.routes import build_router
from packages.config_core.loader import AppConfig, ModelConfig, WebSearchConfig
from packages.prompt_core.loader import PromptManager
from packages.router_core.router import ModelRouter
from packages.web_search_core.service import WebSearchService
from packages.web_search_core.worker_provider import WorkerSearchError, WorkerSearchPending, WorkerSearchProvider


class Adapter:
    def __init__(self):
        self.final_calls = 0
        self.extract_calls = 0

    async def create_chat_completion(self, *, model_config, request_payload):
        system = str(request_payload.messages[0].content)
        if "Rewrite the user text" in system:
            content = json.dumps({"query": "public product release", "risk_categories": []})
        elif "isolated fact extractor" in system:
            self.extract_calls += 1
            assert len(request_payload.messages) == 2
            content = json.dumps({"claims": [{"source_id": "W1", "claim": "The public release is available."}]})
        else:
            self.final_calls += 1
            joined = str([message.content for message in request_payload.messages])
            assert "The public release is available." in joined
            assert "remote untrusted raw snippet" not in joined
            content = "調べてみたところ，公開リリースを確認できました．"
        return {"choices": [{"message": {"role": "assistant", "content": content}}]}

    async def stream_chat_completion(self, *, model_config, request_payload):
        # Exercise the same safe final grounding assertions as the JSON response．
        response = await self.create_chat_completion(model_config=model_config, request_payload=request_payload)
        content = response["choices"][0]["message"]["content"]
        yield ("data: " + json.dumps({"choices": [{"delta": {"content": content}, "finish_reason": None}]}) + "\n\n").encode()
        yield b'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'


class ManagerFixture:
    def __init__(self):
        self.submit_calls = 0
        self.jobs = {}
        self.state = "completed"
        self.requests = []
        self.mutate_view = None
        self.mutate_result = None
        self.drop_submit_once = False

    def _result(self, view):
        return {"contract": "0.4", "job_id": view["job_id"], "attempt_id": "attempt-0001", "status": "succeeded",
                "stop_reason": None, "usage": {"search_requests": 1, "search_credits": 0, "fetch_requests": 0,
                                              "document_bytes": 0, "model_requests": 0},
                "elapsed_seconds": 0.1, "manifest": [], "payload": {"mode": "search", "failures": [],
                    "queries": [{**view["input"]["queries"][0], "status": "ok", "result_count": 2,
                                 "diagnostic": {"engine": "fixture", "unresponsive": False, "reasons": [], "result_count": 2, "code": None}}],
                    "candidates": [{"url": "https://example.com/release", "title": "<b>Release</b>",
                                    "snippet": "remote untrusted raw snippet", "query_id": "Q1", "engine": "fixture", "rank": 1,
                                    "discovered_at": "2026-10-19T00:00:00Z", "supplied": False},
                                   {"url": "http://127.0.0.1/private", "title": "Private", "snippet": "private content", "query_id": "Q1",
                                    "engine": "fixture", "rank": 2, "discovered_at": "2026-10-19T00:00:00Z", "supplied": False}]}}

    def __call__(self, request):
        self.requests.append(request)
        assert request.headers["x-ephy-credential"] == "fixture-only-credential"
        if request.url.path == "/jobs":
            self.submit_calls += 1
            body = json.loads(request.content)
            key = body["submit_key"]
            if key not in self.jobs:
                now = time.time()
                canonical = json.dumps(body["input"], ensure_ascii=False, sort_keys=True, separators=(",", ":"))
                import hashlib
                self.jobs[key] = {**body, "job_id": f"job-{len(self.jobs) + 1:08d}", "mode": "search", "state": "queued",
                                  "input_hash": hashlib.sha256(canonical.encode()).hexdigest(), "created_at": now,
                                  "deadline_at": now + body["deadline_seconds"], "worker_id": None, "attempt_id": None,
                                  "usage": {}, "result": None, "stop_reason": None}
            view = copy.deepcopy(self.jobs[key])
            if self.drop_submit_once:
                self.drop_submit_once = False
                raise httpx.ReadError("untrusted exception body with fixture-only-credential", request=request)
        else:
            requested_id = request.url.path.split("/")[2]
            view = copy.deepcopy(next(view for view in self.jobs.values() if view["job_id"] == requested_id))
            view["state"] = "cancelled" if request.url.path.endswith("/cancel") else self.state
            if view["state"] == "completed":
                view.update(worker_id=view["target_worker_id"], attempt_id="attempt-0001")
                view["result"] = self._result(view)
                view["usage"] = view["result"]["usage"]
                if self.mutate_result:
                    self.mutate_result(view["result"])
        if self.mutate_view:
            self.mutate_view(view)
        if request.url.path.endswith("/result"):
            view = {key: view[key] for key in ("job_id", "state", "stop_reason", "usage", "result")}
        return httpx.Response(200, json=view)


def make_service(monkeypatch, manager=None, **settings):
    monkeypatch.setenv("TEST_MANAGER_CREDENTIAL", "fixture-only-credential")
    config = AppConfig(models={"fast": ModelConfig(provider="llama_cpp", model="fixture", base_url="http://127.0.0.1:8081/v1")},
                       routes={"fast": {"model": "fast"}, "auto": {"model": "fast"}},
                       web_search=WebSearchConfig(enabled=True, provider="worker", manager_credential_env="TEST_MANAGER_CREDENTIAL",
                                                  worker_wait_seconds=0, **settings))
    manager = manager or ManagerFixture()
    client = httpx.AsyncClient(transport=httpx.MockTransport(manager))
    provider = WorkerSearchProvider(config.web_search, client)
    adapter = Adapter()
    return WebSearchService(config, adapter, provider), manager, config, adapter, client


def test_pending_resume_concurrent_and_safe_claims(monkeypatch):
    async def scenario():
        service, manager, _, adapter, client = make_service(monkeypatch)
        manager.state = "running"
        prompt = "raw original user request that must stay local"
        plan = await service.create_plan(prompt)
        # A running worker identity is mandatory．
        manager.mutate_view = lambda view: view.update(worker_id="worker-local") if view["state"] == "running" else None
        with pytest.raises(WorkerSearchPending) as pending:
            await service.search_for_chat(prompt, plan["plan_id"])
        assert pending.value.identity["job_id"] == "job-00000001"
        manager.state = "completed"
        results = await asyncio.gather(*(service.search_for_chat(prompt, plan["plan_id"]) for _ in range(3)))
        assert results[0] == results[1] == results[2]
        assert manager.submit_calls == 1 and len(manager.jobs) == 1 and adapter.extract_calls == 1
        assert results[0][0][0]["title"] == "Release"
        assert len(results[0][0]) == 1
        assert "The public release" in results[0][1]
        assert all(prompt not in request.content.decode() for request in manager.requests)
        assert "public product release" in manager.requests[0].content.decode()
        with pytest.raises(ValueError, match="does not match"):
            await service.search_for_chat("other request", plan["plan_id"])
        await client.aclose()
    asyncio.run(scenario())


def test_uncertain_submit_reuses_same_key_and_safe_error(monkeypatch, caplog):
    async def scenario():
        service, manager, _, _, client = make_service(monkeypatch)
        plan = await service.create_plan("public release")
        manager.drop_submit_once = True
        with pytest.raises(WorkerSearchError) as caught:
            await service.search_for_chat("public release", plan["plan_id"])
        assert str(caught.value) == "worker_manager_unavailable"
        await service.search_for_chat("public release", plan["plan_id"])
        assert manager.submit_calls == 2 and len(manager.jobs) == 1
        assert "fixture-only-credential" not in str(caught.value) + caplog.text
        assert json.loads(manager.requests[0].content)["submit_key"] == json.loads(manager.requests[1].content)["submit_key"]
        await client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("state", ["partial", "failed", "cancelled", "lost"])
def test_terminal_failures_are_not_success(monkeypatch, state):
    async def scenario():
        service, manager, _, adapter, client = make_service(monkeypatch)
        manager.state = state
        plan = await service.create_plan("public release")
        with pytest.raises(WorkerSearchError, match="worker_search_failed"):
            await service.search_for_chat("public release", plan["plan_id"])
        assert adapter.extract_calls == adapter.final_calls == 0
        await client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("mutation", [
    lambda view: view.update(job_id="job-other-0001"),
    lambda view: view.update(input_hash="0" * 64),
    lambda view: view.update(target_worker_id="another-worker"),
    lambda view: view["input"].update(topic="injected different topic"),
    lambda view: view.update(contract="0.5"),
    lambda view: view.update(state="unknown"),
    lambda view: view.update(created_at=10 ** 1000),
    lambda view: view.update(worker_id="wrong-worker") if view["state"] == "completed" else None,
])
def test_job_identity_is_strict(monkeypatch, mutation):
    async def scenario():
        service, manager, _, adapter, client = make_service(monkeypatch)
        plan = await service.create_plan("public release")
        # Bind successful submission before adversarial poll．
        original = manager.__call__
        def handler(request):
            response = original(request)
            if request.url.path != "/jobs":
                view = response.json()
                mutation(view)
                return httpx.Response(200, json=view)
            return response
        client._transport = httpx.MockTransport(handler)
        with pytest.raises(WorkerSearchError):
            await service.search_for_chat("public release", plan["plan_id"])
        assert adapter.extract_calls == 0
        await client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("mutation", [
    lambda result: result.update(job_id="wrong-job-0001"),
    lambda result: result.update(attempt_id="wrong-attempt-0001"),
    lambda result: result.update(status="partial"),
    lambda result: result["payload"]["queries"][0].update(text="different text"),
    lambda result: result["payload"]["queries"][0].update(status="failed"),
    lambda result: result["payload"]["candidates"][0].update(query_id="Q9"),
    lambda result: result.update(unexpected="injected"),
    lambda result: result["payload"]["failures"].append({"stage": "search", "query_id": "Q1", "source_id": None, "code": "failure", "detail": None}),
])
def test_result_envelope_rejected_before_extraction(monkeypatch, mutation):
    async def scenario():
        service, manager, _, adapter, client = make_service(monkeypatch)
        manager.mutate_result = mutation
        plan = await service.create_plan("public release")
        with pytest.raises(WorkerSearchError, match="worker_result_invalid"):
            await service.search_for_chat("public release", plan["plan_id"])
        assert adapter.extract_calls == 0
        await client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("tampered", [False, True])
def test_worker_japanese_query_projection_preserves_approved_input_identity(monkeypatch, tampered):
    async def scenario():
        _, manager, config, _, client = make_service(monkeypatch)
        provider = WorkerSearchProvider(config.web_search, client)
        approved = "Qwen、Llama 比較。"
        projected = "Qwen，Llama 比較．"
        session = provider.new_session(approved, "japanese-plan-0001")
        original_hash = session.input_hash
        def normalize_result(result):
            result["payload"]["queries"][0]["text"] = projected + (" altered scope" if tampered else "")
        manager.mutate_result = normalize_result
        if tampered:
            with pytest.raises(WorkerSearchError, match="worker_result_invalid"):
                await provider.search_job(session)
        else:
            sources = await provider.search_job(session)
            assert sources[0]["url"] == "https://example.com/release"
        submitted = json.loads(manager.requests[0].content)
        view = next(iter(manager.jobs.values()))
        assert submitted["input"]["topic"] == approved
        assert submitted["input"]["queries"][0]["text"] == approved
        assert view["input"] == submitted["input"] == session.submit["input"]
        assert view["input_hash"] == session.input_hash == original_hash
        await client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("url", ["http://192.168.0.2:8321", "https://user:secret@example.com", "https://example.com/?secret=value", "file:///tmp/manager", "https://example.com:invalid"])
def test_credentials_require_safe_manager_endpoint(monkeypatch, url):
    monkeypatch.setenv("TEST_MANAGER_CREDENTIAL", "fixture-only-credential")
    with pytest.raises(ValueError, match="requires HTTPS"):
        WorkerSearchProvider(WebSearchConfig(manager_url=url, manager_credential_env="TEST_MANAGER_CREDENTIAL"))


def test_manager_missing_credential_has_safe_error(monkeypatch):
    monkeypatch.delenv("TEST_MANAGER_CREDENTIAL", raising=False)
    with pytest.raises(ValueError, match="credential environment"):
        WorkerSearchProvider(WebSearchConfig(manager_credential_env="TEST_MANAGER_CREDENTIAL"))


@pytest.mark.parametrize("response", [
    httpx.Response(302, headers={"Location": "https://another.example/secret"}),
    httpx.Response(403, json={"error": "fixture-only-credential public private raw text"}),
    httpx.Response(200, content=b"x" * 2048, headers={"Content-Type": "application/json"}),
    httpx.Response(200, json=[]),
    httpx.Response(200, text="private raw text"),
])
def test_redirect_errors_and_unbounded_payloads_fail_closed(monkeypatch, response, caplog):
    async def scenario():
        monkeypatch.setenv("TEST_MANAGER_CREDENTIAL", "fixture-only-credential")
        requests = []
        def handler(request):
            requests.append(request)
            return response
        client = httpx.AsyncClient(transport=httpx.MockTransport(handler), follow_redirects=True)
        provider = WorkerSearchProvider(WebSearchConfig(manager_credential_env="TEST_MANAGER_CREDENTIAL", worker_response_bytes=1024), client)
        with pytest.raises(WorkerSearchError) as caught:
            await provider.search_job(provider.new_session("public query", "plan-0001"))
        assert len(requests) == 1
        assert "fixture-only-credential" not in str(caught.value) + caplog.text
        assert "private raw text" not in str(caught.value) + caplog.text
        await client.aclose()
    asyncio.run(scenario())


def test_gateway_returns_pending_then_grounded_final_and_explicit_failure(monkeypatch):
    async def scenario():
        service, manager, config, adapter, manager_client = make_service(monkeypatch)
        app = FastAPI()
        app.include_router(build_router())
        app.state.app_config = config
        app.state.web_search_service = service
        app.state.chat_adapter = adapter
        app.state.model_router = ModelRouter(config)
        app.state.prompt_manager = PromptManager()
        app.state.rag_service = SimpleNamespace(search_grounding_sources=lambda **_: [])
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://runtime",
                                     headers={"Prefer": "return=representation, ReSpOnD-AsYnC"}) as client:
            plan = (await client.post("/v1/web-search/plan", json={"query": "public release"})).json()
            payload = {"model": "fast", "messages": [{"role": "user", "content": "public release"}],
                       "metadata": {"web_search": True, "web_search_plan_id": plan["plan_id"]}}
            manager.state = "queued"
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 202
            assert response.headers["Preference-Applied"] == "respond-async"
            assert response.json()["status"] == "pending"
            assert response.json()["plan_id"] == plan["plan_id"]
            assert adapter.final_calls == 0
            manager.state = "completed"
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 200, response.text
            assert response.json()["web_search_status"]["job_id"] == "job-00000001"
            assert response.json()["web_search_status"]["state"] == "completed"
            assert response.json()["choices"][0]["message"]["content"].startswith("調べてみたところ")
            assert manager.submit_calls == 1
            plan = (await client.post("/v1/web-search/plan", json={"query": "new public release"})).json()
            payload["messages"][0]["content"] = "new public release"
            payload["metadata"]["web_search_plan_id"] = plan["plan_id"]
            manager.state = "lost"
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 502
            assert response.json()["detail"]["code"] == "worker_search_failed"
            assert adapter.final_calls == 1
        await manager_client.aclose()
    asyncio.run(scenario())


def test_cancel_and_approval_gate(monkeypatch):
    async def scenario():
        service, manager, _, _, client = make_service(monkeypatch)
        plan = await service.create_plan("alice@example.com public release")
        assert plan["decision"] == "confirm"
        with pytest.raises(ValueError, match="approval"):
            await service.search_for_chat("alice@example.com public release", plan["plan_id"])
        assert manager.submit_calls == 0
        service.approve(plan["plan_id"])
        manager.state = "queued"
        with pytest.raises(WorkerSearchPending):
            await service.search_for_chat("alice@example.com public release", plan["plan_id"])
        assert (await service.cancel_plan(plan["plan_id"]))["state"] == "cancelled"
        await client.aclose()
    asyncio.run(scenario())


def test_streaming_chat_pending_then_provenance_and_final_deltas(monkeypatch):
    async def scenario():
        service, manager, config, adapter, manager_client = make_service(monkeypatch)
        app = FastAPI()
        app.include_router(build_router())
        for name, value in {"app_config": config, "web_search_service": service, "chat_adapter": adapter,
                            "model_router": ModelRouter(config), "prompt_manager": PromptManager(),
                            "rag_service": SimpleNamespace(search_grounding_sources=lambda **_: [])}.items():
            setattr(app.state, name, value)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://runtime",
                                     headers={"Prefer": "respond-async"}) as client:
            plan = (await client.post("/v1/web-search/plan", json={"query": "public release"})).json()
            payload = {"model": "fast", "stream": True, "messages": [{"role": "user", "content": "public release"}],
                       "metadata": {"web_search": True, "web_search_plan_id": plan["plan_id"]}}
            manager.state = "queued"
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 202 and adapter.final_calls == 0
            manager.state = "completed"
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 200
            assert response.headers["content-type"].startswith("text/event-stream")
            frames = response.text.split("\n\n")
            status = next(json.loads(frame.split("data: ", 1)[1]) for frame in frames if frame.startswith("event: web_search_status"))
            assert status["job_id"] == "job-00000001" and status["plan_id"] == plan["plan_id"]
            assert status["state"] == "completed" and status["source_count"] == 1
            assert '"finish_reason": "stop"' in response.text or '"finish_reason":"stop"' in response.text
            assert "data: [DONE]" in response.text
            assert manager.submit_calls == adapter.final_calls == 1
        await manager_client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("stream", [False, True])
@pytest.mark.parametrize("prefer", [None, "xrespond-async", "respond-async-extra"])
def test_legacy_chat_waits_through_pending_without_changing_completion_format(monkeypatch, stream, prefer):
    async def scenario():
        service, manager, config, adapter, manager_client = make_service(monkeypatch, worker_poll_seconds=0.001)
        app = FastAPI()
        app.include_router(build_router())
        for name, value in {"app_config": config, "web_search_service": service, "chat_adapter": adapter,
                            "model_router": ModelRouter(config), "prompt_manager": PromptManager(),
                            "rag_service": SimpleNamespace(search_grounding_sources=lambda **_: [])}.items():
            setattr(app.state, name, value)
        manager.state = "queued"
        polls = []
        def complete_after_pending(view):
            if manager.requests[-1].url.path != "/jobs" and view["state"] == "queued":
                # The first GET still returns queued, forcing WorkerSearchPending．
                polls.append(view["job_id"])
                manager.state = "completed"
        manager.mutate_view = complete_after_pending
        headers = {"Prefer": prefer} if prefer else {}
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://runtime", headers=headers) as client:
            plan = (await client.post("/v1/web-search/plan", json={"query": "public release"})).json()
            payload = {"model": "fast", "stream": stream, "messages": [{"role": "user", "content": "public release"}],
                       "metadata": {"web_search": True, "web_search_plan_id": plan["plan_id"]}}
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 200
            assert "Preference-Applied" not in response.headers
            if stream:
                assert response.headers["content-type"].startswith("text/event-stream")
                frames = response.text.split("\n\n")
                status = next(json.loads(frame.split("data: ", 1)[1]) for frame in frames if frame.startswith("event: web_search_status"))
                assert status["job_id"] == "job-00000001" and status["state"] == "completed"
                assert "data: [DONE]" in response.text
            else:
                assert response.headers["content-type"].startswith("application/json")
                assert response.json()["choices"][0]["message"]["content"].startswith("調べてみたところ")
                assert response.json()["web_search_status"]["job_id"] == "job-00000001"
            assert polls == ["job-00000001"]
            assert manager.submit_calls == adapter.final_calls == 1
            assert len(manager.jobs) == 1
        await manager_client.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("stream", [False, True])
def test_legacy_pending_then_terminal_failure_never_becomes_completion(monkeypatch, stream):
    async def scenario():
        service, manager, config, adapter, manager_client = make_service(monkeypatch, worker_poll_seconds=0.001)
        app = FastAPI()
        app.include_router(build_router())
        for name, value in {"app_config": config, "web_search_service": service, "chat_adapter": adapter,
                            "model_router": ModelRouter(config), "prompt_manager": PromptManager(),
                            "rag_service": SimpleNamespace(search_grounding_sources=lambda **_: [])}.items():
            setattr(app.state, name, value)
        manager.state = "queued"
        def fail_after_pending(view):
            if manager.requests[-1].url.path != "/jobs" and view["state"] == "queued":
                manager.state = "lost"
        manager.mutate_view = fail_after_pending
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://runtime") as client:
            plan = (await client.post("/v1/web-search/plan", json={"query": "public release"})).json()
            payload = {"model": "fast", "stream": stream, "messages": [{"role": "user", "content": "public release"}],
                       "metadata": {"web_search": True, "web_search_plan_id": plan["plan_id"]}}
            response = await client.post("/v1/chat/completions", json=payload)
            assert response.status_code == 502
            assert response.json()["detail"]["code"] == "worker_search_failed"
            assert response.json()["detail"]["state"] == "lost"
            assert manager.submit_calls == 1 and len(manager.jobs) == 1
            assert adapter.final_calls == adapter.extract_calls == 0
        await manager_client.aclose()
    asyncio.run(scenario())
