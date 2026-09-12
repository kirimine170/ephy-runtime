import asyncio
import json

import httpx
import pytest
from fastapi.testclient import TestClient

from apps.gateway.main import app
from apps.gateway.routes import _stream_error_event
from packages.config_core.loader import ModelConfig
from packages.llm_runtime.adapter import BackendStreamError, LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage, RequestMetadata
from packages.router_core.schemas import RouteDecision


MODEL = ModelConfig(provider="llama_cpp", model="synthetic", base_url="http://synthetic/v1", thinking_mode="optional")


@pytest.mark.parametrize("session,mode,resolved,expected", [
    ("voice", "auto", "fast", False),
    ("voice", "auto", "work", True),
    ("voice", "auto", "code", True),
    ("voice", "work", "work", True),
    ("default", "auto", "fast", True),
    ("default", "fast", "fast", False),
])
def test_resolved_voice_fast_policy_is_request_scoped(session, mode, resolved, expected):
    async def check():
        adapter = LlamaCppChatAdapter()
        try:
            request = ChatCompletionRequest(messages=[ChatMessage(role="user", content="synthetic")],
                max_tokens=512, stream=True, metadata=RequestMetadata(mode=mode, session_mode=session, resolved_mode=resolved),
                stream_options={"include_usage": False}, chat_template_kwargs={"unrelated_capability": True})
            payload = adapter._build_payload(MODEL, request)
            assert payload["chat_template_kwargs"]["enable_thinking"] is expected
            assert payload["chat_template_kwargs"]["unrelated_capability"] is True
            assert payload["max_tokens"] == 512
            assert payload["stream_options"]["include_usage"] is True
            assert "metadata" not in payload
            if session == "voice" and resolved == "fast":
                assert payload["reasoning_format"] == "deepseek"
            else:
                assert "reasoning_format" not in payload
        finally:
            await adapter.aclose()
    asyncio.run(check())


def test_stream_passes_terminal_reason_usage_and_done_bytes_unchanged():
    chunks = [b'data: {"choices":[{"delta":{"content":"synthetic"}}]}\n\n',
              b'data: {"choices":[{"finish_reason":"length","delta":{}}]}\n',
              b'\ndata: {"choices":[],"usage":{"completion_tokens":512}}\n\ndata: [DO', b'NE]\n\n']
    async def check():
        class Stream(httpx.AsyncByteStream):
            closed = False
            async def __aiter__(self):
                for chunk in chunks:
                    yield chunk
            async def aclose(self):
                self.closed = True
        stream = Stream()
        def handle(request):
            payload = json.loads(request.content)
            assert payload["stream_options"]["include_usage"] is True
            return httpx.Response(200, stream=stream)
        adapter = LlamaCppChatAdapter()
        await adapter.aclose()
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            result = b"".join([chunk async for chunk in adapter.stream_chat_completion(MODEL, ChatCompletionRequest(stream=True))])
            assert result == b"".join(chunks)
            assert stream.closed
        finally:
            await adapter.aclose()
    asyncio.run(check())


@pytest.mark.parametrize("failure,code,reason", [
    (httpx.ReadTimeout, "llm_timeout", "timeout"),
    (httpx.RemoteProtocolError, "llm_transport_eof", "transport_eof"),
    (httpx.ConnectError, "backend_unavailable", "unknown"),
])
def test_stream_failure_is_fixed_and_private_payload_is_not_exposed(failure, code, reason):
    secret = "PRIVATE-PROMPT-REASONING-KARTE-http://private/path"
    async def check():
        def fail(request):
            raise failure(secret)
        adapter = LlamaCppChatAdapter()
        await adapter.aclose()
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(fail))
        try:
            with pytest.raises(BackendStreamError) as error:
                async for _ in adapter.stream_chat_completion(MODEL, ChatCompletionRequest(stream=True)):
                    pass
            assert str(error.value) == code
            event = _stream_error_event(error.value, "synthetic")
            data = json.loads(event.decode().split("data: ")[1])
            assert data["code"] == code and data["finish_reason"] == reason
            assert secret not in event.decode()
        finally:
            await adapter.aclose()
    asyncio.run(check())


