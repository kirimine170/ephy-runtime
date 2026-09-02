import hashlib
import io
import json
import shutil
from pathlib import Path
from types import SimpleNamespace

import pytest

from packages.model_registry.__main__ import launch_command
from packages.model_registry.service import ModelRegistry, Selection


def gguf(path, suffix=b"weights"):
    path.write_bytes(b"GGUF\x03\x00\x00\x00" + bytes(16) + suffix)
    return path


@pytest.fixture
def registry(tmp_path):
    service = ModelRegistry(tmp_path)
    service.import_model(gguf(tmp_path / "base.gguf"), model_id="base", backend_model="backend-base")
    return service


def copy_profiles(tmp_path):
    configs = tmp_path / "configs"
    configs.mkdir(exist_ok=True)
    shutil.copyfile(Path(__file__).parents[1] / "configs/model-profiles.yaml",
                    configs / "model-profiles.yaml")


def test_import_is_local_idempotent_and_records_digest(registry, tmp_path):
    first = registry.registry().models[0]
    assert first.sha256 == hashlib.sha256((tmp_path / "base.gguf").read_bytes()).hexdigest()
    assert first.path == str(tmp_path / "base.gguf")
    assert first.source == "local"
    registry.import_model(tmp_path / "base.gguf", model_id="base", backend_model="backend-base")
    assert len(registry.registry().models) == 1
    assert registry.catalog()["models"][0]["available"] is True


def test_import_never_replaces_an_existing_id(registry, tmp_path):
    with pytest.raises(ValueError, match="already exists"):
        registry.import_model(gguf(tmp_path / "other.gguf", b"other"), model_id="base")
    assert registry.registry().models[0].backend_model == "backend-base"


def test_qwen_profiles_keep_runtime_policy_out_of_model_code(tmp_path, monkeypatch):
    copy_profiles(tmp_path)
    service = ModelRegistry(tmp_path)
    model = service.import_model(gguf(tmp_path / "large.gguf"), model_id="qwen3.8-27b")
    assert model.profile_id == "qwen3.8-27b"
    assert model.context_size == 32768

    monkeypatch.setattr("packages.model_registry.service.physical_memory_bytes", lambda: 16 * 1024 ** 3)
    catalog = service.catalog()
    large = catalog["models"][0]
    assert large["native_context_size"] == 262144
    assert large["maximum_context_size"] == 1_000_000
    assert large["startup_timeout_seconds"] == 420
    assert large["resource_fit"] is False
    assert "vision" in large["capabilities"]
    assert "vision" not in large["enabled_capabilities"]


def test_profile_context_limit_and_explicit_profile_are_enforced(tmp_path):
    copy_profiles(tmp_path)
    service = ModelRegistry(tmp_path)
    with pytest.raises(ValueError, match="profile maximum"):
        service.import_model(gguf(tmp_path / "too-wide.gguf"), model_id="custom",
                             profile_id="qwen3-8b", context_size=262144)
    model = service.import_model(gguf(tmp_path / "small.gguf"), model_id="custom",
                                 profile_id="qwen3-8b")
    assert model.context_size == 32768
    assert service.catalog()["models"][0]["family"] == "qwen3"


def test_model_override_carries_thinking_policy(tmp_path):
    copy_profiles(tmp_path)
    service = ModelRegistry(tmp_path)
    service.import_model(gguf(tmp_path / "large.gguf"), model_id="qwen3.8-27b")
    service.select("work", Selection(model_id="qwen3.8-27b"), expected_revision=service.revision())
    override = service.model_overrides()["work"]
    assert override["thinking_mode"] == "optional"
    assert override["default_reasoning_effort"] == "medium"
    assert override["preserve_thinking"] is True
    argv = launch_command(service, "work", Path("/server"), "unused", "unused")
    assert "--reasoning-preserve" in argv


@pytest.mark.parametrize("payload", [b"not a model", b"GGUF\x03\0\0\0", b"GGUF\x01\0\0\0" + bytes(32)])
def test_rejects_non_gguf_or_truncated_header(tmp_path, payload):
    path = tmp_path / "bad.gguf"
    path.write_bytes(payload)
    with pytest.raises(ValueError):
        ModelRegistry(tmp_path).import_model(path, model_id="bad")


def test_selection_is_atomic_and_keeps_other_roles(registry):
    revision = registry.revision()
    registry.select("fast", Selection(model_id="base"), expected_revision=revision)
    revision = registry.revision()
    registry.select("code", Selection(model_id="base"), expected_revision=revision)
    assert set(registry.selections().roles) == {"fast", "code"}
    with pytest.raises(ValueError, match="concurrently"):
        registry.select("fast", None, expected_revision=revision)
    assert "fast" in registry.selections().roles
    registry.select("fast", None, expected_revision=registry.revision())
    assert set(registry.selections().roles) == {"code"}
    assert registry.selection_path.stat().st_mode & 0o777 == 0o600


def test_selection_lease_detects_writes_that_bypass_registry_lock(registry):
    with pytest.raises(ValueError, match="outside the registry lock"):
        with registry.selection_lease():
            registry.selection_path.parent.mkdir(parents=True, exist_ok=True)
            registry.selection_path.write_text(
                '{"schema_version":1,"roles":{}}', encoding="utf-8"
            )


def test_modified_file_cannot_be_selected(registry, tmp_path):
    original = registry.revision()
    gguf(tmp_path / "base.gguf", b"changed")
    with pytest.raises(ValueError, match="checksum or size"):
        registry.select("fast", Selection(model_id="base"), expected_revision=original)
    assert registry.revision() == original


