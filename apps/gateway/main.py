from contextlib import asynccontextmanager

from fastapi import FastAPI

from packages.config_core.loader import load_app_config, reload_app_config
from packages.eval_core.runner import EvalRunner
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.prompt_core.loader import PromptManager
from packages.rag_core.service import RagService
from packages.router_core.router import ModelRouter
from packages.web_search_core.service import WebSearchService
from .routes import build_router


def initialize_app_state(app: FastAPI, config) -> None:
    app.state.app_config = config
    app.state.model_router = ModelRouter(config=config)
    app.state.prompt_manager = PromptManager()
    app.state.rag_service = RagService(config=config)
    app.state.web_search_service = WebSearchService(config=config, adapter=app.state.chat_adapter)
    app.state.eval_runner = EvalRunner(config=config)


async def reload_gateway_state(app: FastAPI) -> None:
    existing = getattr(app.state, "web_search_service", None)
    if existing is not None:
        await existing.aclose()
    config = reload_app_config()
    initialize_app_state(app, config)


@asynccontextmanager
async def lifespan(app: FastAPI):
    config = load_app_config()
    adapter = LlamaCppChatAdapter()
    app.state.chat_adapter = adapter
    initialize_app_state(app, config)
    app.state.reload_gateway_state = lambda: reload_gateway_state(app)
    try:
        yield
    finally:
        await app.state.web_search_service.aclose()
        await adapter.aclose()


app = FastAPI(title="Ephy Runtime Gateway", version="0.1.0", lifespan=lifespan)
app.include_router(build_router())
