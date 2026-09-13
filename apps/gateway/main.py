from contextlib import asynccontextmanager
import hashlib
import os

from fastapi import FastAPI

from packages.config_core.loader import load_app_config, reload_app_config
from packages.eval_core.runner import EvalRunner
from packages.eval_core.preference_service import PreferenceService
from packages.eval_core.resident_service import ResidentService
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.identity_core.service import IdentityService
from packages.karte_core.conversation import KarteConversationService
from packages.karte_core.context import KarteContextClient
from packages.prompt_core.loader import PromptManager
from packages.profile_core.runtime import load_ephy_context
from packages.rag_core.service import RagService
from packages.router_core.router import ModelRouter
from packages.web_search_core.service import WebSearchService
from .routes import build_router
from .resident_routes import ResidentPrivacyMiddleware, resident_router
from .resident_participation import participation_router
from .model_transition import InferenceGate, InferenceGateMiddleware, transition_router


def initialize_app_state(app: FastAPI, config) -> None:
    context = load_ephy_context(config.ephy)
    previous = getattr(app.state, "ephy_context", None)
    if previous is not None and context is not None:
        if IdentityService().compare_immutable(previous.identity, context.identity):
            raise ValueError("Ephy immutable identity cannot change during reload")
    prompt_manager = PromptManager(ephy_context=context)
    karte_context_client = KarteContextClient.from_environment()
    # Build the entire replacement before publishing any state．
    replacement = {
        "app_config": config,
        "ephy_context": context,
        "model_router": ModelRouter(config=config),
        "prompt_manager": prompt_manager,
        "rag_service": RagService(config=config, prompt_manager=prompt_manager),
        "eval_runner": EvalRunner(config=config),
        "preference_service": PreferenceService(
            config=config,
            prompt_manager=prompt_manager,
            adapter=app.state.chat_adapter,
        ),
        "web_search_service": WebSearchService(config=config, adapter=app.state.chat_adapter),
        "karte_conversation_service": KarteConversationService.from_environment(
            context_client=karte_context_client,
        ),
        "karte_context_client": karte_context_client,
        "resident_service": None,
    }
    if os.environ.get("EPHY_RESIDENT_ENABLED") == "1":
        instance_id = str(context.identity.identity.instance_id) if context else os.environ.get("EPHY_RESIDENT_INSTANCE_ID", "resident-local")
        owner_reference = context.identity.ownership.owner_reference if context and context.identity.ownership else "selected-owner"
        replacement["resident_service"] = ResidentService(
            store=replacement["preference_service"].store,
            instance_id=instance_id,
            owner_key=hashlib.sha256(owner_reference.encode()).hexdigest(),
        )
    for name, value in replacement.items():
        setattr(app.state, name, value)


async def reload_gateway_state(app: FastAPI) -> None:
    existing = getattr(app.state, "web_search_service", None)
    config = reload_app_config()
    initialize_app_state(app, config)
    if existing is not None:
        await existing.aclose()


@asynccontextmanager
async def lifespan(app: FastAPI):
    adapter = LlamaCppChatAdapter()
    app.state.chat_adapter = adapter
    app.state.ephy_context = None
    app.state.inference_gate = InferenceGate()
    app.state.web_search_service = None
    try:
        initialize_app_state(app, load_app_config())
        app.state.reload_gateway_state = lambda: reload_gateway_state(app)
        yield
    finally:
        if app.state.web_search_service is not None:
            await app.state.web_search_service.aclose()
        await adapter.aclose()


app = FastAPI(title="Local LLM Workbench Gateway", version="0.1.0", lifespan=lifespan)
app.include_router(build_router())
app.include_router(resident_router)
app.include_router(participation_router)
app.include_router(transition_router)
app.add_middleware(InferenceGateMiddleware)
app.add_middleware(ResidentPrivacyMiddleware)
