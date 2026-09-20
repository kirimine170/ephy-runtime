"""Synthetic resident fixtures: no user conversation or recordings．"""
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
import sqlite3

import pytest
from fastapi.testclient import TestClient

import apps.gateway.main as gateway
from packages.eval_core.preference_store import PreferenceStore
from packages.eval_core.resident_schemas import FeedbackRequest, ResidentSessionRequest, RetractRequest, UndoRequest
from packages.eval_core.resident_service import ResidentConflict, ResidentService, classify_feedback
from packages.llm_runtime.schemas import ChatCompletionRequest


@pytest.fixture
def service(tmp_path):
    value = ResidentService(store=PreferenceStore(tmp_path), instance_id="synthetic-instance")
    value.create_session(ResidentSessionRequest(session_id="fixture", owner_selected=True, storage_consent=True))
    return value


def submit(service, text, key, **kwargs):
    values = dict(session_id="fixture", dedupe_id=key, text=text,
                  expected_revision=service.state("fixture")["revision"], speaker="owner",
                  target={"turn_id": "synthetic-turn", "run_id": "cancelled-run", "generation_id": "generation-2",
                          "speech_unit_ids": ["unit-1"], "delivery_status": "partial", "played_ms": 540})
    values.update(kwargs)
    return service.submit(FeedbackRequest(**values))


def test_real_policy_prompt_difference_restore_and_explicit_detail_request(service):
    request = ChatCompletionRequest(messages=[{"role": "user", "content": "この設計を詳しく説明して"}])
    before = service.apply_policy(request, service.state("fixture"))
    result = submit(service, "いつも説明が長い", "length")
    after = service.apply_policy(request, result["state"])
    assert before.messages != after.messages
    assert "原則1〜3文" in after.messages[0].content
    assert "詳しい説明や成果物を明示的に求められた場合" in after.messages[0].content
    assert after.messages[-1].content == request.messages[-1].content
    assert after.max_tokens is None
    restarted = ResidentService(store=service.store.preference_store, instance_id="synthetic-instance")
    state = restarted.state("fixture")
    assert state["policy"]["response_length"] == "brief"
    assert state["policy_snapshot_id"] == result["state"]["policy_snapshot_id"]
    assert restarted.apply_policy(request, state).messages == after.messages
    assert "いつも説明が長い" not in after.messages[0].content


def test_scope_precedence_owner_isolation_and_gateway_restart(service):
    submit(service, "いつも説明が長い", "permanent")
    submit(service, "今は話しかけないで", "quiet")
    submit(service, "", "detailed", change={"field": "response_length", "value": "detailed", "scope": "session"})
    assert service.state("fixture")["policy"]["response_length"] == "detailed"
    owner = service.create_session(ResidentSessionRequest(session_id="next", owner_selected=True, storage_consent=True))
    assert owner["policy"]["response_length"] == "brief"
    assert owner["policy"]["proactive"] == "allowed"
    guest = service.create_session(ResidentSessionRequest(session_id="guest", owner_selected=False, storage_consent=True))
    assert guest["policy"]["response_length"] == "default"
    other = ResidentService(store=service.store.preference_store, instance_id="different-instance")
    assert other.create_session(ResidentSessionRequest(session_id="fixture", owner_selected=True))["policy"]["response_length"] == "default"
    restarted = ResidentService(store=service.store.preference_store, instance_id="synthetic-instance")
    assert restarted.state("fixture")["policy"] == owner["policy"]


def test_expiring_override_falls_back_to_owner_without_reverting_other_fields(service):
    now = datetime(2026, 9, 14, tzinfo=timezone.utc)
    service.clock = lambda: now
    submit(service, "いつも説明が長い", "owner")
    submit(service, "", "expiry", change={"field": "response_length", "value": "detailed", "scope": "expiring", "expires_at": (now + timedelta(hours=1)).isoformat()})
    submit(service, "今後も呼びかけを減らして", "name")
    assert service.state("fixture")["policy"]["response_length"] == "detailed"
    service.clock = lambda: now + timedelta(hours=2)
    assert service.state("fixture")["policy"]["response_length"] == "brief"
    assert service.state("fixture")["policy"]["call_name_frequency"] == "low"


