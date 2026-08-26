from __future__ import annotations

from datetime import datetime, timezone
from enum import StrEnum
from pydantic import BaseModel, ConfigDict, Field, JsonValue, model_validator


class ToolPermission(StrEnum):
    READ_FILES = "read_files"
    WRITE_FILES = "write_files"
    EXECUTE_PROCESS = "execute_process"
    NETWORK_ACCESS = "network_access"


class ApprovalPolicy(StrEnum):
    NEVER = "never"
    ALWAYS = "always"


class RequestOrigin(StrEnum):
    USER = "user"
    MODEL = "model"
    TRUSTED_SYSTEM = "trusted_system"


class SourceTrust(StrEnum):
    USER = "user"
    TRUSTED_SYSTEM = "trusted_system"
    LOCAL_UNTRUSTED = "local_untrusted"
    EXTERNAL_UNTRUSTED = "external_untrusted"


class ToolDecisionType(StrEnum):
    ALLOW = "allow"
    CONFIRM = "confirm"
    BLOCK = "block"


class ToolResultStatus(StrEnum):
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    DENIED = "denied"


SENSITIVE_PERMISSIONS = frozenset(
    {
        ToolPermission.WRITE_FILES,
        ToolPermission.EXECUTE_PROCESS,
        ToolPermission.NETWORK_ACCESS,
    }
)


class ToolDefinition(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str = Field(pattern=r"^[a-z][a-z0-9_.-]{2,63}$")
    version: str = Field(pattern=r"^[0-9]+\.[0-9]+\.[0-9]+$")
    description: str = Field(min_length=1, max_length=500)
    permissions: frozenset[ToolPermission] = Field(min_length=1)
    approval_policy: ApprovalPolicy
    timeout_seconds: int = Field(default=30, ge=1, le=300)
    max_output_bytes: int = Field(default=262_144, ge=1, le=10_485_760)

    @model_validator(mode="after")
    def require_approval_for_sensitive_permissions(self) -> ToolDefinition:
        if self.permissions.intersection(SENSITIVE_PERMISSIONS) and self.approval_policy != ApprovalPolicy.ALWAYS:
            raise ValueError("write, process, and network permissions require approval_policy=always")
        return self


class ToolInvocation(BaseModel):
    model_config = ConfigDict(extra="forbid")

    invocation_id: str = Field(pattern=r"^[A-Za-z0-9_-]{8,128}$")
    tool_name: str
    tool_version: str
    arguments: dict[str, JsonValue] = Field(default_factory=dict)
    workspace_root: str
    requested_by: RequestOrigin
    source_trust: SourceTrust


class ToolPolicyContext(BaseModel):
    model_config = ConfigDict(extra="forbid")

    granted_permissions: frozenset[ToolPermission] = Field(default_factory=frozenset)
    allowed_workspace_roots: tuple[str, ...] = ()
    network_enabled: bool = False


class ApprovalGrant(BaseModel):
    model_config = ConfigDict(extra="forbid")

    invocation_hash: str = Field(pattern=r"^[a-f0-9]{64}$")
    approved_by: str = "user"
    expires_at: datetime
    one_shot: bool = True
    consumed_at: datetime | None = None

    def is_valid_at(self, now: datetime) -> bool:
        normalized_now = now if now.tzinfo else now.replace(tzinfo=timezone.utc)
        normalized_expiry = self.expires_at if self.expires_at.tzinfo else self.expires_at.replace(tzinfo=timezone.utc)
        return self.one_shot and self.consumed_at is None and normalized_expiry > normalized_now


class ToolDecision(BaseModel):
    model_config = ConfigDict(extra="forbid")

    decision: ToolDecisionType
    reason_code: str
    invocation_hash: str = Field(pattern=r"^[a-f0-9]{64}$")
    required_permissions: frozenset[ToolPermission]


class ToolAuditEvent(BaseModel):
    model_config = ConfigDict(extra="forbid")

    timestamp: datetime
    invocation_hash: str = Field(pattern=r"^[a-f0-9]{64}$")
    tool_name: str
    permissions: frozenset[ToolPermission]
    decision: ToolDecisionType
    result_status: ToolResultStatus | None = None
    target_hashes: tuple[str, ...] = ()
    duration_ms: float | None = Field(default=None, ge=0)
    output_truncated: bool = False
    error_code: str | None = None


class ToolExecutionResult(BaseModel):
    model_config = ConfigDict(extra="forbid")

    invocation_id: str
    tool_name: str
    status: ToolResultStatus
    output: dict[str, JsonValue] = Field(default_factory=dict)
    output_truncated: bool = False
    error_code: str | None = None


class ToolExecutionRecord(BaseModel):
    model_config = ConfigDict(extra="forbid")

    decision: ToolDecision
    result: ToolExecutionResult
    audit_event: ToolAuditEvent


class ToolMutationPlan(BaseModel):
    model_config = ConfigDict(extra="forbid")

    invocation: ToolInvocation
    decision: ToolDecision
    preview: dict[str, JsonValue] = Field(default_factory=dict)
    error_code: str | None = None
