from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path

import pytest

from packages.tool_core import (
    ApprovalGrant,
    InMemoryApprovalStore,
    MutationToolExecutor,
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
        invocation_id="invoke_mutation_1234",
        tool_name=tool_name,
        tool_version="1.0.0",
        arguments=arguments,
        workspace_root=str(root),
        requested_by=RequestOrigin.MODEL,
        source_trust=source_trust,
    )


def _context(root: Path, permission: ToolPermission) -> ToolPolicyContext:
    return ToolPolicyContext(
        granted_permissions=frozenset({permission}),
        allowed_workspace_roots=(str(root),),
    )


def _approve(executor: MutationToolExecutor, plan) -> None:
    executor.approval_store.grant(
        ApprovalGrant(
            invocation_hash=plan.decision.invocation_hash,
            expires_at=datetime.now(timezone.utc) + timedelta(minutes=5),
        )
    )


def test_write_plan_prepares_exact_hash_and_preview(tmp_path: Path) -> None:
    target = tmp_path / "note.md"
    target.write_text("before\n", encoding="utf-8")
    invocation = _invocation(tmp_path, "files.write", {"path": "note.md", "content": "after\n"})
    executor = MutationToolExecutor()

    plan = executor.plan(invocation, _context(tmp_path, ToolPermission.WRITE_FILES))

    assert plan.decision.decision == ToolDecisionType.CONFIRM
    assert plan.invocation.arguments["expected_sha256"] == hashlib.sha256(b"before\n").hexdigest()
    assert plan.preview["before_sha256"] == plan.invocation.arguments["expected_sha256"]
    assert "-before" in plan.preview["diff"]
    assert "+after" in plan.preview["diff"]


def test_write_requires_approval_then_atomically_replaces_file(tmp_path: Path) -> None:
    target = tmp_path / "note.md"
    target.write_text("before", encoding="utf-8")
    target.chmod(0o640)
    executor = MutationToolExecutor()
    plan = executor.plan(
        _invocation(tmp_path, "files.write", {"path": "note.md", "content": "after"}),
        _context(tmp_path, ToolPermission.WRITE_FILES),
    )

    denied = executor.execute(plan.invocation, _context(tmp_path, ToolPermission.WRITE_FILES))
    assert denied.result.status == ToolResultStatus.DENIED
    assert denied.result.error_code == "approval_required"
    assert target.read_text(encoding="utf-8") == "before"

    _approve(executor, plan)
    result = executor.execute(plan.invocation, _context(tmp_path, ToolPermission.WRITE_FILES))

    assert result.result.status == ToolResultStatus.SUCCEEDED
    assert target.read_text(encoding="utf-8") == "after"
    assert target.stat().st_mode & 0o777 == 0o640
    assert result.result.output["before_sha256"] == hashlib.sha256(b"before").hexdigest()
    assert result.audit_event.target_hashes
    serialized = json.dumps(result.audit_event.model_dump(mode="json"))
    assert "before" not in serialized
    assert "after" not in serialized
    assert str(target) not in serialized


def test_write_can_create_new_file_only_when_target_stays_missing(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    plan = executor.plan(
        _invocation(tmp_path, "files.write", {"path": "new.txt", "content": "new"}),
        _context(tmp_path, ToolPermission.WRITE_FILES),
    )
    assert plan.invocation.arguments["expected_sha256"] is None
    _approve(executor, plan)

    result = executor.execute(plan.invocation, _context(tmp_path, ToolPermission.WRITE_FILES))

    assert result.result.status == ToolResultStatus.SUCCEEDED
    assert result.result.output["created"] is True
    assert (tmp_path / "new.txt").read_text(encoding="utf-8") == "new"


def test_write_detects_change_after_approval_and_consumes_grant(tmp_path: Path) -> None:
    target = tmp_path / "note.md"
    target.write_text("before", encoding="utf-8")
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.WRITE_FILES)
    plan = executor.plan(
        _invocation(tmp_path, "files.write", {"path": "note.md", "content": "approved"}),
        context,
    )
    _approve(executor, plan)
    target.write_text("changed elsewhere", encoding="utf-8")

    conflict = executor.execute(plan.invocation, context)
    reused = executor.execute(plan.invocation, context)

    assert conflict.result.status == ToolResultStatus.FAILED
    assert conflict.result.error_code == "write_conflict"
    assert target.read_text(encoding="utf-8") == "changed elsewhere"
    assert reused.result.status == ToolResultStatus.DENIED
    assert reused.result.error_code == "approval_consumed"


