from collections.abc import Sequence
import json
import re

import httpx

from packages.config_core.loader import ModelConfig
from .schemas import ChatCompletionRequest, EmbeddingRequest


class BackendStreamError(RuntimeError):
    """A fixed public failure code，without backend URLs or response bodies．"""

    def __init__(self, code: str, finish_reason: str) -> None:
        super().__init__(code)
        self.code = code
        self.finish_reason = finish_reason


class LlamaCppChatAdapter:
    _MAX_SSE_FRAME_BYTES = 256 * 1024
    _MAX_REASONING_BYTES = 128 * 1024
    def __init__(self, timeout: float = 120.0) -> None:
        self._client = httpx.AsyncClient(timeout=timeout)

    async def aclose(self) -> None:
        await self._client.aclose()

    async def create_chat_completion(
        self,
        model_config: ModelConfig,
        request_payload: ChatCompletionRequest,
        lora_adapters: list[dict] | None = None,
    ) -> dict:
        if not model_config.base_url:
            raise RuntimeError(f"Model '{model_config.model}' is missing base_url")

        body = self._build_payload(
            model_config=model_config,
            request_payload=request_payload,
            lora_adapters=lora_adapters,
        )

        try:
            response = await self._client.post(
                f"{model_config.base_url.rstrip('/')}/chat/completions",
                json=body,
            )
            response.raise_for_status()
        except httpx.TimeoutException as exc:
            raise BackendStreamError("llm_timeout", "timeout") from exc
        except httpx.RemoteProtocolError as exc:
            raise BackendStreamError("llm_transport_eof", "transport_eof") from exc
        except httpx.HTTPError as exc:
            raise BackendStreamError("backend_unavailable", "unknown") from exc

        return response.json()

    async def list_lora_adapters(self, model_config: ModelConfig) -> list[dict]:
        if not model_config.base_url:
            raise RuntimeError(f"Model '{model_config.model}' is missing base_url")
        root = model_config.base_url.rstrip("/")
        if root.endswith("/v1"):
            root = root[:-3]
        try:
            response = await self._client.get(f"{root}/lora-adapters")
            response.raise_for_status()
            payload = response.json()
        except (httpx.HTTPError, ValueError) as exc:
            raise RuntimeError(
                f"Backend LoRA inspection failed for model '{model_config.model}': {exc}"
            ) from exc
        if not isinstance(payload, list) or any(not isinstance(item, dict) for item in payload):
            raise RuntimeError("Backend returned an invalid LoRA adapter list")
        return payload

    async def stream_chat_completion(
        self,
        model_config: ModelConfig,
        request_payload: ChatCompletionRequest,
    ):
        if not model_config.base_url:
            raise RuntimeError(f"Model '{model_config.model}' is missing base_url")

        body = self._build_payload(model_config=model_config, request_payload=request_payload)

        try:
            async with self._client.stream(
                "POST",
                f"{model_config.base_url.rstrip('/')}/chat/completions",
                json=body,
                headers={"Accept": "text/event-stream"},
            ) as response:
                response.raise_for_status()
                metadata = request_payload.metadata
                observe_reasoning = metadata is not None and metadata.session_mode == "voice" and metadata.resolved_mode == "fast"
                chunks = self._stream_with_reasoning_count(response, model_config) if observe_reasoning else response.aiter_bytes()
                async for chunk in chunks:
                    if chunk:
                        yield chunk
        except httpx.TimeoutException as exc:
            raise BackendStreamError("llm_timeout", "timeout") from exc
        except httpx.RemoteProtocolError as exc:
            raise BackendStreamError("llm_transport_eof", "transport_eof") from exc
        except httpx.HTTPError as exc:
            raise BackendStreamError("backend_unavailable", "unknown") from exc

    async def _stream_with_reasoning_count(self, response: httpx.Response, model_config: ModelConfig):
        """Observe reasoning content transiently，without changing completion deltas．"""
        pending = bytearray()
        reasoning = bytearray()
        available = True
        terminal = False
        done = False
        try:
            async for chunk in response.aiter_bytes():
                pending.extend(chunk)
                while match := re.search(rb"\r?\n\r?\n", pending):
                    end = match.end()
                    if end > self._MAX_SSE_FRAME_BYTES:
                        raise BackendStreamError("llm_transport_eof", "transport_eof")
                    frame = bytes(pending[:end])
                    del pending[:end]
                    data = b"\n".join(line[5:].lstrip(b" ") for line in frame.splitlines() if line.startswith(b"data:"))
                    if data:
                        if done:
                            raise BackendStreamError("llm_transport_eof", "transport_eof")
                        if data == b"[DONE]":
                            done = True
                            if terminal:
                                count = await self._count_reasoning_content(model_config, reasoning) if available else None
                                usage = {"reasoning_tokens": count, "reasoning_token_source": "retokenized" if count is not None else "unavailable"}
                                yield f"event: generation_usage\ndata: {json.dumps(usage)}\n\n".encode()
                        else:
                            try:
                                payload = json.loads(data)
                                choices = payload.get("choices", [])
                                if not isinstance(choices, list) or len(choices) > 1:
                                    raise ValueError("invalid choices")
                                for choice in choices:
                                    if not isinstance(choice, dict):
                                        raise ValueError("invalid choice")
                                    if choice.get("finish_reason") is not None:
                                        terminal = True
                                    delta = choice.get("delta") or {}
                                    text = delta.get("reasoning_content", "")
                                    if not isinstance(text, str):
                                        raise ValueError("invalid reasoning content")
                                    encoded = text.encode("utf-8")
                                    if available and len(reasoning) + len(encoded) <= self._MAX_REASONING_BYTES:
                                        reasoning.extend(encoded)
                                    else:
                                        available = False
                                        reasoning.clear()
                            except (ValueError, TypeError, AttributeError, UnicodeError) as exc:
                                raise BackendStreamError("llm_transport_eof", "transport_eof") from exc
                    yield frame
                if len(pending) > self._MAX_SSE_FRAME_BYTES:
                    raise BackendStreamError("llm_transport_eof", "transport_eof")
            if pending:
                # Preserve a truncated frame so Runtime can classify the missing
                # terminal boundary，without manufacturing DONE or usage．
                yield bytes(pending)
        finally:
            pending.clear()
            reasoning.clear()

    async def _count_reasoning_content(self, model_config: ModelConfig, reasoning: bytearray) -> int | None:
        if not reasoning:
            return 0
        root = model_config.base_url.rstrip("/")
        if root.endswith("/v1"):
            root = root[:-3]
        try:
            response = await self._client.post(f"{root}/tokenize", json={
                "content": reasoning.decode("utf-8"), "add_special": False, "parse_special": False,
            }, timeout=2.0)
            response.raise_for_status()
            tokens = response.json().get("tokens")
            if not isinstance(tokens, list) or len(tokens) > 200_000 or any(type(token) is not int or token < 0 for token in tokens):
                return None
            return len(tokens)
        except (httpx.HTTPError, ValueError, TypeError, AttributeError):
            return None

    async def create_embedding(
        self,
        model_config: ModelConfig,
        request_payload: EmbeddingRequest,
    ) -> dict:
        if not model_config.base_url:
            raise RuntimeError(f"Model '{model_config.model}' is missing base_url")

        body = request_payload.model_dump(exclude_none=True)
        body["model"] = model_config.model

        try:
            response = await self._client.post(
                f"{model_config.base_url.rstrip('/')}/embeddings",
                json=body,
            )
            response.raise_for_status()
        except httpx.HTTPError as exc:
            raise RuntimeError(f"Backend request failed for model '{model_config.model}': {exc}") from exc

        return response.json()

    def _build_payload(
        self,
        model_config: ModelConfig,
        request_payload: ChatCompletionRequest,
        lora_adapters: list[dict] | None = None,
    ) -> dict:
        body = request_payload.model_dump(exclude_none=True)
        body["model"] = model_config.model
        metadata = body.pop("metadata", {})
        template_kwargs = dict(body.get("chat_template_kwargs") or {})
        voice_fast = metadata.get("session_mode") == "voice" and metadata.get("resolved_mode") == "fast"
        if metadata.get("mode") == "fast" or voice_fast:
            template_kwargs["enable_thinking"] = False
        if voice_fast:
            # Verified against the installed Qwen3 template．Keep this provider
            # control here，not in Conversation or the frontend．
            body["reasoning_format"] = "deepseek"
        elif metadata.get("mode") != "fast" and model_config.thinking_mode in {"optional", "always"}:
            template_kwargs["enable_thinking"] = True
            if model_config.preserve_thinking:
                template_kwargs["preserve_thinking"] = True
            if model_config.default_reasoning_effort:
                template_kwargs["reasoning_effort"] = model_config.default_reasoning_effort
        if template_kwargs:
            body["chat_template_kwargs"] = template_kwargs
        if "temperature" not in body and model_config.default_temperature is not None:
            body["temperature"] = model_config.default_temperature
        if lora_adapters is not None:
            body["lora"] = lora_adapters
        if request_payload.stream:
            body["stream_options"] = {**(body.get("stream_options") or {}), "include_usage": True}
        return body
