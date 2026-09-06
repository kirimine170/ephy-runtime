from pathlib import Path
import os

import pytest

from repository_fixture import copy_template_repository


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]


@pytest.fixture
def template_source(tmp_path: Path) -> Path:
    source = tmp_path / "source"
    copy_template_repository(REPOSITORY_ROOT, source)
    return source


def test_fixture_copies_current_template_inputs_without_local_data(
    template_source: Path, tmp_path: Path
) -> None:
    readme = template_source / "README.md"
    readme.write_text("current local source edit\n", encoding="utf-8")
    for relative in (
        "models/weight.gguf",
        "data/private/conversation.json",
        "configs/runtime.local.yaml",
        ".venv/dependency.py",
        "desktop/frontend/node_modules/dependency.js",
        "untracked.txt",
    ):
        path = template_source / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("local data must stay in source\n", encoding="utf-8")
    external = tmp_path / "external"
    external.mkdir()
    (external / "private.txt").write_text("external private data\n", encoding="utf-8")
    (template_source / "linked-private").symlink_to(external, target_is_directory=True)

    destination = tmp_path / "destination"
    copy_template_repository(template_source, destination)

    assert (destination / "README.md").read_bytes() == readme.read_bytes()
    assert (destination / ".ephy/schema/project.schema.json").is_file()
    assert (destination / "tests/test_web_search_security.py").is_file()
    for relative in (
        "models", "data", "configs", ".venv", "desktop", "untracked.txt", "linked-private"
    ):
        assert not (destination / relative).exists()
    assert (external / "private.txt").read_text() == "external private data\n"


@pytest.mark.parametrize("kind", ("file_symlink", "parent_symlink", "fifo", "missing"))
def test_fixture_rejects_unsafe_or_missing_inputs_before_copying(
    template_source: Path, tmp_path: Path, kind: str
) -> None:
    if kind == "parent_symlink":
        original = template_source / ".ephy"
        external = tmp_path / "external-metadata"
        original.rename(external)
        original.symlink_to(external, target_is_directory=True)
    else:
        original = template_source / "README.md"
        original.unlink()
        if kind == "file_symlink":
            external = tmp_path / "external-readme"
            external.write_text("external private data\n", encoding="utf-8")
            original.symlink_to(external)
        elif kind == "fifo":
            os.mkfifo(original)

    destination = tmp_path / "destination"
    with pytest.raises((ValueError, FileNotFoundError)):
        copy_template_repository(template_source, destination)
    assert not destination.exists()
