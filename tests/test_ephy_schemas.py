from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest
import yaml
from jsonschema import Draft202012Validator, FormatChecker, ValidationError


ROOT = Path(__file__).resolve().parents[1]
SCHEMA_DIR = ROOT / "schemas"
EXAMPLE_DIR = ROOT / "configs" / "examples"

SCHEMA_EXAMPLES = {
    "identity_manifest.schema.json": "identity.example.yaml",
    "ephy_profile.schema.json": "profile.example.yaml",
    "memory_object.schema.json": "memory.example.yaml",
}

FORBIDDEN_PUBLIC_MARKERS = (
    "/Users/",
    "BEGIN PRIVATE KEY",
    "gho_",
    "sk-",
)

REQUIRED_PRIVATE_IGNORE_PATTERNS = {
    "data/private/",
    "instances/",
    "datasets/private/",
    "models/private/",
    "*.identity.yaml",
    "*.private.yaml",
    "*.key",
    "*.pem",
    "*.secret",
}


def _load_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def _load_yaml(path: Path) -> dict:
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _validator(schema_name: str) -> Draft202012Validator:
    schema = _load_json(SCHEMA_DIR / schema_name)
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema, format_checker=FormatChecker())


@pytest.mark.parametrize(("schema_name", "example_name"), SCHEMA_EXAMPLES.items())
def test_example_matches_schema(schema_name: str, example_name: str) -> None:
    _validator(schema_name).validate(_load_yaml(EXAMPLE_DIR / example_name))


def test_identity_rejects_invalid_uuid() -> None:
    manifest = _load_yaml(EXAMPLE_DIR / "identity.example.yaml")
    manifest["identity"]["instance_id"] = "not-a-uuid"

    with pytest.raises(ValidationError):
        _validator("identity_manifest.schema.json").validate(manifest)


def test_identity_rejects_negative_ordinal() -> None:
    manifest = _load_yaml(EXAMPLE_DIR / "identity.example.yaml")
    manifest["identity"]["ordinal"] = -1

    with pytest.raises(ValidationError):
        _validator("identity_manifest.schema.json").validate(manifest)


def test_identity_allows_omitted_parent_instance_id() -> None:
    manifest = _load_yaml(EXAMPLE_DIR / "identity.example.yaml")
    del manifest["identity"]["parent_instance_id"]
    _validator("identity_manifest.schema.json").validate(manifest)


def test_identity_rejects_embedded_owner_data() -> None:
    manifest = _load_yaml(EXAMPLE_DIR / "identity.example.yaml")
    manifest["ownership"]["owner_data_embedded"] = True

    with pytest.raises(ValidationError):
        _validator("identity_manifest.schema.json").validate(manifest)


def test_profile_requires_first_person() -> None:
    profile = _load_yaml(EXAMPLE_DIR / "profile.example.yaml")
    del profile["voice"]["first_person"]

    with pytest.raises(ValidationError):
        _validator("ephy_profile.schema.json").validate(profile)


def test_profile_rejects_unknown_field() -> None:
    profile = _load_yaml(EXAMPLE_DIR / "profile.example.yaml")
    profile["private_note"] = "must not be accepted"

    with pytest.raises(ValidationError):
        _validator("ephy_profile.schema.json").validate(profile)


def test_memory_rejects_confidence_outside_range() -> None:
    memory = _load_yaml(EXAMPLE_DIR / "memory.example.yaml")
    memory["confidence"] = 1.01

    with pytest.raises(ValidationError):
        _validator("memory_object.schema.json").validate(memory)


def test_memory_keeps_storage_and_training_consent_separate() -> None:
    memory = _load_yaml(EXAMPLE_DIR / "memory.example.yaml")
    training_allowed = copy.deepcopy(memory)
    training_allowed["consent"]["training_allowed"] = True

    validator = _validator("memory_object.schema.json")
    validator.validate(memory)
    validator.validate(training_allowed)
    assert memory["consent"]["storage_allowed"] is True
    assert memory["consent"]["training_allowed"] is False


def test_public_examples_do_not_contain_private_markers() -> None:
    for example_path in EXAMPLE_DIR.glob("*.yaml"):
        text = example_path.read_text(encoding="utf-8")
        for marker in FORBIDDEN_PUBLIC_MARKERS:
            assert marker not in text, f"{example_path} contains forbidden marker {marker!r}"


def test_private_paths_and_secret_files_are_ignored() -> None:
    patterns = {
        line.strip()
        for line in (ROOT / ".gitignore").read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }

    assert REQUIRED_PRIVATE_IGNORE_PATTERNS <= patterns
