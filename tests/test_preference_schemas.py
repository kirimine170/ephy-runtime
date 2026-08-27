from datetime import datetime, timezone

import pytest
import yaml
from pydantic import ValidationError

from packages.config_core.loader import ROOT_DIR
from packages.eval_core.preference_schemas import (
    CandidateSpec,
    ConversationScenario,
    CreatePreferenceSessionRequest,
    GenerationParameters,
    PreferenceDataset,
    PreferenceSession,
)


def test_preference_models_reject_extra_fields() -> None:
    with pytest.raises(ValidationError):
        ConversationScenario.model_validate(
            {
                "scenario_id": "synthetic-1",
                "category": "test",
                "messages": [{"role": "user", "content": "hello"}],
                "source_kind": "synthetic",
                "provenance": "unit test",
                "consent": {"storage": True, "training": True},
                "split": "train",
                "unexpected": True,
            }
        )


def test_scenario_must_end_with_user_message() -> None:
    with pytest.raises(ValidationError, match="end with a user"):
        ConversationScenario(
            scenario_id="synthetic-1",
            category="test",
            messages=[{"role": "assistant", "content": "hello"}],
            source_kind="synthetic",
            provenance="unit test",
            consent={"storage": True, "training": True},
            split="train",
        )


def test_generation_requires_nonzero_temperature() -> None:
    with pytest.raises(ValidationError):
        GenerationParameters(temperature=0)


def test_candidate_requires_complete_adapter_identity() -> None:
    with pytest.raises(ValidationError, match="recorded together"):
        CandidateSpec(
            candidate_id="candidate-a",
            model_role="fast",
            model_registration_id="model-a",
            model_sha256="a" * 64,
            adapter_registration_id="adapter-a",
            prompt_revision="b" * 64,
            generation_parameters=GenerationParameters(),
            generated_at=datetime.now(timezone.utc),
        )


def test_prompt_comparison_mode_is_strict_and_old_sessions_remain_compatible() -> None:
    request = CreatePreferenceSessionRequest(
        dataset_path="configs/eval.preference.sample.yaml",
        comparison_mode="prompt_v1_v2",
    )
    next_request = CreatePreferenceSessionRequest(
        dataset_path="configs/eval.preference.v3.yaml",
        comparison_mode="prompt_v2_v3",
    )
    old_session = PreferenceSession(
        session_id="session-1",
        dataset_path="configs/eval.preference.sample.yaml",
        created_at=datetime.now(timezone.utc),
    )

    assert request.comparison_mode == "prompt_v1_v2"
    assert next_request.comparison_mode == "prompt_v2_v3"
    assert old_session.comparison_mode == "same_prompt"
    with pytest.raises(ValidationError):
        CreatePreferenceSessionRequest(
            dataset_path="configs/eval.preference.sample.yaml",
            comparison_mode="unblinded",
        )


def test_sample_dataset_has_all_synthetic_conversation_categories() -> None:
    payload = yaml.safe_load(
        (ROOT_DIR / "configs/eval.preference.sample.yaml").read_text(encoding="utf-8")
    )
    scenarios = [ConversationScenario.model_validate(item) for item in payload["scenarios"]]
    expected = {
        "技術相談",
        "曖昧な依頼への確認",
        "ユーザーからの訂正",
        "提案の妥当性確認",
        "短い雑談",
        "お礼への返答",
        "疲れているときの相談",
        "意見が一致しない場面",
        "過去の制約を保持する多ターン会話",
        "作業中の短い進捗報告",
        "不明なことを認める場面",
        "過剰な質問を避ける場面",
    }

    assert {item.category for item in scenarios} == expected
    assert all(item.source_kind == "synthetic" for item in scenarios)
    assert any(len(item.messages) == 1 for item in scenarios)
    assert any(4 <= len(item.messages) <= 8 for item in scenarios)


def test_v3_dataset_has_thirty_unique_synthetic_scenarios() -> None:
    payload = yaml.safe_load(
        (ROOT_DIR / "configs/eval.preference.v3.yaml").read_text(encoding="utf-8")
    )
    scenarios = [ConversationScenario.model_validate(item) for item in payload["scenarios"]]

    assert len(scenarios) == 30
    assert len({item.scenario_id for item in scenarios}) == 30
    assert all(item.source_kind == "synthetic" for item in scenarios)
    assert all(item.consent.storage and item.consent.training for item in scenarios)
    assert sum(item.category == "技術相談" for item in scenarios) >= 5
    assert sum(item.category == "提案と判断" for item in scenarios) >= 5


def test_dataset_wrapper_rejects_unknown_fields() -> None:
    with pytest.raises(ValidationError):
        PreferenceDataset.model_validate(
            {
                "scenarios": [
                    {
                        "scenario_id": "synthetic-1",
                        "category": "test",
                        "messages": [{"role": "user", "content": "hello"}],
                        "source_kind": "synthetic",
                        "provenance": "unit test",
                        "consent": {"storage": True, "training": True},
                        "split": "train",
                    }
                ],
                "unknown": True,
            }
        )
