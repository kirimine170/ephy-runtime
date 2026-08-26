from __future__ import annotations

from pathlib import Path

import pytest
from pydantic import ValidationError

from packages.identity_core import IdentityService
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage
from packages.profile_core import ProfileService
from packages.prompt_core.loader import PromptManager


ROOT = Path(__file__).resolve().parents[1]
IDENTITY_EXAMPLE = ROOT / "configs" / "examples" / "identity.example.yaml"
PROFILE_EXAMPLE = ROOT / "configs" / "examples" / "profile.example.yaml"


def test_profile_rejects_empty_clarification_example() -> None:
    profile = ProfileService().load(PROFILE_EXAMPLE).model_dump(by_alias=True)
    profile["clarification"]["example"] = [""]
    with pytest.raises(ValidationError):
        ProfileService().validate(profile)


def test_identity_loader_allows_omitted_parent() -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE).model_dump(mode="json")
    del identity["identity"]["parent_instance_id"]
    assert IdentityService().validate(identity).identity.parent_instance_id is None


def test_profile_service_loads_and_resolves_policy() -> None:
    service = ProfileService()
    profile = service.load(PROFILE_EXAMPLE)

    policy = service.resolve_conversation_policy(profile, session_mode="tech")

    assert policy.session_mode == "tech"
    assert policy.first_person == "わたし"
    assert policy.default_suffix == "さん"
    assert policy.speech_register == "warm_polite"
    assert policy.call_name_frequency == "moderate"


def test_profile_modes_keep_core_personality_consistent() -> None:
    service = ProfileService()
    profile = service.load(PROFILE_EXAMPLE)

    policies = [
        service.resolve_conversation_policy(profile, session_mode=mode)
        for mode in ("voice", "writing", "tech")
    ]

    assert {policy.first_person for policy in policies} == {"わたし"}
    assert {policy.default_suffix for policy in policies} == {"さん"}
    assert {policy.speech_register for policy in policies} == {"warm_polite"}


def test_prompt_manager_builds_profile_fragment_from_structured_data() -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    profile = ProfileService().load(PROFILE_EXAMPLE)
    request = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="自己紹介して")],
    )

    manager = PromptManager()
    once = manager.apply_ephy_profile(request, identity, profile, session_mode="voice")
    twice = manager.apply_ephy_profile(once, identity, profile, session_mode="voice")

    profile_messages = [
        message
        for message in twice.messages
        if message.role == "system" and "Ephy Profile Policy" in str(message.content)
    ]
    assert len(profile_messages) == 1
    assert "Ephy個体「エフィ」" in profile_messages[0].content
    assert "一人称は「わたし」" in profile_messages[0].content
    assert "名前に「さん」" in profile_messages[0].content
