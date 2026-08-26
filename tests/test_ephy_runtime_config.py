from __future__ import annotations

from pathlib import Path
from uuid import UUID

import pytest
from pydantic import ValidationError

from packages.config_core.loader import EphyRuntimeConfig, load_app_config, resolve_ephy_paths


INSTANCE_ID = "019c0000-0000-7000-8000-000000000000"


def test_default_ephy_runtime_is_disabled() -> None:
    config = load_app_config().ephy

    assert config.enabled is False
    assert config.private_root is None
    assert config.instance_id is None


def test_resolve_ephy_paths_prefers_environment(tmp_path: Path) -> None:
    config = EphyRuntimeConfig(
        private_root="/config/private",
        instance_id="019c0000-0002-7000-8000-000000000000",
    )

    paths = resolve_ephy_paths(
        config,
        {
            "EPHY_PRIVATE_ROOT": str(tmp_path),
            "EPHY_INSTANCE_ID": INSTANCE_ID,
        },
    )

    assert paths.private_root == tmp_path.resolve()
    assert paths.instance_id == UUID(INSTANCE_ID)
    assert paths.instance_dir == tmp_path.resolve() / "instances" / INSTANCE_ID
    assert paths.identity_path == paths.instance_dir / "identity.yaml"
    assert paths.profile_path == paths.instance_dir / "profile.yaml"


def test_resolve_ephy_paths_rejects_relative_private_root() -> None:
    config = EphyRuntimeConfig()

    with pytest.raises(ValueError, match="absolute"):
        resolve_ephy_paths(
            config,
            {
                "EPHY_PRIVATE_ROOT": "relative/private",
                "EPHY_INSTANCE_ID": INSTANCE_ID,
            },
        )


def test_ephy_config_rejects_filename_path_traversal() -> None:
    with pytest.raises(ValidationError, match="path components"):
        EphyRuntimeConfig(identity_filename="../identity.yaml")

    with pytest.raises(ValidationError, match="path components"):
        EphyRuntimeConfig(profile_filename=r"..\profile.yaml")
