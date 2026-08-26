import importlib.util
import shutil
from pathlib import Path

import pytest
import yaml

from packages.config_core.loader import EphyRuntimeConfig
from packages.profile_core.runtime import load_ephy_context

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("init_local_ephy", ROOT / "scripts/init_local_ephy.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def test_initializes_loadable_private_development_instance_without_owner_data(tmp_path, monkeypatch):
    monkeypatch.delenv("EPHY_PRIVATE_ROOT", raising=False)
    monkeypatch.delenv("EPHY_INSTANCE_ID", raising=False)
    runtime = tmp_path / "runtime"
    shutil.copytree(ROOT / "configs/examples", runtime / "configs/examples")
    private = tmp_path / "private"
    assert module.initialize(runtime, private)["signed"] is False
    config_path = runtime / "configs/ephy.local.yaml"
    config = EphyRuntimeConfig.model_validate(yaml.safe_load(config_path.read_text())["ephy"])
    context = load_ephy_context(config)
    assert context.identity.identity.individual_name == "エフィ"
    assert context.identity.ownership is None
    assert str(context.identity.identity.instance_id) != "019c0000-0000-7000-8000-000000000000"
    assert context.profile.clarification.example == ()
    assert config_path.stat().st_mode & 0o777 == 0o600
    assert private.stat().st_mode & 0o777 == 0o700
    original = config_path.read_bytes()
    with pytest.raises(ValueError, match="already exists"):
        module.initialize(runtime, private)
    assert config_path.read_bytes() == original
    assert len(list((private / "instances").iterdir())) == 1
