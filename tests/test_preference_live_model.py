import json
import os
import tempfile
import asyncio
from pathlib import Path

import pytest

from packages.config_core.loader import ModelConfig, ROOT_DIR, load_app_config
from packages.eval_core.preference_schemas import (
    CreatePreferenceSessionRequest,
    ExportPreferenceRequest,
    SubmitPreferenceVoteRequest,
)
from packages.eval_core.preference_service import PreferenceService
from packages.eval_core.preference_store import PreferenceStore
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.model_registry.service import ModelRegistry
from packages.prompt_core.loader import PromptManager
from packages.profile_core.runtime import load_ephy_context


def test_preference_flow_with_current_fast_model(monkeypatch) -> None:
    if os.getenv("EPHY_MODEL_INTEGRATION") != "1":
        pytest.skip("Set EPHY_MODEL_INTEGRATION=1 to use the current Fast model")
    configured_root = os.getenv("EPHY_PREFERENCE_DATA_ROOT")
    if (
        not configured_root
        or not Path(configured_root).is_absolute()
        or not Path(configured_root).is_dir()
    ):
        pytest.skip("EPHY_PREFERENCE_DATA_ROOT must point to an existing absolute temporary directory")

    asyncio.run(_run_live_flow(monkeypatch, configured_root))


def test_base_vs_selected_lora_with_current_fast_model(monkeypatch) -> None:
    if os.getenv("EPHY_PREFERENCE_LORA_INTEGRATION") != "1":
        pytest.skip(
            "Set EPHY_PREFERENCE_LORA_INTEGRATION=1 with a selected Fast LoRA"
        )
    configured_root = os.getenv("EPHY_PREFERENCE_DATA_ROOT")
    if (
        not configured_root
        or not Path(configured_root).is_absolute()
        or not Path(configured_root).is_dir()
    ):
        pytest.skip(
            "EPHY_PREFERENCE_DATA_ROOT must point to an existing absolute temporary directory"
        )

    asyncio.run(_run_live_lora_flow(monkeypatch, configured_root))


async def _run_live_flow(monkeypatch, configured_root: str) -> None:

    monkeypatch.delenv("LOCAL_LLM_WORKBENCH_DISABLE_LOCAL_CONFIG", raising=False)
    load_app_config.cache_clear()
    with tempfile.TemporaryDirectory(prefix="live-test-", dir=configured_root) as temporary:
        config = load_app_config()
        registry_root = Path(os.getenv("EPHY_PREFERENCE_MODEL_REGISTRY_ROOT", ROOT_DIR)).resolve()
        registry = ModelRegistry(registry_root)
        overrides = {
            role: ModelConfig.model_validate(payload)
            for role, payload in registry.model_overrides().items()
        }
        config = config.model_copy(update={"models": {**config.models, **overrides}})
        adapter = LlamaCppChatAdapter()
        service = PreferenceService(
            config=config,
            prompt_manager=PromptManager(ephy_context=load_ephy_context(config.ephy)),
            adapter=adapter,
            store=PreferenceStore(Path(temporary)),
            registry=registry,
        )
        try:
            session = service.create_session(
                CreatePreferenceSessionRequest(
                    dataset_path="configs/eval.preference.sample.yaml",
                    pair_count=1,
                    generation_parameters={"temperature": 0.8, "max_tokens": 96},
                )
            )
            await service.generate(session["session_id"], 1)
            pair = service.next_pair(session["session_id"])
            assert pair is not None
            service.vote(
                pair.pair_id,
                SubmitPreferenceVoteRequest(selection="left"),
            )
            result = service.export(
                session["session_id"],
                ExportPreferenceRequest(format="dpo", output="live-test.dpo.jsonl"),
            )
        finally:
            await adapter.aclose()

        lines = Path(result["output"]).read_text(encoding="utf-8").splitlines()
        assert len(lines) == 1
        record = json.loads(lines[0])
        assert record["prompt"] and record["chosen"] and record["rejected"]
        assert record["chosen"] != record["rejected"]


async def _run_live_lora_flow(monkeypatch, configured_root: str) -> None:
    monkeypatch.delenv("LOCAL_LLM_WORKBENCH_DISABLE_LOCAL_CONFIG", raising=False)
    load_app_config.cache_clear()
    with tempfile.TemporaryDirectory(prefix="live-lora-test-", dir=configured_root) as temporary:
        config = load_app_config()
        registry_root = Path(
            os.getenv("EPHY_PREFERENCE_MODEL_REGISTRY_ROOT", ROOT_DIR)
        ).resolve()
        registry = ModelRegistry(registry_root)
        overrides = {
            role: ModelConfig.model_validate(payload)
            for role, payload in registry.model_overrides().items()
        }
        config = config.model_copy(update={"models": {**config.models, **overrides}})
        adapter = LlamaCppChatAdapter()
        service = PreferenceService(
            config=config,
            prompt_manager=PromptManager(ephy_context=load_ephy_context(config.ephy)),
            adapter=adapter,
            store=PreferenceStore(Path(temporary)),
            registry=registry,
        )
        try:
            session = service.create_session(
                CreatePreferenceSessionRequest(
                    dataset_path="configs/eval.preference.v3.yaml",
                    pair_count=1,
                    comparison_mode="base_vs_adapter",
                    adapter_scale=32,
                    generation_parameters={"temperature": 0.8, "max_tokens": 96},
                )
            )
            await service.generate(session["session_id"], 1)
            blind = service.next_pair(session["session_id"])
            assert blind is not None
            stored = service.store.get_pair(blind.pair_id)
            assert stored.candidate_a.adapter_registration_id is None
            assert stored.candidate_b.adapter_registration_id is not None
            assert stored.candidate_a.generation_parameters.seed == (
                stored.candidate_b.generation_parameters.seed
            )
            assert service.stats(session["session_id"])["comparison"] == {
                "mode": "base_vs_adapter",
                "blinded": True,
            }
        finally:
            await adapter.aclose()
