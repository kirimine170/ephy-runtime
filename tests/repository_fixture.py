"""Build the template-tool fixture without copying a developer's runtime data."""

from pathlib import Path
import shutil
import stat


TEMPLATE_FIXTURE_FILES = (
    ".ephy/project.yaml",
    ".ephy/project.template.yaml",
    ".ephy/schema/project.schema.json",
    ".github/ISSUE_TEMPLATE/bug.yml",
    ".github/ISSUE_TEMPLATE/feature.yml",
    ".github/ISSUE_TEMPLATE/design.yml",
    ".github/ISSUE_TEMPLATE/config.yml",
    ".github/pull_request_template.md",
    ".github/workflows/validate-template.yml",
    "docs/architecture.md",
    "docs/repository-relations.md",
    "docs/security-and-data.md",
    "docs/adr/README.md",
    "docs/adr/0000-template.md",
    "scripts/init_repository.py",
    "scripts/validate_repository.py",
    "tests/test_init_repository.py",
    "tests/test_validate_repository.py",
    "tests/test_web_search_security.py",
    "AGENTS.md",
    "README.md",
    "README.template.md",
    "CHANGELOG.md",
    ".editorconfig",
    ".gitattributes",
    ".gitignore",
)


def copy_template_repository(source: Path, destination: Path) -> None:
    """Copy only the inputs exercised by initializer and validator tests.

    The fixture reads current checkout contents，including source edits．It does
    not walk the checkout or follow symlinks into model or private directories．
    Validate every input before creating the destination to avoid partial copies．
    """
    source = source.resolve()
    for relative in TEMPLATE_FIXTURE_FILES:
        path = source / relative
        for candidate in (path, *path.parents):
            if candidate == source:
                break
            if candidate.is_symlink():
                raise ValueError(f"template fixture input is a symlink: {relative}")
        if not stat.S_ISREG(path.lstat().st_mode):
            raise ValueError(f"template fixture input is not a regular file: {relative}")

    destination.mkdir()
    for relative in TEMPLATE_FIXTURE_FILES:
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source / relative, target)
