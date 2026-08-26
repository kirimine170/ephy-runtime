from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field


class ChatMessage(BaseModel):
    model_config = ConfigDict(extra="allow")

    role: Literal["system", "user", "assistant", "tool"]
    content: str | list[dict[str, Any]]


class RequestMetadata(BaseModel):
    model_config = ConfigDict(extra="allow")

    mode: str | None = None
    session_mode: Literal["default", "voice", "writing", "tech"] = "default"
    project: str | None = None
    source_path: str | None = None
    source_scope: str | None = None
    tags: list[str] = Field(default_factory=list)
    top_k: int | None = None
    rag_required: bool | None = None
    latency_priority: str | None = None
    web_search: bool = False
    web_search_plan_id: str | None = None


class ChatCompletionRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    model: str = "auto"
    messages: list[ChatMessage] = Field(default_factory=list)
    temperature: float | None = None
    max_tokens: int | None = None
    stream: bool = False
    metadata: RequestMetadata | None = None
    chat_template_kwargs: dict[str, Any] | None = None


class EmbeddingRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    model: str = "embedding"
    input: str | list[str] | list[int] | list[list[int]]
    encoding_format: str | None = None
    dimensions: int | None = None
    user: str | None = None
