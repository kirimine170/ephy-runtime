from datetime import datetime, timezone

import pytest

from packages.eval_core.preference_schemas import (
    CandidateSpec,
    ConversationScenario,
    GenerationParameters,
    PreferencePair,
    PreferenceSession,
    PreferenceVote,
)
from packages.eval_core.preference_store import PreferenceStore


def scenario() -> ConversationScenario:
    return ConversationScenario(
        scenario_id="synthetic-store-1",
        category="test",
        messages=[{"role": "user", "content": "hello"}],
        source_kind="synthetic",
        provenance="unit test",
        consent={"storage": True, "training": True},
        split="train",
    )


def session() -> PreferenceSession:
    return PreferenceSession(
        session_id="session-1",
        dataset_path="fixture.yaml",
        target_pairs=2,
        created_at=datetime.now(timezone.utc),
    )


def candidate(candidate_id: str) -> CandidateSpec:
    return CandidateSpec(
        candidate_id=candidate_id,
        model_role="fast",
        model_registration_id="model-a",
        model_sha256="a" * 64,
        prompt_revision="b" * 64,
        generation_parameters=GenerationParameters(seed=1),
        generated_at=datetime.now(timezone.utc),
    )


def pair() -> PreferencePair:
    return PreferencePair(
        pair_id="pair-1",
        session_id="session-1",
        scenario_id="synthetic-store-1",
        candidate_a=candidate("candidate-a"),
        candidate_b=candidate("candidate-b"),
        response_a="first",
        response_b="second",
        response_a_sha256="c" * 64,
        response_b_sha256="d" * 64,
        display_order="ab",
        status="pending",
        created_at=datetime.now(timezone.utc),
    )


def vote(vote_id: str, selection: str, supersedes: str | None = None) -> PreferenceVote:
    return PreferenceVote(
        vote_id=vote_id,
        pair_id="pair-1",
        selection=selection,
        created_at=datetime.now(timezone.utc),
        supersedes_vote_id=supersedes,
    )


def test_store_requires_absolute_explicit_data_root() -> None:
    with pytest.raises(ValueError, match="EPHY_PREFERENCE_DATA_ROOT"):
        PreferenceStore(environ={}).database_path
    with pytest.raises(ValueError, match="absolute"):
        PreferenceStore(environ={"EPHY_PREFERENCE_DATA_ROOT": "relative"})


def test_store_resumes_pending_pair_after_restart(tmp_path) -> None:
    first = PreferenceStore(tmp_path)
    first.create_session(session(), [scenario()])
    first.add_pair(pair(), 0)

    restarted = PreferenceStore(tmp_path)

    assert restarted.get_session("session-1").session_id == "session-1"
    assert restarted.next_pair("session-1").pair_id == "pair-1"
    assert restarted.database_path == tmp_path / "preferences.sqlite3"


def test_vote_corrections_are_append_only_and_prevent_double_vote(tmp_path) -> None:
    store = PreferenceStore(tmp_path)
    store.create_session(session(), [scenario()])
    store.add_pair(pair(), 0)
    store.add_vote(vote("vote-1", "tie"))

    with pytest.raises(ValueError, match="supersede"):
        store.add_vote(vote("vote-duplicate", "skip"))

    store.add_vote(vote("vote-2", "a", supersedes="vote-1"))

    assert [item.selection for item in store.vote_history("pair-1")] == ["tie", "a"]
    assert store.latest_vote("pair-1").vote_id == "vote-2"
    assert store.session_rows("session-1")[0]["vote"].selection == "a"


@pytest.mark.parametrize("selection", ["tie", "skip"])
def test_tie_and_skip_votes_are_saved(tmp_path, selection) -> None:
    store = PreferenceStore(tmp_path)
    store.create_session(session(), [scenario()])
    store.add_pair(pair(), 0)
    store.add_vote(vote("vote-1", selection))

    assert store.latest_vote("pair-1").selection == selection
