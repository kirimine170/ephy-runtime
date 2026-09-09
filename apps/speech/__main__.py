"""Run separately from Gateway，using an explicitly provisioned inference venv．"""

from __future__ import annotations

import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import sys

from .qwen import MODEL_REVISION, SOURCE_REVISION, QwenAdapter
from .schemas import SpeechError, error_code


def load_private_json(value: str) -> dict:
    from packages.speech_assets import SpeechAssetError, read_private_json_file
    try:
        return read_private_json_file(Path(value), max_bytes=64 * 1024)
    except SpeechAssetError:
        raise SpeechError("tts_asset_invalid") from None


@contextmanager
def quiet_inference():
    saved = (os.dup(1), os.dup(2))
    with open(os.devnull, "w") as sink:
        try:
            os.dup2(sink.fileno(), 1)
            os.dup2(sink.fileno(), 2)
            yield
        finally:
            sys.stdout.flush()
            sys.stderr.flush()
            for target, source in zip((1, 2), saved):
                os.dup2(source, target)
                os.close(source)


def main() -> int:
    if sys.argv[1:] == ["--worker"]:
        from .worker import run_worker
        return run_worker()
    parser = argparse.ArgumentParser(description="Independent local phrase speech service")
    sub = parser.add_subparsers(dest="command", required=True)
    serve = sub.add_parser("serve")
    serve.add_argument("--config")
    serve.add_argument("--port", type=int, default=8767)
    prepare = sub.add_parser("prepare")
    prepare.add_argument("--config", required=True)
    prepare.add_argument("--reference-id", required=True)
    register = sub.add_parser("import-reference")
    register.add_argument("--asset-store", required=True)
    register.add_argument("--audio", required=True)
    register.add_argument("--transcript", required=True)
    register.add_argument("--consent", required=True)
    group = sub.add_parser("group-references")
    group.add_argument("--asset-store", required=True)
    group.add_argument("--reference-id", action="append", required=True)
    args = parser.parse_args()
    try:
        if args.command == "serve":
            if not 1024 <= args.port <= 65535:
                raise SpeechError("invalid_speech_text")
            config = load_private_json(args.config) if args.config else {}
            from .service import create_app
            import uvicorn
            uvicorn.run(create_app(config), host="127.0.0.1", port=args.port, access_log=False,
                        log_level="critical", limit_concurrency=8, timeout_keep_alive=2)
            return 0
        if args.command == "import-reference":
            from packages.speech_assets import SpeechAssetStore
            consent = load_private_json(args.consent)
            asset = SpeechAssetStore(Path(args.asset_store)).register_reference(
                Path(args.audio), Path(args.transcript), consent=consent)
            result = {"reference_id": asset.reference_id, "provenance_id": asset.provenance_id}
        elif args.command == "group-references":
            from packages.speech_assets import SpeechAssetStore
            asset = SpeechAssetStore(Path(args.asset_store)).store_reference_group(args.reference_id)
            result = asset.public_metadata()
        else:
            config = load_private_json(args.config)
            from .service import provider_config
            qwen_config = provider_config(config, "qwen3-tts")
            if qwen_config.get("source_revision") != SOURCE_REVISION or qwen_config.get("model_revision") != MODEL_REVISION:
                raise SpeechError("tts_model_revision_mismatch")
            with quiet_inference():
                asset = QwenAdapter(qwen_config).prepare(args.reference_id)
            result = {"reference_id": asset.reference_id, "clone_prompt_digest": asset.prompt_digest}
        print(json.dumps(result, ensure_ascii=True, allow_nan=False))
        return 0
    except Exception as exc:
        print(json.dumps({"error_code": error_code(exc)}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
