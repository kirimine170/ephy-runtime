"""One optional Pydantic AI loop；Karte scope and candidate adoption stay in Runtime．"""

import asyncio
import time
from packages.runtime_core.participation import MemoryTools, Candidate


def parse_candidate(value):
    value = value.strip()
    if value.startswith("```"):
        value = value.split("\n", 1)[1].rsplit("```", 1)[0].strip()
    return Candidate.model_validate_json(value)


async def run_agent(tools, query, endpoint, model, instructions):
    from openai import AsyncOpenAI
    import httpx2
    from urllib.parse import urlparse

    url = urlparse(endpoint)
    if (
        url.scheme != "http"
        or url.hostname not in {"127.0.0.1", "::1"}
        or url.path != "/v1"
        or url.username
        or url.password
        or url.query
        or url.fragment
    ):
        raise ValueError("explicit_loopback_v1_required")
    from pydantic_ai import Agent, Tool, CancellationToken
    from pydantic_ai.models.openai import OpenAIChatModel
    from pydantic_ai.providers.openai import OpenAIProvider
    from pydantic_ai.usage import UsageLimits
    from pydantic_ai.messages import ToolCallPart

    client = AsyncOpenAI(
        base_url=endpoint,
        api_key="local-only",
        max_retries=0,
        timeout=60,
        http_client=httpx2.AsyncClient(trust_env=False, follow_redirects=False),
    )

    async def search(ctx, query: str):
        return await ctx.deps._execute("karte_search", {"query": query})

    async def read(ctx, doc_id: str):
        return await ctx.deps._execute("karte_read", {"doc_id": doc_id})

    class CountedModel(OpenAIChatModel):
        async def request(self, *args, **kwargs):
            tools.run.model_call()
            response = await super().request(*args, **kwargs)
            for part in response.parts:
                if isinstance(part, ToolCallPart):
                    # Invalid JSON and SDK validation failures consume the same
                    # finite attempt budget before any tool can execute．
                    tools.run.tool_call(part.tool_name, part.args_as_dict())
            return response

    agent = Agent(
        CountedModel(model, provider=OpenAIProvider(openai_client=client)),
        deps_type=MemoryTools,
        output_type=str,
        instructions=instructions,
        retries=0,
        tools=[
            Tool(search, name="karte_search", sequential=True, takes_ctx=True),
            Tool(read, name="karte_read", sequential=True, takes_ctx=True),
        ],
        model_settings={
            "temperature": 0.3,
            "max_tokens": 2048,
            "parallel_tool_calls": False,
            "extra_body": {
                "chat_template_kwargs": {
                    "enable_thinking": True,
                    "preserve_thinking": True,
                }
            },
        },
    )
    agent.instrument = False
    cancellation = CancellationToken()
    try:
        async with asyncio.timeout(max(0.001, tools.run.deadline - time.monotonic())):
            result = await agent.run(
                query,
                deps=tools,
                cancellation_token=cancellation,
                usage_limits=UsageLimits(request_limit=4, tool_calls_limit=6),
            )
        tools.run.models = result.usage.requests
        return tools.validate_candidate(parse_candidate(result.output))
    except asyncio.CancelledError:
        cancellation.cancel()
        tools.run.canceled = True
        raise
    finally:
        await client.close()
