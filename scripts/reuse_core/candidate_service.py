"""Experimental local entry；synthetic fixtures only，one in-flight run．"""

import asyncio
import json
import os
import secrets
import re
import time
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from scripts.reuse_core.memory_probe import (
    FixtureClient,
    INSTRUCTIONS,
    fixed_generate,
    fixed_workflow,
)
from packages.runtime_core.participation import MemoryTools, Run

app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
lock = asyncio.Lock()


@app.post("/candidate")
async def candidate(request: Request):
    if not secrets.compare_digest(
        request.headers.get("authorization", ""),
        "Bearer " + os.environ["EPHY_TTS_BEARER_TOKEN"],
    ):
        return JSONResponse({"error": "forbidden"}, status_code=403)
    body = bytearray()
    async for chunk in request.stream():
        body.extend(chunk)
        if len(body) > 4096:
            return JSONResponse({"error": "request_limit"}, status_code=413)
    try:
        value = json.loads(body)
        if set(value) != {
            "case",
            "engine",
            "operation_id",
            "generation_revision",
        } or value["engine"] not in {"fixed", "pydantic"}:
            raise ValueError()
        if (
            not isinstance(value["operation_id"], str)
            or not re.fullmatch(r"obs_[a-f0-9]{32}", value["operation_id"])
            or type(value["generation_revision"]) is not int
            or value["generation_revision"] < 1
        ):
            raise ValueError()
        client = FixtureClient()
        case = next(c for c in client.fixture["cases"] if c["id"] == value["case"])
    except Exception:
        return JSONResponse({"error": "invalid_fixture"}, status_code=400)
    if lock.locked():
        return JSONResponse({"error": "busy"}, status_code=409)
    async with lock:
        start = time.monotonic()
        run = Run(
            value["operation_id"],
            "fixture-session",
            value["operation_id"],
            value["generation_revision"],
            client.scope(),
            start + 60,
            start + 60,
            lambda: True,
        )
        tools = MemoryTools(run, client)
        if value["engine"] == "pydantic":
            from packages.runtime_core.participation_agent import run_agent

            work = run_agent(
                tools,
                case["query"],
                "http://127.0.0.1:8082/v1",
                "qwen3.8-27b",
                INSTRUCTIONS,
            )
        else:
            work = fixed_workflow(
                tools,
                case["query"],
                lambda q, d: fixed_generate(
                    q, d, endpoint="http://127.0.0.1:8082/v1", model="qwen3.8-27b"
                ),
            )
        task = asyncio.create_task(work)
        try:
            while not task.done():
                if await request.is_disconnected():
                    run.canceled = True
                    task.cancel()
                    break
                await asyncio.sleep(0.05)
            candidate = await task
            return {
                "candidate": candidate.model_dump(),
                "query": case["query"],
                "participants": sorted(client.scope().participants),
                "elapsed_ms": (time.monotonic() - start) * 1000,
            }
        except (Exception, asyncio.CancelledError):
            return JSONResponse({"error": "candidate_unavailable"}, status_code=503)
        finally:
            run.canceled = True
            if not task.done():
                task.cancel()
                try:
                    await task
                except (Exception, asyncio.CancelledError):
                    pass
