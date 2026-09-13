"""Private dedicated-gateway resident APIs; errors omit conversation contents．"""
import sqlite3
import ipaddress

from fastapi import APIRouter, HTTPException, Request
from starlette.responses import JSONResponse

from packages.eval_core.resident_schemas import FeedbackRequest, ResidentSessionRequest, RetractRequest, UndoRequest
from packages.eval_core.resident_service import ResidentConflict

resident_router = APIRouter(prefix="/v1/resident")


def trusted_desktop(request: Request):
    try:
        host_allowed = ipaddress.ip_address(request.url.hostname or "").is_loopback
    except ValueError:
        host_allowed = request.url.hostname == "localhost"
    testing = request.client and request.client.host == "testclient" and request.url.hostname == "testserver"
    return bool((host_allowed or testing) and "origin" not in request.headers
                and len(request.headers.getlist("host")) == 1 and request.client
                and request.client.host in {"127.0.0.1", "::1", "localhost", "testclient"})


class ResidentPrivacyMiddleware:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] == "http" and scope.get("path", "").startswith("/v1/resident/"):
            if not trusted_desktop(Request(scope)):
                return await JSONResponse({"detail": "Resident APIs require the local desktop"}, status_code=403,
                                          headers={"Cache-Control": "no-store"})(scope, receive, send)
            original_send = send
            async def send(message):
                if message["type"] == "http.response.start":
                    message["headers"] = [*message.get("headers", []), (b"cache-control", b"no-store")]
                await original_send(message)
        await self.app(scope, receive, send)


def resident_service(request: Request):
    service = getattr(request.app.state, "resident_service", None)
    if service is None:
        raise HTTPException(404, "Resident mode is disabled")
    if not trusted_desktop(request):
        raise HTTPException(403, "Resident APIs require the local desktop")
    return service


def invoke(operation):
    try:
        return operation()
    except ResidentConflict as exc:
        raise HTTPException(409, str(exc)) from None
    except (OSError, sqlite3.Error):
        raise HTTPException(503, "Resident storage is unavailable; feedback was not saved") from None
    except ValueError as exc:
        # Service errors are fixed messages; Pydantic validation is handled before this boundary．
        raise HTTPException(400, str(exc)) from None


@resident_router.post("/sessions")
def create_session(payload: ResidentSessionRequest, request: Request):
    service = resident_service(request)
    return invoke(lambda: service.create_session(payload))


@resident_router.get("/sessions/{session_id}")
def get_session(session_id: str, request: Request):
    service = resident_service(request)
    return invoke(lambda: service.state(session_id))


@resident_router.post("/feedback")
def submit_feedback(payload: FeedbackRequest, request: Request):
    service = resident_service(request)
    return invoke(lambda: service.submit(payload))


@resident_router.post("/changes/{change_id}/undo")
def undo_change(change_id: str, payload: UndoRequest, request: Request):
    service = resident_service(request)
    return invoke(lambda: service.undo(change_id, payload))


@resident_router.post("/feedback/{event_id}/retract")
def retract_feedback(event_id: str, payload: RetractRequest, request: Request):
    service = resident_service(request)
    return invoke(lambda: service.retract(event_id, payload))