def test_only_one_concurrent_execution_can_consume_approval(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.WRITE_FILES)
    plan = executor.plan(
        _invocation(tmp_path, "files.write", {"path": "race.txt", "content": "winner"}),
        context,
    )
    _approve(executor, plan)

    with ThreadPoolExecutor(max_workers=2) as pool:
        records = list(pool.map(lambda _: executor.execute(plan.invocation, context), range(2)))

    statuses = [record.result.status for record in records]
    assert statuses.count(ToolResultStatus.SUCCEEDED) == 1
    assert statuses.count(ToolResultStatus.DENIED) == 1


@pytest.mark.parametrize("path", ["../outside.txt", ".env", ".ssh/id_ed25519", "private.key"])
def test_write_plan_blocks_unsafe_paths(tmp_path: Path, path: str) -> None:
    executor = MutationToolExecutor()

    plan = executor.plan(
        _invocation(tmp_path, "files.write", {"path": path, "content": "secret"}),
        _context(tmp_path, ToolPermission.WRITE_FILES),
    )

    assert plan.decision.decision == ToolDecisionType.BLOCK
    assert plan.error_code in {"path_traversal", "sensitive_path"}


def test_write_plan_blocks_stale_expected_hash(tmp_path: Path) -> None:
    (tmp_path / "note.md").write_text("current", encoding="utf-8")
    executor = MutationToolExecutor()

    plan = executor.plan(
        _invocation(
            tmp_path,
            "files.write",
            {"path": "note.md", "content": "next", "expected_sha256": "0" * 64},
        ),
        _context(tmp_path, ToolPermission.WRITE_FILES),
    )

    assert plan.decision.decision == ToolDecisionType.BLOCK
    assert plan.error_code == "write_conflict"


def test_untrusted_source_cannot_plan_mutation(tmp_path: Path) -> None:
    invocation = _invocation(
        tmp_path,
        "files.write",
        {"path": "note.md", "content": "unsafe"},
        source_trust=SourceTrust.EXTERNAL_UNTRUSTED,
    )

    plan = MutationToolExecutor().plan(invocation, _context(tmp_path, ToolPermission.WRITE_FILES))

    assert plan.decision.decision == ToolDecisionType.BLOCK
    assert plan.error_code == "untrusted_source_cannot_request_tools"


def test_changed_process_arguments_do_not_match_approval(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)
    plan = executor.plan(
        _invocation(tmp_path, "process.run", {"argv": ["/bin/echo", "approved"]}),
        context,
    )
    _approve(executor, plan)
    changed = plan.invocation.model_copy(update={"arguments": {"argv": ["/bin/echo", "changed"]}})

    result = executor.execute(changed, context)

    assert result.result.status == ToolResultStatus.DENIED
    assert result.result.error_code == "approval_required"


def test_process_plan_rejects_shell_string_and_secret_environment(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)

    shell_string = executor.plan(
        _invocation(tmp_path, "process.run", {"argv": "echo unsafe"}),
        context,
    )
    secret_env = executor.plan(
        _invocation(
            tmp_path,
            "process.run",
            {"argv": ["/bin/echo", "safe"], "env": {"AWS_SECRET_ACCESS_KEY": "secret"}},
        ),
        context,
    )

    assert shell_string.error_code == "invalid_arguments"
    assert secret_env.error_code == "environment_not_allowed"


def test_process_sandbox_profile_denies_network_gui_control_and_signals(tmp_path: Path) -> None:
    profile = MutationToolExecutor._sandbox_profile(tmp_path)

    assert "(deny network*)" in profile
    assert "(deny appleevent-send)" in profile
    assert "(deny signal)" in profile


MACOS_SANDBOX = Path("/usr/bin/sandbox-exec").is_file()


