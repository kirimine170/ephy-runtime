"""Admission control keeps an entire streaming response inside its inference lease．"""

import secrets
import time

from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel, ConfigDict
from starlette.responses import JSONResponse


class InferenceGate:
    def __init__(self):
        self.active = 0
        self.token = None
        self.expires = 0.0

    def transitioning(self):
        if self.token and time.monotonic() >= self.expires:
            self.token = None
        return self.token is not None

    def begin(self):
        if self.transitioning() or self.active:
            raise ValueError("Inference or another model switch is active; retry after it finishes")
        self.token = secrets.token_urlsafe(24)
        self.expires = time.monotonic() + 600
        return self.token

    def end(self, token):
        if not self.token or not secrets.compare_digest(self.token, token):
            raise ValueError("Model transition token does not match")
        self.token = None


class InferenceGateMiddleware:
    paths = {"/v1/chat/completions", "/v1/rag/query", "/v1/embeddings", "/v1/eval/run"}

    @classmethod
    def inference_path(cls, path: str) -> bool:
        return path in cls.paths or (
            path.startswith("/v1/eval/preferences/sessions/") and path.endswith("/generate")
        )

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or not self.inference_path(scope.get("path", "")):
            return await self.app(scope, receive, send)
        gate = scope["app"].state.inference_gate
        if gate.transitioning():
            response = JSONResponse({"detail": "Model switch in progress; retry shortly"}, status_code=503,
                                    headers={"Retry-After": "3"})
            return await response(scope, receive, send)
        gate.active += 1
        try:
            await self.app(scope, receive, send)
        finally:
            gate.active -= 1


class TransitionRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    token: str


transition_router = APIRouter(prefix="/v1/admin/model-transition")


def _local_request(request):
    if not request.client or request.client.host not in {"127.0.0.1", "::1"}:
        raise HTTPException(status_code=403, detail="Model transition requires a local connection")
    if request.headers.get("origin"):
        raise HTTPException(status_code=403, detail="Browser origins cannot mutate runtime state")


@transition_router.post("/begin")
async def begin(request: Request):
    _local_request(request)
    try:
        return {"token": request.app.state.inference_gate.begin()}
    except ValueError as error:
        raise HTTPException(status_code=409, detail=str(error)) from None


@transition_router.post("/end")
async def end(payload: TransitionRequest, request: Request):
    _local_request(request)
    try:
        request.app.state.inference_gate.end(payload.token)
        return {"status": "ready"}
    except ValueError as error:
        raise HTTPException(status_code=409, detail=str(error)) from None