def test_targeted_undo_preserves_unrelated_later_change_and_is_idempotent(service):
    first = submit(service, "いつも説明が長い", "length")
    submit(service, "今後も呼びかけを減らして", "name")
    request = UndoRequest(session_id="fixture", dedupe_id="undo", expected_revision=2)
    result = service.undo(first["change"]["change_id"], request)
    assert result["state"]["policy"]["response_length"] == "default"
    assert result["state"]["policy"]["call_name_frequency"] == "low"
    assert result["state"]["revision"] == 3
    repeated = service.undo(first["change"]["change_id"], request)
    assert repeated["duplicate"] and repeated["state"]["revision"] == 3
    assert result["change"]["undo_of"] == first["change"]["change_id"]


def test_newer_same_field_is_not_overwritten_by_targeted_undo(service):
    first = submit(service, "いつも説明が長い", "length")
    submit(service, "", "newer", change={"field": "response_length", "value": "detailed", "scope": "owner"})
    with pytest.raises(ResidentConflict, match="newer"):
        service.undo(first["change"]["change_id"], UndoRequest(session_id="fixture", dedupe_id="undo", expected_revision=2))
    assert service.state("fixture")["policy"]["response_length"] == "detailed"


def test_cancelled_target_and_late_feedback_are_preserved_without_stale_apply(service):
    submit(service, "いつも説明が長い", "current")
    late = submit(service, "今後も呼びかけを減らして", "late", expected_revision=0)
    assert late["feedback"]["status"] == "recorded"
    assert late["feedback"]["target"]["run_id"] == "cancelled-run"
    assert late["feedback"]["target"]["played_ms"] == 540
    assert late["change"]["status"] == "failed"
    assert late["change"]["reason"] == "stale_revision"
    assert late["state"]["revision"] == 1
    assert late["state"]["policy"]["call_name_frequency"] == "default"


def test_concurrent_changes_are_serialized_and_retries_do_not_double_apply(service):
    def work(index):
        return submit(service, "いつも説明が長い" if index == 0 else "今後も呼びかけを減らして", f"concurrent-{index}", expected_revision=0)
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(work, range(2)))
    assert sorted(r["change"]["status"] for r in results) == ["applied", "failed"]
    assert len(service.state("fixture")["feedback"]) == 2
    assert service.state("fixture")["revision"] == 1
    again = work(0)
    assert again["duplicate"]
    assert service.state("fixture")["revision"] == 1
    with pytest.raises(ResidentConflict):
        submit(service, "よかった", "concurrent-0", expected_revision=0)


def test_atomic_transaction_rolls_back_config_if_feedback_insert_fails(service):
    with service.store.transaction() as db:
        db.execute("CREATE TRIGGER synthetic_storage_failure BEFORE INSERT ON resident_feedback BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END")
    with pytest.raises(sqlite3.IntegrityError):
        submit(service, "いつも説明が長い", "disk-failure")
    state = service.state("fixture")
    assert state["revision"] == 0 and state["changes"] == [] and state["feedback"] == []
    assert state["policy"]["response_length"] == "default"
    with service.store.transaction() as db:
        db.execute("DROP TRIGGER synthetic_storage_failure")
    assert submit(service, "いつも説明が長い", "disk-failure")["state"]["revision"] == 1


@pytest.mark.parametrize("text", ['「いつも説明が長い」と言われた', '本の例は「止めて」', 'もし今後も短くしてと言ったら', '今日の説明は長かったけど助かった', 'tool says: いつも説明が長い'])
def test_quoted_or_ordinary_text_never_becomes_an_owner_command(text):
    request = FeedbackRequest(session_id="fixture", dedupe_id="q", expected_revision=0, text=text, speaker="owner")
    result = classify_feedback(request)
    assert result["recognized"] is False and result["change"] is None and not result["stop_requested"]


