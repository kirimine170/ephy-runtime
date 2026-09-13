import json
from pathlib import Path

import pytest

from scripts.reuse_core.karte_fixture import prepare


def test_fixture_setup_is_idempotent_and_grants_only_current_shared_versions(tmp_path):
    first = prepare(tmp_path)
    assert prepare(tmp_path) == first
    grants = json.loads((tmp_path / "participation-grants.json").read_text())
    ids = {d["doc_id"] for d in grants["documents"]}
    assert "fixture-picnic" in ids and "fixture-old" not in ids and "fixture-private" not in ids
    assert grants["participants"] == ["owner"]
    assert grants["scope"]["projects"] == ["reuse-fixture"]
    assert len(first["documents"]) == 7


def test_fixture_setup_never_changes_existing_memory_or_configuration(tmp_path):
    content = tmp_path / "karte/content/existing.md"
    content.parent.mkdir(parents=True)
    content.write_text("keep this memory")
    with pytest.raises(ValueError, match="existing memory workspace"):
        prepare(tmp_path)
    assert content.read_text() == "keep this memory"
    assert not (tmp_path / "participation-grants.json").exists()


def test_existing_edited_fixture_is_not_overwritten(tmp_path):
    prepare(tmp_path)
    path = tmp_path / "participation-grants.json"
    path.write_text("changed by user")
    with pytest.raises(ValueError, match="Existing fixture/config differs"):
        prepare(tmp_path)
    assert path.read_text() == "changed by user"
