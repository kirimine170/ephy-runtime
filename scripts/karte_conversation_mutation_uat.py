#!/usr/bin/env python3
"""Run Ephy proposal -> Karte review -> receipt UAT for append，collision，and reject."""

from __future__ import annotations

import argparse
from datetime import UTC, datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import uuid


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from packages.karte_core.context import KarteContextClient  # noqa: E402
from packages.karte_core.conversation import (  # noqa: E402
    KarteConversationRequest,
    KarteConversationService,
)


APPEND_DOC_ID = "doc:uat-conversation-append"
APPEND_RELATIVE_PATH = Path("content/projects/ephy/decision/2026-09/existing-context.md")
OCCUPANT_DOC_ID = "doc:uat-collision-occupant"
OCCURRED_AT = "2026-09-02T12:30:00+09:00"
REVISION_PATTERN = re.compile(r"^[0-9a-f]{40}$")
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
KARTE_PROVENANCE_FILENAME = "karte-build-provenance.json"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--karte-executable",
        type=Path,
        default=REPOSITORY_ROOT / "data/runtime/karte/Karte.app/Contents/MacOS/karte",
    )
    parser.add_argument("--karte-repository", type=Path, default=REPOSITORY_ROOT.parent / "karte")
    parser.add_argument("--workspace", type=Path, help="Use and preserve this empty UAT workspace.")
    parser.add_argument("--report", type=Path, help="Write the metadata-only JSON trace to this path.")
    parser.add_argument("--startup-timeout", type=float, default=15.0)
    parser.add_argument("--exchange-timeout", type=float, default=5.0)
    return parser.parse_args(argv)


def _conversation_request(
    *, conversation_id: str, resolution: str, kind: str, intended_doc_id: str | None = None
) -> KarteConversationRequest:
    topic = {
        "append": "既存のEphyとKarteの連携方針へ，review receipt確認を追記したい",
        "collision": "同名のKarte方針文書を新規作成し，doc_idで安全に分ける",
        "reject": "採用しない一時的なKarteメモ案を作る",
    }[conversation_id.rsplit("-", 1)[-1]]
    return KarteConversationRequest.model_validate(
        {
            "conversation_id": conversation_id,
            "messages": [
                {"role": "user", "content": topic},
                {
                    "role": "assistant",
                    "content": f"{topic}方針をまとめます．project優先，年月単位で管理します．",
                },
            ],
            "occurred_at": OCCURRED_AT,
            "project": "ephy",
            "kind": kind,
            "sensitivity": "internal",
            "tags": ["synthetic-uat", "cross-app"],
            "resolution": resolution,
            "intended_doc_id": intended_doc_id,
        }
    )


def prepare_workspace(data_root: Path) -> None:
    if data_root.exists() and any(data_root.iterdir()):
        raise ValueError("UAT workspace must be empty")
    data_root.mkdir(parents=True, exist_ok=True)
    append_target = data_root / APPEND_RELATIVE_PATH
    append_target.parent.mkdir(parents=True, exist_ok=True)
    append_target.write_text(
        "---\n"
        f'doc_id: "{APPEND_DOC_ID}"\n'
        'title: "Ephy and Karte context policy"\n'
        "project: ephy\n"
        "kind: decision\n"
        "tags:\n"
        "  - existing\n"
        "sensitivity: internal\n"
        "---\n"
        "# Ephy and Karte context policy\n\n"
        "Karte owns canonical context and Ephy proposes reviewed changes.\n",
        encoding="utf-8",
    )
    policy_path = data_root / ".mdsys/context/v1/policy.json"
    _atomic_write_json(
        policy_path,
        {
            "protocol_version": "1.0",
            "actors": {
                "ephy": {"sensitivity_ceiling": "internal", "projects": ["ephy"]},
                "human": {"sensitivity_ceiling": "restricted", "projects": ["*"]},
            },
        },
    )


def _publish_reviewed(service: KarteConversationService, request: KarteConversationRequest):
    plan = service.plan(request)
    if not plan.publishable:
        raise RuntimeError(f"synthetic UAT plan unexpectedly needs consultation: {plan.reasons}")
    published = service.publish(request.model_copy(update={"reviewed_plan_sha256": plan.plan_sha256}))
    if published.state != "pending" or published.candidate_id != plan.candidate_id:
        raise RuntimeError("reviewed Ephy proposal did not reach Karte pending outbox")
    return plan


