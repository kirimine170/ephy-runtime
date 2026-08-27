from types import SimpleNamespace

from fastapi.testclient import TestClient

import apps.gateway.main as gateway_main
from packages.eval_core.preference_schemas import BlindPreferencePair, PreferenceVote


class FakePreferenceService:
    def __init__(self, **kwargs):
        self.store = SimpleNamespace(
            get_pair=lambda pair_id: SimpleNamespace(session_id="session-1")
        )

    def list_sessions(self):
        return [{"session_id": "session-1", "reviewed": 0, "remaining": 1}]

    def create_session(self, payload):
        return {
            "session_id": "session-1",
            "model_role": payload.model_role,
            "comparison_mode": payload.comparison_mode,
        }

    async def generate(self, session_id, limit):
        return {"session_id": session_id, "generated": [{"pair_id": "pair-1"}]}

    def next_pair(self, session_id):
        return BlindPreferencePair(
            pair_id="pair-1",
            messages=[{"role": "user", "content": "hello"}],
            response_left="left response",
            response_right="right response",
            category="test",
            progress={"reviewed": 0, "remaining": 1, "total": 1},
        )

    def vote(self, pair_id, payload):
        return PreferenceVote(
            vote_id="vote-1",
            pair_id=pair_id,
            selection="a",
            created_at="2026-01-01T00:00:00Z",
        )

    def stats(self, session_id):
        return {"session_id": session_id, "total": 1, "reviewed": 1, "remaining": 0}

    def export(self, session_id, payload):
        return {"session_id": session_id, "format": payload.format, "records": 1}


def test_preference_api_flow_is_blind(monkeypatch) -> None:
    monkeypatch.setattr(gateway_main, "PreferenceService", FakePreferenceService)

    with TestClient(gateway_main.app) as client:
        created = client.post(
            "/v1/eval/preferences/sessions",
            json={
                "dataset_path": "configs/eval.preference.sample.yaml",
                "comparison_mode": "prompt_v2_v3",
            },
        )
        generated = client.post(
            "/v1/eval/preferences/sessions/session-1/generate", json={"limit": 1}
        )
        pair = client.get("/v1/eval/preferences/sessions/session-1/next")
        vote = client.post(
            "/v1/eval/preferences/pairs/pair-1/vote", json={"selection": "left"}
        )
        stats = client.get("/v1/eval/preferences/sessions/session-1/stats")
        exported = client.post(
            "/v1/eval/preferences/sessions/session-1/export",
            json={"format": "dpo", "output": "exports/test.jsonl"},
        )

    assert created.status_code == 200
    assert created.json()["comparison_mode"] == "prompt_v2_v3"
    assert generated.status_code == 200
    assert vote.status_code == 200
    assert stats.status_code == 200
    assert exported.status_code == 200
    serialized = pair.json()
    assert pair.status_code == 200
    assert "model" not in serialized
    assert "adapter" not in serialized
    assert "candidate" not in serialized
    assert "display_order" not in serialized


def test_preference_api_lists_resumable_sessions(monkeypatch) -> None:
    monkeypatch.setattr(gateway_main, "PreferenceService", FakePreferenceService)

    with TestClient(gateway_main.app) as client:
        response = client.get("/v1/eval/preferences/sessions")

    assert response.json()["sessions"][0]["session_id"] == "session-1"
