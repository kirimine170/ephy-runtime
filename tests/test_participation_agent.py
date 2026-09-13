"""Pinned optional SDK wire format and cancellation；no inference server required．"""

import asyncio
import json
import time
import pytest

pytest.importorskip("pydantic_ai")
httpx = pytest.importorskip("httpx2")
from scripts.reuse_core.memory_probe import FixtureClient, INSTRUCTIONS
from packages.runtime_core.participation import MemoryTools, Run
from packages.runtime_core.participation_agent import run_agent


def tools_for_run():
    client = FixtureClient()
    now = time.monotonic()
    return MemoryTools(
        Run("o", "s", "t", 1, client.scope(), now + 5, now + 5, lambda: True), client
    )


def wire_response(message):
    return httpx.Response(
        200,
        json={
            "id": "fixture",
            "object": "chat.completion",
            "created": 0,
            "model": "fixture-model",
            "choices": [
                {
                    "index": 0,
                    "finish_reason": "tool_calls"
                    if "tool_calls" in message
                    else "stop",
                    "message": {"role": "assistant", **message},
                }
            ],
            "usage": {"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30},
        },
    )


def inject_transport(monkeypatch, handler):
    original = httpx.AsyncClient

    class TransportClient(original):
        def __init__(self, **kwargs):
            kwargs["transport"] = httpx.MockTransport(handler)
            super().__init__(**kwargs)

    monkeypatch.setattr(httpx, "AsyncClient", TransportClient)


def test_sdk_null_tool_messages_and_runtime_scope(monkeypatch):
    tools = tools_for_run()
    requests = []
    document = tools.client.documents["fixture-picnic"]

    async def handler(request):
        body = json.loads(request.content)
        requests.append(body)
        if len(requests) <= 2:
            name, args = (
                ("karte_search", {"query": "公園のピクニック"})
                if len(requests) == 1
                else ("karte_read", {"doc_id": document.doc_id})
            )
            return wire_response(
                {
                    "content": None,
                    "tool_calls": [
                        {
                            "id": f"call-{len(requests)}",
                            "type": "function",
                            "function": {"name": name, "arguments": json.dumps(args)},
                        }
                    ],
                }
            )
        return wire_response(
            {
                "content": json.dumps(
                    {
                        "text": "わたしも，おにぎりを覚えています．",
                        "evidence": [
                            {
                                "doc_id": document.doc_id,
                                "sha256": document.sha256,
                                "quote": document.body,
                            }
                        ],
                        "reason": "grounded",
                    }
                )
            }
        )

    inject_transport(monkeypatch, handler)
    result = asyncio.run(
        run_agent(
            tools,
            "公園のピクニック",
            "http://127.0.0.1:8082/v1",
            "fixture-model",
            INSTRUCTIONS,
        )
    )
    assert (
        result.evidence[0].doc_id == document.doc_id
        and tools.run.models == 3
        and tools.run.tools == 2
    )
    assert requests[0]["chat_template_kwargs"] == {
        "enable_thinking": True,
        "preserve_thinking": True,
    }
    assert requests[0]["parallel_tool_calls"] is False
    assert all(
        set(t["function"]["parameters"]["properties"]).issubset({"query", "doc_id"})
        for t in requests[0]["tools"]
    )
    assert len([m for m in requests[-1]["messages"] if m["role"] == "tool"]) == 2
    assert all(
        scope["projects"] == ["reuse-fixture"] for _, _, scope in tools.client.calls
    )


def test_sdk_cancel_closes_inflight_http_and_invalidates_run(monkeypatch):
    async def scenario():
        tools = tools_for_run()
        started = asyncio.Event()
        closed = asyncio.Event()

        async def handler(request):
            started.set()
            try:
                await asyncio.Event().wait()
            finally:
                closed.set()

        inject_transport(monkeypatch, handler)
        task = asyncio.create_task(
            run_agent(
                tools,
                "synthetic",
                "http://127.0.0.1:8082/v1",
                "fixture-model",
                INSTRUCTIONS,
            )
        )
        await asyncio.wait_for(started.wait(), 2)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        await asyncio.wait_for(closed.wait(), 1)
        assert tools.run.canceled and tools.run.models == 1

    asyncio.run(scenario())


@pytest.mark.parametrize(
    "arguments", ['{"query":3}', '{"query":"公園のピクニック","projects":["private"]}']
)
def test_sdk_invalid_tool_arguments_end_without_scope_mutation(monkeypatch, arguments):
    tools = tools_for_run()
    requests = []

    async def handler(request):
        requests.append(request)
        return wire_response(
            {
                "content": None,
                "tool_calls": [
                    {
                        "id": "bad",
                        "type": "function",
                        "function": {
                            "name": "karte_search",
                            "arguments": arguments,
                        },
                    }
                ],
            }
        )

    inject_transport(monkeypatch, handler)
    with pytest.raises(Exception):
        asyncio.run(
            run_agent(
                tools,
                "synthetic",
                "http://127.0.0.1:8082/v1",
                "fixture-model",
                INSTRUCTIONS,
            )
        )
    assert len(requests) <= 4 and not tools.client.calls and tools.run.tools == 1
