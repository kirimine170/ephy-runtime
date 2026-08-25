import os
from functools import lru_cache
from pathlib import Path

import yaml
from pydantic import BaseModel, ConfigDict, Field


ROOT_DIR = Path(__file__).resolve().parents[2]
CONFIG_DIR = ROOT_DIR / "configs"


class ModelConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    provider: str
    model: str
    base_url: str | None = None
    max_context: int | None = None
    default_temperature: float | None = None


class RouteConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    model: str | None = None
    rag: bool = False
    strategy: str | None = None


class RagConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    chunk_size: int = 1200
    chunk_overlap: int = 150
    top_k: int = 30
    rerank_k: int = 8
    context_max_tokens: int = 12000
    embedding_provider: str = "local_hash"
    embedding_model_alias: str = "embedding"
    embedding_dimensions: int = 64
    reranker_provider: str = "local_overlap"
    reranker_model_alias: str = "reranker"
    reranker_endpoint_path: str = "/rerank"


class VectorDBConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    provider: str = "local_json"
    url: str | None = None
    collection: str = "local_docs"
    store_path: str = "data/index/local_docs.json"


class WebSearchConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    enabled: bool = False
    provider: str = "searxng"
    base_url: str = "http://127.0.0.1:8888"
    engine: str = "duckduckgo"
    max_results: int = 5
    safe_search: int = 1
    timeout_seconds: float = 8.0
    plan_ttl_seconds: int = 300
    max_query_chars: int = 240
    max_snippet_chars: int = 600
    max_context_chars: int = 4000


class AppConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")

    models: dict[str, ModelConfig] = Field(default_factory=dict)
    routes: dict[str, RouteConfig] = Field(default_factory=dict)
    rag: RagConfig = Field(default_factory=RagConfig)
    vector_db: VectorDBConfig = Field(default_factory=VectorDBConfig)
    web_search: WebSearchConfig = Field(default_factory=WebSearchConfig)


def _read_yaml(path: Path) -> dict:
    with path.open("r", encoding="utf-8") as file_obj:
        data = yaml.safe_load(file_obj) or {}
    if not isinstance(data, dict):
        raise ValueError(f"Expected mapping in {path}")
    return data


def _merge_dicts(base: dict, override: dict) -> dict:
    merged = dict(base)
    for key, value in override.items():
        existing = merged.get(key)
        if isinstance(existing, dict) and isinstance(value, dict):
            merged[key] = _merge_dicts(existing, value)
        else:
            merged[key] = value
    return merged


def _load_yaml_with_optional_local(name: str) -> dict:
    payload = _read_yaml(CONFIG_DIR / f"{name}.yaml")
    if os.getenv("EPHY_RUNTIME_DISABLE_LOCAL_CONFIG") == "1":
        return payload
    local_path = CONFIG_DIR / f"{name}.local.yaml"
    if local_path.exists():
        payload = _merge_dicts(payload, _read_yaml(local_path))
    return payload


def _load_optional_yaml_with_optional_local(name: str) -> dict:
    base_path = CONFIG_DIR / f"{name}.yaml"
    payload = _read_yaml(base_path) if base_path.exists() else {}
    if os.getenv("EPHY_RUNTIME_DISABLE_LOCAL_CONFIG") == "1":
        return payload
    local_path = CONFIG_DIR / f"{name}.local.yaml"
    if local_path.exists():
        payload = _merge_dicts(payload, _read_yaml(local_path))
    return payload


@lru_cache(maxsize=1)
def load_app_config() -> AppConfig:
    models_payload = _load_yaml_with_optional_local("models")
    routes_payload = _load_yaml_with_optional_local("routes")
    rag_payload = _load_yaml_with_optional_local("rag")
    web_payload = _load_optional_yaml_with_optional_local("web")
    return AppConfig(
        models=models_payload.get("models", {}),
        routes=routes_payload.get("routes", {}),
        rag=rag_payload.get("rag", {}),
        vector_db=rag_payload.get("vector_db", {}),
        web_search=web_payload.get("web_search", {}),
    )


def reload_app_config() -> AppConfig:
    load_app_config.cache_clear()
    return load_app_config()
