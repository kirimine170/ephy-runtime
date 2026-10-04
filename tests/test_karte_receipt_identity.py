from __future__ import annotations

import json
from pathlib import Path

import pytest

from packages.karte_core.contracts import KarteChangeProposal, KarteReceipt
from packages.karte_core.conversation import KarteConversationService
from packages.karte_core.outbox import KarteOutbox


def _fixture(name: str) -> dict:
    path = Path("schemas/karte-ephy/v1/fixtures") / name
    return json.loads(path.read_text(encoding="utf-8"))


def _snapshot(root: Path) -> dict[str, bytes]:
    return {path.relative_to(root).as_posix(): path.read_bytes() for path in root.rglob("*") if path.is_file()}


@pytest.mark.parametrize("result", ["accepted", "rejected", "conflict", "invalid"])
@pytest.mark.parametrize("operation", ["read", "status", "publish", "list"])
def test_mismatched_receipt_is_rejected_without_mutation(tmp_path: Path, result: str, operation: str) -> None:
    (tmp_path / "content").mkdir()
    (tmp_path / "content/canonical.md").write_bytes(b"# Synthetic canonical content\n")
    proposal = KarteChangeProposal.model_validate(_fixture("append-proposal.json"))
    payload = _fixture("accepted-receipt.json")
    payload.update(candidate_id="candidate-other-001", result=result)
    KarteReceipt.model_validate(payload)
    service = KarteConversationService(tmp_path)
    receipt_path = service.outbox.receipts_dir / f"{proposal.candidate_id}.json"
    receipt_path.write_text(json.dumps(payload), encoding="utf-8")
    pending_path = service.outbox.pending_dir / f"{proposal.candidate_id}.json"
    pending_path.write_text(proposal.model_dump_json(), encoding="utf-8")
    before = _snapshot(tmp_path)

    for client in (service, KarteConversationService(tmp_path)):
        with pytest.raises(ValueError, match="receipt candidate_id does not match filename"):
            if operation == "read":
                client.outbox.read_receipt(proposal.candidate_id)
            elif operation == "status":
                client.status(proposal.candidate_id)
            elif operation == "publish":
                client.outbox.publish(proposal)
            else:
                client.outbox.list_receipts()
        assert _snapshot(tmp_path) == before


def test_receipt_identity_is_case_sensitive(tmp_path: Path) -> None:
    outbox = KarteOutbox(tmp_path)
    payload = _fixture("accepted-receipt.json")
    candidate_id = payload["candidate_id"].upper()
    (outbox.receipts_dir / f"{candidate_id}.json").write_text(json.dumps(payload), encoding="utf-8")

    with pytest.raises(ValueError, match="receipt candidate_id does not match filename"):
        outbox.read_receipt(candidate_id)
    with pytest.raises(ValueError, match="receipt candidate_id does not match filename"):
        outbox.list_receipts()


@pytest.mark.parametrize("result", ["accepted", "rejected", "conflict", "invalid"])
def test_receipt_identity_uses_actual_filename_case(tmp_path: Path, result: str) -> None:
    service = KarteConversationService(tmp_path)
    payload = _fixture("accepted-receipt.json")
    actual_id = payload["candidate_id"]
    requested_id = actual_id.upper()
    payload.update(candidate_id=requested_id, result=result)
    (service.outbox.receipts_dir / f"{actual_id}.json").write_text(json.dumps(payload), encoding="utf-8")
    proposal_payload = _fixture("append-proposal.json")
    proposal_payload["candidate_id"] = requested_id
    proposal = KarteChangeProposal.model_validate(proposal_payload)
    before = _snapshot(tmp_path)

    for client in (service, KarteConversationService(tmp_path)):
        if (client.outbox.receipts_dir / f"{requested_id}.json").exists():
            for action in (
                lambda: client.outbox.read_receipt(requested_id),
                lambda: client.status(requested_id),
                lambda: client.outbox.publish(proposal),
            ):
                with pytest.raises(ValueError, match="receipt candidate_id does not match filename"):
                    action()
        else:
            assert client.outbox.read_receipt(requested_id) is None
            assert client.status(requested_id).state == "missing"
        with pytest.raises(ValueError, match="receipt candidate_id does not match filename"):
            client.outbox.list_receipts()
        assert _snapshot(tmp_path) == before


@pytest.mark.parametrize("result", ["accepted", "rejected", "conflict", "invalid"])
def test_matching_receipt_survives_restart_and_publish_retry(tmp_path: Path, result: str) -> None:
    proposal = KarteChangeProposal.model_validate(_fixture("append-proposal.json"))
    payload = _fixture("accepted-receipt.json")
    payload["result"] = result
    receipt = KarteReceipt.model_validate(payload)
    service = KarteConversationService(tmp_path)
    (service.outbox.receipts_dir / f"{proposal.candidate_id}.json").write_text(json.dumps(payload), encoding="utf-8")
    (service.outbox.receipts_dir / ".partial.json").write_text("{", encoding="utf-8")
    before = _snapshot(tmp_path)

    for client in (service, KarteConversationService(tmp_path)):
        assert client.outbox.read_receipt(proposal.candidate_id) == receipt
        assert client.outbox.list_receipts() == [receipt]
        status = client.status(proposal.candidate_id)
        assert status.candidate_id == proposal.candidate_id
        assert status.state == (result if result in {"accepted", "rejected"} else "processed")
        assert status.receipt == receipt
        assert client.outbox.publish(proposal).state == "processed"
        assert _snapshot(tmp_path) == before