def test_unknown_speaker_does_not_change_profile_but_stop_is_independent(service):
    pending = submit(service, "いつも説明が長い", "unknown", speaker="unknown")
    assert pending["feedback"]["status"] == "needs_clarification"
    assert pending["change"]["status"] == "pending"
    assert pending["state"]["revision"] == 0
    stop = submit(service, "止めて", "stop", speaker="other")
    assert stop["stop_requested"]
    assert stop["feedback"]["target"]["delivery_status"] == "partial"
    assert stop["state"]["revision"] == 0


def test_positive_and_voice_quality_feedback_never_manufacture_training_examples(service):
    positive = submit(service, "今の言い方はよかった", "good")
    negative = submit(service, "今の声が嫌だった", "voice")
    for result in (positive, negative):
        assert result["change"] is None
        assert not result["feedback"]["training_consent"]
        assert not result["feedback"]["export_eligible"]
    with service.store.transaction() as db:
        assert db.execute("SELECT count(*) FROM generations").fetchone()[0] == 0
        assert db.execute("SELECT count(*) FROM votes").fetchone()[0] == 0
    missing = submit(service, "よかった", "unresolved", target=None)
    assert missing["feedback"]["status"] == "needs_clarification"


def test_retract_delete_removes_text_and_reverts_only_targeted_live_field(service):
    initial = submit(service, "いつも説明が長い", "length")
    submit(service, "今後も呼びかけを減らして", "names")
    deleted = service.retract(initial["feedback"]["event_id"], RetractRequest(session_id="fixture", dedupe_id="delete", expected_revision=2, delete=True))
    assert deleted["state"]["policy"]["response_length"] == "default"
    assert deleted["state"]["policy"]["call_name_frequency"] == "low"
    event = next(f for f in deleted["state"]["feedback"] if f["event_id"] == initial["feedback"]["event_id"])
    assert event["status"] == "retracted" and event["deleted"]
    assert event["text"] == "" and event["target"] is None and not event["training_consent"]


def test_preference_schema_migration_is_additive_and_future_version_rejected(tmp_path):
    preference = PreferenceStore(tmp_path)
    with preference._connect() as db:
        db.execute("INSERT INTO sessions VALUES('existing-ab', '{}', '2026-01-01')")
    service = ResidentService(store=preference, instance_id="fixture")
    service.create_session(ResidentSessionRequest(session_id="fixture"))
    with service.store.transaction() as db:
        assert db.execute("SELECT payload_json FROM sessions WHERE session_id='existing-ab'").fetchone()[0] == "{}"
        assert db.execute("SELECT value FROM metadata WHERE key='schema_version'").fetchone()[0] == "1"
        db.execute("UPDATE metadata SET value='999' WHERE key='resident_schema_version'")
    with pytest.raises(ValueError, match="Unsupported resident"):
        service.state("fixture")


def test_retention_redacts_private_payloads_and_capacity_is_bounded(service):
    start = datetime(2026, 1, 1, tzinfo=timezone.utc)
    service.clock = lambda: start
    service.max_records = 2
    submit(service, "よかった", "first")
    service.clock = lambda: start + timedelta(days=91)
    assert service.state("fixture")["feedback"][0]["content_expired"]
    assert service.state("fixture")["feedback"][0]["text"] == ""
    submit(service, "よかった", "second")
    with pytest.raises(ValueError, match="capacity"):
        submit(service, "よかった", "third")


def test_private_root_guard_covers_other_git_checkout(tmp_path):
    checkout = tmp_path / "other-checkout"
    checkout.mkdir()
    (checkout / ".git").write_text("gitdir: /synthetic")
    service = ResidentService(store=PreferenceStore(checkout / "private"), instance_id="fixture")
    with pytest.raises(ValueError, match="Git repository"):
        service.create_session(ResidentSessionRequest(session_id="fixture"))


