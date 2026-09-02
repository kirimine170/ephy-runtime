#!/usr/bin/env python3
"""Run a native Karte permission-denied -> policy-update -> retry trace."""

from __future__ import annotations

import argparse
from datetime import UTC, datetime
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import uuid


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from packages.karte_core.context import ContextResponse, KarteContextClient  # noqa: E402


DOC_ID = "doc:uat-permission-retry"
RELATIVE_PATH = Path("content/projects/ephy/note/2026-09/permission-retry.md")
SYNTHETIC_BODY = "This synthetic restricted body is available only after the policy retry."


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--karte-executable",
        type=Path,
        default=REPOSITORY_ROOT / "data/runtime/karte/Karte.app/Contents/MacOS/karte",
    )
    parser.add_argument("--workspace", type=Path, help="Use and preserve this empty UAT workspace.")
    parser.add_argument("--report", type=Path, help="Write the metadata-only JSON trace to this path.")
    parser.add_argument("--startup-timeout", type=float, default=15.0)
    parser.add_argument("--exchange-timeout", type=float, default=5.0)
    return parser.parse_args(argv)


def prepare_workspace(data_root: Path) -> None:
    if data_root.exists() and any(data_root.iterdir()):
        raise ValueError("UAT workspace must be empty")
    target = data_root / RELATIVE_PATH
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        "---\n"
        f'doc_id: "{DOC_ID}"\n'
        'title: "Permission retry fixture"\n'
        "project: ephy\n"
        "kind: note\n"
        "tags:\n"
        "  - synthetic-uat\n"
        "sensitivity: restricted\n"
        "---\n"
        f"{SYNTHETIC_BODY}\n",
        encoding="utf-8",
    )
    write_ephy_policy(data_root, sensitivity_ceiling="internal")


def write_ephy_policy(data_root: Path, *, sensitivity_ceiling: str) -> None:
    if sensitivity_ceiling not in {"internal", "restricted"}:
        raise ValueError("UAT policy ceiling must be internal or restricted")
    policy_path = data_root / ".mdsys/context/v1/policy.json"
    policy_path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "protocol_version": "1.0",
        "actors": {
            "ephy": {"sensitivity_ceiling": sensitivity_ceiling, "projects": ["ephy"]},
            "human": {"sensitivity_ceiling": "restricted", "projects": ["*"]},
        },
    }
    _atomic_write_json(policy_path, payload)


def validate_denied_response(response: ContextResponse) -> None:
    if response.status != "denied" or response.document is not None or response.results:
        raise RuntimeError("denied response disclosed content or returned an unexpected status")


def validate_retry_response(response: ContextResponse) -> None:
    document = response.document
    if response.status != "ok" or document is None:
        raise RuntimeError("policy retry did not return the synthetic document")
    if document.doc_id != DOC_ID or document.relative_path != RELATIVE_PATH.as_posix():
        raise RuntimeError("policy retry returned a different document identity")
    if SYNTHETIC_BODY not in document.body or document.sensitivity != "restricted":
        raise RuntimeError("policy retry returned incomplete synthetic content")


def build_report(
    *,
    data_root: Path,
    executable: Path,
    process_id: int,
    denied: ContextResponse,
    retried: ContextResponse,
    started_at: datetime,
) -> dict:
    document = retried.document
    assert document is not None
    return {
        "trace_version": "1.0",
        "started_at": started_at.isoformat(),
        "completed_at": datetime.now(tz=UTC).isoformat(),
        "karte_executable": str(executable.resolve()),
        "workspace": str(data_root.resolve()),
        "karte_pid": process_id,
        "steps": [
            {"name": "default_ephy_policy", "status": denied.status, "content_disclosed": False},
            {"name": "restricted_policy_retry", "status": retried.status, "content_disclosed": True},
        ],
        "document": {
            "doc_id": document.doc_id,
            "relative_path": document.relative_path,
            "sensitivity": document.sensitivity,
            "sha256": document.sha256,
        },
    }


def run_uat(args: argparse.Namespace) -> dict:
    executable = args.karte_executable.expanduser().resolve(strict=True)
    if not executable.is_file() or not os.access(executable, os.X_OK) or executable.is_symlink():
        raise ValueError("Karte executable must be an executable regular file")

    temporary = args.workspace is None
    data_root = (
        Path(tempfile.mkdtemp(prefix="ephy-karte-permission-retry-"))
        if temporary
        else args.workspace.expanduser().resolve(strict=False)
    )
    process: subprocess.Popen | None = None
    log_handle = None
    started_at = datetime.now(tz=UTC)
    try:
        data_root.mkdir(parents=True, exist_ok=True)
        prepare_workspace(data_root)
        log_path = data_root / "karte-native.log"
        log_handle = log_path.open("wb")
        environment = dict(os.environ)
        environment["KARTE_DATA_DIR"] = str(data_root.resolve())
        process = subprocess.Popen(
            [str(executable)],
            cwd=str(executable.parent),
            env=environment,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
        )
        _wait_for_runtime(data_root, process, timeout_seconds=args.startup_timeout)

        client = KarteContextClient(data_root, timeout_seconds=args.exchange_timeout)
        denied = client.read(DOC_ID, projects=["ephy"], tags=[], sensitivity_ceiling="restricted")
        validate_denied_response(denied)

        write_ephy_policy(data_root, sensitivity_ceiling="restricted")
        retried = client.read(DOC_ID, projects=["ephy"], tags=[], sensitivity_ceiling="restricted")
        validate_retry_response(retried)

        report = build_report(
            data_root=data_root,
            executable=executable,
            process_id=process.pid,
            denied=denied,
            retried=retried,
            started_at=started_at,
        )
        if args.report is not None:
            _atomic_write_json(args.report.expanduser(), report)
        return report
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if log_handle is not None:
            log_handle.close()
        if temporary:
            shutil.rmtree(data_root, ignore_errors=True)


def _wait_for_runtime(data_root: Path, process: subprocess.Popen, *, timeout_seconds: float) -> None:
    marker = data_root / ".mdsys/runtime/karte.pid"
    deadline = time.monotonic() + max(1.0, timeout_seconds)
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"Karte exited before startup with code {process.returncode}")
        try:
            if int(marker.read_text(encoding="utf-8").strip()) == process.pid:
                return
        except (FileNotFoundError, ValueError, OSError):
            pass
        time.sleep(0.05)
    raise TimeoutError("Karte did not publish its data-root runtime identity")


def _atomic_write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    data = (json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")
    try:
        with temporary.open("xb") as file_obj:
            os.chmod(temporary, 0o600)
            file_obj.write(data)
            file_obj.flush()
            os.fsync(file_obj.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    try:
        report = run_uat(parse_args(argv))
    except (OSError, RuntimeError, TimeoutError, ValueError) as exc:
        print(f"Karte permission retry UAT failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
