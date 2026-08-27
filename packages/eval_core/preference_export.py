from __future__ import annotations

import json
import os
import tempfile
from pathlib import Path
from typing import Literal

from .preference_store import PreferenceStore


class PreferenceExporter:
    def __init__(self, store: PreferenceStore) -> None:
        self._store = store

    def export(
        self,
        session_id: str,
        *,
        export_format: Literal["dpo", "sft"],
        output: str,
    ) -> dict:
        destination = self._resolve_output(output)
        records = self._records(session_id, export_format)
        if destination.exists():
            raise ValueError("Preference export destination already exists")
        destination.parent.mkdir(parents=True, exist_ok=True)
        descriptor, temporary = tempfile.mkstemp(
            prefix=f".{destination.name}.", dir=destination.parent
        )
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
                for record in records:
                    handle.write(json.dumps(record, ensure_ascii=False, separators=(",", ":")))
                    handle.write("\n")
                handle.flush()
                os.fsync(handle.fileno())
            os.link(temporary, destination)
        finally:
            Path(temporary).unlink(missing_ok=True)
        return {
            "session_id": session_id,
            "format": export_format,
            "output": str(destination),
            "records": len(records),
        }

    def _resolve_output(self, output: str) -> Path:
        root = self._store.data_root
        requested = Path(output).expanduser()
        destination = requested if requested.is_absolute() else root / requested
        resolved = destination.resolve(strict=False)
        if not resolved.is_relative_to(root):
            raise ValueError("Preference exports must remain under EPHY_PREFERENCE_DATA_ROOT")
        return resolved

    def _records(self, session_id: str, export_format: Literal["dpo", "sft"]) -> list[dict]:
        records = []
        for item in self._store.session_rows(session_id):
            pair = item["pair"]
            scenario = item["scenario"]
            vote = item["vote"]
            if (
                vote is None
                or vote.selection not in {"a", "b"}
                or not scenario.consent.training
                or scenario.deletion_status != "active"
                or scenario.split != "train"
                or pair.status == "duplicate_generation"
                or pair.response_a == pair.response_b
            ):
                continue
            chosen = pair.response_a if vote.selection == "a" else pair.response_b
            rejected = pair.response_b if vote.selection == "a" else pair.response_a
            chosen_candidate = pair.candidate_a if vote.selection == "a" else pair.candidate_b
            rejected_candidate = pair.candidate_b if vote.selection == "a" else pair.candidate_a
            prompt = [message.model_dump() for message in scenario.messages]
            if export_format == "dpo":
                records.append(
                    {
                        "prompt": prompt,
                        "chosen": [{"role": "assistant", "content": chosen}],
                        "rejected": [{"role": "assistant", "content": rejected}],
                        "metadata": {
                            "pair_id": pair.pair_id,
                            "scenario_id": scenario.scenario_id,
                            "category": scenario.category,
                            "provenance": scenario.provenance,
                            "chosen_prompt_variant": chosen_candidate.prompt_variant,
                            "rejected_prompt_variant": rejected_candidate.prompt_variant,
                            "chosen_prompt_revision": chosen_candidate.prompt_revision,
                            "rejected_prompt_revision": rejected_candidate.prompt_revision,
                        },
                    }
                )
            elif vote.approved_for_sft:
                records.append(
                    {
                        "messages": [
                            *prompt,
                            {"role": "assistant", "content": chosen},
                        ]
                    }
                )
        return records