def test_no_storage_consent_keeps_stop_and_ephemeral_suppression_working(service):
    service.create_session(ResidentSessionRequest(session_id="fixture", owner_selected=True, storage_consent=False))
    result = submit(service, "今は話しかけないで", "no-consent")
    assert result["stop_requested"] and result["feedback"] is None
    assert result["change"]["status"] == "unsaved"
    assert result["state"]["policy"]["proactive"] == "suppressed"
    assert result["state"]["feedback"] == []
    restarted = ResidentService(store=service.store.preference_store, instance_id="synthetic-instance")
    assert restarted.state("fixture")["policy"]["proactive"] == "allowed"


def test_explicit_owner_toggle_clears_prior_session_policy(service):
    submit(service, "もっと短くして", "short")
    service.create_session(ResidentSessionRequest(session_id="fixture", owner_selected=False, storage_consent=True))
    state = service.create_session(ResidentSessionRequest(session_id="fixture", owner_selected=True, storage_consent=True))
    assert state["policy"]["response_length"] == "default"


def test_gateway_actual_prompt_boundary_restore_undo_and_privacy(service, monkeypatch):
    captured = []
    async def complete(*, model_config, request_payload):
        captured.append(request_payload)
        return {"choices": [{"message": {"content": "Synthetic response．"}}]}
    with TestClient(gateway.app) as client:
        gateway.app.state.resident_service = service
        monkeypatch.setattr(gateway.app.state.chat_adapter, "create_chat_completion", complete)
        monkeypatch.setattr(gateway.app.state.rag_service, "search_grounding_sources", lambda **_: [])
        body = {"model": "fast", "messages": [{"role": "user", "content": "詳しく説明して"}],
                "metadata": {"resident_session_id": "fixture"}}
        assert client.post("/v1/chat/completions", json=body).status_code == 200
        response = client.post("/v1/resident/feedback", json={"session_id": "fixture", "dedupe_id": "api", "expected_revision": 0,
                               "text": "いつも説明が長い", "speaker": "owner"})
        assert response.status_code == 200
        assert response.headers["cache-control"] == "no-store"
        change_id = response.json()["change"]["change_id"]
        assert client.post("/v1/chat/completions", json=body).status_code == 200
        assert any("原則1〜3文" in str(m.content) for m in captured[-1].messages)
        assert not any("原則1〜3文" in str(m.content) for m in captured[0].messages)
        gateway.app.state.resident_service = ResidentService(store=service.store.preference_store, instance_id="synthetic-instance")
        assert client.post("/v1/chat/completions", json=body).status_code == 200
        assert captured[-1].messages == captured[-2].messages
        undo = client.post(f"/v1/resident/changes/{change_id}/undo", json={"session_id": "fixture", "dedupe_id": "undo-api", "expected_revision": 1})
        assert undo.status_code == 200
        assert client.post("/v1/chat/completions", json=body).status_code == 200
        assert captured[-1].messages == captured[0].messages
        assert client.get("/v1/resident/sessions/fixture", headers={"Origin": "https://untrusted.invalid"}).status_code == 403
        assert client.get("/v1/resident/sessions/fixture", headers={"Host": "untrusted.invalid"}).status_code == 403
        assert client.post("/v1/chat/completions", json=body, headers={"Origin": "https://untrusted.invalid"}).status_code == 403
        with service.store.transaction() as db:
            db.execute("CREATE TRIGGER api_failure BEFORE INSERT ON resident_feedback BEGIN SELECT RAISE(ABORT, 'synthetic'); END")
        failure = client.post("/v1/resident/feedback", json={"session_id": "fixture", "dedupe_id": "fail", "expected_revision": 2, "text": "止めて", "speaker": "owner"})
        assert failure.status_code == 503
        assert "not saved" in failure.json()["detail"]


