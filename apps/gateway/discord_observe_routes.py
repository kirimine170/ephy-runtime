"""Dedicated private community path; never falls back to public web search．"""
from fastapi import APIRouter, HTTPException, Request
from starlette.responses import JSONResponse

from packages.discord_observe_core.schemas import ObserveRequest
from packages.web_search_core.worker_provider import WorkerSearchError

from .routes import _run_until_worker_disconnect, _WorkerRequestDisconnected

discord_observe_router = APIRouter()


@discord_observe_router.post("/v1/discord/observe")
async def observe_discord(payload: ObserveRequest, request: Request):
    service = getattr(request.app.state, "discord_observe_service", None)
    if service is None:
        raise HTTPException(status_code=503, detail="discord_observe_disabled")
    gate = getattr(request.app.state, "inference_gate", None)
    if gate is not None:
        if gate.transitioning():
            raise HTTPException(status_code=503, detail="model_transition_in_progress")
        participation = getattr(request.app.state, "resident_participation", None)
        if participation is not None:
            participation.cancel_current()
        gate.active += 1
    try:
        status, body = await _run_until_worker_disconnect(request.receive, lambda: service.observe(payload))
        return JSONResponse(status_code=status, content=body)
    except WorkerSearchError as exc:
        raise HTTPException(status_code=502, detail={"code": exc.code, **exc.identity}) from None
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    except _WorkerRequestDisconnected:
        raise HTTPException(status_code=499, detail="client_disconnected") from None
    finally:
        if gate is not None:
            gate.active -= 1