def test_adapter_requires_exact_base_and_can_be_disabled(registry, tmp_path):
    registry.import_adapter(gguf(tmp_path / "style.gguf", b"adapter"), adapter_id="style", base_model_id="base")
    registry.import_model(gguf(tmp_path / "other.gguf", b"other"), model_id="other")
    with pytest.raises(ValueError, match="exact base"):
        registry.resolve(Selection(model_id="other", adapter_id="style"))
    with pytest.raises(ValueError, match="exact base"):
        registry.resolve(Selection(model_id="base", adapter_id="missing"))
    registry.select("fast", Selection(model_id="base", adapter_id="style"), expected_revision=registry.revision())
    argv = launch_command(registry, "fast", Path("/local/llama-server"), "unused", "unused")
    assert argv[argv.index("--alias") + 1] == "backend-base"
    assert argv[argv.index("--lora") + 1] == str(tmp_path / "style.gguf")
    assert registry.model_overrides()["fast"]["model"] == "backend-base"
    registry.select("fast", Selection(model_id="base"), expected_revision=registry.revision())
    assert "--lora" not in launch_command(registry, "fast", Path("/server"), "unused", "unused")


def test_selection_does_not_change_identity_or_existing_yaml(registry, tmp_path):
    identity = tmp_path / "identity.yaml"
    identity.write_text("unchanged identity")
    overrides = tmp_path / "configs/models.local.yaml"
    overrides.write_text("models:\n  work:\n    default_temperature: 0.1\n")
    registry.select("fast", Selection(model_id="base"), expected_revision=registry.revision())
    assert identity.read_text() == "unchanged identity"
    assert "default_temperature: 0.1" in overrides.read_text()


def test_invalid_registry_never_silently_falls_back(registry):
    registry.select("fast", Selection(model_id="base"), expected_revision=registry.revision())
    registry.registry_path.write_text('{"bad": true}')
    with pytest.raises(ValueError):
        launch_command(registry, "fast", Path("/server"), "legacy.gguf", "legacy")


def test_unknown_or_embedding_role_is_rejected(registry):
    with pytest.raises(ValueError, match="embedding"):
        registry.select("embedding", Selection(model_id="base"), expected_revision=registry.revision())
    with pytest.raises(ValueError, match="Unknown model"):
        registry.resolve(Selection(model_id="unknown"))


def test_download_checks_size_checksum_and_never_overwrites(tmp_path):
    data = b"GGUF\x03\0\0\0" + bytes(32)
    checksum = hashlib.sha256(data).hexdigest()
    opener = SimpleNamespace(open=lambda *_args, **_kwargs: io.BytesIO(data))
    service = ModelRegistry(tmp_path)
    entry = service.download(model_id="download", url="https://example.org/model.gguf",
                             sha256=checksum, size_bytes=len(data), revision="pinned-revision", opener=opener)
    assert Path(entry.path).read_bytes() == data
    with pytest.raises(ValueError, match="already exists"):
        service.download(model_id="download", url="https://example.org/model.gguf", sha256=checksum,
                         size_bytes=len(data), revision="other", opener=opener)
    assert not list((tmp_path / "models/registry").glob("*.part"))


@pytest.mark.parametrize("checksum_ok,size_delta", [(False, 0), (True, 1), (True, -1)])
def test_failed_download_is_not_published(tmp_path, checksum_ok, size_delta):
    data = b"GGUF\x03\0\0\0" + bytes(32)
    checksum = hashlib.sha256(data).hexdigest() if checksum_ok else "0" * 64
    service = ModelRegistry(tmp_path)
    with pytest.raises(ValueError):
        service.download(model_id="bad", url="https://example.org/model.gguf", sha256=checksum,
                         size_bytes=len(data) + size_delta, revision="rev",
                         opener=SimpleNamespace(open=lambda *_args, **_kwargs: io.BytesIO(data)))
    assert service.registry().models == []
    assert not list((tmp_path / "models/registry").iterdir())


def test_download_rejects_low_disk_before_network(tmp_path, monkeypatch):
    monkeypatch.setattr("packages.model_registry.service.shutil.disk_usage", lambda _: SimpleNamespace(free=0))
    with pytest.raises(ValueError, match="disk"):
        ModelRegistry(tmp_path).download(model_id="base", url="https://example.org/model.gguf",
                                         sha256="0" * 64, size_bytes=100, revision="rev")


def test_download_dry_run_reports_space_without_mutation(tmp_path, monkeypatch):
    monkeypatch.setattr("packages.model_registry.service.shutil.disk_usage", lambda _: SimpleNamespace(free=500))
    report = ModelRegistry(tmp_path).plan_download(model_id="base", url="https://example.org/model.gguf",
                                                   sha256="0" * 64, size_bytes=100, revision="rev")
    assert report["required_disk_bytes"] == 100 + 64 * 1024 * 1024
    assert report["free_disk_bytes"] == 500 and report["has_space"] is False
    assert not list(tmp_path.iterdir())


@pytest.mark.parametrize("url", ["http://example.org/model", "https://user:password@example.org/model", "https://example.org/model?token=example"])
def test_download_does_not_store_credentials_or_use_plain_http(tmp_path, url):
    with pytest.raises(ValueError, match="credential-free"):
        ModelRegistry(tmp_path).download(model_id="base", url=url, sha256="0" * 64, size_bytes=100, revision="rev")
