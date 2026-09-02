from __future__ import annotations

from pathlib import Path
import shutil

from scripts.check_karte_contract import CONTRACT_SPECS, compare_contracts


SOURCE_ROOT = Path(__file__).resolve().parents[1]


def _repository_with_contract(root: Path) -> Path:
    for relative_root in CONTRACT_SPECS:
        destination = root / relative_root
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(SOURCE_ROOT / relative_root, destination)
    return root


def test_cross_repository_contract_accepts_identical_files(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")

    assert compare_contracts(ephy_root, karte_root) == []


def test_cross_repository_contract_reports_byte_drift_without_content(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")
    proposal_path = karte_root / "schemas/karte-ephy/v1/fixtures/create-proposal.json"
    proposal_path.write_bytes(proposal_path.read_bytes().replace(b"Synthetic placement decision", b"Changed fixture", 1))

    errors = compare_contracts(ephy_root, karte_root)

    assert len(errors) == 1
    assert errors[0].startswith("contract drift: schemas/karte-ephy/v1/fixtures/create-proposal.json Ephy=")
    assert "Synthetic placement decision" not in errors[0]


def test_cross_repository_contract_reports_missing_file(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")
    (karte_root / "schemas/karte-ephy/v1/receipt.schema.json").unlink()

    errors = compare_contracts(ephy_root, karte_root)

    assert "Karte contract is missing schemas/karte-ephy/v1/receipt.schema.json" in errors
    assert "Karte contract is missing required file schemas/karte-ephy/v1/receipt.schema.json" in errors


def test_context_policy_and_audit_schemas_are_required(tmp_path: Path) -> None:
    ephy_root = _repository_with_contract(tmp_path / "ephy")
    karte_root = _repository_with_contract(tmp_path / "karte")
    (karte_root / "schemas/karte-context/v1/policy.schema.json").unlink()
    (karte_root / "schemas/karte-context/v1/audit.schema.json").unlink()

    errors = compare_contracts(ephy_root, karte_root)

    assert "Karte contract is missing schemas/karte-context/v1/policy.schema.json" in errors
    assert "Karte contract is missing required file schemas/karte-context/v1/policy.schema.json" in errors
    assert "Karte contract is missing schemas/karte-context/v1/audit.schema.json" in errors
    assert "Karte contract is missing required file schemas/karte-context/v1/audit.schema.json" in errors
