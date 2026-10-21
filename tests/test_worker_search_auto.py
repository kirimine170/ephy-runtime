from copy import deepcopy

import pytest

from packages.config_core.loader import WebSearchConfig
from packages.web_search_core.worker_provider import (
    WorkerSearchError,
    WorkerSearchProvider,
)


def fixture(monkeypatch, target="auto"):
    monkeypatch.setenv("AUTO_TEST_CREDENTIAL", "fixture-only")
    provider = WorkerSearchProvider(WebSearchConfig(target_worker_id=target,
        manager_credential_env="AUTO_TEST_CREDENTIAL"))
    session = provider.new_session("public question", "fixed-plan")
    view = {**deepcopy(session.submit), "job_id": "job-00000001", "mode": "search",
            "input_hash": session.input_hash, "state": "queued", "worker_id": None,
            "created_at": 10, "deadline_at": 10 + session.submit["deadline_seconds"]}
    return provider, session, view


@pytest.mark.parametrize("worker", ["worker-a", "worker-b"])
def test_auto_binds_actual_worker_per_job(monkeypatch, worker):
    provider, session, view = fixture(monkeypatch)
    provider._validate_view(session, view)
    assert session.identity()["worker_id"] is None
    for state in ("running", "cancel_requested", "completed", "partial"):
        view.update(state=state, worker_id=worker)
        provider._validate_view(session, view)
        assert session.identity()["worker_id"] == worker
        assert session.identity()["target_worker_id"] == "auto"


@pytest.mark.parametrize("replacement", ["worker-b", None, "auto", "bad worker", 123])
@pytest.mark.parametrize("state", ["completed", "partial"])
def test_auto_reassignment_or_invalid_worker_is_rejected(monkeypatch, replacement, state):
    provider, session, view = fixture(monkeypatch)
    view.update(state="running", worker_id="worker-a")
    provider._validate_view(session, view)
    view.update(state=state, worker_id=replacement)
    with pytest.raises(WorkerSearchError, match="identity_mismatch"):
        provider._validate_view(session, view)
    assert session.assigned_worker_id == "worker-a"


def test_invalid_auto_view_cannot_poison_assignment(monkeypatch):
    provider, session, view = fixture(monkeypatch)
    bad = deepcopy(view)
    bad.update(state="running", worker_id="worker-wrong", deadline_at=9999)
    with pytest.raises(WorkerSearchError):
        provider._validate_view(session, bad)
    assert session.assigned_worker_id is None and session.job_id is None
    view.update(state="completed", worker_id="worker-correct")
    provider._validate_view(session, view)
    assert session.identity()["worker_id"] == "worker-correct"


@pytest.mark.parametrize("state", ["running", "cancel_requested", "completed", "partial"])
def test_explicit_target_keeps_exact_worker_check(monkeypatch, state):
    provider, session, view = fixture(monkeypatch, "worker-explicit")
    view.update(state=state, worker_id="worker-other")
    with pytest.raises(WorkerSearchError, match="worker_search_failed" if state == "partial" else "identity_mismatch"):
        provider._validate_view(session, view)
    view["worker_id"] = "worker-explicit"
    provider._validate_view(session, view)
    assert session.identity()["worker_id"] == "worker-explicit"
    assert "target_worker_id" not in session.identity()


def test_public_partial_wrong_worker_remains_a_failure(monkeypatch):
    provider, session, view = fixture(monkeypatch, "worker-explicit")
    view.update(state="partial", worker_id=None)
    with pytest.raises(WorkerSearchError, match="worker_search_failed"):
        provider._validate_view(session, view)
    assert session.accepted_sources is None and session.job_id is None


def test_manager_progress_only_changes_for_validated_identity_or_state(monkeypatch):
    provider, session, view = fixture(monkeypatch)
    observed = []
    session.progress_callback = lambda: observed.append(session.identity())
    provider._validate_view(session, view)
    assert len(observed) == 1
    provider._validate_view(session, view)
    assert len(observed) == 1
    view.update(state="running", worker_id="worker-a")
    provider._validate_view(session, view)
    provider._validate_view(session, view)
    assert len(observed) == 2
    view.update(state="partial", worker_id="worker-a")
    provider._validate_view(session, view)
    assert len(observed) == 3
    view["worker_id"] = "wrong-worker"
    with pytest.raises(WorkerSearchError, match="identity_mismatch"):
        provider._validate_view(session, view)
    assert len(observed) == 3
