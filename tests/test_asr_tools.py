import json
from pathlib import Path

import pytest

from scripts.asr import configure, evaluate


def test_cer_preserves_numbers_negation_and_counts_failed_speech():
    assert evaluate.normalize('午後３時，ではなく４時．') == '午後3時ではなく4時'
    assert evaluate.distance('午後3時ではなく4時', '午後3時') == 6
    result = evaluate.failed_trial({'id':'test', 'reference':'待って', 'critical_terms':['待って']}, RuntimeError('private phrase'))
    assert result['edit_distance'] == result['reference_characters'] == 3
    assert result['critical_terms_total'] == 1 and result['critical_terms_matched'] == 0
    assert result['error_code'] == 'runner_failed'
    assert 'private' not in json.dumps(result)
    assert evaluate.error_code(RuntimeError('sensitive')) == 'runner_failed'
    assert evaluate.percentile([None, 10, 30, 20], .95) == 30
    assert evaluate.percentile([None], .95) is None


def test_configuration_rejects_corrupt_model_before_writing(tmp_path):
    helper = tmp_path / 'helper'; helper.write_bytes(b'fixture'); helper.chmod(0o755)
    assets = json.loads(Path(configure.__file__).with_name('assets.json').read_text())
    model = next(asset for asset in assets['models'] if asset['id'] == 'large-v3-turbo-f16')
    (tmp_path / model['file']).write_bytes(b'corrupt cache')
    with pytest.raises(ValueError, match='model_asset_mismatch'):
        configure.configuration(helper, tmp_path, model['id'])
    with pytest.raises(ValueError, match='unsupported_model'):
        configure.configuration(helper, tmp_path, 'unknown')
