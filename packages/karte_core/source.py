from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path, PurePosixPath
from typing import Any

import yaml


@dataclass(frozen=True)
class KarteSourceIssue:
    code: str
    relative_path: str
    message: str
    doc_id: str | None = None


@dataclass(frozen=True)
class KarteDocument:
    doc_id: str
    title: str
    tags: list[str]
    relative_path: str
    absolute_path: Path
    updated_at: str
    sha256: str
    frontmatter: dict[str, Any]
    body: str


@dataclass
class KarteScanResult:
    documents: list[KarteDocument] = field(default_factory=list)
    issues: list[KarteSourceIssue] = field(default_factory=list)


class KarteSourceAdapter:
    """Read-only adapter for Karte's canonical Markdown documents."""

    def __init__(self, karte_data_dir: str | Path) -> None:
        data_root = Path(karte_data_dir).expanduser().resolve(strict=True)
        if not data_root.is_dir():
            raise ValueError("KARTE_DATA_DIR must be a directory")
        content_root = (data_root / "content").resolve(strict=True)
        if not content_root.is_dir() or not _is_within(content_root, data_root):
            raise ValueError("KARTE_DATA_DIR/content must be inside KARTE_DATA_DIR")
        self.data_root = data_root
        self.content_root = content_root

    def scan(self) -> KarteScanResult:
        result = KarteScanResult()
        parsed: list[KarteDocument] = []
        for root, dirnames, filenames in os.walk(self.content_root, followlinks=False):
            root_path = Path(root)
            retained_dirs: list[str] = []
            for dirname in sorted(dirnames):
                candidate = root_path / dirname
                if candidate.is_symlink():
                    result.issues.append(self._symlink_issue(candidate, directory=True))
                else:
                    retained_dirs.append(dirname)
            dirnames[:] = retained_dirs
            for filename in sorted(filenames):
                candidate = root_path / filename
                if candidate.suffix.lower() != ".md":
                    continue
                document, issue = self._read_candidate(candidate)
                if issue is not None:
                    result.issues.append(issue)
                elif document is not None:
                    parsed.append(document)

        by_doc_id: dict[str, list[KarteDocument]] = {}
        for document in parsed:
            by_doc_id.setdefault(document.doc_id, []).append(document)
        duplicate_ids = {doc_id for doc_id, documents in by_doc_id.items() if len(documents) > 1}
        for doc_id in sorted(duplicate_ids):
            for document in by_doc_id[doc_id]:
                result.issues.append(
                    KarteSourceIssue(
                        code="duplicate_doc_id",
                        relative_path=document.relative_path,
                        doc_id=doc_id,
                        message="doc_id is used by more than one canonical Markdown file",
                    )
                )
        result.documents = sorted(
            (document for document in parsed if document.doc_id not in duplicate_ids),
            key=lambda item: item.relative_path,
        )
        result.issues.sort(key=lambda item: (item.relative_path, item.code))
        return result

    def read_document(self, relative_path: str) -> KarteDocument:
        normalized = _validate_relative_path(relative_path)
        candidate = self.data_root / Path(*PurePosixPath(normalized).parts)
        document, issue = self._read_candidate(candidate)
        if issue is not None:
            raise ValueError(f"{issue.code}: {issue.relative_path}")
        if document is None:
            raise ValueError("document could not be read")
        return document

    def _read_candidate(self, candidate: Path) -> tuple[KarteDocument | None, KarteSourceIssue | None]:
        relative_path = self._relative_path(candidate)
        try:
            resolved = candidate.resolve(strict=True)
        except (FileNotFoundError, RuntimeError):
            return None, KarteSourceIssue("broken_symlink", relative_path, "Markdown symlink target is missing")
        if not _is_within(resolved, self.content_root):
            return None, KarteSourceIssue("symlink_escape", relative_path, "Markdown path resolves outside content")
        if not resolved.is_file():
            return None, KarteSourceIssue("not_regular_file", relative_path, "Markdown path is not a regular file")
        try:
            raw = candidate.read_bytes()
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            return None, KarteSourceIssue("invalid_utf8", relative_path, "Markdown must be UTF-8")
        except OSError:
            return None, KarteSourceIssue("read_failed", relative_path, "Markdown file could not be read")
        try:
            frontmatter, body = _parse_frontmatter(text)
            doc_id = _required_string(frontmatter, "doc_id")
            title_value = frontmatter.get("title")
            if title_value is not None and not isinstance(title_value, str):
                raise ValueError("title must be a string")
            title = title_value.strip() if isinstance(title_value, str) else candidate.stem
            tags = _parse_tags(frontmatter.get("tags"))
        except (ValueError, yaml.YAMLError) as exc:
            return None, KarteSourceIssue("invalid_frontmatter", relative_path, str(exc))
        stat = candidate.stat()
        return KarteDocument(
            doc_id=doc_id,
            title=title or candidate.stem,
            tags=tags,
            relative_path=relative_path,
            absolute_path=candidate.absolute(),
            updated_at=datetime.fromtimestamp(stat.st_mtime, tz=UTC).isoformat(),
            sha256=hashlib.sha256(raw).hexdigest(),
            frontmatter=frontmatter,
            body=body,
        ), None

    def _symlink_issue(self, candidate: Path, *, directory: bool) -> KarteSourceIssue:
        relative_path = self._relative_path(candidate)
        try:
            resolved = candidate.resolve(strict=True)
        except (FileNotFoundError, RuntimeError):
            return KarteSourceIssue("broken_symlink", relative_path, "Symlink target is missing")
        if not _is_within(resolved, self.content_root):
            return KarteSourceIssue("symlink_escape", relative_path, "Symlink resolves outside content")
        code = "directory_symlink_unsupported" if directory else "symlink_unsupported"
        return KarteSourceIssue(code, relative_path, "Symlink is not followed during canonical scan")

    def _relative_path(self, candidate: Path) -> str:
        try:
            return candidate.absolute().relative_to(self.data_root).as_posix()
        except ValueError:
            return candidate.name


