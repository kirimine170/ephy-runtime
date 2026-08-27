import json
from datetime import datetime, timezone

import pytest

from packages.eval_core.preference_export import PreferenceExporter
from packages.eval_core.preference_schemas import (
    CandidateSpec,
    ConversationScenario,
    GenerationParameters,
    PreferencePair,
    PreferenceSession,
    PreferenceVote,
)
from packages.eval_core.preference_store import PreferenceStore


def candidate(candidate_id: str, prompt_variant: str) -> CandidateSpec:
    return CandidateSpec(
        candidate_id=candidate_id,
        model_role="fast",
        model_registration_id="model-a",
        model_sha256="a" * 64,
        prompt_variant=prompt_variant,
        prompt_revision="b" * 64,
        generation_parameters=GenerationParameters(seed=1),
        generated_at=datetime.now(timezone.utc),
    )


def add_record(
    store: PreferenceStore,
    index: int,
    *,
    selection: str = "a",
    training: bool = True,
    split: str = "train",
    deletion_status: str = "active",
    approved_for_sft: bool = False,
) -> None:
    scenario_id = f"scenario-{index}"
    session_id = f"session-{index}"
    pair_id = f"pair-{index}"
    scenario = ConversationScenario(
        scenario_id=scenario_id,
        category="test",
        messages=[{"role": "user", "content": f"prompt {index}"}],
        source_kind="synthetic",
        provenance="unit test",
        consent={"storage": True, "training": training},
        deletion_status=deletion_status,
        split=split,
    )
    session = PreferenceSession(
        session_id=session_id,
        dataset_path="fixture.yaml",
        target_pairs=1,
        created_at=datetime.now(timezone.utc),
    )
    pair = PreferencePair(
        pair_id=pair_id,
        session_id=session_id,
        scenario_id=scenario_id,
        candidate_a=candidate(f"a-{index}", "v1"),
        candidate_b=candidate(f"b-{index}", "v2"),
        response_a=f"chosen {index}",
        response_b=f"rejected {index}",
        response_a_sha256="c" * 64,
        response_b_sha256="d" * 64,
        display_order="ab",
        status="pending",
        created_at=datetime.now(timezone.utc),
    )
    vote = PreferenceVote(
        vote_id=f"vote-{index}",
        pair_id=pair_id,
        selection=selection,
        approved_for_sft=approved_for_sft,
        created_at=datetime.now(timezone.utc),
    )
    store.create_session(session, [scenario])
    store.add_pair(pair, 0)
    store.add_vote(vote)


def records(path):
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]


def test_dpo_export_uses_only_latest_eligible_training_vote(tmp_path) -> None:
    store = PreferenceStore(tmp_path)
    add_record(store, 1)
    first = store.latest_vote("pair-1")
    store.add_vote(
        PreferenceVote(
            vote_id="vote-1-corrected",
            pair_id="pair-1",
            selection="b",
            supersedes_vote_id=first.vote_id,
            created_at=datetime.now(timezone.utc),
        )
    )
    destination = tmp_path / "exports/data.dpo.jsonl"

    result = PreferenceExporter(store).export("session-1", export_format="dpo", output="exports/data.dpo.jsonl")

    assert result["records"] == 1
    assert records(destination)[0]["chosen"][0]["content"] == "rejected 1"
    assert records(destination)[0]["metadata"]["provenance"] == "unit test"
    assert records(destination)[0]["metadata"]["chosen_prompt_variant"] == "v2"
    assert records(destination)[0]["metadata"]["rejected_prompt_variant"] == "v1"


@pytest.mark.parametrize(
    "options",
    [
        {"training": False},
        {"split": "validation"},
        {"split": "holdout"},
        {"deletion_status": "deleted"},
        {"selection": "tie"},
        {"selection": "skip"},
    ],
)
def test_dpo_export_excludes_ineligible_records(tmp_path, options) -> None:
    store = PreferenceStore(tmp_path)
    add_record(store, 1, **options)

    result = PreferenceExporter(store).export("session-1", export_format="dpo", output="empty.jsonl")

    assert result["records"] == 0
    assert (tmp_path / "empty.jsonl").read_text(encoding="utf-8") == ""


def test_sft_export_requires_explicit_approval(tmp_path) -> None:
    store = PreferenceStore(tmp_path)
    add_record(store, 1, approved_for_sft=False)
    unapproved = PreferenceExporter(store).export(
        "session-1", export_format="sft", output="unapproved.sft.jsonl"
    )

    assert unapproved["records"] == 0

    other_root = tmp_path / "approved"
    approved_store = PreferenceStore(other_root)
    add_record(approved_store, 2, approved_for_sft=True)
    approved = PreferenceExporter(approved_store).export(
        "session-2", export_format="sft", output="approved.sft.jsonl"
    )

    assert approved["records"] == 1
    assert records(other_root / "approved.sft.jsonl")[0]["messages"][-1]["content"] == "chosen 2"


def test_export_rejects_escape_and_overwrite(tmp_path) -> None:
    store = PreferenceStore(tmp_path / "data")
    add_record(store, 1)
    exporter = PreferenceExporter(store)

    with pytest.raises(ValueError, match="under EPHY"):
        exporter.export("session-1", export_format="dpo", output="../outside.jsonl")

    exporter.export("session-1", export_format="dpo", output="safe.jsonl")
    with pytest.raises((ValueError, FileExistsError)):
        exporter.export("session-1", export_format="dpo", output="safe.jsonl")