def _write_collision_occupant(data_root: Path, plan) -> Path:
    placement = plan.proposal.placement
    primary = (
        data_root
        / "content"
        / "projects"
        / placement.project
        / placement.kind
        / placement.year_month
        / placement.preferred_filename
    )
    primary.parent.mkdir(parents=True, exist_ok=True)
    primary.write_text(
        "---\n"
        f'doc_id: "{OCCUPANT_DOC_ID}"\n'
        'title: "Existing same-name document"\n'
        "project: ephy\n"
        f"kind: {placement.kind}\n"
        "sensitivity: internal\n"
        "---\n"
        "# Existing same-name document\n\nThis canonical file must not be overwritten.\n",
        encoding="utf-8",
    )
    return primary


def _verify_karte_artifact_revision(executable: Path) -> str:
    if executable.parent.name != "MacOS" or executable.parent.parent.name != "Contents":
        raise ValueError("Karte executable must be inside a macOS app bundle")
    app_bundle = executable.parents[2]
    provenance_path = executable.parent.parent / "Resources" / KARTE_PROVENANCE_FILENAME
    try:
        provenance = json.loads(provenance_path.read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError, OSError) as exc:
        raise ValueError("Karte artifact lacks readable embedded build provenance") from exc
    revision = str(provenance.get("source_revision") or "")
    target = str(provenance.get("target") or "")
    if (
        provenance.get("schema_version") != "1.0"
        or not REVISION_PATTERN.fullmatch(revision)
        or not target.startswith("darwin-")
    ):
        raise ValueError("Karte artifact build provenance is invalid")
    if sys.platform == "darwin":
        completed = subprocess.run(
            ["codesign", "--verify", "--deep", "--verbose=2", str(app_bundle)],
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )
        if completed.returncode != 0:
            detail = (completed.stdout + "\n" + completed.stderr).strip()
            raise RuntimeError(f"Karte artifact code signature is invalid: {detail[-2000:]}")
    return revision


def _run_karte_review_bridge(karte_repository: Path, data_root: Path, expected_revision: str) -> str:
    repository = karte_repository.expanduser().resolve(strict=True)
    if not (repository / "go.mod").is_file():
        raise ValueError("Karte repository does not contain go.mod")
    if not REVISION_PATTERN.fullmatch(expected_revision):
        raise ValueError("Karte artifact revision is invalid")
    with tempfile.TemporaryDirectory(prefix="ephy-karte-mutation-uat-source-") as temporary_source:
        clean_repository = Path(temporary_source) / "karte"
        clone = subprocess.run(
            ["git", "clone", "--quiet", "--no-hardlinks", "--no-checkout", str(repository), str(clean_repository)],
            text=True,
            capture_output=True,
            timeout=60,
            check=False,
        )
        if clone.returncode != 0:
            raise RuntimeError(f"could not materialize clean Karte source: {clone.stderr.strip()[-2000:]}")
        checkout = subprocess.run(
            ["git", "checkout", "--quiet", "--detach", expected_revision],
            cwd=clean_repository,
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )
        if checkout.returncode != 0:
            raise RuntimeError(f"could not checkout Karte UAT revision: {checkout.stderr.strip()[-2000:]}")
        source_dist = repository / "frontend/dist"
        if not source_dist.is_dir():
            raise ValueError("Karte frontend/dist is required to compile the Wails review bridge")
        shutil.copytree(source_dist, clean_repository / "frontend/dist", dirs_exist_ok=True)
        environment = dict(os.environ)
        environment["KARTE_MUTATION_UAT_ROOT"] = str(data_root.resolve())
        environment.setdefault("GOCACHE", str(Path(tempfile.gettempdir()) / "ephy-karte-mutation-uat-gocache"))
        environment.setdefault("GOPATH", str(Path(tempfile.gettempdir()) / "ephy-karte-mutation-uat-gopath"))
        completed = subprocess.run(
            ["go", "test", "-run", "TestEphyMutationUATBridge", "-count=1", "."],
            cwd=clean_repository,
            env=environment,
            text=True,
            capture_output=True,
            timeout=180,
            check=False,
        )
        if completed.returncode != 0:
            detail = (completed.stdout + "\n" + completed.stderr).strip()
            raise RuntimeError(f"Karte review bridge failed: {detail[-4000:]}")
        return expected_revision


