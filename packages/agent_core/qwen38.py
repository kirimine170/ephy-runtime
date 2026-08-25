from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from enum import StrEnum
import json
from pathlib import Path
import time
from typing import Any, Callable
from uuid import uuid4

import httpx

from packages.tool_core import (
    ApprovalGrant,
    MutationToolExecutor,
    ReadOnlyToolExecutor,
    RequestOrigin,
    SourceTrust,
    ToolDecisionType,
    ToolExecutionRecord,
    ToolInvocation,
    ToolMutationPlan,
    ToolPermission,
    ToolPolicyContext,
)


DEFAULT_MODEL = "qwen3.8-27b"


class AgentRunStatus(StrEnum):
    RUNNING = "running"
    COMPLETED = "completed"
    APPROVAL_REQUIRED = "approval_required"
    APPROVAL_DENIED = "approval_denied"
    MAX_STEPS = "max_steps"
    FAILED = "failed"


class LlamaCppRequestError(RuntimeError):
    pass


@dataclass(frozen=True)
class PendingApproval:
    public_tool_name: str
    tool_call_id: str
    plan: ToolMutationPlan


@dataclass
class AgentSession:
    task: str
    workspace: Path
    messages: list[dict[str, Any]]
    status: AgentRunStatus = AgentRunStatus.RUNNING
    final_message: str = ""
    pending_approval: PendingApproval | None = None
    remaining_tool_calls: list[dict[str, Any]] = field(default_factory=list)
    model_turns: int = 0
    tool_calls: int = 0
    prompt_tokens: int = 0
    generated_tokens: int = 0
    model_duration_seconds: float = 0.0
    started_at: float = field(default_factory=time.monotonic)
    events: list[dict[str, Any]] = field(default_factory=list)

    def summary(self, model: str) -> dict[str, Any]:
        return {
            "status": self.status.value,
            "model": model,
            "workspace": str(self.workspace),
            "model_turns": self.model_turns,
            "tool_calls": self.tool_calls,
            "prompt_tokens": self.prompt_tokens,
            "generated_tokens": self.generated_tokens,
            "model_duration_seconds": round(self.model_duration_seconds, 3),
            "wall_duration_seconds": round(time.monotonic() - self.started_at, 3),
            "final_message": self.final_message,
            "events": self.events,
        }


@dataclass(frozen=True)
class _ToolBinding:
    internal_name: str
    version: str = "1.0.0"
    mutation: bool = False


TOOL_BINDINGS = {
    "read_file": _ToolBinding("files.read"),
    "list_files": _ToolBinding("files.list"),
    "search_files": _ToolBinding("files.search"),
    "git_status": _ToolBinding("git.status"),
    "git_diff": _ToolBinding("git.diff"),
    "git_log": _ToolBinding("git.log"),
    "write_file": _ToolBinding("files.write", mutation=True),
    "run_process": _ToolBinding("process.run", mutation=True),
}


READ_ONLY_TOOLS: list[dict[str, Any]] = [
    {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read one UTF-8 text file inside the workspace. Paths are relative to the workspace.",
            "parameters": {
                "type": "object",
                "properties": {"path": {"type": "string"}},
                "required": ["path"],
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "list_files",
            "description": "List files and directories inside the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "default": "."},
                    "recursive": {"type": "boolean", "default": False},
                    "max_entries": {"type": "integer", "minimum": 1, "maximum": 2000, "default": 200},
                },
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "search_files",
            "description": "Search for literal text in UTF-8 files inside the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {"type": "string"},
                    "path": {"type": "string", "default": "."},
                    "case_sensitive": {"type": "boolean", "default": False},
                    "max_results": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 100},
                },
                "required": ["query"],
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_status",
            "description": "Show the read-only Git working tree status.",
            "parameters": {"type": "object", "properties": {}, "additionalProperties": False},
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_diff",
            "description": "Show a read-only Git diff, optionally for one path.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "staged": {"type": "boolean", "default": False},
                },
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_log",
            "description": "Show recent Git commit metadata.",
            "parameters": {
                "type": "object",
                "properties": {"limit": {"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
                "additionalProperties": False,
            },
        },
    },
]