@pytest.mark.skipif(not MACOS_SANDBOX, reason="macOS sandbox-exec is unavailable")
def test_process_runs_in_workspace_with_bounded_environment(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)
    plan = executor.plan(
        _invocation(
            tmp_path,
            "process.run",
            {"argv": ["/bin/echo", "hello"], "env": {"NO_COLOR": "1"}},
        ),
        context,
    )
    _approve(executor, plan)

    result = executor.execute(plan.invocation, context)

    assert result.result.status == ToolResultStatus.SUCCEEDED
    assert result.result.output["return_code"] == 0
    assert result.result.output["stdout"] == "hello\n"


@pytest.mark.skipif(not MACOS_SANDBOX, reason="macOS sandbox-exec is unavailable")
def test_process_can_write_inside_workspace_without_inheriting_secrets(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "must-not-leak")
    target = tmp_path / "generated.txt"
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)
    plan = executor.plan(
        _invocation(
            tmp_path,
            "process.run",
            {"argv": ["/bin/sh", "-c", "printf generated > generated.txt; /usr/bin/env"]},
        ),
        context,
    )
    _approve(executor, plan)

    result = executor.execute(plan.invocation, context)

    assert result.result.output["return_code"] == 0
    assert target.read_text(encoding="utf-8") == "generated"
    assert "AWS_SECRET_ACCESS_KEY" not in result.result.output["stdout"]
    assert "must-not-leak" not in result.result.output["stdout"]


@pytest.mark.skipif(not MACOS_SANDBOX, reason="macOS sandbox-exec is unavailable")
def test_process_sandbox_blocks_read_and_write_outside_workspace(tmp_path: Path) -> None:
    outside = tmp_path.parent / f"outside-{os.getpid()}.txt"
    outside.write_text("outside secret", encoding="utf-8")
    write_target = tmp_path.parent / f"blocked-{os.getpid()}.txt"
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)

    read_plan = executor.plan(
        _invocation(tmp_path, "process.run", {"argv": ["/bin/cat", str(outside)]}),
        context,
    )
    _approve(executor, read_plan)
    read_result = executor.execute(read_plan.invocation, context)

    write_plan = executor.plan(
        _invocation(
            tmp_path,
            "process.run",
            {"argv": ["/bin/sh", "-c", f"printf bad > {write_target}"]},
        ),
        context,
    )
    _approve(executor, write_plan)
    write_result = executor.execute(write_plan.invocation, context)

    assert read_result.result.output["return_code"] != 0
    assert "outside secret" not in read_result.result.output["stdout"]
    assert write_result.result.output["return_code"] != 0
    assert not write_target.exists()


@pytest.mark.skipif(not MACOS_SANDBOX, reason="macOS sandbox-exec is unavailable")
def test_process_timeout_terminates_process_group(tmp_path: Path) -> None:
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)
    plan = executor.plan(
        _invocation(
            tmp_path,
            "process.run",
            {"argv": ["/bin/sleep", "5"], "timeout_seconds": 1},
        ),
        context,
    )
    _approve(executor, plan)

    started = datetime.now(timezone.utc)
    result = executor.execute(plan.invocation, context)

    assert result.result.status == ToolResultStatus.FAILED
    assert result.result.error_code == "process_timeout"
    assert (datetime.now(timezone.utc) - started).total_seconds() < 4


@pytest.mark.skipif(not MACOS_SANDBOX, reason="macOS sandbox-exec is unavailable")
def test_process_output_is_capped_and_audit_excludes_output(tmp_path: Path) -> None:
    large = tmp_path / "large.txt"
    large.write_text("x" * 300_000, encoding="utf-8")
    executor = MutationToolExecutor()
    context = _context(tmp_path, ToolPermission.EXECUTE_PROCESS)
    plan = executor.plan(
        _invocation(tmp_path, "process.run", {"argv": ["/bin/cat", str(large)]}),
        context,
    )
    _approve(executor, plan)

    result = executor.execute(plan.invocation, context)

    assert result.result.status == ToolResultStatus.SUCCEEDED
    assert result.result.output_truncated is True
    total = len(result.result.output["stdout"].encode()) + len(result.result.output["stderr"].encode())
    assert total <= 262_144
    assert "x" * 100 not in json.dumps(result.audit_event.model_dump(mode="json"))
