from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

import pytest

from packages.karte_core.contracts import KarteChangeProposal
from packages.karte_core.conversation import KarteConversationService


ROOT = Path(__file__).resolve().parents[1]
MISMATCH = "receipt candidate_id does not match filename"


def _fixture(name: str) -> dict:
    return json.loads((ROOT / "schemas/karte-ephy/v1/fixtures" / name).read_text(encoding="utf-8"))


def _snapshot(root: Path) -> dict[str, bytes]:
    return {path.relative_to(root).as_posix(): path.read_bytes() for path in root.rglob("*") if path.is_file()}


@pytest.fixture(scope="module")
def karte_receipt_driver(tmp_path_factory: pytest.TempPathFactory) -> Path:
    configured = os.environ.get("EPHY_TEST_KARTE_ROOT")
    if not configured:
        pytest.skip("set EPHY_TEST_KARTE_ROOT to the receipt identity companion checkout")
    if sys.platform != "linux":
        pytest.skip("the production Runtime directory fsync writer requires Linux for this gate")
    karte_root = Path(configured).resolve(strict=True)
    module_root = tmp_path_factory.mktemp("karte_receipt_driver")
    shutil.copyfile(ROOT / "tests/fixtures/karte_receipt_identity/main.go", module_root / "main.go")
    go_version = next(line for line in (karte_root / "go.mod").read_text(encoding="utf-8").splitlines() if line.startswith("go "))
    (module_root / "go.mod").write_text(
        f"module karte/receiptidentityverification\n\n{go_version}\n\n"
        f"require karte v0.0.0\n\nreplace karte => {json.dumps(karte_root.as_posix())}\n",
        encoding="utf-8",
    )
    binary = module_root / "karte-receipt-driver"
    built = subprocess.run(
        ["go", "build", "-mod=mod", "-o", str(binary), "."],
        cwd=module_root, capture_output=True, text=True, timeout=120,
    )
    assert built.returncode == 0, built.stdout + built.stderr
    return binary


@pytest.mark.parametrize("result", ["accepted", "rejected", "conflict", "invalid"])
def test_karte_runtime_receipt_identity_round_trip(tmp_path: Path, karte_receipt_driver: Path, result: str) -> None:
    (tmp_path / ".receipt-identity-fixture").write_bytes(b"synthetic\n")
    (tmp_path / "content").mkdir()
    canonical = tmp_path / "content/canonical.md"
    canonical.write_bytes(b"# Synthetic canonical content\n")
    service = KarteConversationService(tmp_path)
    proposal = KarteChangeProposal.model_validate(_fixture("append-proposal.json"))
    assert service.outbox.publish(proposal).state == "pending"
    receipt = _fixture("accepted-receipt.json")
    receipt["result"] = result
    other = {**receipt, "candidate_id": "candidate-other-001"}

    def go(action: str, candidate_id: str, payload: dict | None = None, *, expected: int = 0) -> str:
        completed = subprocess.run(
            [str(karte_receipt_driver), action, str(tmp_path), candidate_id],
            input=json.dumps(payload) if payload is not None else None,
            capture_output=True, text=True, timeout=10,
        )
        assert completed.returncode == expected, completed.stdout + completed.stderr
        return completed.stdout if expected == 0 else completed.stderr.strip()

    def fresh_runtime(mode: str) -> None:
        code = """
import json, sys
sys.path[:0] = json.loads(sys.argv[1])
from packages.karte_core.contracts import KarteChangeProposal
from packages.karte_core.conversation import KarteConversationService
service = KarteConversationService(sys.argv[2])
proposal = KarteChangeProposal.model_validate_json(sys.argv[3])
actions = [lambda: service.outbox.read_receipt(proposal.candidate_id),
           lambda: service.status(proposal.candidate_id),
           lambda: service.outbox.publish(proposal), service.outbox.list_receipts]
if sys.argv[4] == "mismatch":
    for action in actions:
        try:
            action()
        except ValueError as exc:
            assert str(exc) == "receipt candidate_id does not match filename"
        else:
            raise AssertionError("mismatched receipt was accepted")
else:
    receipt = actions[0]()
    assert receipt.candidate_id == proposal.candidate_id and receipt.result == sys.argv[5]
    status = actions[1]()
    assert status.receipt == receipt
    assert status.state == (receipt.result if receipt.result in {"accepted", "rejected"} else "processed")
    assert actions[2]().state == "processed"
    assert {item.candidate_id for item in actions[3]()} == {proposal.candidate_id, "candidate-other-001"}
"""
        completed = subprocess.run(
            [sys.executable, "-B", "-c", code, json.dumps(sys.path), str(tmp_path), proposal.model_dump_json(), mode, result],
            capture_output=True, text=True, timeout=15,
        )
        assert completed.returncode == 0, completed.stdout + completed.stderr

    go("write", other["candidate_id"], other)
    receipt_path = service.outbox.receipts_dir / f"{proposal.candidate_id}.json"
    receipt_path.write_bytes((service.outbox.receipts_dir / f"{other['candidate_id']}.json").read_bytes())
    before = _snapshot(tmp_path)
    assert go("read", proposal.candidate_id, expected=3) == MISMATCH
    fresh_runtime("mismatch")
    assert _snapshot(tmp_path) == before
    receipt_path.unlink()
    go("write", proposal.candidate_id, receipt)
    before = _snapshot(tmp_path)
    go("write", proposal.candidate_id, receipt)
    assert json.loads(go("read", proposal.candidate_id)) == receipt
    fresh_runtime("matching")
    fresh_runtime("matching")
    assert _snapshot(tmp_path) == before
    assert canonical.read_bytes() == b"# Synthetic canonical content\n"
