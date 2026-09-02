from __future__ import annotations

from fastapi.testclient import TestClient

from apps.gateway.main import app
from packages.karte_core.conversation import KarteConversationService


def _payload(**overrides) -> dict:
    payload = {
        "conversation_id": "api-conversation-001",
        "occurred_at": "2026-09-01T10:30:00+09:00",
        "messages": [
            {"role": "user", "content": "Karteへの保存方針を決定したい"},
            {"role": "assistant", "content": "project優先で保存する方針に決定します．"},
        ],
        "project": "ephy",
        "resolution": "create",
        "sensitivity": "internal",
        "tags": [],
    }
    payload.update(overrides)
    return payload


def test_karte_conversation_api_plans_publishes_and_reports_status(tmp_path) -> None:
    root = tmp_path / "karte_data"
    (root / "content").mkdir(parents=True)
    service = KarteConversationService(root)

    with TestClient(app) as client:
        original = app.state.karte_conversation_service
        app.state.karte_conversation_service = service
        try:
            plan_response = client.post("/v1/karte/conversations/plan", json=_payload())
            publish_response = client.post(
                "/v1/karte/conversations/publish",
                json={**_payload(), "reviewed_plan_sha256": plan_response.json()["plan_sha256"]},
            )
            candidate_id = publish_response.json()["candidate_id"]
            status_response = client.get(f"/v1/karte/proposals/{candidate_id}")
        finally:
            app.state.karte_conversation_service = original

    assert plan_response.status_code == 200
    assert plan_response.json()["publishable"] is True
    assert plan_response.json()["context_status"] == {
        "status": "not_required",
        "searched_count": 0,
        "read_count": 0,
        "read_failed_count": 0,
    }
    assert publish_response.status_code == 200
    assert publish_response.json()["state"] == "pending"
    assert status_response.status_code == 200
    assert status_response.json()["state"] == "pending"


def test_karte_conversation_api_requires_configured_workspace() -> None:
    with TestClient(app) as client:
        original = app.state.karte_conversation_service
        app.state.karte_conversation_service = None
        try:
            response = client.post("/v1/karte/conversations/plan", json=_payload())
        finally:
            app.state.karte_conversation_service = original

    assert response.status_code == 503
    assert response.json()["detail"].startswith("Karte integration is unavailable")


def test_karte_conversation_api_does_not_publish_unresolved_project(tmp_path) -> None:
    root = tmp_path / "karte_data"
    (root / "content").mkdir(parents=True)
    service = KarteConversationService(root)

    with TestClient(app) as client:
        original = app.state.karte_conversation_service
        app.state.karte_conversation_service = service
        try:
            response = client.post(
                "/v1/karte/conversations/publish",
                json=_payload(project=None, resolution="auto"),
            )
        finally:
            app.state.karte_conversation_service = original

    assert response.status_code == 400
    assert "consultation" in response.json()["detail"]
    assert list((root / ".mdsys/ephy/outbox/pending").glob("*.json")) == []


def test_karte_conversation_api_rejects_unreviewed_publishable_plan(tmp_path) -> None:
    root = tmp_path / "karte_data"
    (root / "content").mkdir(parents=True)
    service = KarteConversationService(root)

    with TestClient(app) as client:
        original = app.state.karte_conversation_service
        app.state.karte_conversation_service = service
        try:
            response = client.post(
                "/v1/karte/conversations/publish",
                json={**_payload(), "reviewed_plan_sha256": "0" * 64},
            )
        finally:
            app.state.karte_conversation_service = original

    assert response.status_code == 400
    assert "changed after review" in response.json()["detail"]
    assert list((root / ".mdsys/ephy/outbox/pending").glob("*.json")) == []
