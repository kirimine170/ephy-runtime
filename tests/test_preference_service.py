import asyncio
from contextlib import contextmanager
from pathlib import Path
from types import SimpleNamespace

import pytest

from packages.config_core.loader import AppConfig, ModelConfig, RouteConfig, ROOT_DIR
from packages.eval_core.preference_schemas import (
    CreatePreferenceSessionRequest,
    SubmitPreferenceVoteRequest,
)
from packages.eval_core.preference_service import PreferenceService
from packages.eval_core.preference_store import PreferenceStore
from packages.prompt_core.loader import PromptManager


class FakeRegistry:
    @contextmanager
    def selection_lease(self):
        yield

    def selections(self):
        return SimpleNamespace(roles={"fast": object()})

    def resolve(self, selection, verify=False):
        model = SimpleNamespace(id="registered-fast", sha256="a" * 64, backend_model="backend-fast")
        adapter = SimpleNamespace(id="warm-politeness-v1", sha256="b" * 64)
        return model, adapter


class FakeAdapter:
    def __init__(self, responses):
        self.responses = iter(responses)
        self.requests = []

    async def create_chat_completion(self, *, model_config, request_payload):
        self.requests.append(request_payload)
        return {"choices": [{"message": {"content": next(self.responses)}}]}


def write_dataset(root: Path) -> Path:
    configs = root / "configs"
    configs.mkdir(parents=True)
    path = configs / "preference.yaml"
    path.write_text(
        """
scenarios:
  - scenario_id: synthetic-service-1
    category: test
    messages:
      - role: user
        content: hello
    source_kind: synthetic
    provenance: unit test
    consent: {storage: true, training: true}
    split: train
""".strip(),
        encoding="utf-8",
    )
    return path


def make_service(tmp_path, responses, display_order="ba") -> tuple[PreferenceService, FakeAdapter]:
    repository = tmp_path / "repository"
    write_dataset(repository)
    config = AppConfig(
        models={"fast": ModelConfig(provider="llama_cpp", model="backend-fast", base_url="http://test")},
        routes={"fast": RouteConfig(model="fast")},
    )
    adapter = FakeAdapter(responses)
    service = PreferenceService(
        config=config,
        prompt_manager=PromptManager(prompts_dir=ROOT_DIR / "prompts"),
        adapter=adapter,
        store=PreferenceStore(tmp_path / "data"),
        registry=FakeRegistry(),
        repository_root=repository,
        choose_display_order=lambda: display_order,
        seed_factory=iter(range(100, 200)).__next__,
    )
    return service, adapter


def test_service_generates_blind_randomized_pair_and_metadata(tmp_path) -> None:
    service, adapter = make_service(tmp_path, ["candidate one", "candidate two"], "ba")
    session = service.create_session(
        CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml", pair_count=1)
    )
    asyncio.run(service.generate(session["session_id"], 1))

    blind = service.next_pair(session["session_id"])
    stored = service.store.get_pair(blind.pair_id)

    assert blind.response_left == "candidate two"
    assert blind.response_right == "candidate one"
    assert "model" not in blind.model_dump_json()
    assert stored.candidate_a.model_registration_id == "registered-fast"
    assert stored.candidate_a.adapter_registration_id == "warm-politeness-v1"
    assert stored.candidate_a.model_sha256 == "a" * 64
    assert stored.candidate_a.adapter_sha256 == "b" * 64
    assert adapter.requests[0].temperature > 0
    assert adapter.requests[0].seed != adapter.requests[1].seed


@pytest.mark.parametrize(
    ("display_order", "display_selection", "canonical"),
    [("ab", "left", "a"), ("ab", "right", "b"), ("ba", "left", "b"), ("ba", "right", "a")],
)
def test_vote_mapping_is_server_side(tmp_path, display_order, display_selection, canonical) -> None:
    service, _ = make_service(tmp_path, ["one", "two"], display_order)
    session = service.create_session(
        CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml", pair_count=1)
    )
    asyncio.run(service.generate(session["session_id"], 1))
    pair_id = service.next_pair(session["session_id"]).pair_id

    saved = service.vote(pair_id, SubmitPreferenceVoteRequest(selection=display_selection))

    assert saved.selection == canonical
    assert service.stats(session["session_id"])["display_selection_rate"][display_selection] == 1.0


def test_duplicate_generation_retries_twice_and_is_not_reviewed(tmp_path) -> None:
    service, adapter = make_service(tmp_path, ["same", " same ", "same", "same"])
    session = service.create_session(
        CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml", pair_count=1)
    )
    asyncio.run(service.generate(session["session_id"], 1))

    assert len(adapter.requests) == 4
    assert service.next_pair(session["session_id"]) is None
    assert service.stats(session["session_id"])["duplicate_generation"] == 1


def test_dataset_path_traversal_and_nonsynthetic_repository_data_are_rejected(tmp_path) -> None:
    service, _ = make_service(tmp_path, ["one", "two"])
    outside = tmp_path / "outside.yaml"
    outside.write_text("scenarios: []", encoding="utf-8")
    with pytest.raises(ValueError, match="repository configs"):
        service.create_session(CreatePreferenceSessionRequest(dataset_path=str(outside)))

    fixture = tmp_path / "repository/configs/preference.yaml"
    fixture.write_text(fixture.read_text().replace("synthetic", "manual"), encoding="utf-8")
    with pytest.raises(ValueError, match="synthetic"):
        service.create_session(
            CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml")
        )


def test_dataset_requires_storage_consent(tmp_path) -> None:
    service, _ = make_service(tmp_path, ["one", "two"])
    fixture = tmp_path / "repository/configs/preference.yaml"
    fixture.write_text(
        fixture.read_text().replace("storage: true", "storage: false"),
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="storage consent"):
        service.create_session(
            CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml")
        )


def test_service_rejects_data_root_inside_repository(tmp_path) -> None:
    repository = tmp_path / "repository"
    write_dataset(repository)
    config = AppConfig(
        models={"fast": ModelConfig(provider="llama_cpp", model="backend-fast")},
        routes={"fast": RouteConfig(model="fast")},
    )
    service = PreferenceService(
        config=config,
        prompt_manager=PromptManager(prompts_dir=ROOT_DIR / "prompts"),
        adapter=FakeAdapter(["one", "two"]),
        store=PreferenceStore(repository / "data/preferences"),
        registry=FakeRegistry(),
        repository_root=repository,
    )

    with pytest.raises(ValueError, match="separate from the Git repository"):
        service.create_session(
            CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml")
        )


def test_service_rejects_data_root_that_contains_repository(tmp_path) -> None:
    data_root = tmp_path / "preference-root"
    repository = data_root / "source/ephy-runtime"
    write_dataset(repository)
    config = AppConfig(
        models={"fast": ModelConfig(provider="llama_cpp", model="backend-fast")},
        routes={"fast": RouteConfig(model="fast")},
    )
    service = PreferenceService(
        config=config,
        prompt_manager=PromptManager(prompts_dir=ROOT_DIR / "prompts"),
        adapter=FakeAdapter(["one", "two"]),
        store=PreferenceStore(data_root),
        registry=FakeRegistry(),
        repository_root=repository,
    )

    with pytest.raises(ValueError, match="separate from the Git repository"):
        service.create_session(
            CreatePreferenceSessionRequest(dataset_path="configs/preference.yaml")
        )


def test_existing_eval_runner_regression_is_covered_by_original_test_module() -> None:
    assert (ROOT_DIR / "tests/test_eval_runner.py").is_file()
