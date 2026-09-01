import asyncio
import inspect
import json

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import StreamingResponse

from packages.eval_core.api import EvalRunRequest
from packages.eval_core.preference_schemas import (
    CreatePreferenceSessionRequest,
    ExportPreferenceRequest,
    GeneratePreferencePairsRequest,
    SubmitPreferenceVoteRequest,
)
from packages.karte_core.conversation import KarteConversationRequest
from packages.karte_core.context import (
    KarteContextError,
    KarteContextReadRequest,
    KarteContextSearchRequest,
)
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage, EmbeddingRequest, RequestMetadata
from packages.rag_core.schemas import IndexBrowseRequest, IndexSourceRequest, IngestRequest, RAGQueryRequest, SearchRequest
from packages.router_core.schemas import RouteDecision, RoutePlanResponse
from packages.web_search_core.schemas import WebSearchApproveRequest, WebSearchPlanRequest


def _stringify_message_content(content) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for item in content:
            if isinstance(item, dict) and item.get("type") == "text":
                parts.append(str(item.get("text", "")))
        return "\n".join(part for part in parts if part)
    return str(content or "")


def _latest_user_message_text(messages: list[ChatMessage]) -> str:
    for message in reversed(messages):
        if message.role == "user":
            return _stringify_message_content(message.content).strip()
    return ""


def _resolve_grounding_scope(metadata: RequestMetadata | None) -> tuple[str | None, str | None, list[str], int | None]:
    if metadata is None:
        return None, None, [], None
    source_scope = (metadata.source_scope or "").strip().lower()
    project = (metadata.project or "").strip() or None
    source_path = (metadata.source_path or "").strip() or None
    tags = [tag for tag in metadata.tags if tag]
    top_k = metadata.top_k if metadata.top_k and metadata.top_k > 0 else None
    if source_scope == "selected_docs":
        return project, source_path, tags, top_k
    if source_scope in {"project", "personal_context"}:
        return project, None, tags, top_k
    return None, source_path if source_scope == "selected_docs" else None, tags, top_k


def _with_rag_required(payload: ChatCompletionRequest) -> ChatCompletionRequest:
    metadata = payload.metadata or RequestMetadata()
    updated_metadata = metadata.model_copy(update={"rag_required": True})
    return payload.model_copy(update={"metadata": updated_metadata})


def _stream_error_event(exc: RuntimeError, model: str) -> bytes:
    payload = {
        "error": str(exc),
        "code": "backend_unavailable",
        "model": model,
    }
    return f"event: error\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n".encode("utf-8")


def _mark_local_sources(sources: list[dict]) -> list[dict]:
    marked: list[dict] = []
    for index, source in enumerate(sources, start=1):
        marked.append(
            {
                **source,
                "source_type": "local",
                "source_id": f"L{index}",
                "trust_level": "local_untrusted",
            }
        )
    return marked


def _mark_karte_context_sources(sources: list[dict]) -> list[dict]:
    marked: list[dict] = []
    for index, source in enumerate(sources, start=1):
        marked.append(
            {
                **source,
                "chunk_id": f"karte:{source.get('doc_id', '')}",
                "source_path": source.get("relative_path"),
                "original_source_path": None,
                "heading_path": [],
                "chunk_text": source.get("snippet", ""),
                "source_type": "karte_context",
                "source_id": f"K{index}",
                "trust_level": "local_untrusted",
            }
        )
    return marked


def _karte_conversation_service(request: Request):
    service = getattr(request.app.state, "karte_conversation_service", None)
    if service is None:
        raise HTTPException(
            status_code=503,
            detail="Karte integration is unavailable. Set KARTE_DATA_DIR to a valid Karte workspace and restart Ephy.",
        )
    return service


def _karte_context_client(request: Request):
    client = getattr(request.app.state, "karte_context_client", None)
    if client is None:
        raise HTTPException(
            status_code=503,
            detail="Karte Personal Context is unavailable. Set KARTE_DATA_DIR and start Karte.",
        )
    return client


