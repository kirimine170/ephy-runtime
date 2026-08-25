from packages.config_core.loader import load_app_config
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage, RequestMetadata
from packages.router_core.router import ModelRouter


def test_explicit_mode_routing() -> None:
    router = ModelRouter(load_app_config())
    payload = ChatCompletionRequest(
        model="auto",
        metadata=RequestMetadata(mode="work"),
        messages=[ChatMessage(role="user", content="設計を整理して")],
    )

    decision = router.route_chat(payload)

    assert decision.mode == "work"
    assert decision.model_alias == "work"
    assert decision.selected_model.model == "qwen3-30b-a3b"


def test_auto_routing_prefers_code_for_implementation_requests() -> None:
    router = ModelRouter(load_app_config())
    payload = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="Implement a Python function and add pytest coverage.")],
    )

    decision = router.route_chat(payload)

    assert decision.mode == "code"
    assert decision.model_alias == "code"
    assert decision.selected_model.model == "qwen3.8-27b"


def test_auto_routing_prefers_work_for_long_non_code_requests() -> None:
    router = ModelRouter(load_app_config())
    payload = ChatCompletionRequest(
        model="auto",
        messages=[
            ChatMessage(
                role="user",
                content=(
                    "以下の議事録を要約して、論点、決定事項、残課題、次のアクションを整理してください。\n\n"
                    + ("A案とB案の比較検討を進める。関係者への共有方法と実施順も含めて整理する。\n" * 30)
                ),
            )
        ],
    )

    decision = router.route_chat(payload)

    assert decision.mode == "work"
    assert decision.model_alias == "work"


def test_auto_routing_prefers_rag_for_source_grounded_requests() -> None:
    router = ModelRouter(load_app_config())
    payload = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="Based on the project documents and source notes, answer with evidence.")],
    )

    decision = router.route_chat(payload)

    assert decision.mode == "rag"
    assert decision.model_alias == "work"


def test_auto_routing_uses_embedding_assist_for_ambiguous_prompt(monkeypatch) -> None:
    router = ModelRouter(load_app_config())
    monkeypatch.setattr(router, "_embed_text", lambda text, cache_key=None: {
        "Please help me compare two approaches for our service rollout.": [1.0, 0.0],
        "prototype:fast:Give a concise direct answer to a short general question.": [0.0, 1.0],
        "prototype:fast:Answer a simple greeting or short factual question in a few sentences.": [0.0, 1.0],
        "prototype:work:Prepare a design proposal with tradeoffs, planning steps, and a structured summary.": [1.0, 0.0],
        "prototype:work:Summarize a long meeting transcript and organize key decisions and next steps.": [1.0, 0.0],
        "prototype:code:Implement a function, debug a traceback, refactor code, and add tests.": [0.0, -1.0],
        "prototype:code:Analyze source code, explain a bug, and propose a concrete patch.": [0.0, -1.0],
        "prototype:rag:Answer based on provided documents, sources, notes, and citations.": [-1.0, 0.0],
        "prototype:rag:Use project documents and source material as grounding before answering.": [-1.0, 0.0],
    }.get(text if cache_key is None else cache_key))
    payload = ChatCompletionRequest(
        model="auto",
        messages=[ChatMessage(role="user", content="Please help me compare two approaches for our service rollout.")],
    )

    decision = router.route_chat(payload)

    assert decision.mode == "work"
    assert decision.model_alias == "work"
