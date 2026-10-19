import asyncio
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading

import httpx
import pytest

from packages.config_core.loader import ModelConfig
from packages.llm_runtime.adapter import BackendStreamError, LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage, RequestMetadata


def test_stream_idle_limit_is_distinct_from_nonstream_request_timeout():
    async def scenario():
        observed = []
        def handler(request):
            observed.append(request.extensions["timeout"])
            return httpx.Response(200, json={"choices": []})
        adapter = LlamaCppChatAdapter()
        original = adapter._client
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handler), timeout=120)
        model = ModelConfig(provider="llama_cpp", model="fixture", base_url="http://fixture/v1")
        request = ChatCompletionRequest(messages=[ChatMessage(role="user", content="public fixture")])
        await adapter.create_chat_completion(model, request)
        request.stream = True
        request.metadata = RequestMetadata(web_search=True, web_search_plan_id="a" * 32)
        async for _ in adapter.stream_chat_completion(model, request):
            pass
        assert observed[0]["read"] == 120
        assert observed[1] == {"connect": 10.0, "read": 600.0, "write": 30.0, "pool": 30.0}
        await adapter.aclose()
        await original.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("stall", [False, True])
def test_real_stream_waits_for_active_generation_but_detects_idle(stall):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            try:
                for _ in range(5):
                    self.wfile.write(b'data: {"choices":[{"delta":{"content":"fact"}}]}\n\n')
                    self.wfile.flush()
                    # Total active duration exceeds the read-idle allowance．
                    threading.Event().wait(0.5 if stall else 0.05)
                self.wfile.write(b'data: [DONE]\n\n')
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    async def scenario():
        adapter = LlamaCppChatAdapter(stream_idle_timeout=0.2)
        model = ModelConfig(provider="llama_cpp", model="fixture", base_url=f"http://127.0.0.1:{server.server_port}/v1")
        request = ChatCompletionRequest(messages=[ChatMessage(role="user", content="public fixture")], stream=True)
        try:
            if stall:
                with pytest.raises(BackendStreamError, match="llm_timeout"):
                    async for _ in adapter.stream_chat_completion(model, request):
                        pass
            else:
                chunks = [chunk async for chunk in adapter.stream_chat_completion(model, request)]
                assert b"".join(chunks).count(b"fact") == 5
                assert b"[DONE]" in b"".join(chunks)
        finally:
            await adapter.aclose()
    try:
        asyncio.run(scenario())
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@pytest.mark.parametrize("metadata", [
    None,
    RequestMetadata(),
    RequestMetadata(web_search=True),
    RequestMetadata(web_search=True, web_search_plan_id="invalid"),
    RequestMetadata(web_search=True, web_search_plan_id="z" * 32),
    RequestMetadata(web_search=True, web_search_plan_id="a" * 31),
    RequestMetadata(web_search=True, web_search_plan_id="a" * 33),
    RequestMetadata(web_search=True, web_search_plan_id=" " + "a" * 32),
    RequestMetadata(web_search=False, web_search_plan_id="a" * 32),
    RequestMetadata(web_search=True, web_search_plan_id="a" * 32, source_scope="recorded_conversation"),
])
def test_ordinary_and_invalid_plan_streams_keep_existing_read_timeout(metadata):
    async def scenario():
        observed = []
        def handler(request):
            observed.append(request.extensions["timeout"])
            return httpx.Response(200, content=b"data: [DONE]\n\n")
        adapter = LlamaCppChatAdapter()
        original = adapter._client
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handler), timeout=120)
        model = ModelConfig(provider="llama_cpp", model="fixture", base_url="http://fixture/v1")
        request = ChatCompletionRequest(messages=[ChatMessage(role="user", content="public fixture")],
                                        stream=True, metadata=metadata)
        try:
            async for _ in adapter.stream_chat_completion(model, request):
                pass
            assert observed == [{"connect": 10.0, "read": 120.0, "write": 30.0, "pool": 30.0}]
        finally:
            await adapter.aclose()
            await original.aclose()
    asyncio.run(scenario())


@pytest.mark.parametrize("plan_id", ["a" * 32, "ABCDEF0123456789" * 2])
def test_valid_planned_stream_only_extends_idle_read_without_elapsed_deadline(plan_id):
    async def scenario():
        observed = []
        def handler(request):
            observed.append(request.extensions["timeout"])
            return httpx.Response(200, content=b"data: [DONE]\n\n")
        adapter = LlamaCppChatAdapter()
        original = adapter._client
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handler), timeout=120)
        model = ModelConfig(provider="llama_cpp", model="fixture", base_url="http://fixture/v1")
        request = ChatCompletionRequest(messages=[ChatMessage(role="user", content="public fixture")],
                                        stream=True, metadata=RequestMetadata(web_search=True, web_search_plan_id=plan_id))
        try:
            async for _ in adapter.stream_chat_completion(model, request):
                pass
            assert observed == [{"connect": 10.0, "read": 600.0, "write": 30.0, "pool": 30.0}]
        finally:
            await adapter.aclose()
            await original.aclose()
    asyncio.run(scenario())