def _parse_frontmatter(text: str) -> tuple[dict[str, Any], str]:
    lines = text.splitlines(keepends=True)
    if not lines or lines[0].lstrip("\ufeff").strip() != "---":
        raise ValueError("YAML frontmatter is required")
    closing_index = next((index for index, line in enumerate(lines[1:], start=1) if line.strip() == "---"), None)
    if closing_index is None:
        raise ValueError("YAML frontmatter closing delimiter is missing")
    payload = yaml.safe_load("".join(lines[1:closing_index]))
    if not isinstance(payload, dict):
        raise ValueError("YAML frontmatter must be a mapping")
    if any(not isinstance(key, str) for key in payload):
        raise ValueError("YAML frontmatter keys must be strings")
    return payload, "".join(lines[closing_index + 1 :]).lstrip("\r\n")


def _required_string(frontmatter: dict[str, Any], key: str) -> str:
    value = frontmatter.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{key} must be a non-empty string")
    return value.strip()


def _parse_tags(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, str):
        candidates = value.split(",")
    elif isinstance(value, list) and all(isinstance(item, str) for item in value):
        candidates = value
    else:
        raise ValueError("tags must be a comma-separated string or a string list")
    return list(dict.fromkeys(tag.strip() for tag in candidates if tag.strip()))


def _validate_relative_path(value: str) -> str:
    if not value or "\\" in value:
        raise ValueError("relative path must use forward slashes")
    path = PurePosixPath(value)
    if path.is_absolute() or not path.parts or path.parts[0] != "content":
        raise ValueError("relative path must be below content/")
    if any(part in {"", ".", ".."} for part in path.parts) or path.suffix.lower() != ".md":
        raise ValueError("invalid canonical Markdown path")
    return str(path)


def _is_within(candidate: Path, root: Path) -> bool:
    try:
        candidate.relative_to(root)
        return True
    except ValueError:
        return False
