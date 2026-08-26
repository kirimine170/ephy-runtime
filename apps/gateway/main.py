from contextlib import asynccontextmanager

from fastapi import FastAPI

from packages.config_core.loader import load_app_config, reload_app_config
from packages.eval_core.runner import EvalRunner
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.identity_core.service import IdentityService
from packages.prompt_core.loader import PromptManager
from packages.profile_core.runtime import load_ephy_context
from packages.rag_core.service import RagService
from packages.router_core.router import ModelRouter
from packages.web_search_core.service import WebSearchService
from .routes import build_router
from .model_transition import InferenceGate, InferenceGateMiddleware, transition_router


def initialize_app_state(app: FastAPI, config) -> None:
    context = load_ephy_context(config.ephy)
    previous = getattr(app.state, "ephy_context", None)
    if previous is not None and context is not None:
        if IdentityService().compare_immutable(previous.identity, context.identity):
            raise ValueError("Ephy immutable identity cannot change during reload")
    prompt_manager = PromptManager(ephy_context=context)
    # Build the entire replacement before publishing any state．
    replacement = {
        "app_config": config,
        "ephy_context": context,
        "model_router": ModelRouter(config=config),
        "prompt_manager": prompt_manager,
        "rag_service": RagService(config=config, prompt_manager=prompt_manager),
        "eval_runner": EvalRunner(config=config),
        "web_search_service": WebSearchService(config=config, adapter=app.state.chat_adapter),
    }
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


app = FastAPI(title="Ephy Runtime Gateway", version="0.1.0", lifespan=lifespan)
app.include_router(build_router())
app.include_router(transition_router)
app.add_middleware(InferenceGateMiddleware)
