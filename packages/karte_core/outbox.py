from __future__ import annotations

import json
import os
import uuid
from dataclasses import dataclass
from pathlib import Path

from .contracts import KarteChangeProposal, KarteReceipt


@dataclass(frozen=True)
class ProposalPublishResult:
    candidate_id: str
    state: str
    path: str


class KarteOutbox:
    """Ephy-owned proposal writer and read-only receipt client."""

    def __init__(self, karte_data_dir: str | Path) -> None:
        self.data_root = Path(karte_data_dir).expanduser().resolve(strict=True)
        if not self.data_root.is_dir():
            raise ValueError("KARTE_DATA_DIR must be a directory")
        self.outbox_root = self._checked_path(Path(".mdsys/ephy/outbox"))
        self.pending_dir = self._checked_path(Path(".mdsys/ephy/outbox/pending"))
        self.accepted_dir = self._checked_path(Path(".mdsys/ephy/outbox/accepted"))
        self.rejected_dir = self._checked_path(Path(".mdsys/ephy/outbox/rejected"))
        self.receipts_dir = self._checked_path(Path(".mdsys/ephy/outbox/receipts"))
        for directory in (self.pending_dir, self.accepted_dir, self.rejected_dir, self.receipts_dir):
            directory.mkdir(parents=True, exist_ok=True)
            self._assert_within_root(directory)

    def publish(self, proposal: KarteChangeProposal) -> ProposalPublishResult:
        proposal.require_publishable()
        receipt = self.read_receipt(proposal.candidate_id)
        if receipt is not None:
            return ProposalPublishResult(proposal.candidate_id, "processed", str(self._receipt_path(proposal.candidate_id)))
        payload = proposal.model_dump(mode="json")
        for state, directory in (("pending", self.pending_dir), ("accepted", self.accepted_dir), ("rejected", self.rejected_dir)):
            existing = directory / f"{proposal.candidate_id}.json"
            if not existing.exists():
                continue
            if self._read_json_file(existing) != payload:
                raise ValueError("candidate_id already exists with different proposal content")
            return ProposalPublishResult(proposal.candidate_id, state, str(existing))
        destination = self.pending_dir / f"{proposal.candidate_id}.json"
        _atomic_write_json(destination, payload)
        return ProposalPublishResult(proposal.candidate_id, "pending", str(destination))

    def read_receipt(self, candidate_id: str) -> KarteReceipt | None:
        KarteChangeProposal.validate_candidate_id(candidate_id)
        path = self._receipt_path(candidate_id)
        if not path.exists():
            return None
        return KarteReceipt.model_validate(self._read_json_file(path))

    def list_receipts(self) -> list[KarteReceipt]:
        return [KarteReceipt.model_validate(self._read_json_file(path)) for path in sorted(self.receipts_dir.glob("*.json")) if not path.name.startswith(".")]

    def _receipt_path(self, candidate_id: str) -> Path:
        return self.receipts_dir / f"{candidate_id}.json"

    def _checked_path(self, relative: Path) -> Path:
        candidate = self.data_root / relative
        self._assert_within_root(candidate)
        return candidate

    def _assert_within_root(self, candidate: Path) -> None:
        try:
            candidate.resolve(strict=False).relative_to(self.data_root)
        except ValueError as exc:
            raise ValueError("outbox path escapes KARTE_DATA_DIR") from exc

    def _read_json_file(self, path: Path) -> dict:
        if path.is_symlink() or not path.is_file():
            raise ValueError("outbox JSON must be a regular file")
        self._assert_within_root(path)
        return json.loads(path.read_text(encoding="utf-8"))


def _atomic_write_json(destination: Path, payload: dict) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    temp_path = destination.parent / f".{destination.name}.{uuid.uuid4().hex}.tmp"
    data = (json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")
    try:
        with temp_path.open("xb") as file_obj:
            file_obj.write(data)
            file_obj.flush()
            os.fsync(file_obj.fileno())
        os.replace(temp_path, destination)
        directory_fd = os.open(destination.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass
