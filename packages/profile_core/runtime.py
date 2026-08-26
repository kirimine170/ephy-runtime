"""Load private Ephy state without exposing private values in diagnostics."""

from dataclasses import dataclass

from packages.config_core.loader import EphyRuntimeConfig, resolve_ephy_paths
from packages.identity_core.schemas import IdentityManifest, IdentityStatus
from packages.identity_core.service import IdentityService
from packages.profile_core.schemas import EphyProfile
from packages.profile_core.service import ProfileService


@dataclass(frozen=True)
class EphyContext:
    identity: IdentityManifest
    profile: EphyProfile


def load_ephy_context(config: EphyRuntimeConfig) -> EphyContext | None:
    if not config.enabled:
        return None
    try:
        paths = resolve_ephy_paths(config)
        instance_dir = paths.instance_dir.resolve(strict=True)
        if not instance_dir.is_relative_to(paths.private_root / "instances"):
            raise ValueError("Instance path escapes private root")
        for path in (paths.identity_path, paths.profile_path):
            resolved = path.resolve(strict=True)
            if not resolved.is_relative_to(instance_dir) or not resolved.is_file():
                raise ValueError("Invalid private document path")
            if resolved.stat().st_size > 65536:
                raise ValueError("Private document is too large")
        identity = IdentityService().load(paths.identity_path)
        profile = ProfileService().load(paths.profile_path)
        if identity.identity.instance_id != paths.instance_id:
            raise ValueError("Instance ID mismatch")
        if identity.identity.status != IdentityStatus.ACTIVE:
            raise ValueError("Identity is not active")
        return EphyContext(identity=identity, profile=profile)
    except Exception:
        # YAML/Pydantic exceptions can contain owner data or document contents．
        raise ValueError("Ephy private configuration is invalid or unavailable") from None
