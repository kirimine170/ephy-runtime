from __future__ import annotations

from datetime import datetime, timezone
import difflib
import hashlib
import os
from pathlib import Path
import signal
import stat
import subprocess
import tempfile
from threading import Lock, Thread
import time

from pydantic import JsonValue

from .approval import InMemoryApprovalStore
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
    ToolMutationPlan,
    ToolPermission,
    ToolPolicyContext,
    ToolResultStatus,
)


MAX_WRITE_BYTES = 1_048_576
ALLOWED_ENV_KEYS = frozenset({"LANG", "LC_ALL", "NO_COLOR", "PYTHONUTF8", "TZ"})

MUTATION_TOOL_DEFINITIONS = {
    definition.name: definition
    for definition in (
        ToolDefinition(
            name="files.write",
            version="1.0.0",
            description="Atomically create or replace a UTF-8 file inside an allowed workspace",
            permissions=frozenset({ToolPermission.WRITE_FILES}),
            approval_policy=ApprovalPolicy.ALWAYS,
            max_output_bytes=65_536,
        ),
        ToolDefinition(
            name="process.run",
            version="1.0.0",
            description="Run an approved argv command inside the workspace sandbox",
            permissions=frozenset({ToolPermission.EXECUTE_PROCESS}),
            approval_policy=ApprovalPolicy.ALWAYS,
            timeout_seconds=120,
            max_output_bytes=262_144,
        ),
    )
}


class MutationToolError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


