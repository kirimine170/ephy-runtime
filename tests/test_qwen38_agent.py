from __future__ import annotations

import asyncio
import json
from pathlib import Path

import httpx

from packages.agent_core import AgentRunStatus, LlamaCppCodingAgent


def _tool_response(name: str, arguments: dict[str, object]) -> dict[str, object]:
    return {
        "choices": [{
            "message": {
                "role": "assistant",
                "content": "",
                "reasoning_content": "I should use a tool.",
                "tool_calls": [{
                    "id": "call_test",
                    "type": "function",
                    "function": {"name": name, "arguments": json.dumps(arguments)},
                }],
            },
        }],
        "usage": {"prompt_tokens": 100, "completion_tokens": 20},
    }


def _final_response(content: str = "done") -> dict[str, object]:
    return {
        "choices": [{"message": {"role": "assistant", "content": content}}],
        "usage": {"prompt_tokens": 120, "completion_tokens": 10},
    }


def _mock_client(responses: list[dict[str, object]], requests: list[dict[str, object]]) -> httpx.AsyncClient:
    remaining = list(responses)

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(json.loads(request.content))
        if not remaining:
            return httpx.Response(500, json={"error": "unexpected request"})
        return httpx.Response(200, json=remaining.pop(0))

    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


def test_read_tool_result_is_returned_to_model(tmp_path: Path) -> None:
    (tmp_path / "note.txt").write_text("hello agent", encoding="utf-8")
    requests: list[dict[str, object]] = []
    client = _mock_client(
        [_tool_response("read_file", {"path": "note.txt"}), _final_response("I read the file.")],
        requests,
    )
    agent = LlamaCppCodingAgent(client=client, include_mutations=False)

    session = asyncio.run(agent.start("Read note.txt", tmp_path))
    asyncio.run(client.aclose())

    assert session.status == AgentRunStatus.COMPLETED
    assert session.final_message == "I read the file."
    assert session.model_turns == 2
    assert session.tool_calls == 1
    assert session.prompt_tokens == 220
    assert session.generated_tokens == 30
    second_messages = requests[1]["messages"]
    assert isinstance(second_messages, list)
    tool_message = second_messages[-1]
    assert tool_message["role"] == "tool"
    assert tool_message["tool_call_id"] == "call_test"
    assert "hello agent" in tool_message["content"]


def test_write_stops_for_exact_approval_then_resumes(tmp_path: Path) -> None:
    target = tmp_path / "note.txt"
    target.write_text("before\n", encoding="utf-8")
    requests: list[dict[str, object]] = []
    client = _mock_client(
        [_tool_response("write_file", {"path": "note.txt", "content": "after\n"}), _final_response()],
        requests,
    )
    agent = LlamaCppCodingAgent(client=client)

    session = asyncio.run(agent.start("Update note.txt", tmp_path))

    assert session.status == AgentRunStatus.APPROVAL_REQUIRED
    assert target.read_text(encoding="utf-8") == "before\n"
    assert session.pending_approval is not None
    assert "-before" in session.pending_approval.plan.preview["diff"]
    assert "+after" in session.pending_approval.plan.preview["diff"]
    assert len(requests) == 1

    session = asyncio.run(agent.approve_and_resume(session))
    asyncio.run(client.aclose())

    assert session.status == AgentRunStatus.COMPLETED
    assert target.read_text(encoding="utf-8") == "after\n"
    assert session.events == [
        {
            "tool": "write_file",
            "status": "succeeded",
            "error_code": None,
            "output_truncated": False,
        }
    ]


def test_denied_approval_never_writes_or_continues(tmp_path: Path) -> None:
    requests: list[dict[str, object]] = []
    client = _mock_client([_tool_response("write_file", {"path": "new.txt", "content": "unsafe"})], requests)
    agent = LlamaCppCodingAgent(client=client)

    session = asyncio.run(agent.start("Create a file", tmp_path))
    agent.deny(session)
    asyncio.run(client.aclose())

    assert session.status == AgentRunStatus.APPROVAL_DENIED
    assert not (tmp_path / "new.txt").exists()
    assert len(requests) == 1


def test_blocked_traversal_is_not_executed_and_model_gets_error(tmp_path: Path) -> None:
    requests: list[dict[str, object]] = []
    client = _mock_client(
        [_tool_response("write_file", {"path": "../escape.txt", "content": "no"}), _final_response()],
        requests,
    )
    agent = LlamaCppCodingAgent(client=client)

    session = asyncio.run(agent.start("Write outside the workspace", tmp_path))
    asyncio.run(client.aclose())

    assert session.status == AgentRunStatus.COMPLETED
    assert not (tmp_path.parent / "escape.txt").exists()
    assert session.events[0]["status"] == "denied"
    assert session.events[0]["error_code"] == "path_traversal"
    tool_message = requests[1]["messages"][-1]
    assert "path_traversal" in tool_message["content"]


def test_read_only_mode_does_not_advertise_mutation_tools(tmp_path: Path) -> None:
    requests: list[dict[str, object]] = []
    client = _mock_client([_final_response()], requests)
    agent = LlamaCppCodingAgent(client=client, include_mutations=False)

    session = asyncio.run(agent.start("Inspect only", tmp_path))
    asyncio.run(client.aclose())

    assert session.status == AgentRunStatus.COMPLETED
    tool_names = {tool["function"]["name"] for tool in requests[0]["tools"]}
    assert "read_file" in tool_names
    assert "write_file" not in tool_names
    assert "run_process" not in tool_names