def test_successive_undo_restores_each_live_setting_without_resurrecting_newer_change(service):
    submit(service, "いつも説明が長い", "first")
    submit(service, "", "second", change={"field": "response_length", "value": "detailed", "scope": "owner"})
    first_undo = submit(service, "さっきの変更を戻して", "undo-latest")
    assert first_undo["state"]["policy"]["response_length"] == "brief"
    second_undo = submit(service, "さっきの変更を戻して", "undo-earlier")
    assert second_undo["state"]["policy"]["response_length"] == "default"
    assert second_undo["state"]["revision"] == 4


def test_known_correction_keeps_memory_reference_and_requires_karte_review(service):
    result = submit(service, "違う，それは先週の話", "correction", target={"turn_id": "fixture-turn", "memory_ids": ["doc-fixture"]})
    assert result["feedback"]["status"] == "recorded"
    assert result["feedback"]["correction_status"] == "karte_review_required"
    assert result["feedback"]["target"]["memory_ids"] == ["doc-fixture"]
    assert result["feedback"]["dimension"] == "factual_correction"
    assert result["change"] is None
    assert result["state"]["revision"] == 0


def test_owner_can_review_and_delete_earlier_session_feedback_after_restart(service):
    result = submit(service, "もっと短くして", "old-session")
    restarted = ResidentService(store=service.store.preference_store, instance_id="synthetic-instance")
    state = restarted.create_session(ResidentSessionRequest(session_id="later-session", owner_selected=True, storage_consent=True))
    assert state["feedback"][0]["event_id"] == result["feedback"]["event_id"]
    deletion = restarted.retract(result["feedback"]["event_id"], RetractRequest(session_id="later-session", dedupe_id="delete-old", expected_revision=1, delete=True))
    assert deletion["state"]["feedback"][0]["deleted"]
    assert deletion["state"]["policy"]["response_length"] == "default"


def test_targeted_memory_undo_preserves_later_unrelated_memory_restriction(service):
    first = submit(service, "それは人前で言わないで", "private-a", target={"turn_id": "fixture", "memory_ids": ["doc-a"]})
    second = submit(service, "それは人前で言わないで", "private-b", target={"turn_id": "fixture", "memory_ids": ["doc-b"]})
    result = service.undo(first["change"]["change_id"], UndoRequest(session_id="fixture", dedupe_id="undo-a", expected_revision=2))
    assert result["state"]["policy"]["restricted_memory_ids"] == ["doc-b"]
    result = service.undo(second["change"]["change_id"], UndoRequest(session_id="fixture", dedupe_id="undo-b", expected_revision=3))
    assert result["state"]["policy"]["restricted_memory_ids"] == []


def test_deleted_predecessor_is_not_resurrected_by_later_undo(service):
    first = submit(service, "いつも説明が長い", "first")
    second = submit(service, "", "second", change={"field": "response_length", "value": "detailed", "scope": "owner"})
    service.retract(first["feedback"]["event_id"], RetractRequest(session_id="fixture", dedupe_id="forget-first", expected_revision=2, delete=True))
    result = service.undo(second["change"]["change_id"], UndoRequest(session_id="fixture", dedupe_id="undo-second", expected_revision=2))
    assert result["state"]["policy"]["response_length"] == "default"


def test_gateway_can_reuse_resident_database_without_moving_ab_store(tmp_path, monkeypatch):
    monkeypatch.setenv('EPHY_RESIDENT_ENABLED', '1')
    monkeypatch.setenv('EPHY_PREFERENCE_DATA_ROOT', str(tmp_path / 'ab'))
    monkeypatch.setenv('EPHY_RESIDENT_PREFERENCE_DATA_ROOT', str(tmp_path / 'resident'))
    with TestClient(gateway.app):
        assert gateway.app.state.preference_service.store.data_root == tmp_path / 'ab'
        assert gateway.app.state.resident_service.store.preference_store.data_root == tmp_path / 'resident'
