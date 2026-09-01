from __future__ import annotations

import fcntl
import hashlib
import json
import os
import shutil
import tempfile
from contextlib import contextmanager
from pathlib import Path
from typing import Annotated, Literal
from urllib.parse import urlparse
from urllib.request import HTTPRedirectHandler, Request, build_opener

import yaml
from pydantic import BaseModel, ConfigDict, Field, model_validator


Identifier = Annotated[str, Field(pattern=r"^[a-z0-9][a-z0-9._-]{0,95}$")]
Digest = Annotated[str, Field(pattern=r"^[0-9a-f]{64}$")]
Role = Literal["fast", "work", "code"]
Capability = Literal["text", "vision", "reasoning", "tool_use", "long_context"]
ThinkingMode = Literal["disabled", "optional", "always"]
ReasoningEffort = Literal["low", "medium", "high", "xhigh"]
PORTS = {"fast": 8081, "work": 8082, "code": 8083}


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


class Artifact(StrictModel):
    id: Identifier
    path: str = Field(min_length=1)
    sha256: Digest
    size_bytes: int = Field(ge=24)
    source: str = "local"
    revision: str = Field(min_length=1)


class ModelArtifact(Artifact):
    backend_model: Identifier
    role: Literal["chat"] = "chat"
    quantization: str = "unknown"
    context_size: int = Field(default=8192, ge=512, le=1_000_000)
    profile_id: Identifier | None = None


class RuntimeModelProfile(StrictModel):
    family: str = Field(min_length=1)
    parameter_count_billions: float = Field(gt=0)
    capabilities: list[Capability] = Field(min_length=1)
    enabled_capabilities: list[Capability] = Field(min_length=1)
    thinking_mode: ThinkingMode = "disabled"
    default_reasoning_effort: ReasoningEffort | None = None
    preserve_thinking: bool = False
    native_context_size: int = Field(ge=512, le=1_000_000)
    maximum_context_size: int = Field(ge=512, le=1_000_000)
    default_context_size: int = Field(ge=512, le=1_000_000)
    startup_timeout_seconds: int = Field(default=180, ge=30, le=900)
    resource_class: Literal["light", "medium", "large"]
    estimated_minimum_memory_bytes: int = Field(ge=0)
    gpu_layers: int = Field(default=99, ge=0, le=999)

    @model_validator(mode="after")
    def validate_profile(self):
        if not set(self.enabled_capabilities).issubset(self.capabilities):
            raise ValueError("Enabled capabilities must be native model capabilities")
        if self.default_context_size > self.maximum_context_size:
            raise ValueError("Default context exceeds the maximum context")
        if self.native_context_size > self.maximum_context_size:
            raise ValueError("Native context exceeds the maximum context")
        if self.default_reasoning_effort and self.thinking_mode == "disabled":
            raise ValueError("Reasoning effort requires thinking support")
        return self


class RuntimeModelProfiles(StrictModel):
    schema_version: Literal[1] = 1
    profiles: dict[Identifier, RuntimeModelProfile] = Field(default_factory=dict)


class AdapterArtifact(Artifact):
    base_model_id: Identifier
    base_sha256: Digest
    experimental: bool = True


class Registry(StrictModel):
    schema_version: Literal[1] = 1
    models: list[ModelArtifact] = Field(default_factory=list)
    adapters: list[AdapterArtifact] = Field(default_factory=list)

    @model_validator(mode="after")
    def unique_ids(self):
        ids = [entry.id for entry in [*self.models, *self.adapters]]
        if len(ids) != len(set(ids)):
            raise ValueError("Registry IDs must be unique")
        return self


class Selection(StrictModel):
    model_id: Identifier
    adapter_id: Identifier | None = None


class Selections(StrictModel):
    schema_version: Literal[1] = 1
    roles: dict[Role, Selection] = Field(default_factory=dict)


