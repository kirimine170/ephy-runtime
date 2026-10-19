from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class WebSearchPlanRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    query: str
    context: str | None = Field(default=None, strict=True, max_length=2400)


class WebSearchApproveRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    plan_id: str


class WebSearchPlanResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    plan_id: str
    decision: Literal["allow", "confirm", "block"]
    outbound_query: str = ""
    risk_categories: list[str] = Field(default_factory=list)
    expires_at: str


class WebSearchSource(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_type: Literal["web"] = "web"
    source_id: str
    title: str
    url: str
    snippet: str
    trust_level: Literal["external_untrusted"] = "external_untrusted"
    injection_suspected: bool = False


class ExtractedClaim(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_id: str
    claim: str
