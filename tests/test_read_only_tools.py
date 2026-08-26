from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess

import pytest

from packages.tool_core import (
    ReadOnlyToolExecutor,
    RequestOrigin,
    SourceTrust,
    ToolDecisionType,
    ToolInvocation,
    ToolPermission,
    ToolPolicyContext,
    ToolResultStatus,
)


def _invocation(
    root: Path,
    tool_name: str,
    arguments: dict[str, object],
    *,
    source_trust: SourceTrust = SourceTrust.USER,
) -> ToolInvocation:
    return ToolInvocation(
        invocation_id="invoke_readonly_1234",
        tool_name=tool_name,
        tool_version="1.0.0",
        arguments=arguments,
        workspace_root=str(root),
        requested_by=RequestOrigin.MODEL,
        source_trust=source_trust,
    )


def _context(root: Path, *, grant_read: bool = True) -> ToolPolicyContext:
    permissions = frozenset({ToolPermission.READ_FILES}) if grant_read else frozenset()
    return ToolPolicyContext(
        granted_permissions=permissions,
        allowed_workspace_roots=(str(root),),
    )


def _execute(
    root: Path,
    tool_name: str,
    arguments: dict[str, object],
    *,
    source_trust: SourceTrust = SourceTrust.USER,
):
    return ReadOnlyToolExecutor().execute(
        _invocation(root, tool_name, arguments, source_trust=source_trust),
        _context(root),
    )


def test_read_file_returns_relative_path_and_metadata_only_audit(tmp_path: Path) -> None:
    note = tmp_path / "note.md"
    note.write_text("private source content", encoding="utf-8")

    record = _execute(tmp_path, "files.read", {"path": "note.md"})

    assert record.decision.decision == ToolDecisionType.ALLOW
    assert record.result.status == ToolResultStatus.SUCCEEDED
    assert record.result.output == {"path": "note.md", "content": "private source content"}
    serialized_audit = json.dumps(record.audit_event.model_dump(mode="json"))
    assert "private source content" not in serialized_audit
    assert str(note) not in serialized_audit
    assert len(record.audit_event.target_hashes) == 1


def test_read_file_enforces_output_limit(tmp_path: Path) -> None:
    (tmp_path / "large.txt").write_text("x" * 300_000, encoding="utf-8")

    record = _execute(tmp_path, "files.read", {"path": "large.txt"})

    assert record.result.status == ToolResultStatus.SUCCEEDED
    assert record.result.output_truncated is True
    assert len(str(record.result.output["content"]).encode("utf-8")) == 262_144
    assert record.audit_event.output_truncated is True


def test_read_file_truncates_without_splitting_utf8_character(tmp_path: Path) -> None:
    (tmp_path / "large-ja.txt").write_text("日" * 100_000, encoding="utf-8")

    record = _execute(tmp_path, "files.read", {"path": "large-ja.txt"})

    assert record.result.status == ToolResultStatus.SUCCEEDED
    assert record.result.output_truncated is True
    assert str(record.result.output["content"]).endswith("日")
    assert len(str(record.result.output["content"]).encode("utf-8")) <= 262_144


@pytest.mark.parametrize(
    ("path", "error_code"),
    [
        ("../outside.txt", "path_traversal"),
        (".env", "sensitive_path"),
        ("secret.pem", "sensitive_path"),
        (".git/config", "sensitive_path"),
    ],
)
def test_read_file_rejects_unsafe_paths(tmp_path: Path, path: str, error_code: str) -> None:
    (tmp_path / ".env").write_text("TOKEN=secret", encoding="utf-8")
    (tmp_path / "secret.pem").write_text("private key", encoding="utf-8")
    (tmp_path / ".git").mkdir()
    (tmp_path / ".git" / "config").write_text("credential", encoding="utf-8")

    record = _execute(tmp_path, "files.read", {"path": path})

    assert record.result.status == ToolResultStatus.FAILED
    assert record.result.error_code == error_code


def test_read_file_rejects_absolute_path_outside_workspace(tmp_path: Path) -> None:
    outside = tmp_path.parent / "outside.txt"
    outside.write_text("outside", encoding="utf-8")

    record = _execute(tmp_path, "files.read", {"path": str(outside)})

    assert record.result.status == ToolResultStatus.FAILED
    assert record.result.error_code == "path_outside_workspace"


def test_read_file_rejects_symlink_even_when_target_is_inside_workspace(tmp_path: Path) -> None:
    target = tmp_path / "target.txt"
    target.write_text("target", encoding="utf-8")
    link = tmp_path / "link.txt"
    link.symlink_to(target)

    record = _execute(tmp_path, "files.read", {"path": "link.txt"})

    assert record.result.status == ToolResultStatus.FAILED
    assert record.result.error_code == "symlink_not_allowed"


@pytest.mark.skipif(not hasattr(os, "mkfifo"), reason="FIFO is not supported")
def test_read_file_rejects_special_file(tmp_path: Path) -> None:
    fifo = tmp_path / "events.pipe"
    os.mkfifo(fifo)

    record = _execute(tmp_path, "files.read", {"path": "events.pipe"})

    assert record.result.status == ToolResultStatus.FAILED
    assert record.result.error_code == "not_regular_file"


