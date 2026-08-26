import hashlib
import io
import json
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


@pytest.mark.parametrize("url", ["http://example.org/model", "https://user:password@example.org/model", "https://example.org/model?token=example"])
def test_download_does_not_store_credentials_or_use_plain_http(tmp_path, url):
    with pytest.raises(ValueError, match="credential-free"):
        ModelRegistry(tmp_path).download(model_id="base", url=url, sha256="0" * 64, size_bytes=100, revision="rev")