MUTATION_TOOLS: list[dict[str, Any]] = [
    {
        "type": "function",
        "function": {
            "name": "write_file",
            "description": (
                "Create or atomically replace one UTF-8 file inside the workspace. "
                "Supply the entire desired file content. This always requires user approval."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "content": {"type": "string"},
                },
                "required": ["path", "content"],
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "run_process",
            "description": (
                "Run one sandboxed process without a shell. argv[0] must be an existing absolute executable path. "
                "Network access and writes outside the workspace are denied. This always requires user approval."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "argv": {
                        "type": "array",
                        "items": {"type": "string"},
                        "minItems": 1,
                        "maxItems": 128,
                    },
                    "cwd": {"type": "string", "default": "."},
                    "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 120, "default": 30},
                },
                "required": ["argv"],
                "additionalProperties": False,
            },
        },
    },
]


EventHandler = Callable[[str, dict[str, Any]], None]


class LlamaCppCodingAgent:
    def __init__(
        self,
        *,
        model: str = DEFAULT_MODEL,
        base_url: str = "http://127.0.0.1:8083/v1",
        max_steps: int = 24,
        include_mutations: bool = True,
        reasoning_effort: str = "medium",
        temperature: float = 0.2,
        client: httpx.AsyncClient | None = None,
        event_handler: EventHandler | None = None,
    ) -> None:
        if not 1 <= max_steps <= 100:
            raise ValueError("max_steps must be between 1 and 100")
        if reasoning_effort not in {"low", "medium", "high", "max"}:
            raise ValueError("reasoning_effort must be low, medium, high, or max")
        self.model = model
        self.base_url = base_url.rstrip("/")
        self.max_steps = max_steps
        self.include_mutations = include_mutations
        self.reasoning_effort = reasoning_effort
        self.temperature = temperature
        self._client = client or httpx.AsyncClient(timeout=httpx.Timeout(1_800.0, connect=10.0))
        self._owns_client = client is None
        self._read_executor = ReadOnlyToolExecutor()
        self._mutation_executor = MutationToolExecutor()
        self._event_handler = event_handler

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()

    async def start(self, task: str, workspace: str | Path) -> AgentSession:
        if not task.strip():
            raise ValueError("task must not be empty")
        root = Path(workspace).expanduser().resolve(strict=True)
        if not root.is_dir():
            raise ValueError("workspace must be a directory")
        session = AgentSession(
            task=task,
            workspace=root,
            messages=[
                {"role": "system", "content": self._system_prompt(root)},
                {"role": "user", "content": task},
            ],
        )
        return await self._advance(session)

    async def approve_and_resume(self, session: AgentSession) -> AgentSession:
        pending = session.pending_approval
        if session.status != AgentRunStatus.APPROVAL_REQUIRED or pending is None:
            raise ValueError("session has no pending approval")
        self._mutation_executor.approval_store.grant(
            ApprovalGrant(
                invocation_hash=pending.plan.decision.invocation_hash,
                expires_at=datetime.now(timezone.utc) + timedelta(minutes=5),
            )
        )
        record = self._mutation_executor.execute(pending.plan.invocation, self._policy_context(session.workspace))
        self._append_tool_result(session, pending.public_tool_name, pending.tool_call_id, record)
        session.events.append(self._event_from_record(pending.public_tool_name, record))
        session.pending_approval = None
        session.status = AgentRunStatus.RUNNING
        self._emit("tool_result", session.events[-1])
        return await self._advance(session)

    def deny(self, session: AgentSession) -> AgentSession:
        pending = session.pending_approval
        if session.status != AgentRunStatus.APPROVAL_REQUIRED or pending is None:
            raise ValueError("session has no pending approval")
        session.messages.append(
            {
                "role": "tool",
                "tool_call_id": pending.tool_call_id,
                "name": pending.public_tool_name,
                "content": json.dumps(
                    {"status": "denied", "error_code": "approval_denied"},
                    ensure_ascii=False,
                ),
            }
        )
        session.events.append({"tool": pending.public_tool_name, "status": "denied", "error_code": "approval_denied"})
        session.pending_approval = None
        session.remaining_tool_calls.clear()
        session.status = AgentRunStatus.APPROVAL_DENIED
        return session

    async def _advance(self, session: AgentSession) -> AgentSession:
        while session.status == AgentRunStatus.RUNNING:
            if session.remaining_tool_calls:
                calls = session.remaining_tool_calls
                session.remaining_tool_calls = []
                if self._process_tool_calls(session, calls):
                    return session
                continue

            payload = {
                "model": self.model,
                "messages": session.messages,
                "tools": self._tools(),
                "stream": False,
                "temperature": self.temperature,
                "chat_template_kwargs": {"enable_thinking": True},
                "thinking_budget_tokens": self._reasoning_budget(),
                "cache_prompt": True,
            }
            self._emit("model_start", {"turn": session.model_turns + 1})
            model_started_at = time.monotonic()
            response = await self._chat(payload)
            session.model_duration_seconds += time.monotonic() - model_started_at
            session.model_turns += 1
            usage = response.get("usage") if isinstance(response.get("usage"), dict) else {}
            session.prompt_tokens += self._integer(usage.get("prompt_tokens"))
            session.generated_tokens += self._integer(usage.get("completion_tokens"))
            choices = response.get("choices")
            first_choice = choices[0] if isinstance(choices, list) and choices else None
            message = first_choice.get("message") if isinstance(first_choice, dict) else None
            if not isinstance(message, dict):
                raise LlamaCppRequestError("llama.cpp response did not contain a message object")
            session.messages.append(message)
            calls = message.get("tool_calls") or []
            self._emit(
                "model_result",
                {
                    "turn": session.model_turns,
                    "tool_calls": len(calls) if isinstance(calls, list) else 0,
                    "generated_tokens": self._integer(usage.get("completion_tokens")),
                },
            )
            if not calls:
                session.final_message = str(message.get("content") or "")
                session.status = AgentRunStatus.COMPLETED
                return session
            if not isinstance(calls, list):
                raise LlamaCppRequestError("llama.cpp returned invalid tool_calls")
            if self._process_tool_calls(session, calls):
                return session
        return session

    def _process_tool_calls(self, session: AgentSession, calls: list[dict[str, Any]]) -> bool:
        for index, call in enumerate(calls):
            if session.tool_calls >= self.max_steps:
                session.status = AgentRunStatus.MAX_STEPS
                session.final_message = "Agent stopped after reaching the tool-call limit."
                return True
            session.tool_calls += 1
            function = call.get("function") if isinstance(call, dict) else None
            public_name = function.get("name") if isinstance(function, dict) else None
            arguments = function.get("arguments") if isinstance(function, dict) else None
            tool_call_id = (
                str(call.get("id") or f"call_{uuid4().hex}")
                if isinstance(call, dict)
                else f"call_{uuid4().hex}"
            )
            if isinstance(arguments, str):
                try:
                    arguments = json.loads(arguments)
                except json.JSONDecodeError:
                    arguments = None
            if not isinstance(public_name, str) or not isinstance(arguments, dict):
                self._append_raw_tool_error(
                    session,
                    str(public_name or "unknown"),
                    tool_call_id,
                    "invalid_tool_call",
                )
                continue
            binding = TOOL_BINDINGS.get(public_name)
            if binding is None or (binding.mutation and not self.include_mutations):
                self._append_raw_tool_error(session, public_name, tool_call_id, "unknown_tool")
                continue
            invocation = ToolInvocation(
                invocation_id=f"agent_{uuid4().hex}",
                tool_name=binding.internal_name,
                tool_version=binding.version,
                arguments=arguments,
                workspace_root=str(session.workspace),
                requested_by=RequestOrigin.MODEL,
                source_trust=SourceTrust.USER,
            )
            self._emit("tool_start", {"tool": public_name})
            if binding.mutation:
                plan = self._mutation_executor.plan(invocation, self._policy_context(session.workspace))
                if plan.decision.decision == ToolDecisionType.CONFIRM:
                    session.pending_approval = PendingApproval(
                        public_tool_name=public_name,
                        tool_call_id=tool_call_id,
                        plan=plan,
                    )
                    session.remaining_tool_calls = calls[index + 1 :]
                    session.status = AgentRunStatus.APPROVAL_REQUIRED
                    self._emit("approval_required", {"tool": public_name, "preview": plan.preview})
                    return True
                if plan.decision.decision == ToolDecisionType.BLOCK:
                    self._append_raw_tool_error(
                        session,
                        public_name,
                        tool_call_id,
                        plan.error_code or plan.decision.reason_code,
                    )
                    self._emit("tool_result", session.events[-1])
                    continue
                record = self._mutation_executor.execute(plan.invocation, self._policy_context(session.workspace))
            else:
                record = self._read_executor.execute(invocation, self._policy_context(session.workspace))
            self._append_tool_result(session, public_name, tool_call_id, record)
            session.events.append(self._event_from_record(public_name, record))
            self._emit("tool_result", session.events[-1])
        return False

    async def _chat(self, payload: dict[str, Any]) -> dict[str, Any]:
        try:
            response = await self._client.post(f"{self.base_url}/chat/completions", json=payload)
            response.raise_for_status()
        except httpx.HTTPStatusError as exc:
            detail = exc.response.text[:2_000]
            raise LlamaCppRequestError(f"llama.cpp returned HTTP {exc.response.status_code}: {detail}") from exc
        except httpx.HTTPError as exc:
            raise LlamaCppRequestError(f"Could not reach llama.cpp at {self.base_url}: {exc}") from exc
        try:
            payload = response.json()
        except ValueError as exc:
            raise LlamaCppRequestError("llama.cpp returned invalid JSON") from exc
        if not isinstance(payload, dict):
            raise LlamaCppRequestError("llama.cpp returned an invalid response")
        return payload

    def _reasoning_budget(self) -> int:
        return {
            "low": 1_024,
            "medium": 4_096,
            "high": 8_192,
            "max": -1,
        }[self.reasoning_effort]

    def _policy_context(self, workspace: Path) -> ToolPolicyContext:
        permissions = {ToolPermission.READ_FILES}
        if self.include_mutations:
            permissions.update({ToolPermission.WRITE_FILES, ToolPermission.EXECUTE_PROCESS})
        return ToolPolicyContext(
            granted_permissions=frozenset(permissions),
            allowed_workspace_roots=(str(workspace),),
            network_enabled=False,
        )

    def _tools(self) -> list[dict[str, Any]]:
        tools = list(READ_ONLY_TOOLS)
        if self.include_mutations:
            tools.extend(MUTATION_TOOLS)
        return tools

    def _system_prompt(self, workspace: Path) -> str:
        mutation_guidance = (
            "You may request write_file and run_process. Each mutation pauses for explicit user approval. "
            if self.include_mutations
            else "This run is read-only. Do not attempt to modify files or run processes. "
        )
        return (
            "You are a local coding agent working inside one explicitly allowed workspace. "
            f"Workspace: {workspace}. "
            "Inspect relevant files before proposing changes. Prefer small, focused changes and verify them with tests. "
            "Use at most one tool call per assistant turn. Never access secrets, .git internals, or paths outside the workspace. "
            "run_process requires argv[0] to be an existing absolute executable path and does not accept shell syntax. "
            + mutation_guidance
            + "When the task is complete, provide a concise summary of changes, verification, and remaining limitations."
        )

    @staticmethod
    def _append_tool_result(
        session: AgentSession,
        public_name: str,
        tool_call_id: str,
        record: ToolExecutionRecord,
    ) -> None:
        content = {
            "status": record.result.status.value,
            "output": record.result.output,
            "output_truncated": record.result.output_truncated,
            "error_code": record.result.error_code,
        }
        session.messages.append(
            {
                "role": "tool",
                "tool_call_id": tool_call_id,
                "name": public_name,
                "content": json.dumps(content, ensure_ascii=False, separators=(",", ":")),
            }
        )

    @staticmethod
    def _append_raw_tool_error(
        session: AgentSession,
        public_name: str,
        tool_call_id: str,
        error_code: str,
    ) -> None:
        session.messages.append(
            {
                "role": "tool",
                "tool_call_id": tool_call_id,
                "name": public_name,
                "content": json.dumps({"status": "denied", "error_code": error_code}),
            }
        )
        session.events.append({"tool": public_name, "status": "denied", "error_code": error_code})

    @staticmethod
    def _event_from_record(public_name: str, record: ToolExecutionRecord) -> dict[str, Any]:
        event = {
            "tool": public_name,
            "status": record.result.status.value,
            "error_code": record.result.error_code,
            "output_truncated": record.result.output_truncated,
        }
        return_code = record.result.output.get("return_code")
        if public_name == "run_process" and isinstance(return_code, int) and not isinstance(return_code, bool):
            event["return_code"] = return_code
        return event

    @staticmethod
    def _integer(value: Any) -> int:
        return value if isinstance(value, int) and not isinstance(value, bool) else 0

    def _emit(self, name: str, payload: dict[str, Any]) -> None:
        if self._event_handler is not None:
            self._event_handler(name, payload)
