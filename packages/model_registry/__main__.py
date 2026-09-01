import argparse
import json
import os
import sys
from pathlib import Path

from .service import ModelRegistry, PORTS, Selection


def launch_command(registry, role, server, fallback_path, fallback_alias):
    selection = registry.selections().roles.get(role)
    adapter = None
    if selection:
        model, adapter = registry.resolve(selection)
        path, alias, context = model.path, model.backend_model, model.context_size
        profile_match = registry.profile_for_model(model)
        gpu_layers = profile_match[1].gpu_layers if profile_match else 99
    else:
        if not Path(fallback_path).is_file():
            raise ValueError("Default model file is missing")
        path, alias, context = fallback_path, fallback_alias, 32768
        gpu_layers = 99
    command = [str(server), "-m", path, "--host", "127.0.0.1", "--port", str(PORTS[role]),
               "--ctx-size", str(context), "--alias", alias, "--n-gpu-layers", str(gpu_layers)]
    if adapter:
        command.extend(["--lora", adapter.path])
    return command


def main():
    parser = argparse.ArgumentParser(description="Ephy local GGUF model registry")
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("list")
    import_parser = commands.add_parser("import")
    import_parser.add_argument("path", type=Path)
    import_parser.add_argument("--id", required=True)
    import_parser.add_argument("--backend-model")
    import_parser.add_argument("--quantization", default="unknown")
    import_parser.add_argument("--context-size", type=int)
    import_parser.add_argument("--profile")
    adapter_parser = commands.add_parser("import-adapter")
    adapter_parser.add_argument("path", type=Path)
    adapter_parser.add_argument("--id", required=True)
    adapter_parser.add_argument("--base-model", required=True)
    for name in ("check", "select"):
        select = commands.add_parser(name)
        select.add_argument("--role", choices=list(PORTS), required=True)
        select.add_argument("--model-id")
        select.add_argument("--adapter-id")
        if name == "select":
            select.add_argument("--expected-revision", required=True)
    download = commands.add_parser("download")
    for name in ("id", "url", "sha256", "revision"):
        download.add_argument(f"--{name}", required=True)
    download.add_argument("--size-bytes", type=int, required=True)
    download.add_argument("--dry-run", action="store_true")
    launch = commands.add_parser("launch")
    launch.add_argument("--role", choices=list(PORTS), required=True)
    launch.add_argument("--server", type=Path, required=True)
    launch.add_argument("--fallback-path", required=True)
    launch.add_argument("--fallback-alias", required=True)
    launch.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    registry = ModelRegistry(args.root)
    try:
        if args.command == "list":
            result = registry.catalog()
        elif args.command == "import":
            result = registry.import_model(args.path, model_id=args.id, backend_model=args.backend_model,
                                           quantization=args.quantization, context_size=args.context_size,
                                           profile_id=args.profile).model_dump()
        elif args.command == "import-adapter":
            result = registry.import_adapter(args.path, adapter_id=args.id, base_model_id=args.base_model).model_dump()
        elif args.command in ("check", "select"):
            if args.adapter_id and not args.model_id:
                raise ValueError("An adapter requires a model")
            selection = Selection(model_id=args.model_id, adapter_id=args.adapter_id) if args.model_id else None
            if args.command == "check":
                if selection:
                    registry.resolve(selection)
                result = {"status": "valid"}
            else:
                result = registry.select(args.role, selection, expected_revision=args.expected_revision)
        elif args.command == "download":
            method = registry.plan_download if args.dry_run else registry.download
            result = method(model_id=args.id, url=args.url, sha256=args.sha256,
                            size_bytes=args.size_bytes, revision=args.revision)
            if not args.dry_run:
                result = result.model_dump()
        else:
            command = launch_command(registry, args.role, args.server, args.fallback_path, args.fallback_alias)
            if args.dry_run:
                result = {"argv": command}
            else:
                if not args.server.is_file() or not os.access(args.server, os.X_OK):
                    raise ValueError("llama-server executable is missing")
                environment = dict(os.environ)
                library_var = "DYLD_LIBRARY_PATH" if sys.platform == "darwin" else "LD_LIBRARY_PATH"
                environment[library_var] = str(args.server.parent) + (
                    ":" + environment[library_var] if environment.get(library_var) else "")
                os.execve(args.server, command, environment)
                return
        print(json.dumps(result, ensure_ascii=False))
    except (ValueError, OSError) as error:
        print(f"Model registry: {error}", file=sys.stderr)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
