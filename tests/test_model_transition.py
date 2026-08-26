import asyncio
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

from apps.gateway.main import app
from apps.gateway.model_transition import InferenceGate, InferenceGateMiddleware


def test_gateway_transition_blocks_inference_until_matching_release():
    with TestClient(app, client=("127.0.0.1", 50000)) as client:
        token = client.post("/v1/admin/model-transition/begin", json={}).json()["token"]
        assert client.post("/v1/admin/model-transition/begin", json={}).status_code == 409
        for path in ("chat/completions", "rag/query", "embeddings", "eval/run"):
            response = client.post(f"/v1/{path}", json={})
            assert response.status_code == 503
            assert response.headers["retry-after"] == "3"
        assert client.get("/health").status_code == 200
        assert client.post("/v1/admin/model-transition/end", json={"token": "wrong"}).status_code == 409
        assert client.post("/v1/admin/model-transition/end", json={"token": token}).status_code == 200
        assert app.state.inference_gate.transitioning() is False


def test_remote_client_and_browser_origin_cannot_mutate_transition():
    with TestClient(app, client=("192.0.2.10", 50000)) as client:
        assert client.post("/v1/admin/model-transition/begin", json={}).status_code == 403
    with TestClient(app, client=("127.0.0.1", 50000)) as client:
        assert client.post("/v1/admin/model-transition/begin", json={}, headers={"Origin": "https://example.org"}).status_code == 403


def test_stream_response_holds_lease_until_body_finishes():
    async def scenario():
        gate = InferenceGate()
        sent = asyncio.Event()
        finish = asyncio.Event()

        async def streaming_app(scope, receive, send):
            await send({"type": "http.response.start", "status": 200, "headers": []})
            sent.set()
            await finish.wait()
            await send({"type": "http.response.body", "body": b"done"})

        async def discard(_): pass
        middleware = InferenceGateMiddleware(streaming_app)
        task = asyncio.create_task(middleware({"type": "http", "path": "/v1/chat/completions",
            "app": SimpleNamespace(state=SimpleNamespace(inference_gate=gate))}, None, discard))
        await sent.wait()
        assert gate.active == 1
        with pytest.raises(ValueError, match="active"):
            gate.begin()
        finish.set()
        await task
        assert gate.active == 0
        assert gate.begin()

    asyncio.run(scenario())


def test_abandoned_transition_expires(monkeypatch):
    gate = InferenceGate()
    gate.begin()
    monkeypatch.setattr("apps.gateway.model_transition.time.monotonic", lambda: gate.expires + 1)
    assert not gate.transitioning()