def build_router() -> APIRouter:
    router = APIRouter()

    @router.get("/health")
    async def health(request: Request) -> dict:
        config = request.app.state.app_config
        return {
            "status": "ok",
            "service": "local-llm-workbench-gateway",
            "configured_models": sorted(config.models.keys()),
            "web_search_enabled": config.web_search.enabled,
            "ephy_enabled": request.app.state.ephy_context is not None,
            "karte_enabled": getattr(request.app.state, "karte_conversation_service", None) is not None,
            "karte_context_enabled": getattr(request.app.state, "karte_context_client", None) is not None,
        }

    @router.get("/v1/models")
    async def list_models(request: Request) -> dict:
        config = request.app.state.app_config
        data = []
        for alias, model_cfg in config.models.items():
            data.append(
                {
                    "id": alias,
                    "object": "model",
                    "owned_by": model_cfg.provider,
                    "backend_model": model_cfg.model,
                }
            )
        return {"object": "list", "data": data}

    @router.post("/v1/chat/completions")
    async def chat_completions(payload: ChatCompletionRequest, request: Request) -> dict:
        router_service = request.app.state.model_router
        adapter = request.app.state.chat_adapter
        prompt_manager = request.app.state.prompt_manager
        rag_service = request.app.state.rag_service
        web_search_service = request.app.state.web_search_service

        try:
            query_text = _latest_user_message_text(payload.messages)
            project, source_path, tags, top_k = _resolve_grounding_scope(payload.metadata)
            local_sources = []
            karte_context_status: dict | None = None
            source_scope = ((payload.metadata.source_scope if payload.metadata else None) or "").strip().lower()
            if query_text and source_scope == "personal_context":
                context_client = getattr(request.app.state, "karte_context_client", None)
                if context_client is None:
                    karte_context_status = {"status": "unavailable", "source_count": 0}
                else:
                    try:
                        context_response = await asyncio.to_thread(
                            context_client.search,
                            query_text,
                            projects=[project] if project else [],
                            tags=tags,
                            top_k=top_k or 5,
                        )
                        local_sources = _mark_karte_context_sources(
                            [item.model_dump(mode="json") for item in context_response.results]
                        )
                        karte_context_status = {
                            "status": context_response.status,
                            "source_count": len(local_sources),
                            "diagnostics": [item.model_dump() for item in context_response.diagnostics],
                        }
                    except (KarteContextError, ValueError):
                        karte_context_status = {"status": "unavailable", "source_count": 0}
            elif query_text:
                try:
                    local_sources = _mark_local_sources(
                        rag_service.search_grounding_sources(
                            query=query_text,
                            project=project,
                            source_path=source_path,
                            tags=tags,
                            top_k=top_k,
                        )
                    )
                except RuntimeError:
                    local_sources = []

            web_sources: list[dict] = []
            web_context = ""
            web_search_status: dict | None = None
            if payload.metadata and payload.metadata.web_search:
                plan_id = (payload.metadata.web_search_plan_id or "").strip()
                if not plan_id:
                    raise ValueError("web_search_plan_id is required when web_search is enabled")
                try:
                    web_sources, web_context = await web_search_service.search_for_chat(query_text, plan_id)
                    web_search_status = {
                        "status": "completed",
                        "source_count": len(web_sources),
                    }
                except RuntimeError as exc:
                    web_search_status = {
                        "status": "unavailable",
                        "detail": str(exc),
                        "source_count": 0,
                    }

            sources = [*local_sources, *web_sources]

            routing_payload = payload
            requested_mode = ((payload.metadata.mode if payload.metadata else None) or payload.model or "auto").strip()
            if sources and requested_mode == "auto":
                routing_payload = _with_rag_required(payload)

            decision: RouteDecision = router_service.route_chat(routing_payload)
            effective_payload = prompt_manager.apply_mode_prompt(payload, decision.mode)
            if local_sources or web_context:
                effective_payload = prompt_manager.apply_untrusted_context(
                    effective_payload,
                    local_context=rag_service._build_context(local_sources) if local_sources else "",
                    web_context=web_context,
                )
            if web_search_status and web_search_status["status"] == "unavailable":
                effective_payload = prompt_manager.apply_web_unavailable(effective_payload)
            elif web_search_status and web_search_status["source_count"] == 0:
                effective_payload = prompt_manager.apply_web_unavailable(effective_payload, "returned no usable results")
            if payload.stream:
                async def generate_chat_stream():
                    if web_search_status:
                        yield f"event: web_search_status\ndata: {json.dumps(web_search_status, ensure_ascii=False)}\n\n".encode("utf-8")
                    if karte_context_status:
                        yield f"event: karte_context_status\ndata: {json.dumps(karte_context_status, ensure_ascii=False)}\n\n".encode("utf-8")
                    if sources:
                        yield f"event: sources\ndata: {json.dumps({'sources': sources}, ensure_ascii=False)}\n\n".encode("utf-8")
                    try:
                        async for chunk in adapter.stream_chat_completion(
                            model_config=decision.selected_model,
                            request_payload=effective_payload,
                        ):
                            yield chunk
                    except RuntimeError as exc:
                        yield _stream_error_event(exc, decision.selected_model.model)

                return StreamingResponse(
                    generate_chat_stream(),
                    media_type="text/event-stream",
                )
            response = await adapter.create_chat_completion(model_config=decision.selected_model, request_payload=effective_payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc

        if sources:
            response["sources"] = sources
        if web_search_status:
            response["web_search_status"] = web_search_status
        if karte_context_status:
            response["karte_context_status"] = karte_context_status
        return response

    @router.post("/v1/karte/context/search")
    async def karte_context_search(payload: KarteContextSearchRequest, request: Request) -> dict:
        try:
            response = await asyncio.to_thread(
                _karte_context_client(request).search,
                payload.query,
                projects=payload.projects,
                tags=payload.tags,
                sensitivity_ceiling=payload.sensitivity_ceiling,
                top_k=payload.top_k,
            )
            return response.model_dump(mode="json")
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except KarteContextError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc

    @router.post("/v1/karte/context/read")
    async def karte_context_read(payload: KarteContextReadRequest, request: Request) -> dict:
        try:
            response = await asyncio.to_thread(
                _karte_context_client(request).read,
                payload.doc_id,
                projects=payload.projects,
                tags=payload.tags,
                sensitivity_ceiling=payload.sensitivity_ceiling,
            )
            return response.model_dump(mode="json")
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except KarteContextError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc

    @router.post("/v1/karte/conversations/plan")
    async def karte_conversation_plan(payload: KarteConversationRequest, request: Request) -> dict:
        try:
            return _karte_conversation_service(request).plan(payload).model_dump(mode="json")
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except OSError as exc:
            raise HTTPException(status_code=503, detail="Karte workspace could not be read") from exc

    @router.post("/v1/karte/conversations/publish")
    async def karte_conversation_publish(payload: KarteConversationRequest, request: Request) -> dict:
        try:
            return _karte_conversation_service(request).publish(payload).model_dump(mode="json")
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except OSError as exc:
            raise HTTPException(status_code=503, detail="Karte outbox could not be updated") from exc

    @router.get("/v1/karte/proposals/{candidate_id}")
    async def karte_proposal_status(candidate_id: str, request: Request) -> dict:
        try:
            return _karte_conversation_service(request).status(candidate_id).model_dump(mode="json")
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except OSError as exc:
            raise HTTPException(status_code=503, detail="Karte outbox could not be read") from exc

    @router.post("/v1/web/search/plan")
    async def web_search_plan(payload: WebSearchPlanRequest, request: Request) -> dict:
        if not payload.query.strip():
            raise HTTPException(status_code=400, detail="query is required")
        return await request.app.state.web_search_service.create_plan(payload.query)

    @router.post("/v1/web/search/approve")
    async def web_search_approve(payload: WebSearchApproveRequest, request: Request) -> dict:
        try:
            return request.app.state.web_search_service.approve(payload.plan_id)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/embeddings")
    async def embeddings(payload: EmbeddingRequest, request: Request) -> dict:
        config = request.app.state.app_config
        adapter = request.app.state.chat_adapter
        requested_alias = (payload.model or "").strip()
        if not requested_alias or requested_alias == "auto":
            requested_alias = config.rag.embedding_model_alias

        try:
            model_config = config.models[requested_alias]
        except KeyError as exc:
            raise HTTPException(status_code=400, detail=f"Unknown embedding model alias '{requested_alias}'") from exc

        try:
            return await adapter.create_embedding(model_config=model_config, request_payload=payload)
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc

    @router.post("/v1/router/plan")
    async def route_plan(payload: ChatCompletionRequest, request: Request) -> dict:
        router_service = request.app.state.model_router
        try:
            plan: RoutePlanResponse = router_service.plan_chat(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return plan.model_dump()

    @router.post("/v1/ingest")
    async def ingest_documents(payload: IngestRequest, request: Request) -> dict:
        rag_service = request.app.state.rag_service
        try:
            return rag_service.ingest(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc

    @router.post("/v1/rag/search")
    async def rag_search(payload: SearchRequest, request: Request) -> dict:
        rag_service = request.app.state.rag_service
        try:
            return rag_service.search(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc

    @router.post("/v1/rag/query")
    async def rag_query(payload: RAGQueryRequest, request: Request) -> dict:
        rag_service = request.app.state.rag_service
        adapter = request.app.state.chat_adapter
        router_service = request.app.state.model_router
        try:
            if payload.stream and payload.answer:
                search_results = rag_service.search(
                    SearchRequest(
                        query=payload.query,
                        project=payload.project,
                        source_path=payload.source_path,
                        tags=payload.tags,
                        top_k=payload.top_k,
                    )
                )
                sources = _mark_local_sources(search_results["results"])
                context = rag_service._build_context(sources)
                chat_request = ChatCompletionRequest(
                    model="auto",
                    messages=rag_service._prompt_manager.build_rag_messages(payload.query, context),
                    metadata={"mode": "rag", "project": payload.project},
                    temperature=0.2,
                    stream=True,
                )
                decision = router_service.route_chat(chat_request)

                async def generate_rag_stream():
                    yield f"event: sources\ndata: {json.dumps({'sources': sources})}\n\n".encode("utf-8")
                    try:
                        async for chunk in adapter.stream_chat_completion(
                            model_config=decision.selected_model,
                            request_payload=chat_request,
                        ):
                            yield chunk
                    except RuntimeError as exc:
                        yield _stream_error_event(exc, decision.selected_model.model)

                return StreamingResponse(generate_rag_stream(), media_type="text/event-stream")
            response = await rag_service.query(payload=payload, router=router_service, adapter=adapter)
            response["sources"] = _mark_local_sources(response.get("sources", []))
            return response
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc

    @router.post("/v1/rag/index")
    async def rag_index(payload: IndexBrowseRequest, request: Request) -> dict:
        rag_service = request.app.state.rag_service
        try:
            return rag_service.browse_index(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/rag/source")
    async def rag_source(payload: IndexSourceRequest, request: Request) -> dict:
        rag_service = request.app.state.rag_service
        try:
            return rag_service.get_source_chunks(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/eval/run")
    async def eval_run(payload: EvalRunRequest, request: Request) -> dict:
        runner = request.app.state.eval_runner
        try:
            report = await runner.run_dataset(
                dataset_path=payload.dataset_path,
                project=payload.project,
                source_path=payload.source_path,
                top_k=payload.top_k,
                with_answer=payload.with_answer,
            )
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc
        return report.model_dump()

    @router.get("/v1/eval/preferences/sessions")
    async def preference_sessions(request: Request) -> dict:
        try:
            sessions = request.app.state.preference_service.list_sessions()
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return {"sessions": sessions}

    @router.post("/v1/eval/preferences/sessions")
    async def preference_session_create(
        payload: CreatePreferenceSessionRequest,
        request: Request,
    ) -> dict:
        try:
            return request.app.state.preference_service.create_session(payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/eval/preferences/sessions/{session_id}/generate")
    async def preference_generate(
        session_id: str,
        payload: GeneratePreferencePairsRequest,
        request: Request,
    ) -> dict:
        try:
            return await request.app.state.preference_service.generate(session_id, payload.limit)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        except RuntimeError as exc:
            raise HTTPException(status_code=502, detail=str(exc)) from exc

    @router.get("/v1/eval/preferences/sessions/{session_id}/next")
    async def preference_next(session_id: str, request: Request) -> dict:
        try:
            pair = request.app.state.preference_service.next_pair(session_id)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return {"pair": pair.model_dump(mode="json") if pair is not None else None}

    @router.post("/v1/eval/preferences/pairs/{pair_id}/vote")
    async def preference_vote(
        pair_id: str,
        payload: SubmitPreferenceVoteRequest,
        request: Request,
    ) -> dict:
        try:
            vote = request.app.state.preference_service.vote(pair_id, payload)
            pair = request.app.state.preference_service.store.get_pair(pair_id)
            stats = request.app.state.preference_service.stats(pair.session_id)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return {"vote_id": vote.vote_id, "pair_id": pair_id, "stats": stats}

    @router.get("/v1/eval/preferences/sessions/{session_id}/stats")
    async def preference_stats(session_id: str, request: Request) -> dict:
        try:
            return request.app.state.preference_service.stats(session_id)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/eval/preferences/sessions/{session_id}/export")
    async def preference_export(
        session_id: str,
        payload: ExportPreferenceRequest,
        request: Request,
    ) -> dict:
        try:
            return request.app.state.preference_service.export(session_id, payload)
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc

    @router.post("/v1/admin/reload")
    async def reload_config(request: Request) -> dict:
        try:
            reload_result = request.app.state.reload_gateway_state()
            if inspect.isawaitable(reload_result):
                await reload_result
        except ValueError as exc:
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return {
            "status": "reloaded",
            "configured_models": sorted(request.app.state.app_config.models.keys()),
        }

    return router
