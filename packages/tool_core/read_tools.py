from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
from typing import Callable

from pydantic import JsonValue

from .path_guard import PathPolicyError, WorkspacePathGuard
from .policy import invocation_hash, plan_tool_invocation
from .schemas import (
    ApprovalPolicy,
    ToolAuditEvent,
    ToolDecision,
    ToolDecisionType,
    ToolDefinition,
    ToolExecutionRecord,
    ToolExecutionResult,
    ToolInvocation,
    ToolPermission,
    ToolPolicyContext,
    ToolResultStatus,
)


READ_ONLY_TOOL_DEFINITIONS = {
    definition.name: definition
    for definition in (
        ToolDefinition(
            name="files.read",
            version="1.0.0",
            description="Read a UTF-8 text file inside an allowed workspace",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            max_output_bytes=262_144,
        ),
        ToolDefinition(
            name="files.list",
            version="1.0.0",
            description="List files inside an allowed workspace",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            max_output_bytes=262_144,
        ),
        ToolDefinition(
            name="files.search",
            version="1.0.0",
            description="Search UTF-8 text files inside an allowed workspace",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            timeout_seconds=30,
            max_output_bytes=262_144,
        ),
        ToolDefinition(
            name="git.status",
            version="1.0.0",
            description="Show read-only Git working tree status",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            timeout_seconds=15,
        ),
        ToolDefinition(
            name="git.diff",
            version="1.0.0",
            description="Show a read-only Git diff for the working tree or index",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            timeout_seconds=15,
        ),
        ToolDefinition(
            name="git.log",
            version="1.0.0",
            description="Show recent Git commit metadata",
            permissions=frozenset({ToolPermission.READ_FILES}),
            approval_policy=ApprovalPolicy.NEVER,
            timeout_seconds=15,
        ),
    )
}


class ReadOnlyToolError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


@dataclass(frozen=True)
class _OperationResult:
    output: dict[str, JsonValue]
    target_paths: tuple[Path, ...]
    truncated: bool = False


