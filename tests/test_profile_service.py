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


@pytest.mark.parametrize("mode", ["default", "voice", "tech"])
def test_warm_polite_uses_concrete_casual_politeness_guidance(mode) -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    profile = ProfileService().load(PROFILE_EXAMPLE)
    result = PromptManager().apply_ephy_profile(
        ChatCompletionRequest(), identity, profile, session_mode=mode,
    )
    content = result.messages[0].content
    assert "Ephyの柔らかい敬語" in content
    assert "教えてくれますか？" in content
    assert "話す範囲は相手に委ね" in content
    assert "無条件の同意を足しません" in content
    assert "文末の「よ」は，「もちろんですよ」以外では使いません" in content
    assert "二つの道の一般論を並べません" in content
    assert "探究を続けたい気持ちと，知見を誰かに届けたい気持ち" in content
    assert "文章作成を頼まれたら文章を" in content
    assert "与えられていない日付，進捗，固有名詞，判断を創作しません" in content
    assert "感嘆符は通常使いません" in content
    assert "一人称は「わたし」" in content


def test_warm_polite_prompt_versions_are_explicit_and_distinct() -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    profile = ProfileService().load(PROFILE_EXAMPLE)
    manager = PromptManager()

    v1 = manager.apply_ephy_profile(
        ChatCompletionRequest(),
        identity,
        profile,
        warm_polite_prompt_version="v1",
    )
    v2 = manager.apply_ephy_profile(
        ChatCompletionRequest(),
        identity,
        profile,
        warm_polite_prompt_version="v2",
    )
    v3 = manager.apply_ephy_profile(
        ChatCompletionRequest(),
        identity,
        profile,
        warm_polite_prompt_version="v3",
    )

    assert "Ephyの柔らかい敬語 v2" not in v1.messages[0].content
    assert "Ephyの柔らかい敬語 v2" in v2.messages[0].content
    assert "Ephyの柔らかい敬語 v3" in v3.messages[0].content
    assert v1.messages[0].content != v2.messages[0].content
    assert v2.messages[0].content != v3.messages[0].content


def test_writing_mode_prefers_prose_over_chat_register() -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    profile = ProfileService().load(PROFILE_EXAMPLE)
    result = PromptManager().apply_ephy_profile(
        ChatCompletionRequest(), identity, profile, session_mode="writing",
    )
    content = result.messages[0].content
    assert "Ephyの柔らかい敬語" not in content
    assert "日常チャット用の話し言葉へ寄せず" in content
    assert "読みやすい文語の丁寧語" in content


def test_warm_polite_guidance_is_not_injected_into_other_registers() -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    payload = ProfileService().load(PROFILE_EXAMPLE).model_dump(by_alias=True)
    payload["voice"]["register"] = "formal"
    result = PromptManager().apply_ephy_profile(
        ChatCompletionRequest(), identity, ProfileService().validate(payload),
    )
    assert "Ephyの柔らかい敬語" not in result.messages[0].content


def test_generic_runtime_does_not_get_ephy_speech_guidance() -> None:
    result = PromptManager().apply_output_policies(ChatCompletionRequest())
    assert not any("Ephyの柔らかい敬語" in message.content for message in result.messages)


def test_warm_polite_resource_is_read_again_for_next_request(tmp_path) -> None:
    identity = IdentityService().load(IDENTITY_EXAMPLE)
    profile = ProfileService().load(PROFILE_EXAMPLE)
    resource = tmp_path / "ephy_warm_polite_ja.md"
    resource.write_text("最初の口調指定", encoding="utf-8")
    manager = PromptManager(prompts_dir=tmp_path)
    first = manager.apply_ephy_profile(ChatCompletionRequest(), identity, profile)
    resource.write_text("更新した口調指定", encoding="utf-8")
    second = manager.apply_ephy_profile(ChatCompletionRequest(), identity, profile)
    assert "最初の口調指定" in first.messages[0].content
    assert "更新した口調指定" in second.messages[0].content
