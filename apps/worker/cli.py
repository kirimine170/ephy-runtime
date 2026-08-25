from __future__ import annotations

import argparse
import json
import sys

from packages.agent_core import (
    AgentRunStatus,
    DEFAULT_MODEL as DEFAULT_QWEN38_MODEL,
    LlamaCppCodingAgent,
    LlamaCppRequestError,
)
from packages.config_core.loader import load_app_config
from packages.karte_core.service import export_karte_bundle, import_karte_bundle
from packages.runtime_core.smoke import SmokeRunner
from packages.runtime_core.watch import run_watch_loop
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.rag_core.schemas import IngestRequest, RAGQueryRequest, SearchRequest
from packages.rag_core.service import RagService
from packages.eval_core.runner import EvalRunner
from packages.router_core.router import ModelRouter


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="ephy-runtime")
    subparsers = parser.add_subparsers(dest="command", required=True)

    ingest = subparsers.add_parser("ingest")
    ingest.add_argument("paths", nargs="+")
    ingest.add_argument("--project")
    ingest.add_argument("--no-recursive", action="store_true")
    ingest.add_argument("--tags", nargs="*", default=[])

    search = subparsers.add_parser("search")
    search.add_argument("query")
    search.add_argument("--project")
    search.add_argument("--source-path")
    search.add_argument("--tags", nargs="*", default=[])
    search.add_argument("--top-k", type=int, default=5)

    query = subparsers.add_parser("query")
    query.add_argument("query")
    query.add_argument("--project")
    query.add_argument("--source-path")
    query.add_argument("--tags", nargs="*", default=[])
    query.add_argument("--top-k", type=int, default=5)
    query.add_argument("--search-only", action="store_true")

    evaluate = subparsers.add_parser("eval")
    evaluate.add_argument("dataset")
    evaluate.add_argument("--project")
    evaluate.add_argument("--top-k", type=int, default=5)
    evaluate.add_argument("--with-answer", action="store_true")
    evaluate.add_argument("--output")

    smoke = subparsers.add_parser("smoke")
    smoke.add_argument("--gateway-url", default="http://127.0.0.1:8000")
    smoke.add_argument("--skip-qdrant", action="store_true")
    smoke.add_argument("--skip-embedding", action="store_true")
    smoke.add_argument("--skip-reranker", action="store_true")

    watch = subparsers.add_parser("watch")
    watch.add_argument("paths", nargs="+")
    watch.add_argument("--project")
    watch.add_argument("--no-recursive", action="store_true")
    watch.add_argument("--tags", nargs="*", default=[])
    watch.add_argument("--interval", type=float, default=2.0)
    watch.add_argument("--cycles", type=int)

    karte_import = subparsers.add_parser("karte-import")
    karte_import.add_argument("bundle_path")
    karte_import.add_argument("--output-dir", default="data/karte/imported")
    karte_import.add_argument("--default-project")
    karte_import.add_argument("--default-tags", nargs="*", default=[])

    karte_export = subparsers.add_parser("karte-export")
    karte_export.add_argument("output_path")
    karte_export.add_argument("--project")
    karte_export.add_argument("--source-query")
    karte_export.add_argument("--tags", nargs="*", default=[])

    agent = subparsers.add_parser("agent", help="Run the local Qwen3.8 coding-agent PoC")
    agent.add_argument("task")
    agent.add_argument("--workspace", default=".")
    agent.add_argument("--model", default=DEFAULT_QWEN38_MODEL)
    agent.add_argument("--llama-url", default="http://127.0.0.1:8083/v1")
    agent.add_argument("--max-steps", type=int, default=24)
    agent.add_argument("--temperature", type=float, default=0.2)
    agent.add_argument("--reasoning-effort", choices=("low", "medium", "high", "max"), default="medium")
    agent.add_argument("--read-only", action="store_true")
    agent.add_argument("--yes", action="store_true", help="Approve every mutation without an interactive prompt")

    return parser


