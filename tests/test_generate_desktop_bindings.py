from scripts.generate_desktop_bindings import normalize_models


def test_generated_class_order_is_deterministic_and_preserves_references():
    first = '\texport class Z {\n\t    value: A;\n\t}\n'
    second = '\texport class A {\n\t    value: string;\n\t}\n'
    one = 'export namespace main {\n\t\n' + first + '\n' + second + '}\n\n'
    two = 'export namespace main {\n' + second + first + '}\n'
    result = normalize_models(one)
    assert result == normalize_models(two)
    assert result == normalize_models(result)
    assert first.rstrip() in result and second.rstrip() in result
    assert not any(line != line.rstrip() for line in result.splitlines())


def test_duplicate_generated_class_is_rejected():
    import pytest
    with pytest.raises(ValueError, match='Duplicate'):
        normalize_models('export namespace main {\n\texport class A {\n\t}\n\texport class A {\n\t}\n}\n')


def test_multiple_namespaces_preserve_class_ownership_and_same_named_types():
    main = 'export namespace main {\n\texport class Target {\n\t    value: recording.Target;\n\t}\n}\n'
    recording = 'export namespace recording {\n\texport class Target {\n\t    value: string;\n\t}\n}\n'
    result = normalize_models(recording + main)
    assert result == normalize_models(main + recording)
    assert result == normalize_models(result)
    assert result.index('value: recording.Target') < result.index('export namespace recording')
    assert result.index('value: string') > result.index('export namespace recording')