class MutationToolExecutor:
    def __init__(self, approval_store: InMemoryApprovalStore | None = None) -> None:
        self.approval_store = approval_store or InMemoryApprovalStore()
        self._write_lock = Lock()

    def plan(self, invocation: ToolInvocation, context: ToolPolicyContext) -> ToolMutationPlan:
        definition = MUTATION_TOOL_DEFINITIONS.get(invocation.tool_name)
        if definition is None:
            decision = self._blocked_decision(invocation, "unknown_tool")
            return ToolMutationPlan(invocation=invocation, decision=decision, error_code="unknown_tool")
        preliminary = plan_tool_invocation(definition, invocation, context)
        if preliminary.decision == ToolDecisionType.BLOCK:
            return ToolMutationPlan(invocation=invocation, decision=preliminary, error_code=preliminary.reason_code)
        try:
            guard = WorkspacePathGuard(invocation.workspace_root, context.allowed_workspace_roots)
            if invocation.tool_name == "files.write":
                prepared, preview = self._prepare_write(invocation, guard)
            else:
                prepared, preview = self._prepare_process(invocation, guard, definition)
            decision = plan_tool_invocation(definition, prepared, context)
            return ToolMutationPlan(invocation=prepared, decision=decision, preview=preview)
        except (PathPolicyError, MutationToolError) as exc:
            decision = self._blocked_decision(invocation, exc.code, definition.permissions)
            return ToolMutationPlan(invocation=invocation, decision=decision, error_code=exc.code)

    def execute(self, invocation: ToolInvocation, context: ToolPolicyContext) -> ToolExecutionRecord:
        started = time.perf_counter()
        definition = MUTATION_TOOL_DEFINITIONS.get(invocation.tool_name)
        if definition is None:
            decision = self._blocked_decision(invocation, "unknown_tool")
            return self._denied(invocation, decision, started, frozenset())
        decision = self.approval_store.authorize_and_consume(definition, invocation, context)
        if decision.decision != ToolDecisionType.ALLOW:
            return self._denied(invocation, decision, started, definition.permissions)
        try:
            guard = WorkspacePathGuard(invocation.workspace_root, context.allowed_workspace_roots)
            if invocation.tool_name == "files.write":
                output, targets, truncated = self._execute_write(invocation.arguments, guard)
            else:
                output, targets, truncated = self._execute_process(invocation.arguments, guard, definition)
            result = ToolExecutionResult(
                invocation_id=invocation.invocation_id,
                tool_name=invocation.tool_name,
                status=ToolResultStatus.SUCCEEDED,
                output=output,
                output_truncated=truncated,
            )
            audit = self._audit(
                invocation,
                definition,
                decision,
                started,
                ToolResultStatus.SUCCEEDED,
                targets,
                truncated,
            )
        except (PathPolicyError, MutationToolError) as exc:
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

    def _prepare_write(
        self,
        invocation: ToolInvocation,
        guard: WorkspacePathGuard,
    ) -> tuple[ToolInvocation, dict[str, JsonValue]]:
        arguments = invocation.arguments
        self._only_arguments(arguments, {"path", "content", "expected_sha256"})
        raw_path = self._string_argument(arguments, "path")
        content = self._string_argument(arguments, "content", allow_empty=True)
        encoded = content.encode("utf-8")
        if len(encoded) > MAX_WRITE_BYTES:
            raise MutationToolError("write_too_large", "Write exceeds the byte limit")
        path = guard.resolve(raw_path, expected="any", allow_missing=True)
        parent = guard.resolve(str(path.parent), expected="directory")
        current_content, current_hash = self._existing_text_and_hash(path, guard)
        expected = arguments.get("expected_sha256", current_hash)
        if expected is not None and (not isinstance(expected, str) or len(expected) != 64):
            raise MutationToolError("invalid_arguments", "expected_sha256 is invalid")
        if expected != current_hash:
            raise MutationToolError("write_conflict", "Expected file hash does not match current content")
        prepared_arguments = dict(arguments)
        prepared_arguments["expected_sha256"] = expected
        prepared = invocation.model_copy(update={"arguments": prepared_arguments})
        after_hash = hashlib.sha256(encoded).hexdigest()
        before_lines = current_content.splitlines(keepends=True) if current_content is not None else []
        after_lines = content.splitlines(keepends=True)
        diff = "".join(
            difflib.unified_diff(
                before_lines,
                after_lines,
                fromfile=guard.relative(path) if current_content is not None else "/dev/null",
                tofile=guard.relative(path),
                n=3,
            )
        )
        if len(diff.encode("utf-8")) > 65_536:
            diff = diff.encode("utf-8")[:65_536].decode("utf-8", errors="ignore")
        return prepared, {
            "path": guard.relative(path),
            "parent": guard.relative(parent),
            "exists": current_hash is not None,
            "before_sha256": current_hash,
            "after_sha256": after_hash,
            "bytes": len(encoded),
            "diff": diff,
        }

    def _prepare_process(
        self,
        invocation: ToolInvocation,
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> tuple[ToolInvocation, dict[str, JsonValue]]:
        arguments = invocation.arguments
        self._validate_process_arguments(arguments, guard, definition)
        argv = arguments["argv"]
        cwd = guard.resolve(self._optional_string(arguments, "cwd", "."), expected="directory")
        env = self._environment_argument(arguments)
        timeout = self._timeout_argument(arguments, definition)
        return invocation, {
            "argv": argv,
            "cwd": guard.relative(cwd),
            "environment_keys": sorted(env),
            "timeout_seconds": timeout,
            "network": "denied",
            "filesystem": "workspace write; workspace and system runtime read",
        }

    def _execute_write(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
    ) -> tuple[dict[str, JsonValue], tuple[Path, ...], bool]:
        self._only_arguments(arguments, {"path", "content", "expected_sha256"})
        if "expected_sha256" not in arguments:
            raise MutationToolError("expected_sha_required", "Prepared invocation is required")
        content = self._string_argument(arguments, "content", allow_empty=True)
        encoded = content.encode("utf-8")
        if len(encoded) > MAX_WRITE_BYTES:
            raise MutationToolError("write_too_large", "Write exceeds the byte limit")
        expected = arguments["expected_sha256"]
        if expected is not None and (not isinstance(expected, str) or len(expected) != 64):
            raise MutationToolError("invalid_arguments", "expected_sha256 is invalid")

        with self._write_lock:
            path = guard.resolve(self._string_argument(arguments, "path"), expected="any", allow_missing=True)
            parent = guard.resolve(str(path.parent), expected="directory")
            _, current_hash = self._existing_text_and_hash(path, guard)
            if current_hash != expected:
                raise MutationToolError("write_conflict", "File changed after approval preview")
            existing_mode = stat.S_IMODE(path.stat().st_mode) if path.exists() else 0o600
            descriptor, temporary_name = tempfile.mkstemp(prefix=".ephy-write-", dir=parent)
            temporary = Path(temporary_name)
            try:
                os.fchmod(descriptor, existing_mode)
                self._write_all(descriptor, encoded)
                os.fsync(descriptor)
                os.close(descriptor)
                descriptor = -1
                _, latest_hash = self._existing_text_and_hash(path, guard)
                if latest_hash != expected:
                    raise MutationToolError("write_conflict", "File changed during write")
                os.replace(temporary, path)
                directory_fd = os.open(parent, os.O_RDONLY)
                try:
                    os.fsync(directory_fd)
                finally:
                    os.close(directory_fd)
            finally:
                if descriptor >= 0:
                    os.close(descriptor)
                if temporary.exists():
                    temporary.unlink()
            after_hash = hashlib.sha256(encoded).hexdigest()
            return {
                "path": guard.relative(path),
                "created": current_hash is None,
                "before_sha256": current_hash,
                "after_sha256": after_hash,
                "bytes_written": len(encoded),
            }, (path,), False

    def _execute_process(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> tuple[dict[str, JsonValue], tuple[Path, ...], bool]:
        self._validate_process_arguments(arguments, guard, definition)
        argv = [str(value) for value in arguments["argv"]]
        executable = Path(argv[0]).expanduser()
        if not executable.is_absolute() or not executable.is_file():
            raise MutationToolError("executable_not_allowed", "Executable must be an existing absolute path")
        cwd = guard.resolve(self._optional_string(arguments, "cwd", "."), expected="directory")
        timeout = self._timeout_argument(arguments, definition)
        sandbox = Path("/usr/bin/sandbox-exec")
        if not sandbox.is_file():
            raise MutationToolError("sandbox_unavailable", "A supported process sandbox is unavailable")
        profile = self._sandbox_profile(guard.root)
        command = [str(sandbox), "-p", profile, *argv]
        with tempfile.TemporaryDirectory(prefix=".ephy-process-", dir=guard.root) as temporary_root:
            env = {
                "PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
                "HOME": str(guard.root),
                "TMPDIR": temporary_root,
                **self._environment_argument(arguments),
            }
            stdout, stderr, return_code, truncated, timed_out = self._run_bounded_process(
                command,
                cwd,
                env,
                timeout,
                definition.max_output_bytes,
            )
        if timed_out:
            raise MutationToolError("process_timeout", "Process exceeded its approved timeout")
        return {
            "return_code": return_code,
            "stdout": stdout,
            "stderr": stderr,
        }, (guard.root, cwd), truncated

    def _existing_text_and_hash(
        self,
        path: Path,
        guard: WorkspacePathGuard,
    ) -> tuple[str | None, str | None]:
        if not path.exists():
            return None, None
        path = guard.resolve(str(path), expected="file")
        if path.stat().st_size > MAX_WRITE_BYTES:
            raise MutationToolError("write_target_too_large", "Existing file exceeds the byte limit")
        descriptor = guard.open_regular_file(path)
        try:
            raw = self._read_all(descriptor, MAX_WRITE_BYTES + 1)
        finally:
            os.close(descriptor)
        try:
            content = raw.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise MutationToolError("binary_file_not_supported", "Only UTF-8 files can be replaced") from exc
        return content, hashlib.sha256(raw).hexdigest()

    def _validate_process_arguments(
        self,
        arguments: dict[str, JsonValue],
        guard: WorkspacePathGuard,
        definition: ToolDefinition,
    ) -> None:
        self._only_arguments(arguments, {"argv", "cwd", "env", "timeout_seconds"})
        argv = arguments.get("argv")
        if not isinstance(argv, list) or not 1 <= len(argv) <= 128:
            raise MutationToolError("invalid_arguments", "argv must be a non-empty list")
        if any(not isinstance(item, str) or not item or len(item) > 8_192 for item in argv):
            raise MutationToolError("invalid_arguments", "argv contains an invalid value")
        if sum(len(item.encode("utf-8")) for item in argv) > 65_536:
            raise MutationToolError("invalid_arguments", "argv exceeds the byte limit")
        executable = Path(argv[0]).expanduser()
        if not executable.is_absolute() or not executable.is_file() or executable.is_symlink():
            raise MutationToolError("executable_not_allowed", "Executable must be an existing absolute non-symlink file")
        guard.resolve(self._optional_string(arguments, "cwd", "."), expected="directory")
        self._environment_argument(arguments)
        self._timeout_argument(arguments, definition)

    @staticmethod
    def _sandbox_profile(root: Path) -> str:
        escaped_root = str(root).replace("\\", "\\\\").replace('"', '\\"')
        return (
            '(version 1) '
            '(allow default) '
            '(deny network*) '
            '(deny appleevent-send) '
            '(deny signal) '
            '(deny file-read* (subpath "/Users") (subpath "/Volumes") (subpath "/private/var/folders")) '
            f'(allow file-read* (subpath "{escaped_root}")) '
            '(deny file-write*) '
            f'(allow file-write* (subpath "{escaped_root}"))'
        )

    @staticmethod
    def _run_bounded_process(
        command: list[str],
        cwd: Path,
        env: dict[str, str],
        timeout: int,
        max_output_bytes: int,
    ) -> tuple[str, str, int, bool, bool]:
        try:
            process = subprocess.Popen(
                command,
                cwd=cwd,
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                start_new_session=True,
            )
        except OSError as exc:
            raise MutationToolError("process_start_failed", "Process could not be started") from exc
        lock = Lock()
        remaining = [max_output_bytes]
        truncated = [False]
        buffers: dict[str, list[bytes]] = {"stdout": [], "stderr": []}

        def drain(name: str, stream) -> None:
            while True:
                chunk = stream.read(65_536)
                if not chunk:
                    break
                with lock:
                    accepted = min(len(chunk), remaining[0])
                    if accepted:
                        buffers[name].append(chunk[:accepted])
                        remaining[0] -= accepted
                    if accepted < len(chunk):
                        truncated[0] = True

        threads = [
            Thread(target=drain, args=("stdout", process.stdout), daemon=True),
            Thread(target=drain, args=("stderr", process.stderr), daemon=True),
        ]
        for thread in threads:
            thread.start()
        timed_out = False
        try:
            return_code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=2)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
            return_code = process.returncode
        for thread in threads:
            thread.join(timeout=2)
        stdout = b"".join(buffers["stdout"]).decode("utf-8", errors="replace")
        stderr = b"".join(buffers["stderr"]).decode("utf-8", errors="replace")
        return stdout, stderr, return_code, truncated[0], timed_out

    @staticmethod
    def _environment_argument(arguments: dict[str, JsonValue]) -> dict[str, str]:
        value = arguments.get("env", {})
        if not isinstance(value, dict):
            raise MutationToolError("invalid_arguments", "env must be an object")
        result: dict[str, str] = {}
        for key, item in value.items():
            if key not in ALLOWED_ENV_KEYS or not isinstance(item, str) or len(item) > 1_024:
                raise MutationToolError("environment_not_allowed", "Environment contains a disallowed value")
            result[key] = item
        return result

    @staticmethod
    def _timeout_argument(arguments: dict[str, JsonValue], definition: ToolDefinition) -> int:
        value = arguments.get("timeout_seconds", min(30, definition.timeout_seconds))
        if not isinstance(value, int) or isinstance(value, bool) or not 1 <= value <= definition.timeout_seconds:
            raise MutationToolError("invalid_arguments", "timeout_seconds is outside the allowed range")
        return value

    @staticmethod
    def _only_arguments(arguments: dict[str, JsonValue], allowed: set[str]) -> None:
        if set(arguments) - allowed:
            raise MutationToolError("invalid_arguments", "Unsupported arguments were provided")

    @staticmethod
    def _string_argument(
        arguments: dict[str, JsonValue],
        name: str,
        *,
        allow_empty: bool = False,
    ) -> str:
        value = arguments.get(name)
        if not isinstance(value, str) or (not allow_empty and not value):
            raise MutationToolError("invalid_arguments", f"{name} must be a string")
        return value

    @staticmethod
    def _optional_string(arguments: dict[str, JsonValue], name: str, default: str) -> str:
        value = arguments.get(name, default)
        if not isinstance(value, str) or not value:
            raise MutationToolError("invalid_arguments", f"{name} must be a non-empty string")
        return value

    @staticmethod
    def _read_all(descriptor: int, limit: int) -> bytes:
        chunks: list[bytes] = []
        remaining = limit
        while remaining:
            chunk = os.read(descriptor, min(65_536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    @staticmethod
    def _write_all(descriptor: int, content: bytes) -> None:
        written = 0
        while written < len(content):
            written += os.write(descriptor, content[written:])

    def _blocked_decision(
        self,
        invocation: ToolInvocation,
        reason_code: str,
        permissions: frozenset[ToolPermission] = frozenset(),
    ) -> ToolDecision:
        return ToolDecision(
            decision=ToolDecisionType.BLOCK,
            reason_code=reason_code,
            invocation_hash=invocation_hash(invocation),
            required_permissions=permissions,
        )

    def _denied(
        self,
        invocation: ToolInvocation,
        decision: ToolDecision,
        started: float,
        permissions: frozenset[ToolPermission],
    ) -> ToolExecutionRecord:
        result = ToolExecutionResult(
            invocation_id=invocation.invocation_id,
            tool_name=invocation.tool_name,
            status=ToolResultStatus.DENIED,
            error_code=decision.reason_code,
        )
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
            target_hashes=tuple(hashlib.sha256(str(path).encode("utf-8")).hexdigest() for path in targets),
            duration_ms=(time.perf_counter() - started) * 1_000,
            output_truncated=truncated,
            error_code=error_code,
        )