class ReadOnlyToolExecutor:
    def execute(self, invocation: ToolInvocation, context: ToolPolicyContext) -> ToolExecutionRecord:
        started = time.perf_counter()
        definition = READ_ONLY_TOOL_DEFINITIONS.get(invocation.tool_name)
        if definition is None:
            decision = self._blocked_decision(invocation, "unknown_tool")
            return self._record_denied(invocation, decision, started)

        decision = plan_tool_invocation(definition, invocation, context)
        if decision.decision != ToolDecisionType.ALLOW:
            return self._record_denied(invocation, decision, started)

        try:
            guard = WorkspacePathGuard(invocation.workspace_root, context.allowed_workspace_roots)
            operation = self._dispatch(definition, invocation.arguments, guard)
            result = ToolExecutionResult(
                invocation_id=invocation.invocation_id,
                tool_name=invocation.tool_name,
                status=ToolResultStatus.SUCCEEDED,
                output=operation.output,
                output_truncated=operation.truncated,
            )
            audit = self._audit(
                invocation,
                definition,
                decision,
                started,
                ToolResultStatus.SUCCEEDED,
                operation.target_paths,
                operation.truncated,
            )
        except (PathPolicyError, ReadOnlyToolError) as exc:
            result = ToolExecutionResult(
                invocation_id=invocation.invocation_id,
                tool_name=invocation.tool_name,
                status=ToolResultStatus.FAILED,
                error_code=exc.code,
            )
            audit = self._audit(
                invocation,
                definition,
                decision,
                started,
                ToolResultStatus.FAILED,
                (),
                False,
                exc.code,
            )
        return ToolExecutionRecord(decision=decision, result=result, audit_event=audit)

    def _dispatch(
        self,
        definition: ToolDefinition,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
    ) -> _OperationResult:
        handlers: dict[str, Callable[[dict[str, JsonValue], WorkspacePathGuard, ToolDefinition], _OperationResult]] = {
            "files.read": self._read_file,
            "files.list": self._list_files,
            "files.search": self._search_files,
            "git.status": self._git_status,
            "git.diff": self._git_diff,
            "git.log": self._git_log,
        }
        return handlers[definition.name](arguments, guard, definition)

    def _read_file(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, {"path"})
        path = guard.resolve(self._string_argument(arguments, "path"), expected="file")
        descriptor = guard.open_regular_file(path)
        try:
            raw = self._read_descriptor(descriptor, definition.max_output_bytes + 1)
        finally:
            os.close(descriptor)
        truncated = len(raw) > definition.max_output_bytes
        raw = raw[: definition.max_output_bytes]
        try:
            content = raw.decode("utf-8")
        except UnicodeDecodeError as exc:
            if truncated and exc.end == len(raw) and exc.start >= len(raw) - 3:
                content = raw[: exc.start].decode("utf-8")
            else:
                raise ReadOnlyToolError("binary_file_not_supported", "Only UTF-8 text files can be read") from exc
        return _OperationResult(
            output={"path": guard.relative(path), "content": content},
            target_paths=(path,),
            truncated=truncated,
        )

    def _list_files(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, {"path", "recursive", "max_entries"})
        path = guard.resolve(self._optional_string(arguments, "path", "."), expected="directory")
        recursive = self._bool_argument(arguments, "recursive", False)
        max_entries = self._int_argument(arguments, "max_entries", 200, minimum=1, maximum=2_000)
        entries: list[dict[str, JsonValue]] = []
        candidates = path.rglob("*") if recursive else path.iterdir()
        truncated = False
        for candidate in candidates:
            if len(entries) >= max_entries:
                truncated = True
                break
            if not guard.is_safe_entry(candidate):
                continue
            resolved = candidate.resolve(strict=True)
            stat_result = resolved.stat()
            entry: dict[str, JsonValue] = {
                "path": guard.relative(resolved),
                "type": "directory" if resolved.is_dir() else "file",
            }
            if resolved.is_file():
                entry["size_bytes"] = stat_result.st_size
            entries.append(entry)
            if self._encoded_size({"entries": entries}) > definition.max_output_bytes:
                entries.pop()
                truncated = True
                break
        entries.sort(key=lambda item: str(item["path"]))
        return _OperationResult(
            output={"path": guard.relative(path), "entries": entries},
            target_paths=(path,),
            truncated=truncated,
        )

    def _search_files(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, {"path", "query", "case_sensitive", "max_results"})
        path = guard.resolve(self._optional_string(arguments, "path", "."), expected="any")
        query = self._string_argument(arguments, "query")
        if len(query) > 512:
            raise ReadOnlyToolError("invalid_arguments", "Search query is too long")
        case_sensitive = self._bool_argument(arguments, "case_sensitive", False)
        max_results = self._int_argument(arguments, "max_results", 100, minimum=1, maximum=1_000)
        needle = query if case_sensitive else query.casefold()
        matches: list[dict[str, JsonValue]] = []
        truncated = False
        deadline = time.monotonic() + definition.timeout_seconds
        candidates = (path,) if path.is_file() else path.rglob("*")
        for candidate in candidates:
            if time.monotonic() > deadline:
                truncated = True
                break
            if len(matches) >= max_results:
                truncated = True
                break
            if not guard.is_safe_entry(candidate):
                continue
            resolved = candidate.resolve(strict=True)
            if not resolved.is_file() or resolved.stat().st_size > 2_097_152:
                continue
            descriptor = guard.open_regular_file(resolved)
            try:
                raw = self._read_descriptor(descriptor, 2_097_153)
            finally:
                os.close(descriptor)
            if len(raw) > 2_097_152:
                continue
            try:
                content = raw.decode("utf-8")
            except UnicodeDecodeError:
                continue
            for line_number, line in enumerate(content.splitlines(), start=1):
                haystack = line if case_sensitive else line.casefold()
                if needle not in haystack:
                    continue
                matches.append(
                    {
                        "path": guard.relative(resolved),
                        "line": line_number,
                        "text": line[:2_000],
                    }
                )
                if len(matches) >= max_results or self._encoded_size({"matches": matches}) > definition.max_output_bytes:
                    if self._encoded_size({"matches": matches}) > definition.max_output_bytes:
                        matches.pop()
                    truncated = True
                    break
            if truncated:
                break
        return _OperationResult(
            output={"query": query, "matches": matches},
            target_paths=(path,),
            truncated=truncated,
        )

    def _git_status(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, set())
        output, truncated = self._run_git(
            guard,
            definition,
            ["status", "--short", "--branch", "--untracked-files=normal"],
        )
        return _OperationResult(output={"text": output}, target_paths=(guard.root,), truncated=truncated)

    def _git_diff(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, {"path", "staged"})
        command = ["diff", "--no-ext-diff", "--no-textconv"]
        if self._bool_argument(arguments, "staged", False):
            command.append("--cached")
        targets = [guard.root]
        path_argument = arguments.get("path")
        if path_argument is not None:
            path = guard.resolve(
                self._string_argument(arguments, "path"),
                expected="any",
                allow_missing=True,
            )
            command.extend(["--", guard.relative(path)])
            targets.append(path)
        output, truncated = self._run_git(guard, definition, command)
        return _OperationResult(output={"text": output}, target_paths=tuple(targets), truncated=truncated)

    def _git_log(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> _OperationResult:
        self._only_arguments(arguments, {"limit"})
        limit = self._int_argument(arguments, "limit", 20, minimum=1, maximum=100)
        output, truncated = self._run_git(
            guard,
            definition,
            ["log", f"--max-count={limit}", "--format=%H%x09%aI%x09%an%x09%s"],
        )
        commits = []
        for line in output.splitlines():
            parts = line.split("\t", 3)
            if len(parts) == 4:
                commits.append({"sha": parts[0], "authored_at": parts[1], "author": parts[2], "subject": parts[3]})
        return _OperationResult(output={"commits": commits}, target_paths=(guard.root,), truncated=truncated)

    def _run_git(
        self,
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
        arguments: list[str],
    ) -> tuple[str, bool]:
        git_marker = guard.root / ".git"
        if not git_marker.is_dir() or git_marker.is_symlink():
            raise ReadOnlyToolError("not_git_repository", "Workspace is not a supported Git repository")
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "LANG": "C.UTF-8",
            "LC_ALL": "C.UTF-8",
            "HOME": "/nonexistent",
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_OPTIONAL_LOCKS": "0",
            "GIT_PAGER": "cat",
            "GIT_TERMINAL_PROMPT": "0",
        }
        command = [
            "git",
            "-c",
            "core.fsmonitor=false",
            "-c",
            "core.pager=cat",
            "-C",
            str(guard.root),
            *arguments,
        ]
        try:
            completed = subprocess.run(
                command,
                cwd=guard.root,
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=definition.timeout_seconds,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            code = "git_timeout" if isinstance(exc, subprocess.TimeoutExpired) else "git_unavailable"
            raise ReadOnlyToolError(code, "Git operation failed") from exc
        if completed.returncode != 0:
            raise ReadOnlyToolError("git_failed", "Git operation failed")
        truncated = len(completed.stdout) > definition.max_output_bytes
        raw = completed.stdout[: definition.max_output_bytes]
        return raw.decode("utf-8", errors="replace"), truncated

    @staticmethod
    def _only_arguments(arguments: dict[str, JsonValue], allowed: set[str]) -> None:
        if set(arguments) - allowed:
            raise ReadOnlyToolError("invalid_arguments", "Unsupported arguments were provided")

    @staticmethod
    def _string_argument(arguments: dict[str, JsonValue], name: str) -> str:
        value = arguments.get(name)
        if not isinstance(value, str) or not value:
            raise ReadOnlyToolError("invalid_arguments", f"{name} must be a non-empty string")
        return value

    @staticmethod
    def _optional_string(arguments: dict[str, JsonValue], name: str, default: str) -> str:
        value = arguments.get(name, default)
        if not isinstance(value, str) or not value:
            raise ReadOnlyToolError("invalid_arguments", f"{name} must be a non-empty string")
        return value

    @staticmethod
    def _bool_argument(arguments: dict[str, JsonValue], name: str, default: bool) -> bool:
        value = arguments.get(name, default)
        if not isinstance(value, bool):
            raise ReadOnlyToolError("invalid_arguments", f"{name} must be a boolean")
        return value

    @staticmethod
    def _int_argument(
        arguments: dict[str, JsonValue],
        name: str,
        default: int,
        *,
        minimum: int,
        maximum: int,
    ) -> int:
        value = arguments.get(name, default)
        if not isinstance(value, int) or isinstance(value, bool) or not minimum <= value <= maximum:
            raise ReadOnlyToolError("invalid_arguments", f"{name} is outside the allowed range")
        return value

    @staticmethod
    def _encoded_size(value: object) -> int:
        return len(json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))

    @staticmethod
    def _read_descriptor(descriptor: int, limit: int) -> bytes:
        chunks: list[bytes] = []
        remaining = limit
        while remaining > 0:
            chunk = os.read(descriptor, min(65_536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    @staticmethod
    def _target_hash(path: Path) -> str:
        return hashlib.sha256(str(path).encode("utf-8")).hexdigest()

    def _blocked_decision(self, invocation: ToolInvocation, reason_code: str) -> ToolDecision:
        return ToolDecision(
            decision=ToolDecisionType.BLOCK,
            reason_code=reason_code,
            invocation_hash=invocation_hash(invocation),
            required_permissions=frozenset(),
        )

    def _record_denied(
        self,
        invocation: ToolInvocation,
        decision: ToolDecision,
        started: float,
    ) -> ToolExecutionRecord:
        result = ToolExecutionResult(
            invocation_id=invocation.invocation_id,
            tool_name=invocation.tool_name,
            status=ToolResultStatus.DENIED,
            error_code=decision.reason_code,
        )
        definition = READ_ONLY_TOOL_DEFINITIONS.get(invocation.tool_name)
        permissions = definition.permissions if definition else frozenset()
        audit = ToolAuditEvent(
            timestamp=datetime.now(timezone.utc),
            invocation_hash=decision.invocation_hash,
            tool_name=invocation.tool_name,
            permissions=permissions,
            decision=decision.decision,
            result_status=ToolResultStatus.DENIED,
            duration_ms=(time.perf_counter() - started) * 1_000,
            error_code=decision.reason_code,
        )
        return ToolExecutionRecord(decision=decision, result=result, audit_event=audit)

    def _audit(
        self,
        invocation: ToolInvocation,
        definition: ToolDefinition,
        decision: ToolDecision,
        started: float,
        status: ToolResultStatus,
        targets: tuple[Path, ...],
        truncated: bool,
        error_code: str | None = None,
    ) -> ToolAuditEvent:
        return ToolAuditEvent(
            timestamp=datetime.now(timezone.utc),
            invocation_hash=decision.invocation_hash,
            tool_name=invocation.tool_name,
            permissions=definition.permissions,
            decision=decision.decision,
            result_status=status,
            target_hashes=tuple(self._target_hash(path) for path in targets),
            duration_ms=(time.perf_counter() - started) * 1_000,
            output_truncated=truncated,
            error_code=error_code,
        )
