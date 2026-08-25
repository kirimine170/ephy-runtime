import packages.config_core.loader as loader_module
from packages.config_core.loader import load_app_config


def test_load_app_config_contains_phase1_models() -> None:
    config = load_app_config()

    assert "fast" in config.models
    assert "work" in config.models
    assert "code" in config.models
    assert "auto" in config.routes
    assert config.rag.embedding_provider == "local_hash"
    assert config.rag.embedding_model_alias == "embedding"
    assert config.rag.reranker_provider == "local_overlap"
    assert config.rag.reranker_endpoint_path == "/rerank"
    assert config.vector_db.provider == "local_json"


def test_load_app_config_applies_local_overrides(tmp_path, monkeypatch) -> None:
    monkeypatch.delenv("EPHY_RUNTIME_DISABLE_LOCAL_CONFIG", raising=False)
    config_dir = tmp_path / "configs"
    config_dir.mkdir()
    (config_dir / "models.yaml").write_text(
        """
models:
  embedding:
    provider: local
    model: base-embedding
""".strip(),
        encoding="utf-8",
    )
    (config_dir / "models.local.yaml").write_text(
        """
models:
  embedding:
    provider: llama_cpp
    model: override-embedding
    base_url: http://localhost:8090/v1
""".strip(),
        encoding="utf-8",
    )
    (config_dir / "routes.yaml").write_text("routes: {}", encoding="utf-8")
    (config_dir / "rag.yaml").write_text(
        """
rag:
  embedding_provider: local_hash
vector_db:
  provider: local_json
""".strip(),
        encoding="utf-8",
    )
    (config_dir / "rag.local.yaml").write_text(
        """
rag:
  embedding_provider: openai_compatible
  embedding_model_alias: embedding
""".strip(),
        encoding="utf-8",
    )

    monkeypatch.setattr(loader_module, "CONFIG_DIR", config_dir)
    loader_module.load_app_config.cache_clear()
    try:
        config = loader_module.load_app_config()
    finally:
        loader_module.load_app_config.cache_clear()

    assert config.models["embedding"].provider == "llama_cpp"
    assert config.models["embedding"].model == "override-embedding"
    assert config.models["embedding"].base_url == "http://localhost:8090/v1"
    assert config.rag.embedding_provider == "openai_compatible"