def _verify_reject_canonical_check(data_root: Path, candidate_id: str) -> dict:
    evidence_path = data_root / ".mdsys/ephy/mutation-uat-canonical-check.json"
    try:
        evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError, OSError) as exc:
        raise RuntimeError("Karte review bridge did not emit reject canonical-tree evidence") from exc
    before_sha256 = str(evidence.get("before_sha256") or "")
    after_sha256 = str(evidence.get("after_sha256") or "")
    before_count = evidence.get("before_count")
    after_count = evidence.get("after_count")
    if (
        evidence.get("schema_version") != "1.0"
        or evidence.get("candidate_id") != candidate_id
        or not isinstance(before_count, int)
        or not isinstance(after_count, int)
        or not SHA256_PATTERN.fullmatch(before_sha256)
        or not SHA256_PATTERN.fullmatch(after_sha256)
    ):
        raise RuntimeError("reject canonical-tree evidence is invalid")
    if (
        evidence.get("tree_unchanged") is not True
        or before_count != after_count
        or before_sha256 != after_sha256
    ):
        raise RuntimeError("reject changed canonical Markdown")
    return {
        "candidate_id": candidate_id,
        "before_count": before_count,
        "after_count": after_count,
        "before_sha256": before_sha256,
        "after_sha256": after_sha256,
        "tree_unchanged": True,
    }


def _verify_receipts(
    *, service: KarteConversationService, data_root: Path, plans: dict[str, object], client: KarteContextClient
) -> dict:
    append_status = service.status(plans["append"].candidate_id)
    collision_status = service.status(plans["collision"].candidate_id)
    reject_status = service.status(plans["reject"].candidate_id)
    if append_status.state != "accepted" or append_status.receipt is None:
        raise RuntimeError("Ephy did not read the accepted append receipt")
    if collision_status.state != "accepted" or collision_status.receipt is None:
        raise RuntimeError("Ephy did not read the accepted collision receipt")
    if reject_status.state != "rejected" or reject_status.receipt is None:
        raise RuntimeError("Ephy did not read the rejected receipt")

    append_receipt = append_status.receipt
    collision_receipt = collision_status.receipt
    reject_receipt = reject_status.receipt
    if append_receipt.doc_id != APPEND_DOC_ID or append_receipt.relative_path != APPEND_RELATIVE_PATH.as_posix():
        raise RuntimeError("append receipt changed canonical identity")
    append_content = (data_root / append_receipt.relative_path).read_text(encoding="utf-8")
    if plans["append"].summary_markdown not in append_content or "Karte owns canonical context" not in append_content:
        raise RuntimeError("append acceptance lost existing or proposed content")

    if collision_receipt.doc_id is None or collision_receipt.relative_path is None:
        raise RuntimeError("collision receipt lacks canonical identity")
    collision_path = data_root / collision_receipt.relative_path
    expected_suffix = f"--{collision_receipt.doc_id[:8]}.md"
    if not collision_receipt.relative_path.endswith(expected_suffix) or not collision_path.is_file():
        raise RuntimeError("same-name collision did not receive the doc_id prefix suffix")
    occupant_path = (
        data_root
        / "content"
        / "projects"
        / plans["collision"].proposal.placement.project
        / plans["collision"].proposal.placement.kind
        / plans["collision"].proposal.placement.year_month
        / plans["collision"].proposal.placement.preferred_filename
    )
    if OCCUPANT_DOC_ID not in occupant_path.read_text(encoding="utf-8"):
        raise RuntimeError("same-name collision overwrote the existing canonical document")

    if reject_receipt.result != "rejected" or reject_receipt.resulting_sha256 is not None:
        raise RuntimeError("reject receipt implies a canonical write")
    if reject_receipt.relative_path and (data_root / reject_receipt.relative_path).exists():
        raise RuntimeError("rejected proposal created canonical content")

    append_read = client.read(APPEND_DOC_ID, projects=["ephy"], tags=[], sensitivity_ceiling="internal")
    collision_read = client.read(
        collision_receipt.doc_id, projects=["ephy"], tags=[], sensitivity_ceiling="internal"
    )
    if append_read.status != "ok" or append_read.document is None:
        raise RuntimeError("accepted append is not readable through Karte Personal Context")
    if collision_read.status != "ok" or collision_read.document is None:
        raise RuntimeError("accepted collision document is not readable through Karte Personal Context")
    if append_read.document.sha256 != append_receipt.resulting_sha256:
        raise RuntimeError("append receipt SHA does not match Personal Context read")
    if collision_read.document.sha256 != collision_receipt.resulting_sha256:
        raise RuntimeError("collision receipt SHA does not match Personal Context read")

    return {
        "append": _receipt_report(append_receipt),
        "collision": _receipt_report(collision_receipt),
        "reject": _receipt_report(reject_receipt),
    }