def test_list_files_skips_sensitive_and_symlink_entries(tmp_path: Path) -> None:
    (tmp_path / "docs").mkdir()
    (tmp_path / "docs" / "readme.md").write_text("safe", encoding="utf-8")
    (tmp_path / ".env").write_text("SECRET=1", encoding="utf-8")
    (tmp_path / "linked.md").symlink_to(tmp_path / "docs" / "readme.md")

    record = _execute(tmp_path, "files.list", {"path": ".", "recursive": True})

    paths = {entry["path"] for entry in record.result.output["entries"]}
    assert record.result.status == ToolResultStatus.SUCCEEDED
    assert "docs" in paths
    assert "docs/readme.md" in paths
    assert ".env" not in paths
    assert "linked.md" not in paths


def test_list_files_caps_entry_count(tmp_path: Path) -> None:
    for index in range(5):
        (tmp_path / f"{index}.txt").write_text(str(index), encoding="utf-8")

    record = _execute(tmp_path, "files.list", {"path": ".", "max_entries": 2})

    assert len(record.result.output["entries"]) == 2
    assert record.result.output_truncated is True


def test_search_files_returns_lines_and_skips_binary_and_sensitive_files(tmp_path: Path) -> None:
    (tmp_path / "notes.md").write_text("first\nChain of Thought\nlast", encoding="utf-8")
    (tmp_path / "binary.bin").write_bytes(b"\xff\xfeChain of Thought")
    (tmp_path / ".env").write_text("Chain of Thought secret", encoding="utf-8")

    record = _execute(tmp_path, "files.search", {"path": ".", "query": "chain of thought"})

    assert record.result.status == ToolResultStatus.SUCCEEDED
    assert record.result.output["matches"] == [
        {"path": "notes.md", "line": 2, "text": "Chain of Thought"}
    ]


def test_search_rejects_unknown_arguments(tmp_path: Path) -> None:
    record = _execute(
        tmp_path,
        "files.search",
        {"path": ".", "query": "needle", "command": "cat /etc/passwd"},
    )

    assert record.result.status == ToolResultStatus.FAILED
    assert record.result.error_code == "invalid_arguments"


def test_permission_and_trust_policy_are_enforced_before_execution(tmp_path: Path) -> None:
    invocation = _invocation(tmp_path, "files.read", {"path": "missing.txt"})
    denied = ReadOnlyToolExecutor().execute(invocation, _context(tmp_path, grant_read=False))
    untrusted = ReadOnlyToolExecutor().execute(
        invocation.model_copy(update={"source_trust": SourceTrust.EXTERNAL_UNTRUSTED}),
        _context(tmp_path),
    )

    assert denied.result.status == ToolResultStatus.DENIED
    assert denied.result.error_code == "permission_not_granted"
    assert untrusted.result.status == ToolResultStatus.DENIED
    assert untrusted.result.error_code == "untrusted_source_cannot_request_tools"


def test_workspace_root_requires_exact_canonical_allowlist_match(tmp_path: Path) -> None:
    other = tmp_path / "other"
    other.mkdir()
    invocation = _invocation(tmp_path, "files.list", {"path": "."})
    context = ToolPolicyContext(
        granted_permissions=frozenset({ToolPermission.READ_FILES}),
        allowed_workspace_roots=(str(other),),
    )

    record = ReadOnlyToolExecutor().execute(invocation, context)

    assert record.result.status == ToolResultStatus.DENIED
    assert record.result.error_code == "workspace_root_not_allowed"


def test_workspace_root_accepts_equivalent_canonical_path(tmp_path: Path) -> None:
    invocation = _invocation(tmp_path, "files.list", {"path": "."}).model_copy(
        update={"workspace_root": str(tmp_path / ".")}
    )
    context = ToolPolicyContext(
        granted_permissions=frozenset({ToolPermission.READ_FILES}),
        allowed_workspace_roots=(str(tmp_path),),
    )

    record = ReadOnlyToolExecutor().execute(invocation, context)

    assert record.result.status == ToolResultStatus.SUCCEEDED


def test_unknown_tool_is_denied(tmp_path: Path) -> None:
    record = _execute(tmp_path, "files.delete", {"path": "note.md"})

    assert record.decision.decision == ToolDecisionType.BLOCK
    assert record.result.status == ToolResultStatus.DENIED
    assert record.result.error_code == "unknown_tool"


@pytest.mark.skipif(shutil.which("git") is None, reason="git is unavailable")
def test_git_tools_use_fixed_read_only_operations(tmp_path: Path) -> None:
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "config", "user.name", "Test User"], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "config", "user.email", "test@example.com"], check=True)
    tracked = tmp_path / "tracked.txt"
    tracked.write_text("before\n", encoding="utf-8")
    subprocess.run(["git", "-C", str(tmp_path), "add", "tracked.txt"], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "commit", "-q", "-m", "initial"], check=True)
    tracked.write_text("after\n", encoding="utf-8")
    (tmp_path / "untracked.txt").write_text("new", encoding="utf-8")

    status = _execute(tmp_path, "git.status", {})
    diff = _execute(tmp_path, "git.diff", {"path": "tracked.txt"})
    log = _execute(tmp_path, "git.log", {"limit": 1})

    assert status.result.status == ToolResultStatus.SUCCEEDED
    assert "tracked.txt" in status.result.output["text"]
    assert "untracked.txt" in status.result.output["text"]
    assert diff.result.status == ToolResultStatus.SUCCEEDED
    assert "-before" in diff.result.output["text"]
    assert "+after" in diff.result.output["text"]
    assert log.result.status == ToolResultStatus.SUCCEEDED
    assert len(log.result.output["commits"]) == 1
    assert log.result.output["commits"][0]["subject"] == "initial"
