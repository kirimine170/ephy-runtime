from collections.abc import Sequence

import httpx

from packages.config_core.loader import ModelConfig
from .schemas import ChatCompletionRequest, EmbeddingRequest


class LlamaCppChatAdapter:
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
        except httpx.HTTPError as exc:
            raise RuntimeError(f"Backend request failed for model '{model_config.model}': {exc}") from exc

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
                async for chunk in response.aiter_bytes():
                    if chunk:
                        yield chunk
        except httpx.HTTPError as exc:
            raise RuntimeError(f"Backend request failed for model '{model_config.model}': {exc}") from exc

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
        if metadata.get("mode") == "fast":
            template_kwargs["enable_thinking"] = False
        elif model_config.thinking_mode in {"optional", "always"}:
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
        return body
