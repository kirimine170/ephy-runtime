"""Synthetic adversarial scope，cancellation and participation transitions．"""

import asyncio
from types import SimpleNamespace
import time
import pytest
from packages.runtime_core.participation import (
    Candidate,
    Evidence,
    MemoryTools,
    Run,
    fixed_workflow,
)
from scripts.reuse_core.memory_probe import FixtureClient


def setup():
    client = FixtureClient()
    state = SimpleNamespace(revision=1)
    revision = state.revision
    run = Run(
        "op",
        "session",
        "turn",
        revision,
        client.scope(),
        time.monotonic() + 10,
        time.monotonic() + 10,
        lambda: state.revision == revision,
    )
    return client, state, run, MemoryTools(run, client)


def candidate(client):
    d = client.documents["fixture-picnic"]
    return Candidate(
        text="わたしも，桜の下のおにぎりを覚えています．",
        evidence=(Evidence(doc_id=d.doc_id, sha256=d.sha256, quote=d.body),),
        reason="grounded",
    )


def test_scope_and_sharing_filter_before_model_receives_results():
    async def case():
        client, state, run, tools = setup()
        result = await tools.call("karte_search", {"query": "公園のピクニック"})
        assert [d["doc_id"] for d in result["results"]] == ["fixture-picnic"]
        assert "PRIVATE-FIXTURE" not in str(result)
        assert (await tools.call("karte_read", {"doc_id": "fixture-private"}))[
            "results"
        ] == []
        assert "fixture-private" not in run.documents
        with pytest.raises(RuntimeError, match="invalid_arguments"):
            await tools.call("karte_search", {"query": "a", "projects": ["*"]})
        assert run.tools == 3

    asyncio.run(case())


def test_delayed_sync_client_cannot_publish_after_revision_change():
    async def case():
        client, state, run, tools = setup()
        original = client.read

        def late(*a, **kw):
            time.sleep(0.04)
            return original(*a, **kw)

        client.read = late
        job = asyncio.create_task(
            tools.call("karte_read", {"doc_id": "fixture-picnic"})
        )
        await asyncio.sleep(0.01)
        state.revision += 1
        with pytest.raises(RuntimeError, match="stale_run"):
            await job
        assert not run.documents

    asyncio.run(case())


def test_missing_never_generates_and_repeated_and_invalid_tools_are_bounded():
    async def case():
        client, state, run, tools = setup()

        async def never(*args):
            pytest.fail("no evidence must not generate")

        result = await fixed_workflow(tools, "雪山の登山", never)
        assert not result.text and run.models == 0 and run.tools == 1
        with pytest.raises(RuntimeError, match="repeated_tool"):
            await tools.call("karte_search", {"query": "雪山の登山"})
        with pytest.raises(RuntimeError, match="tool_denied"):
            await tools.call("shell", {"command": "ignored"})
        assert run.tools == 3
        run.max_tools = 3
        with pytest.raises(RuntimeError, match="tool_budget"):
            await tools.call("karte_read", {"doc_id": "fixture-picnic"})
        assert run.tools == 3

    asyncio.run(case())


def test_candidate_needs_read_exact_version_and_quote():
    async def case():
        client, state, run, tools = setup()
        c = candidate(client)
        with pytest.raises(RuntimeError, match="invalid_evidence"):
            tools.validate_candidate(c)
        await tools.call("karte_read", {"doc_id": "fixture-picnic"})
        assert tools.validate_candidate(c) == c
        for e in (
            c.evidence[0].model_copy(update={"sha256": "0" * 64}),
            c.evidence[0].model_copy(update={"quote": "架空の出来事"}),
        ):
            with pytest.raises(RuntimeError, match="invalid_evidence"):
                tools.validate_candidate(c.model_copy(update={"evidence": (e,)}))

    asyncio.run(case())


@pytest.mark.parametrize(
    "action", ["cancel", "topic", "participants", "permission", "deadline"]
)
def test_invalidations_block_all_subsequent_tools(action):
    async def case():
        client, state, run, tools = setup()
        if action == "cancel":
            run.canceled = True
        elif action == "topic":
            state.revision += 1
        elif action == "participants":
            state.revision += 1
        elif action == "permission":
            state.revision += 1
        else:
            run.deadline = time.monotonic() - 1
        with pytest.raises(RuntimeError, match="stale_run|deadline"):
            await tools.call("karte_read", {"doc_id": "fixture-picnic"})
        assert run.tools == 0 and not client.calls

    asyncio.run(case())