def _receipt_report(receipt) -> dict:
    return {
        "candidate_id": receipt.candidate_id,
        "result": receipt.result,
        "doc_id": receipt.doc_id,
        "relative_path": receipt.relative_path,
        "resulting_sha256": receipt.resulting_sha256,
    }


def run_uat(args: argparse.Namespace) -> dict:
    executable = args.karte_executable.expanduser().resolve(strict=True)
    if not executable.is_file() or not os.access(executable, os.X_OK) or executable.is_symlink():
        raise ValueError("Karte executable must be an executable regular file")
    karte_artifact_revision = _verify_karte_artifact_revision(executable)
    temporary = args.workspace is None
    data_root = (
        Path(tempfile.mkdtemp(prefix="ephy-karte-mutation-uat-"))
        if temporary
        else args.workspace.expanduser().resolve(strict=False)
    )
    process: subprocess.Popen | None = None
    log_handle = None
    started_at = datetime.now(tz=UTC)
    try:
        prepare_workspace(data_root)
        log_handle = (data_root / "karte-native.log").open("wb")
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
        service = KarteConversationService(data_root, context_client=client)
        plans = {
            "append": _publish_reviewed(
                service,
                _conversation_request(
                    conversation_id="conversation-uat-append",
                    resolution="append",
                    kind="decision",
                    intended_doc_id=APPEND_DOC_ID,
                ),
            ),
            "collision": _publish_reviewed(
                service,
                _conversation_request(
                    conversation_id="conversation-uat-collision", resolution="create", kind="decision"
                ),
            ),
            "reject": _publish_reviewed(
                service,
                _conversation_request(
                    conversation_id="conversation-uat-reject", resolution="create", kind="note"
                ),
            ),
        }
        _write_collision_occupant(data_root, plans["collision"])
        manifest = {
            "schema_version": "1.0",
            "actions": [
                {"candidate_id": plans["append"].candidate_id, "decision": "accept"},
                {"candidate_id": plans["collision"].candidate_id, "decision": "accept"},
                {"candidate_id": plans["reject"].candidate_id, "decision": "reject"},
            ],
        }
        _atomic_write_json(data_root / ".mdsys/ephy/mutation-uat-manifest.json", manifest)
        karte_repository_revision = _run_karte_review_bridge(
            args.karte_repository, data_root, karte_artifact_revision
        )
        reject_canonical_tree = _verify_reject_canonical_check(
            data_root, plans["reject"].candidate_id
        )
        receipts = _verify_receipts(service=service, data_root=data_root, plans=plans, client=client)
        report = {
            "trace_version": "1.0",
            "started_at": started_at.isoformat(),
            "completed_at": datetime.now(tz=UTC).isoformat(),
            "workspace_sha256": hashlib.sha256(str(data_root.resolve()).encode()).hexdigest(),
            "karte_executable": str(executable),
            "karte_artifact_revision": karte_artifact_revision,
            "karte_repository_revision": karte_repository_revision,
            "review_surface": "Karte App ListEphyProposals／AcceptEphyProposal／RejectEphyProposal",
            "steps": [
                "Ephy planned and published reviewed append／create／reject candidates",
                "Karte resolved append and same-name create placement for human review",
                "Karte accepted append and collision，and rejected the third proposal",
                "Karte artifact signature and embedded revision matched the review bridge source",
                "Reject review left the complete canonical Markdown tree unchanged",
                "Ephy re-read every receipt and Karte Personal Context re-read accepted documents",
            ],
            "reject_canonical_tree": reject_canonical_tree,
            "receipts": receipts,
        }
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
    except (OSError, RuntimeError, TimeoutError, ValueError, subprocess.SubprocessError) as exc:
        print(f"Karte conversation mutation UAT failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