def test_cancel_propagates_and_closes_upstream_without_done():
    async def check():
        started = asyncio.Event()
        class Stream(httpx.AsyncByteStream):
            closed = False
            async def __aiter__(self):
                yield b'data: {"choices":[{"delta":{"content":"synthetic"}}]}\n\n'
                started.set()
                await asyncio.Future()
            async def aclose(self):
                self.closed = True
        stream = Stream()
        adapter = LlamaCppChatAdapter()
        await adapter.aclose()
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(lambda request: httpx.Response(200, stream=stream)))
        seen = []
        async def consume():
            async for chunk in adapter.stream_chat_completion(MODEL, ChatCompletionRequest(stream=True)):
                seen.append(chunk)
        try:
            task = asyncio.create_task(consume())
            await asyncio.wait_for(started.wait(), 1)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
            assert stream.closed
            assert b"[DONE]" not in b"".join(seen)
        finally:
            await adapter.aclose()
    asyncio.run(check())


@pytest.mark.parametrize("resolved_mode", ["fast", "work"])
def test_gateway_preserves_mode_persona_and_original_route_input_for_continuation(monkeypatch, resolved_mode):
    captured = {}
    with TestClient(app) as client:
        def route(request):
            captured["routing_request"] = request
            return RouteDecision(mode=resolved_mode, model_alias=resolved_mode, selected_model=app.state.app_config.models[resolved_mode])
        async def stream(*, model_config, request_payload):
            captured["inference_request"] = request_payload
            yield b'data: {"choices":[{"delta":{"content":"complete"},"finish_reason":"stop"}]}\n\n'
            yield b"data: [DONE]\n\n"
        monkeypatch.setattr(app.state.model_router, "route_chat", route)
        monkeypatch.setattr(app.state.chat_adapter, "stream_chat_completion", stream)
        response = client.post("/v1/chat/completions", json={
            "model": "auto", "stream": True, "max_tokens": 512,
            "messages": [{"role": "user", "content": "synthetic original request"},
                         {"role": "assistant", "content": "synthetic committed prefix"}],
            "metadata": {"mode": "auto", "session_mode": "voice", "resolved_mode": "code",
                         "routing_message_count": 1, "completion_guidance": "synthetic bounded continuation guidance"},
        })
        assert response.status_code == 200
        route_request = captured["routing_request"]
        assert len(route_request.messages) == 1
        assert route_request.messages[0].content == "synthetic original request"
        inference = captured["inference_request"]
        assert inference.metadata.mode == "auto" and inference.metadata.resolved_mode == resolved_mode
        assert inference.metadata.session_mode == "voice" and inference.max_tokens == 512
        assert [message.role for message in inference.messages].count("user") == 1
        system = [message.content for message in inference.messages if message.role == "system"]
        # All system policies precede conversation，including explicit continuation．
        assert len(system) >= 2
        assert app.state.prompt_manager.get_mode_system_prompt(resolved_mode) in system[0]
        assert system[-1] == "synthetic bounded continuation guidance"
        first_user = next(i for i, message in enumerate(inference.messages) if message.role == "user")
        assert all(message.role == "system" for message in inference.messages[:first_user])
        assert all(message.role != "system" for message in inference.messages[first_user:])
        assert inference.messages[-1].role == "assistant"
        assert inference.messages[-1].content == "synthetic committed prefix"


def _sse(payload):
    return b"data: " + json.dumps(payload, ensure_ascii=False).encode() + b"\r\n\r\n"


def _voice_request():
    return ChatCompletionRequest(stream=True, max_tokens=512,
        metadata=RequestMetadata(mode="auto", resolved_mode="fast", session_mode="voice"))


