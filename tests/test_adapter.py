import asyncio

from packages.config_core.loader import ModelConfig
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage, RequestMetadata


def test_build_payload_overrides_model_and_drops_metadata() -> None:
    adapter = LlamaCppChatAdapter()
    model_config = ModelConfig(
        provider="llama_cpp",
        model="qwen3-8b",
        base_url="http://localhost:8081/v1",
        default_temperature=0.7,
    )
    request = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="hello")],
        metadata=RequestMetadata(mode="fast"),
    )

    payload = adapter._build_payload(model_config=model_config, request_payload=request)

    assert payload["model"] == "qwen3-8b"
    assert payload["temperature"] == 0.7
    assert payload["chat_template_kwargs"] == {"enable_thinking": False}
    assert "metadata" not in payload


def test_build_payload_preserves_thinking_for_non_fast_modes() -> None:
    adapter = LlamaCppChatAdapter()
    model_config = ModelConfig(
        provider="llama_cpp",
        model="qwen3-30b",
        base_url="http://localhost:8082/v1",
    )
    request = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="analyze this")],
        metadata=RequestMetadata(mode="work"),
    )

    payload = adapter._build_payload(model_config=model_config, request_payload=request)

    assert "chat_template_kwargs" not in payload


def test_build_payload_adds_request_scoped_lora_control() -> None:
    adapter = LlamaCppChatAdapter()
    model_config = ModelConfig(
        provider="llama_cpp",
        model="qwen3-8b",
        base_url="http://localhost:8081/v1",
    )
    request = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="hello")],
        metadata=RequestMetadata(mode="fast"),
    )

    payload = adapter._build_payload(
        model_config=model_config,
        request_payload=request,
        lora_adapters=[{"id": 0, "scale": 0.0}],
    )

    assert payload["lora"] == [{"id": 0, "scale": 0.0}]


def test_list_lora_adapters_uses_backend_root_not_v1() -> None:
    class Response:
        def raise_for_status(self):
            return None

        def json(self):
            return [{"id": 0, "path": "/tmp/style.gguf", "scale": 1.0}]

    class Client:
        def __init__(self):
            self.url = None

        async def get(self, url):
            self.url = url
            return Response()

    adapter = LlamaCppChatAdapter()
    client = Client()
    adapter._client = client
    model_config = ModelConfig(
        provider="llama_cpp",
        model="qwen3-8b",
        base_url="http://localhost:8081/v1",
    )

    result = asyncio.run(adapter.list_lora_adapters(model_config))

    assert client.url == "http://localhost:8081/lora-adapters"
    assert result[0]["id"] == 0
