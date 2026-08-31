from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path

from packages.config_core.loader import load_app_config
from packages.karte_core.service import export_karte_bundle, import_karte_bundle
from packages.karte_core.contracts import KarteChangeProposal
from packages.karte_core.outbox import KarteOutbox
from packages.karte_core.source import KarteSourceAdapter
from packages.karte_core.watcher import KarteWatchService
from packages.runtime_core.smoke import SmokeRunner
from packages.runtime_core.watch import run_watch_loop
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.rag_core.schemas import IngestRequest, RAGQueryRequest, SearchRequest
from packages.rag_core.service import RagService
from packages.eval_core.runner import EvalRunner
from packages.eval_core.preference_schemas import (
    CreatePreferenceSessionRequest,
    ExportPreferenceRequest,
    GenerationParameters,
)
from packages.eval_core.preference_service import PreferenceService
from packages.prompt_core.loader import PromptManager
from packages.profile_core.runtime import load_ephy_context
from packages.router_core.router import ModelRouter


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="local-llm-workbench")
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

    preference = subparsers.add_parser("preference")
    preference_commands = preference.add_subparsers(dest="preference_command", required=True)

    preference_generate = preference_commands.add_parser("generate")
    preference_generate.add_argument("--dataset", required=True)
    preference_generate.add_argument("--role", choices=("fast", "work", "code"), default="fast")
    preference_generate.add_argument("--count", type=int, default=20, choices=range(1, 101))
    preference_generate.add_argument("--prefetch", type=int, default=4, choices=range(1, 11))
    preference_generate.add_argument(
        "--comparison",
        choices=("same_prompt", "prompt_v1_v2", "prompt_v2_v3", "base_vs_adapter"),
        default="same_prompt",
    )
    preference_generate.add_argument("--temperature", type=float, default=0.8)
    preference_generate.add_argument("--top-p", type=float, default=0.95)
    preference_generate.add_argument("--seed", type=int)
    preference_generate.add_argument("--max-tokens", type=int, default=512)
    preference_generate.add_argument("--adapter-scale", type=float, default=1.0)

    preference_stats = preference_commands.add_parser("stats")
    preference_stats.add_argument("--session", required=True)

    preference_export = preference_commands.add_parser("export")
    preference_export.add_argument("--session", required=True)
    preference_export.add_argument("--format", choices=("dpo", "sft"), required=True)
    preference_export.add_argument("--output", required=True)

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

    karte_index = subparsers.add_parser("karte-index")
    karte_index.add_argument("--data-dir", required=True)
    karte_index.add_argument("--project", default="karte")
    karte_index.add_argument("--tags", nargs="*", default=[])

    karte_watch = subparsers.add_parser("karte-watch")
    karte_watch.add_argument("--data-dir", required=True)
    karte_watch.add_argument("--project", default="karte")
    karte_watch.add_argument("--tags", nargs="*", default=[])
    karte_watch.add_argument("--interval", type=float, default=2.0)
    karte_watch.add_argument("--debounce", type=float, default=0.25)
    karte_watch.add_argument("--cycles", type=int)
    karte_watch.add_argument("--max-events", type=int, default=256)

    karte_propose = subparsers.add_parser("karte-propose")
    karte_propose.add_argument("proposal_path")
    karte_propose.add_argument("--data-dir", required=True)

    karte_receipts = subparsers.add_parser("karte-receipts")
    karte_receipts.add_argument("--data-dir", required=True)

    return parser


async def run_async(args: argparse.Namespace) -> int:
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

    if args.command == "preference":
        adapter = LlamaCppChatAdapter()
        service = PreferenceService(
            config=config,
            prompt_manager=PromptManager(ephy_context=load_ephy_context(config.ephy)),
            adapter=adapter,
        )
        try:
            if args.preference_command == "generate":
                generation_parameters = GenerationParameters(
                    temperature=args.temperature,
                    top_p=args.top_p,
                    seed=args.seed,
                    max_tokens=args.max_tokens,
                )
                session = service.create_session(
                    CreatePreferenceSessionRequest(
                        dataset_path=args.dataset,
                        model_role=args.role,
                        pair_count=args.count,
                        prefetch=args.prefetch,
                        comparison_mode=args.comparison,
                        adapter_scale=args.adapter_scale,
                        generation_parameters=generation_parameters,
                    )
                )
                await service.generate(session["session_id"], args.count)
                payload = {
                    "session": session,
                    "stats": service.stats(session["session_id"]),
                }
            elif args.preference_command == "stats":
                payload = service.stats(args.session)
            else:
                payload = service.export(
                    args.session,
                    ExportPreferenceRequest(format=args.format, output=args.output),
                )
        finally:
            await adapter.aclose()
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

    if args.command == "karte-index":
        payload = rag_service.ingest_karte(
            KarteSourceAdapter(args.data_dir),
            project=args.project,
            tags=args.tags,
        )
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    if args.command == "karte-watch":
        watcher = KarteWatchService(
            adapter=KarteSourceAdapter(args.data_dir),
            rag_service=rag_service,
            project=args.project,
            tags=args.tags,
            interval_seconds=args.interval,
            debounce_seconds=args.debounce,
            max_events=args.max_events,
        )
        watcher.full_rescan()
        cycles = 0
        try:
            while args.cycles is None or cycles < args.cycles:
                cycles += 1
                time.sleep(args.interval)
                watcher.poll_once()
        except KeyboardInterrupt:
            pass
        print(json.dumps({"health": watcher.health(), "events": watcher.events()}, ensure_ascii=False, indent=2))
        return 0

    if args.command == "karte-propose":
        proposal = KarteChangeProposal.model_validate_json(
            Path(args.proposal_path).read_text(encoding="utf-8")
        )
        result = KarteOutbox(args.data_dir).publish(proposal)
        print(json.dumps(result.__dict__, ensure_ascii=False, indent=2))
        return 0

    if args.command == "karte-receipts":
        receipts = [receipt.model_dump(mode="json") for receipt in KarteOutbox(args.data_dir).list_receipts()]
        print(json.dumps(receipts, ensure_ascii=False, indent=2))
        return 0

    return 1


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    import asyncio

    return asyncio.run(run_async(args))


if __name__ == "__main__":
    sys.exit(main())