async def run_async(args: argparse.Namespace) -> int:
    if args.command == "agent":
        return await _run_agent(args)

    config = load_app_config()
    rag_service = RagService(config=config)

    if args.command == "ingest":
        result = rag_service.ingest(
            IngestRequest(
                paths=args.paths,
                project=args.project,
                recursive=not args.no_recursive,
                tags=args.tags,
            )
        )
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    if args.command == "search":
        result = rag_service.search(
            SearchRequest(
                query=args.query,
                project=args.project,
                source_path=args.source_path,
                tags=args.tags,
                top_k=args.top_k,
            )
        )
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    if args.command == "query":
        router = ModelRouter(config=config)
        adapter = LlamaCppChatAdapter()
        try:
            result = await rag_service.query(
                payload=RAGQueryRequest(
                    query=args.query,
                    project=args.project,
                    source_path=args.source_path,
                    tags=args.tags,
                    top_k=args.top_k,
                    answer=not args.search_only,
                ),
                router=router,
                adapter=adapter,
            )
        finally:
            await adapter.aclose()
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    if args.command == "eval":
        runner = EvalRunner(config=config)
        result = await runner.run_dataset(
            dataset_path=args.dataset,
            project=args.project,
            top_k=args.top_k,
            with_answer=args.with_answer,
        )
        payload = result.model_dump()
        if args.output:
            with open(args.output, "w", encoding="utf-8") as file_obj:
                json.dump(payload, file_obj, ensure_ascii=False, indent=2)
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    if args.command == "smoke":
        runner = SmokeRunner(config=config, gateway_url=args.gateway_url)
        payload = runner.run(
            include_qdrant=not args.skip_qdrant,
            include_embedding=not args.skip_embedding,
            include_reranker=not args.skip_reranker,
        )
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0 if payload["ok"] else 1

    if args.command == "watch":
        payload = run_watch_loop(
            rag_service=rag_service,
            paths=args.paths,
            project=args.project,
            recursive=not args.no_recursive,
            tags=args.tags,
            interval_seconds=args.interval,
            max_cycles=args.cycles,
        )
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    if args.command == "karte-import":
        payload = import_karte_bundle(
            rag_service=rag_service,
            bundle_path=args.bundle_path,
            output_dir=args.output_dir,
            default_project=args.default_project,
            default_tags=args.default_tags,
        )
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    if args.command == "karte-export":
        payload = export_karte_bundle(
            rag_service=rag_service,
            output_path=args.output_path,
            project=args.project,
            source_query=args.source_query,
            tags=args.tags,
        )
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    return 1


async def _run_agent(args: argparse.Namespace) -> int:
    def report_event(name: str, payload: dict[str, object]) -> None:
        if name == "model_start":
            print(f"[agent] model turn {payload['turn']}...", file=sys.stderr, flush=True)
        elif name == "tool_start":
            print(f"[agent] tool: {payload['tool']}", file=sys.stderr, flush=True)
        elif name == "tool_result":
            print(
                f"[agent] tool result: {payload['tool']} -> {payload['status']}",
                file=sys.stderr,
                flush=True,
            )

    agent = LlamaCppCodingAgent(
        model=args.model,
        base_url=args.llama_url,
        max_steps=args.max_steps,
        include_mutations=not args.read_only,
        reasoning_effort=args.reasoning_effort,
        temperature=args.temperature,
        event_handler=report_event,
    )
    try:
        session = await agent.start(args.task, args.workspace)
        while session.status == AgentRunStatus.APPROVAL_REQUIRED:
            pending = session.pending_approval
            if pending is None:
                raise RuntimeError("approval state is inconsistent")
            print(
                json.dumps(
                    {
                        "approval_required": pending.public_tool_name,
                        "preview": pending.plan.preview,
                    },
                    ensure_ascii=False,
                    indent=2,
                ),
                file=sys.stderr,
            )
            approved = args.yes
            if not approved and sys.stdin.isatty():
                answer = input("Approve this one action? [y/N] ").strip().casefold()
                approved = answer in {"y", "yes"}
            if not approved:
                agent.deny(session)
                break
            session = await agent.approve_and_resume(session)
    except (LlamaCppRequestError, OSError, ValueError) as exc:
        print(json.dumps({"status": "failed", "error": str(exc)}, ensure_ascii=False, indent=2))
        return 1
    finally:
        await agent.aclose()

    print(json.dumps(session.summary(agent.model), ensure_ascii=False, indent=2))
    if session.status == AgentRunStatus.COMPLETED:
        return 0
    if session.status in {AgentRunStatus.APPROVAL_REQUIRED, AgentRunStatus.APPROVAL_DENIED}:
        return 2
    return 1


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    import asyncio

    return asyncio.run(run_async(args))


if __name__ == "__main__":
    sys.exit(main())
