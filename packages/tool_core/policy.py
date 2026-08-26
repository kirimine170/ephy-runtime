from __future__ import annotations

from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

from .schemas import (
    ApprovalGrant,
    ApprovalPolicy,
    SourceTrust,
    ToolDecision,
    ToolDecisionType,
    ToolDefinition,
    ToolInvocation,
    ToolPermission,
    ToolPolicyContext,
)


UNTRUSTED_SOURCES = frozenset({SourceTrust.LOCAL_UNTRUSTED, SourceTrust.EXTERNAL_UNTRUSTED})


def invocation_hash(invocation: ToolInvocation) -> str:
    approval_payload = {
        "tool_name": invocation.tool_name,
        "tool_version": invocation.tool_version,
        "arguments": invocation.arguments,
        "workspace_root": invocation.workspace_root,
        "requested_by": invocation.requested_by.value,
        "source_trust": invocation.source_trust.value,
    }
    encoded = json.dumps(
        approval_payload,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def plan_tool_invocation(
    definition: ToolDefinition,
    invocation: ToolInvocation,
    context: ToolPolicyContext,
    approval: ApprovalGrant | None = None,
    *,
    now: datetime | None = None,
) -> ToolDecision:
    digest = invocation_hash(invocation)
    required = definition.permissions

    if invocation.tool_name != definition.name or invocation.tool_version != definition.version:
        return _decision(ToolDecisionType.BLOCK, "tool_identity_mismatch", digest, required)
    if invocation.source_trust in UNTRUSTED_SOURCES:
        return _decision(ToolDecisionType.BLOCK, "untrusted_source_cannot_request_tools", digest, required)
    if not required.issubset(context.granted_permissions):
        return _decision(ToolDecisionType.BLOCK, "permission_not_granted", digest, required)
    if ToolPermission.NETWORK_ACCESS in required and not context.network_enabled:
        return _decision(ToolDecisionType.BLOCK, "network_disabled", digest, required)
    if context.allowed_workspace_roots and not _workspace_root_allowed(
        invocation.workspace_root,
        context.allowed_workspace_roots,
    ):
        return _decision(ToolDecisionType.BLOCK, "workspace_root_not_allowed", digest, required)

    if definition.approval_policy == ApprovalPolicy.ALWAYS:
        current_time = now or datetime.now(timezone.utc)
        if approval is None:
            return _decision(ToolDecisionType.CONFIRM, "approval_required", digest, required)
        if approval.invocation_hash != digest:
            return _decision(ToolDecisionType.CONFIRM, "approval_hash_mismatch", digest, required)
        if not approval.one_shot:
            return _decision(ToolDecisionType.CONFIRM, "approval_must_be_one_shot", digest, required)
        if approval.consumed_at is not None:
            return _decision(ToolDecisionType.CONFIRM, "approval_consumed", digest, required)
        if not approval.is_valid_at(current_time):
            return _decision(ToolDecisionType.CONFIRM, "approval_expired", digest, required)

    return _decision(ToolDecisionType.ALLOW, "policy_satisfied", digest, required)


def _decision(
    decision: ToolDecisionType,
    reason_code: str,
    digest: str,
    permissions: frozenset[ToolPermission],
) -> ToolDecision:
    return ToolDecision(
        decision=decision,
        reason_code=reason_code,
        invocation_hash=digest,
        required_permissions=permissions,
    )


def _workspace_root_allowed(workspace_root: str, allowed_roots: tuple[str, ...]) -> bool:
    try:
        candidate = Path(workspace_root).expanduser().resolve(strict=False)
        return any(candidate == Path(root).expanduser().resolve(strict=False) for root in allowed_roots)
    except (OSError, RuntimeError, ValueError):
        return False
