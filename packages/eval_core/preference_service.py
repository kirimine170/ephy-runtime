from __future__ import annotations

import asyncio
import hashlib
import secrets
import unicodedata
from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable
from uuid import uuid4

import yaml

from packages.config_core.loader import AppConfig, ROOT_DIR
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage
from packages.model_registry.service import ModelRegistry
from packages.prompt_core.loader import PromptManager
from packages.router_core.router import ModelRouter
from .preference_export import PreferenceExporter
from .preference_schemas import (
    BlindPreferencePair,
    CandidateSpec,
    ConversationScenario,
    CreatePreferenceSessionRequest,
    ExportPreferenceRequest,
    GenerationParameters,
    PreferenceDataset,
    PreferencePair,
    PreferenceSession,
    PreferenceVote,
    SubmitPreferenceVoteRequest,
)
from .preference_store import PreferenceStore


PROMPT_COMPARISONS = {
    "prompt_v1_v2": ("v1", "v2"),
    "prompt_v2_v3": ("v2", "v3"),
}


class PreferenceService:
    def __init__(
        self,
        *,
        config: AppConfig,
        prompt_manager: PromptManager,
        adapter: LlamaCppChatAdapter,
        store: PreferenceStore | None = None,
        registry: ModelRegistry | None = None,
        repository_root: Path = ROOT_DIR,
        choose_display_order: Callable[[], str] | None = None,
        seed_factory: Callable[[], int] | None = None,
    ) -> None:
        self._config = config
        self._prompt_manager = prompt_manager
        self._adapter = adapter
        self._store = store or PreferenceStore()
        self._registry = registry or ModelRegistry(repository_root)
        self._repository_root = repository_root.resolve()
        self._router = ModelRouter(config=config)
        self._choose_display_order = choose_display_order or (
            lambda: "ab" if secrets.randbelow(2) == 0 else "ba"
        )
        self._seed_factory = seed_factory or (lambda: secrets.randbelow(2_147_483_647))
        self._generation_lock = asyncio.Lock()

    @property
    def store(self) -> PreferenceStore:
        return self._store

    def create_session(self, request: CreatePreferenceSessionRequest) -> dict:
        self._ensure_safe_data_root()
        dataset_path, scenarios = self._load_dataset(request.dataset_path)
        session = PreferenceSession(
            session_id=str(uuid4()),
            dataset_path=str(dataset_path),
            model_role=request.model_role,
            target_pairs=request.pair_count,
            prefetch=request.prefetch,
            comparison_mode=request.comparison_mode,
            generation_parameters=request.generation_parameters,
            created_at=datetime.now(timezone.utc),
        )
        self._store.create_session(session, scenarios)
        return self._session_summary(session)

    def list_sessions(self) -> list[dict]:
        self._ensure_safe_data_root()
        return [self._session_summary(session) for session in self._store.list_sessions()]

    async def generate(self, session_id: str, limit: int | None = None) -> dict:
        self._ensure_safe_data_root()
        async with self._generation_lock:
            with self._registry.selection_lease():
                session = self._store.get_session(session_id)
                existing = self._store.generation_count(session_id)
                remaining = max(session.target_pairs - existing, 0)
                requested = limit if limit is not None else session.prefetch
                to_generate = min(requested, remaining)
                generated = []
                for offset in range(to_generate):
                    generation_index = existing + offset
                    scenario = self._store.scenario_for_generation(session_id, generation_index)
                    pair = await self._generate_pair(session, scenario, generation_index)
                    self._store.add_pair(pair, generation_index)
                    generated.append({"pair_id": pair.pair_id, "status": pair.status})
            return {
                "session_id": session_id,
                "generated": generated,
                "stats": self.stats(session_id),
            }

    def next_pair(self, session_id: str) -> BlindPreferencePair | None:
        self._ensure_safe_data_root()
        self._store.get_session(session_id)
        pair = self._store.next_pair(session_id)
        if pair is None:
            return None
        scenario = self._store.get_scenario(pair.session_id, pair.scenario_id)
        if pair.display_order == "ab":
            left, right = pair.response_a, pair.response_b
        else:
            left, right = pair.response_b, pair.response_a
        stats = self.stats(session_id)
        return BlindPreferencePair(
            pair_id=pair.pair_id,
            messages=scenario.messages,
            response_left=left,
            response_right=right,
            category=scenario.category,
            progress={
                "reviewed": stats["reviewed"],
                "remaining": stats["remaining"],
                "total": stats["total"],
            },
        )

    def vote(self, pair_id: str, request: SubmitPreferenceVoteRequest) -> PreferenceVote:
        self._ensure_safe_data_root()
        pair = self._store.get_pair(pair_id)
        selection = request.selection
        if selection == "left":
            canonical = "a" if pair.display_order == "ab" else "b"
        elif selection == "right":
            canonical = "b" if pair.display_order == "ab" else "a"
        else:
            canonical = selection
        if request.approved_for_sft and canonical not in {"a", "b"}:
            raise ValueError("Only a selected response can be approved for SFT")
        vote = PreferenceVote(
            vote_id=str(uuid4()),
            pair_id=pair_id,
            selection=canonical,
            reason_tags=request.reason_tags,
            note=request.note,
            reviewer_type="human",
            approved_for_sft=request.approved_for_sft,
            created_at=datetime.now(timezone.utc),
            supersedes_vote_id=request.supersedes_vote_id,
        )
        return self._store.add_vote(vote)

    def stats(self, session_id: str) -> dict:
        self._ensure_safe_data_root()
        session = self._store.get_session(session_id)
        rows = self._store.session_rows(session_id)
        reviewed = 0
        tie = 0
        skip = 0
        duplicates = 0
        display_selections = Counter()
        reason_tags = Counter()
        categories: dict[str, Counter] = defaultdict(Counter)
        candidates: dict[str, dict] = {}
        prompt_variants: dict[str, Counter] = defaultdict(Counter)
        for item in rows:
            pair = item["pair"]
            scenario = item["scenario"]
            vote = item["vote"]
            if pair.status == "duplicate_generation":
                duplicates += 1
            for candidate in (pair.candidate_a, pair.candidate_b):
                candidates.setdefault(
                    candidate.candidate_id,
                    {
                        "model_registration_id": candidate.model_registration_id,
                        "adapter_registration_id": candidate.adapter_registration_id,
                        "wins": 0,
                        "losses": 0,
                    },
                )
                if candidate.prompt_variant:
                    prompt_variants[candidate.prompt_variant]["generated"] += 1
            if vote is None:
                continue
            reviewed += 1
            reason_tags.update(vote.reason_tags)
            if vote.selection == "tie":
                tie += 1
                categories[scenario.category]["tie"] += 1
            elif vote.selection == "skip":
                skip += 1
                categories[scenario.category]["skip"] += 1
            else:
                winner = pair.candidate_a if vote.selection == "a" else pair.candidate_b
                loser = pair.candidate_b if vote.selection == "a" else pair.candidate_a
                candidates[winner.candidate_id]["wins"] += 1
                candidates[loser.candidate_id]["losses"] += 1
                if winner.prompt_variant:
                    prompt_variants[winner.prompt_variant]["wins"] += 1
                if loser.prompt_variant:
                    prompt_variants[loser.prompt_variant]["losses"] += 1
                displayed = (
                    "left"
                    if (vote.selection == "a") == (pair.display_order == "ab")
                    else "right"
                )
                display_selections[displayed] += 1
                categories[scenario.category][displayed] += 1
        generated = len(rows)
        remaining = max(session.target_pairs - reviewed - duplicates, 0)
        decided_sides = display_selections["left"] + display_selections["right"]
        comparison = self._comparison_stats(
            session.comparison_mode,
            prompt_variants,
            remaining=remaining,
        )
        return {
            "session_id": session_id,
            "model_role": session.model_role,
            "comparison_mode": session.comparison_mode,
            "target_pairs": session.target_pairs,
            "total": session.target_pairs,
            "generated": generated,
            "reviewed": reviewed,
            "remaining": remaining,
            "queued": sum(1 for item in rows if item["pair"].status == "pending"),
            "tie": tie,
            "skip": skip,
            "candidates": candidates,
            "categories": {key: dict(value) for key, value in categories.items()},
            "display_selections": dict(display_selections),
            "display_selection_rate": {
                "left": display_selections["left"] / decided_sides if decided_sides else 0.0,
                "right": display_selections["right"] / decided_sides if decided_sides else 0.0,
            },
            "reason_tags": dict(reason_tags),
            "duplicate_generation": duplicates,
            "comparison": comparison,
        }

    def export(self, session_id: str, request: ExportPreferenceRequest) -> dict:
        self._ensure_safe_data_root()
        self._store.get_session(session_id)
        return PreferenceExporter(self._store).export(
            session_id,
            export_format=request.format,
            output=request.output,
        )

    def _load_dataset(self, dataset_path: str) -> tuple[Path, list[ConversationScenario]]:
        requested = Path(dataset_path).expanduser()
        resolved = (
            requested.resolve(strict=False)
            if requested.is_absolute()
            else (self._repository_root / requested).resolve(strict=False)
        )
        configs_root = (self._repository_root / "configs").resolve()
        data_root = self._ensure_safe_data_root()
        in_configs = resolved.is_relative_to(configs_root)
        in_data_root = resolved.is_relative_to(data_root)
        if not in_configs and not in_data_root:
            raise ValueError(
                "Preference dataset must be under repository configs or EPHY_PREFERENCE_DATA_ROOT"
            )
        if resolved.suffix.lower() not in {".yaml", ".yml"}:
            raise ValueError("Preference dataset must be YAML")
        if not resolved.is_file() or resolved.is_symlink():
            raise ValueError("Preference dataset is unavailable or unsafe")
        if resolved.stat().st_size > 2 * 1024 * 1024:
            raise ValueError("Preference dataset exceeds the 2 MiB limit")
        payload = yaml.safe_load(resolved.read_text(encoding="utf-8")) or {}
        dataset = PreferenceDataset.model_validate(payload)
        scenarios = dataset.scenarios
        if len({scenario.scenario_id for scenario in scenarios}) != len(scenarios):
            raise ValueError("Preference scenario IDs must be unique")
        if any(not scenario.consent.storage for scenario in scenarios):
            raise ValueError("Preference scenarios require explicit storage consent")
        if in_configs and any(scenario.source_kind != "synthetic" for scenario in scenarios):
            raise ValueError("Repository preference fixtures must be synthetic")
        return resolved, scenarios

    def _ensure_safe_data_root(self) -> Path:
        data_root = self._store.data_root
        if (
            data_root == self._repository_root
            or data_root.is_relative_to(self._repository_root)
            or self._repository_root.is_relative_to(data_root)
        ):
            raise ValueError(
                "EPHY_PREFERENCE_DATA_ROOT must be separate from the Git repository"
            )
        return data_root

    async def _generate_pair(
        self,
        session: PreferenceSession,
        scenario: ConversationScenario,
        generation_index: int,
    ) -> PreferencePair:
        model, adapter = self._current_artifacts(session.model_role)
        base_request = ChatCompletionRequest(
            model="auto",
            messages=[ChatMessage(role=item.role, content=item.content) for item in scenario.messages],
            metadata={"mode": session.model_role, "session_mode": "default"},
            stream=False,
        )
        if session.comparison_mode in PROMPT_COMPARISONS:
            (
                response_a,
                response_b,
                candidate_a,
                candidate_b,
            ) = await self._generate_prompt_comparison(
                session,
                base_request,
                generation_index,
                model,
                adapter,
            )
        else:
            effective_request = self._prompt_manager.apply_mode_prompt(
                base_request,
                session.model_role,
                ephy_prompt_version="v2",
            )
            decision = self._route_request(effective_request, model.backend_model)
            prompt_revision = self._prompt_revision(effective_request)
            first_seed = self._seed(session.generation_parameters, generation_index, 0)
            first_parameters = session.generation_parameters.model_copy(update={"seed": first_seed})
            response_a = await self._generate_response(
                decision.selected_model, effective_request, first_parameters
            )
            candidate_a = self._candidate_spec(
                session.model_role,
                model,
                adapter,
                "v2",
                prompt_revision,
                first_parameters,
            )

            response_b = ""
            candidate_b = None
            for attempt in range(3):
                second_seed = self._seed(session.generation_parameters, generation_index, attempt + 1)
                second_parameters = session.generation_parameters.model_copy(
                    update={"seed": second_seed}
                )
                response_b = await self._generate_response(
                    decision.selected_model, effective_request, second_parameters
                )
                candidate_b = self._candidate_spec(
                    session.model_role,
                    model,
                    adapter,
                    "v2",
                    prompt_revision,
                    second_parameters,
                )
                if self._normalize_response(response_a) != self._normalize_response(response_b):
                    break
        assert candidate_b is not None
        duplicate = self._normalize_response(response_a) == self._normalize_response(response_b)
        return PreferencePair(
            pair_id=str(uuid4()),
            session_id=session.session_id,
            scenario_id=scenario.scenario_id,
            candidate_a=candidate_a,
            candidate_b=candidate_b,
            response_a=response_a,
            response_b=response_b,
            response_a_sha256=self._sha256(response_a),
            response_b_sha256=self._sha256(response_b),
            display_order=self._choose_display_order(),
            status="duplicate_generation" if duplicate else "pending",
            created_at=datetime.now(timezone.utc),
        )

    async def _generate_prompt_comparison(
        self,
        session: PreferenceSession,
        base_request: ChatCompletionRequest,
        generation_index: int,
        model,
        adapter,
    ) -> tuple[str, str, CandidateSpec, CandidateSpec]:
        first_variant, second_variant = PROMPT_COMPARISONS[session.comparison_mode]
        request_first = self._prompt_manager.apply_mode_prompt(
            base_request,
            session.model_role,
            ephy_prompt_version=first_variant,
        )
        request_second = self._prompt_manager.apply_mode_prompt(
            base_request,
            session.model_role,
            ephy_prompt_version=second_variant,
        )
        revision_first = self._prompt_revision(request_first)
        revision_second = self._prompt_revision(request_second)
        if revision_first == revision_second:
            raise ValueError(
                f"Prompt {first_variant}/{second_variant} comparison requires an enabled "
                "warm_polite Ephy Profile"
            )
        decision_first = self._route_request(request_first, model.backend_model)
        decision_second = self._route_request(request_second, model.backend_model)
        response_first = ""
        response_second = ""
        candidate_first = None
        candidate_second = None
        for attempt in range(4):
            shared_seed = self._seed(
                session.generation_parameters,
                generation_index,
                attempt,
            )
            parameters = session.generation_parameters.model_copy(
                update={"seed": shared_seed}
            )
            response_first = await self._generate_response(
                decision_first.selected_model,
                request_first,
                parameters,
            )
            response_second = await self._generate_response(
                decision_second.selected_model,
                request_second,
                parameters,
            )
            candidate_first = self._candidate_spec(
                session.model_role,
                model,
                adapter,
                first_variant,
                revision_first,
                parameters,
            )
            candidate_second = self._candidate_spec(
                session.model_role,
                model,
                adapter,
                second_variant,
                revision_second,
                parameters,
            )
            if self._normalize_response(response_first) != self._normalize_response(response_second):
                break
        assert candidate_first is not None and candidate_second is not None
        return response_first, response_second, candidate_first, candidate_second

    def _route_request(self, request: ChatCompletionRequest, backend_model: str):
        decision = self._router.route_chat(request)
        if decision.selected_model.model != backend_model:
            raise ValueError(
                "Gateway model configuration is stale; reload it before preference generation"
            )
        return decision

    async def _generate_response(self, model_config, request, parameters: GenerationParameters) -> str:
        payload = request.model_copy(
            update={
                "temperature": parameters.temperature,
                "max_tokens": parameters.max_tokens,
                "top_p": parameters.top_p,
                "seed": parameters.seed,
            }
        )
        response = await self._adapter.create_chat_completion(
            model_config=model_config,
            request_payload=payload,
        )
        try:
            content = response["choices"][0]["message"]["content"]
        except (KeyError, IndexError, TypeError) as exc:
            raise RuntimeError("Preference generation returned no assistant response") from exc
        if not isinstance(content, str):
            raise RuntimeError("Preference generation returned non-text content")
        content = content.strip()
        if not content:
            raise RuntimeError("Preference generation returned an empty response")
        return content

    def _current_artifacts(self, role: str):
        selection = self._registry.selections().roles.get(role)
        if selection is None:
            raise ValueError(f"Model Manager has no registered selection for role '{role}'")
        return self._registry.resolve(selection, verify=False)

    @staticmethod
    def _candidate_spec(
        role,
        model,
        adapter,
        prompt_variant,
        prompt_revision,
        parameters,
    ) -> CandidateSpec:
        return CandidateSpec(
            candidate_id=str(uuid4()),
            model_role=role,
            model_registration_id=model.id,
            model_sha256=model.sha256,
            adapter_registration_id=adapter.id if adapter else None,
            adapter_sha256=adapter.sha256 if adapter else None,
            prompt_variant=prompt_variant,
            prompt_revision=prompt_revision,
            generation_parameters=parameters,
            generated_at=datetime.now(timezone.utc),
        )

    def _seed(self, parameters: GenerationParameters, generation_index: int, offset: int) -> int:
        if parameters.seed is None:
            return self._seed_factory()
        return parameters.seed + generation_index * 4 + offset

    @staticmethod
    def _prompt_revision(request: ChatCompletionRequest) -> str:
        system = "\n\n".join(
            str(message.content) for message in request.messages if message.role == "system"
        )
        return hashlib.sha256(system.encode("utf-8")).hexdigest()

    @staticmethod
    def _normalize_response(response: str) -> str:
        return " ".join(unicodedata.normalize("NFKC", response).split())

    @staticmethod
    def _sha256(value: str) -> str:
        return hashlib.sha256(value.encode("utf-8")).hexdigest()

    @staticmethod
    def _comparison_stats(
        comparison_mode: str,
        prompt_variants: dict[str, Counter],
        *,
        remaining: int,
    ) -> dict:
        comparison_variants = PROMPT_COMPARISONS.get(comparison_mode)
        if comparison_variants is None:
            return {"mode": comparison_mode}
        if remaining > 0:
            return {"mode": comparison_mode, "blinded": True}

        variants = {}
        for name in comparison_variants:
            wins = int(prompt_variants[name]["wins"])
            losses = int(prompt_variants[name]["losses"])
            decided = wins + losses
            variants[name] = {
                "wins": wins,
                "losses": losses,
                "win_rate": wins / decided if decided else 0.0,
            }
        first_variant, second_variant = comparison_variants
        if variants[second_variant]["wins"] > variants[first_variant]["wins"]:
            winner = second_variant
        elif variants[first_variant]["wins"] > variants[second_variant]["wins"]:
            winner = first_variant
        else:
            winner = "tie"
        return {
            "mode": comparison_mode,
            "blinded": False,
            "winner": winner,
            "variants": variants,
        }

    def _session_summary(self, session: PreferenceSession) -> dict:
        stats = self.stats(session.session_id)
        return {
            "session_id": session.session_id,
            "dataset_path": session.dataset_path,
            "model_role": session.model_role,
            "comparison_mode": session.comparison_mode,
            "target_pairs": session.target_pairs,
            "prefetch": session.prefetch,
            "created_at": session.created_at.isoformat(),
            "reviewed": stats["reviewed"],
            "remaining": stats["remaining"],
            "generated": stats["generated"],
        }
