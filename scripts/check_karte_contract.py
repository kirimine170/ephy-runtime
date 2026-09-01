#!/usr/bin/env python3
"""Verify that Ephy and Karte publish the same V1 filesystem contract."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import sys


CONTRACT_RELATIVE_ROOT = Path("schemas/karte-ephy/v1")
REQUIRED_FILES = {
    Path("proposal.schema.json"),
    Path("receipt.schema.json"),
    Path("fixtures/accepted-receipt.json"),
    Path("fixtures/create-proposal.json"),
    Path("fixtures/invalid-traversal-proposal.json"),
    Path("fixtures/append-proposal.json"),
    Path("fixtures/consultation-proposal.json"),
}


def compare_contracts(ephy_root: Path, karte_root: Path) -> list[str]:
    ephy_files, ephy_errors = _load_contract(ephy_root, "Ephy")
    karte_files, karte_errors = _load_contract(karte_root, "Karte")
    errors = [*ephy_errors, *karte_errors]

    ephy_names = set(ephy_files)
    karte_names = set(karte_files)
    for relative_path in sorted(ephy_names - karte_names):
        errors.append(f"Karte contract is missing {relative_path.as_posix()}")
    for relative_path in sorted(karte_names - ephy_names):
        errors.append(f"Karte contract has unexpected file {relative_path.as_posix()}")
    for relative_path in sorted(ephy_names & karte_names):
        ephy_bytes = ephy_files[relative_path]
        karte_bytes = karte_files[relative_path]
        if ephy_bytes != karte_bytes:
            errors.append(
                f"contract drift: {relative_path.as_posix()} "
                f"Ephy={_sha256(ephy_bytes)} Karte={_sha256(karte_bytes)}"
            )
    return errors


def _load_contract(repository_root: Path, label: str) -> tuple[dict[Path, bytes], list[str]]:
    contract_root = repository_root.resolve() / CONTRACT_RELATIVE_ROOT
    if not contract_root.is_dir():
        return {}, [f"{label} contract directory is missing: {CONTRACT_RELATIVE_ROOT.as_posix()}"]

    files: dict[Path, bytes] = {}
    errors: list[str] = []
    for path in sorted(contract_root.rglob("*.json")):
        relative_path = path.relative_to(contract_root)
        data = path.read_bytes()
        try:
            json.loads(data)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            errors.append(f"{label} contract JSON is invalid: {relative_path.as_posix()}: {exc}")
            continue
        files[relative_path] = data

    for relative_path in sorted(REQUIRED_FILES - set(files)):
        errors.append(f"{label} contract is missing required file {relative_path.as_posix()}")
    return files, errors


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ephy-root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--karte-root", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    errors = compare_contracts(args.ephy_root, args.karte_root)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    count = len(list((args.ephy_root.resolve() / CONTRACT_RELATIVE_ROOT).rglob("*.json")))
    print(f"Karte-Ephy V1 contract matches across repositories: {count} JSON files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