def digest_file(path: Path) -> str:
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def inspect_artifact(path: Path) -> tuple[Path, int, str]:
    path = path.expanduser().resolve(strict=True)
    if not path.is_file():
        raise ValueError("Artifact must be a regular GGUF file")
    with path.open("rb") as source:
        header = source.read(8)
    if header[:4] != b"GGUF" or int.from_bytes(header[4:8], "little") not in (2, 3):
        raise ValueError("Artifact must be a GGUF v2/v3 file")
    size = path.stat().st_size
    if size < 24:
        raise ValueError("Incomplete GGUF header")
    return path, size, digest_file(path)


def atomic_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(payload, output, ensure_ascii=False, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def _read(path: Path, schema):
    return schema.model_validate_json(path.read_text(encoding="utf-8")) if path.exists() else schema()


def _read_yaml(path: Path, schema):
    if not path.exists():
        return schema()
    payload = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    return schema.model_validate(payload)


def physical_memory_bytes() -> int | None:
    try:
        return int(os.sysconf("SC_PHYS_PAGES")) * int(os.sysconf("SC_PAGE_SIZE"))
    except (OSError, ValueError, KeyError):
        return None


class ModelRegistry:
    def __init__(self, root: Path):
        self.root = root.resolve()
        self.registry_path = self.root / "configs/model-registry.local.json"
        self.selection_path = self.root / "configs/runtime-selection.local.json"
        self.profiles_path = self.root / "configs/model-profiles.yaml"

    @contextmanager
    def _lock(self):
        lock = self.root / "data/runtime/model-registry.lock"
        lock.parent.mkdir(parents=True, exist_ok=True)
        with lock.open("a") as handle:
            fcntl.flock(handle, fcntl.LOCK_EX)
            yield

    @contextmanager
    def selection_lease(self):
        """Keep Model Manager selection stable for a bounded inference batch."""
        with self._lock():
            revision = self.revision()
            yield
            if self.revision() != revision:
                raise ValueError("Model selection changed outside the registry lock")

    def registry(self) -> Registry:
        return _read(self.registry_path, Registry)

    def selections(self) -> Selections:
        return _read(self.selection_path, Selections)

    def profiles(self) -> RuntimeModelProfiles:
        return _read_yaml(self.profiles_path, RuntimeModelProfiles)

    def profile_for_model(self, model: ModelArtifact) -> tuple[str, RuntimeModelProfile] | None:
        profiles = self.profiles().profiles
        profile_id = model.profile_id or (model.id if model.id in profiles else None)
        if profile_id is None:
            return None
        profile = profiles.get(profile_id)
        if profile is None:
            raise ValueError(f"Unknown Runtime model profile: {profile_id}")
        return profile_id, profile

    def revision(self) -> str:
        data = self.selection_path.read_bytes() if self.selection_path.exists() else b""
        return hashlib.sha256(data).hexdigest()

    def catalog(self) -> dict:
        with self._lock():
            registry = self.registry()
            data = registry.model_dump()
            host_memory = physical_memory_bytes()
            model_records = {model.id: model for model in registry.models}
            for entry in data["models"]:
                path = Path(entry["path"])
                entry["available"] = path.is_file() and path.stat().st_size == entry["size_bytes"]
                profile_match = self.profile_for_model(model_records[entry["id"]])
                if profile_match:
                    profile_id, profile = profile_match
                    profile_data = profile.model_dump()
                    entry.update(profile_data)
                    entry["profile_id"] = profile_id
                    entry["resource_fit"] = (
                        None if host_memory is None
                        else host_memory >= profile.estimated_minimum_memory_bytes
                    )
                    entry["resource_warning"] = (
                        "Host memory is below this profile's advisory estimate"
                        if entry["resource_fit"] is False else None
                    )
            for entry in data["adapters"]:
                path = Path(entry["path"])
                entry["available"] = path.is_file() and path.stat().st_size == entry["size_bytes"]
            return {
                **data,
                "profiles": {
                    profile_id: profile.model_dump()
                    for profile_id, profile in self.profiles().profiles.items()
                },
                "host_memory_bytes": host_memory,
                "selections": self.selections().model_dump()["roles"],
                "revision": self.revision(),
            }

    def import_model(self, path: Path, *, model_id: str, backend_model: str | None = None,
                     source: str = "local", revision: str | None = None,
                     quantization: str = "unknown", context_size: int | None = None,
                     profile_id: str | None = None) -> ModelArtifact:
        resolved, size, checksum = inspect_artifact(path)
        profiles = self.profiles().profiles
        inferred_profile_id = profile_id or (model_id if model_id in profiles else None)
        profile = profiles.get(inferred_profile_id) if inferred_profile_id else None
        if inferred_profile_id and profile is None:
            raise ValueError(f"Unknown Runtime model profile: {inferred_profile_id}")
        selected_context = context_size or (profile.default_context_size if profile else 8192)
        if profile and selected_context > profile.maximum_context_size:
            raise ValueError("Context size exceeds the Runtime model profile maximum")
        record = ModelArtifact(id=model_id, backend_model=backend_model or model_id,
                               path=str(resolved), size_bytes=size, sha256=checksum,
                               source=source, revision=revision or checksum,
                               quantization=quantization, context_size=selected_context,
                               profile_id=inferred_profile_id)
        self._register(record)
        return record

    def import_adapter(self, path: Path, *, adapter_id: str, base_model_id: str) -> AdapterArtifact:
        base = next((m for m in self.registry().models if m.id == base_model_id), None)
        if base is None:
            raise ValueError("Register the exact base model before its adapter")
        self.verify(base)
        resolved, size, checksum = inspect_artifact(path)
        record = AdapterArtifact(id=adapter_id, path=str(resolved), size_bytes=size, sha256=checksum,
                                 revision=checksum, base_model_id=base.id, base_sha256=base.sha256)
        self._register(record)
        return record

    def _register(self, record: ModelArtifact | AdapterArtifact) -> None:
        with self._lock():
            registry = self.registry()
            existing = next((r for r in [*registry.models, *registry.adapters] if r.id == record.id), None)
            if existing is not None:
                if existing != record:
                    raise ValueError("Artifact ID already exists with different metadata; use a new ID")
                return
            field = "models" if isinstance(record, ModelArtifact) else "adapters"
            updated = registry.model_copy(update={field: [*getattr(registry, field), record]})
            atomic_json(self.registry_path, updated.model_dump())

    @staticmethod
    def verify(record: Artifact) -> None:
        resolved, size, checksum = inspect_artifact(Path(record.path))
        if str(resolved) != record.path or size != record.size_bytes or checksum != record.sha256:
            raise ValueError("Artifact checksum or size mismatch; selection was not changed")

    def resolve(self, selection: Selection, *, verify: bool = True) -> tuple[ModelArtifact, AdapterArtifact | None]:
        registry = self.registry()
        model = next((m for m in registry.models if m.id == selection.model_id), None)
        if model is None:
            raise ValueError("Unknown model ID")
        profile_match = self.profile_for_model(model)
        if profile_match and model.context_size > profile_match[1].maximum_context_size:
            raise ValueError("Selected context exceeds the Runtime model profile maximum")
        adapter = None
        if selection.adapter_id:
            adapter = next((a for a in registry.adapters if a.id == selection.adapter_id), None)
            if adapter is None or adapter.base_model_id != model.id or adapter.base_sha256 != model.sha256:
                raise ValueError("LoRA is not registered for this exact base model")
        if verify:
            self.verify(model)
            if adapter:
                self.verify(adapter)
        return model, adapter

    def select(self, role: Role, selection: Selection | None, *, expected_revision: str) -> dict:
        if role not in PORTS:
            raise ValueError("Only fast/work/code can be switched; embedding requires re-indexing")
        if selection:
            self.resolve(selection)
        with self._lock():
            if self.revision() != expected_revision:
                raise ValueError("Selection changed concurrently; refresh and try again")
            roles = dict(self.selections().roles)
            if selection:
                roles[role] = selection
            else:
                roles.pop(role, None)
            atomic_json(self.selection_path, Selections(roles=roles).model_dump())
            return {"revision": self.revision()}

    def model_overrides(self) -> dict:
        overrides = {}
        for role, selection in self.selections().roles.items():
            model, _ = self.resolve(selection, verify=False)
            override = {"provider": "llama_cpp", "model": model.backend_model,
                        "base_url": f"http://127.0.0.1:{PORTS[role]}/v1",
                        "max_context": model.context_size}
            profile_match = self.profile_for_model(model)
            if profile_match:
                _, profile = profile_match
                override.update({
                    "thinking_mode": profile.thinking_mode,
                    "default_reasoning_effort": profile.default_reasoning_effort,
                    "preserve_thinking": profile.preserve_thinking,
                })
            overrides[role] = override
        return overrides

    def plan_download(self, *, model_id: str, url: str, sha256: str, size_bytes: int,
                      revision: str) -> dict:
        # No network or directory creation during dry-run．
        parsed = urlparse(url)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query:
            raise ValueError("Use a credential-free HTTPS URL without query parameters")
        ModelArtifact(id=model_id, backend_model=model_id, path="pending", sha256=sha256,
                      size_bytes=size_bytes, revision=revision, source=url)
        if any(r.id == model_id for r in [*self.registry().models, *self.registry().adapters]):
            raise ValueError("Model ID already exists")
        directory = self.root / "models/registry"
        destination = directory / f"{model_id}.gguf"
        if destination.exists():
            raise ValueError("Destination already exists; import or use a new ID")
        existing = directory
        while not existing.exists():
            existing = existing.parent
        free = shutil.disk_usage(existing).free
        required = size_bytes + 64 * 1024 * 1024
        return {"model_id": model_id, "destination": str(destination), "size_bytes": size_bytes,
                "required_disk_bytes": required, "free_disk_bytes": free, "has_space": free >= required,
                "revision": revision, "sha256": sha256, "resume_policy": "restart"}

    def download(self, *, model_id: str, url: str, sha256: str, size_bytes: int,
                 revision: str, opener=None) -> ModelArtifact:
        plan = self.plan_download(model_id=model_id, url=url, sha256=sha256,
                                  size_bytes=size_bytes, revision=revision)
        if not plan["has_space"]:
            raise ValueError("Insufficient free disk space")
        destination = Path(plan["destination"])
        directory = destination.parent
        directory.mkdir(parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(prefix=f".{model_id}.", suffix=".part", dir=directory)
        try:
            transport = opener or build_opener(HTTPSOnlyRedirect())
            with os.fdopen(fd, "wb") as output, transport.open(Request(url), timeout=30) as response:
                total = 0
                while block := response.read(1024 * 1024):
                    total += len(block)
                    if total > size_bytes:
                        raise ValueError("Download exceeds declared size")
                    output.write(block)
                output.flush()
                os.fsync(output.fileno())
            if total != size_bytes or digest_file(Path(temporary)) != sha256:
                raise ValueError("Downloaded size or checksum mismatch")
            if shutil.disk_usage(directory).free < 64 * 1024 * 1024:
                raise ValueError("Insufficient free disk space after download")
            inspect_artifact(Path(temporary))
            os.link(temporary, destination)  # Atomic no-overwrite publication．
            return self.import_model(destination, model_id=model_id, source=url, revision=revision)
        finally:
            Path(temporary).unlink(missing_ok=True)


class HTTPSOnlyRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        parsed = urlparse(newurl)
        if parsed.scheme != "https" or parsed.username or parsed.password:
            raise ValueError("Unsafe download redirect")
        return super().redirect_request(req, fp, code, msg, headers, newurl)
