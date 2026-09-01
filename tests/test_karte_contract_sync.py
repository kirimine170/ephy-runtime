from __future__ import annotations

from pathlib import Path
import shutil

from scripts.check_karte_contract import CONTRACT_RELATIVE_ROOT, compare_contracts


SOURCE_CONTRACT = Path(__file__).resolve().parents[1] / CONTRACT_RELATIVE_ROOT


def _repository_with_contract(root: Path) -> Path:
    destination = root / CONTRACT_RELATIVE_ROOT
    destination.parent.mkdir(parents=True)
    shutil.copytree(SOURCE_CONTRACT, destination)
    return root


def test_cross_repository_contract_accepts_identical_files(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")

    assert compare_contracts(ephy_root, karte_root) == []


def test_cross_repository_contract_reports_byte_drift_without_content(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")
    proposal_path = karte_root / CONTRACT_RELATIVE_ROOT / "fixtures" / "create-proposal.json"
    proposal_path.write_bytes(proposal_path.read_bytes().replace(b"Synthetic placement decision", b"Changed fixture", 1))

    errors = compare_contracts(ephy_root, karte_root)

    assert len(errors) == 1
    assert errors[0].startswith("contract drift: fixtures/create-proposal.json Ephy=")
    assert "Synthetic placement decision" not in errors[0]


def test_cross_repository_contract_reports_missing_file(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")
    (karte_root / CONTRACT_RELATIVE_ROOT / "receipt.schema.json").unlink()

    errors = compare_contracts(ephy_root, karte_root)

    assert "Karte contract is missing receipt.schema.json" in errors
    assert "Karte contract is missing required file receipt.schema.json" in errors