@pytest.mark.parametrize("reasoning,expected,tokenizer_failure", [
    ("", 0, False), ("PRIVATE 推論内容", 3, False), ("PRIVATE 推論内容", None, True),
])
def test_voice_reasoning_content_count_is_labeled_and_metadata_only(reasoning, expected, tokenizer_failure):
    async def check():
        original = (_sse({"choices": [{"delta": {"reasoning_content": reasoning}}]}) +
                    _sse({"choices": [{"delta": {"content": "PRIVATE answer"}, "finish_reason": "stop"}]}) +
                    _sse({"choices": [], "usage": {"completion_tokens": 7}}))
        class Stream(httpx.AsyncByteStream):
            async def __aiter__(self):
                # Deliberately split UTF-8 and SSE delimiters over transport chunks．
                for index in range(0, len(original), 7):
                    yield original[index:index+7]
                yield b"data: [DONE]\n\n"
        tokenizer_calls = []
        async def handle(request):
            if request.url.path.endswith("/tokenize"):
                tokenizer_calls.append(json.loads(request.content))
                assert request.extensions["timeout"]["read"] == 2.0
                if tokenizer_failure:
                    raise httpx.ReadTimeout("PRIVATE tokenizer error")
                return httpx.Response(200, json={"tokens": [1, 2, 3]})
            assert json.loads(request.content)["reasoning_format"] == "deepseek"
            return httpx.Response(200, stream=Stream())
        adapter = LlamaCppChatAdapter(); await adapter.aclose()
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            result = b"".join([chunk async for chunk in adapter.stream_chat_completion(MODEL, _voice_request())])
            assert result.startswith(original) and result.endswith(b"data: [DONE]\n\n")
            event = result[len(original):].split(b"\n\n")[0]
            assert event.startswith(b"event: generation_usage\n")
            usage = json.loads(event.split(b"data: ")[1])
            assert usage == {"reasoning_tokens": expected, "reasoning_token_source": "retokenized" if expected is not None else "unavailable"}
            assert b"PRIVATE" not in event
            assert bool(tokenizer_calls) == bool(reasoning)
            if reasoning:
                assert tokenizer_calls[0] == {"content": reasoning, "add_special": False, "parse_special": False}
        finally:
            await adapter.aclose()
    asyncio.run(check())


def test_reasoning_buffer_and_frame_size_are_bounded():
    async def check():
        for oversized_frame in (False, True):
            class Stream(httpx.AsyncByteStream):
                async def __aiter__(self):
                    if oversized_frame:
                        yield b"data: " + b"x" * (256 * 1024)
                    else:
                        for _ in range(2):
                            yield _sse({"choices": [{"delta": {"reasoning_content": "x" * (80 * 1024)}}]})
                        yield _sse({"choices": [{"delta": {}, "finish_reason": "length"}]})
                        yield b"data: [DONE]\n\n"
            def handle(request):
                assert not request.url.path.endswith("/tokenize"), "oversized reasoning must not be retained or tokenized"
                return httpx.Response(200, stream=Stream())
            adapter = LlamaCppChatAdapter(); await adapter.aclose()
            adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
            try:
                if oversized_frame:
                    with pytest.raises(BackendStreamError, match="llm_transport_eof"):
                        async for _ in adapter.stream_chat_completion(MODEL, _voice_request()):
                            pass
                else:
                    result = b"".join([chunk async for chunk in adapter.stream_chat_completion(MODEL, _voice_request())])
                    assert b'"reasoning_tokens": null' in result
                    assert b'"reasoning_token_source": "unavailable"' in result
                    assert result.endswith(b"data: [DONE]\n\n")
            finally:
                await adapter.aclose()
    asyncio.run(check())


def test_cancel_during_reasoning_tokenization_closes_stream_and_does_not_fabricate_done():
    async def check():
        started = asyncio.Event()
        class Stream(httpx.AsyncByteStream):
            closed = False
            async def __aiter__(self):
                yield _sse({"choices": [{"delta": {"reasoning_content": "PRIVATE reasoning"}, "finish_reason": "stop"}]})
                yield b"data: [DONE]\n\n"
            async def aclose(self):
                self.closed = True
        stream = Stream()
        async def handle(request):
            if request.url.path.endswith("/tokenize"):
                started.set()
                await asyncio.Future()
            return httpx.Response(200, stream=stream)
        adapter = LlamaCppChatAdapter(); await adapter.aclose()
        adapter._client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
        seen = []
        async def consume():
            async for chunk in adapter.stream_chat_completion(MODEL, _voice_request()):
                seen.append(chunk)
        try:
            task = asyncio.create_task(consume())
            await asyncio.wait_for(started.wait(), 1)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
            assert stream.closed
            assert b"[DONE]" not in b"".join(seen)
            assert b"generation_usage" not in b"".join(seen)
        finally:
            await adapter.aclose()
    asyncio.run(check())
