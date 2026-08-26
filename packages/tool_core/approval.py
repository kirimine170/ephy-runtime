from __future__ import annotations

from datetime import datetime, timezone
from threading import Lock

from .policy import invocation_hash, plan_tool_invocation
from .schemas import (
    ApprovalGrant,
    ToolDecision,
    ToolDecisionType,
    ToolDefinition,
    ToolInvocation,
    ToolPolicyContext,
)


class InMemoryApprovalStore:
    def __init__(self) -> None:
        self._grants: dict[str, ApprovalGrant] = {}
        self._lock = Lock()

    def grant(self, approval: ApprovalGrant) -> None:
        with self._lock:
            self._grants[approval.invocation_hash] = approval.model_copy(deep=True)

    def authorize_and_consume(
        self,
        definition: ToolDefinition,
        invocation: ToolInvocation,
        context: ToolPolicyContext,
        *,
        now: datetime | None = None,
    ) -> ToolDecision:
        current_time = now or datetime.now(timezone.utc)
        digest = invocation_hash(invocation)
        with self._lock:
            approval = self._grants.get(digest)
            decision = plan_tool_invocation(
                definition,
                invocation,
                context,
                approval,
                now=current_time,
            )
            if decision.decision == ToolDecisionType.ALLOW and approval is not None:
                self._grants[digest] = approval.model_copy(update={"consumed_at": current_time})
            return decision

    def get(self, digest: str) -> ApprovalGrant | None:
        with self._lock:
            approval = self._grants.get(digest)
            return approval.model_copy(deep=True) if approval else None
