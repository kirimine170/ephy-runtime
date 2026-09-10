"""Synthetic voice fixtures only．Never import a user's reference material．"""

import hashlib
import io
import json
import os
import stat
import struct
import wave
import zipfile

import numpy as np
import pytest

from packages.speech_assets import SpeechAssetError, SpeechAssetStore, read_private_json_file
from packages.speech_assets import store as implementation


PRIVATE_TEXT = "合成された正確な参照文です．\n"
PRIVATE_ATTESTOR = "synthetic-private-attestor"


def wav_bytes(*, frames=160, rate=16000, channels=1):
    destination = io.BytesIO()
    with wave.open(destination, "wb") as output:
        output.setnchannels(channels)
        output.setsampwidth(2)
        output.setframerate(rate)
        output.writeframes(b"\x01\x00" * frames * channels)
    return destination.getvalue()


def consent(**overrides):
    return {"authority": "owned", "storage_allowed": True, "voice_clone_allowed": True,
            "synthesis_allowed": True, "transcript_verified": True, "clean_reference": True,
            "attested_by": PRIVATE_ATTESTOR, "attested_at": "2026-09-07T00:00:00+00:00", **overrides}


@pytest.fixture
def fixture(tmp_path):
    # resolve removes the platform's /var -> /private/var temporary alias．
    base = tmp_path.resolve()
    audio, transcript = base / "synthetic.wav", base / "synthetic.txt"
    audio.write_bytes(wav_bytes())
    transcript.write_text(PRIVATE_TEXT, encoding="utf-8")
    root = base / "private-store"
    return SpeechAssetStore(root), root, audio, transcript


def register(fixture):
    asset_store, _, audio, transcript = fixture
    return asset_store.register_reference(audio, transcript, consent=consent())


def arrays():
    return {"ref_code": np.arange(32, dtype=np.int64).reshape(2, 16),
            "ref_spk_embedding": np.array([0.25, -0.5, 0.75], dtype=np.float32)}


def clone(asset_store, reference, **overrides):
    arguments = {"provider": "qwen3_tts", "model_revision": "synthetic-model-pin",
                 "tokenizer_revision": "synthetic-tokenizer-pin", "arrays": arrays(),
                 "metadata": {"x_vector_only_mode": False, "icl_mode": True, "ref_text": reference.transcript},
                 **overrides}
    return asset_store.store_clone_prompt(reference.reference_id, **arguments)


def snapshot(root):
    return {str(path.relative_to(root)): (path.read_bytes(), path.stat().st_ino, path.stat().st_mtime_ns)
            for path in root.rglob("*") if path.is_file()}


def test_reference_is_private_exact_and_does_not_modify_source(fixture):
    asset_store, root, audio, transcript = fixture
    before = {path: (path.read_bytes(), path.stat().st_mtime_ns) for path in (audio, transcript)}
    reference = register(fixture)
    reloaded = asset_store.load_reference(reference.reference_id)
    assert reference.reference_id.startswith("ref_") and len(reference.reference_id) == 68
    assert reference.provenance_id.startswith("prov_") and len(reference.provenance_id) == 37
    assert reloaded.transcript == PRIVATE_TEXT
    assert reloaded.audio_bytes == audio.read_bytes() == reloaded.audio_path.read_bytes()
    assert reloaded.reference_digest == reference.reference_digest
    for path in (root, *root.rglob("*")):
        assert stat.S_IMODE(path.stat().st_mode) == (0o700 if path.is_dir() else 0o600)
    for path, value in before.items():
        assert (path.read_bytes(), path.stat().st_mtime_ns) == value
    exported = json.dumps(reference.public_metadata()) + repr(reference)
    for private in (PRIVATE_TEXT, PRIVATE_ATTESTOR, str(root), "reference.wav", "transcript"):
        assert private not in exported


def test_reference_audio_bytes_remain_the_validated_snapshot(fixture):
    reference = register(fixture)
    validated = reference.audio_bytes
    reference.audio_path.write_bytes(wav_bytes(frames=320))
    assert reference.audio_bytes == validated
    with pytest.raises(SpeechAssetError, match="voice_asset_integrity"):
        fixture[0].load_reference(reference.reference_id)


def test_reference_group_is_ordered_bounded_private_and_immutable(fixture):
    store, root, audio, transcript = fixture
    first = register(fixture)
    audio.write_bytes(wav_bytes(frames=240))
    transcript.write_text("二つ目の合成参照文です．\n", encoding="utf-8")
    second = store.register_reference(audio, transcript, consent=consent())
    group = store.store_reference_group([first.reference_id, second.reference_id])
    loaded = store.load_reference_group(group.group_digest)
    assert [item.reference_id for item in loaded.references] == [first.reference_id, second.reference_id]
    assert loaded.public_metadata() == {"reference_group_digest": group.group_digest,
        "provenance_id": group.provenance_id, "reference_count": 2}
    exposed = json.dumps(loaded.public_metadata()) + repr(loaded)
    for private in (PRIVATE_TEXT, PRIVATE_ATTESTOR, str(root), "reference.wav", "transcript"):
        assert private not in exposed
    with pytest.raises(SpeechAssetError, match="voice_asset_exists"):
        store.store_reference_group([first.reference_id, second.reference_id])
    with pytest.raises(SpeechAssetError, match="voice_asset_group_invalid"):
        store.store_reference_group([first.reference_id, first.reference_id])
    assert stat.S_IMODE((root / "groups" / group.group_digest).stat().st_mode) == 0o700
    assert stat.S_IMODE((root / "groups" / group.group_digest / "manifest.json").stat().st_mode) == 0o600


def test_irodori_reference_is_audio_only_explicit_and_groupable(fixture):
    store, root, audio, _ = fixture
    reference = store.register_irodori_reference(
        audio, consent=consent(transcript_verified=False))
    loaded = store.load_reference(reference.reference_id)
    assert loaded.transcript is None and loaded.transcript_path is None
    assert loaded.metadata["conditioning_provider"] == "irodori-tts"
    assert loaded.metadata["transcript_mode"] == "not_consumed"
    assert not (root / "references" / reference.reference_id / "transcript.txt").exists()
    group = store.store_reference_group([reference.reference_id])
    assert group.references[0].reference_id == reference.reference_id
    with pytest.raises(SpeechAssetError, match="voice_asset_clone_metadata_invalid"):
        clone(store, reference)


@pytest.mark.parametrize("verified", [True, None])
def test_irodori_audio_only_requires_explicit_transcript_state(fixture, verified):
    store, root, audio, _ = fixture
    record = consent(transcript_verified=False)
    if verified is None:
        record.pop("transcript_verified")
    else:
        record["transcript_verified"] = verified
    with pytest.raises(SpeechAssetError, match="voice_asset_consent_required"):
        store.register_irodori_reference(audio, consent=record)
    assert not list((root / "references").iterdir())


def test_reference_group_rechecks_member_integrity_and_link_count(fixture):
    store, root, _, _ = fixture
    reference = register(fixture)
    group = store.store_reference_group([reference.reference_id])
    os.link(root / "groups" / group.group_digest / "manifest.json", root / "groups" / group.group_digest / "copy.json")
    with pytest.raises(SpeechAssetError):
        store.load_reference_group(group.group_digest)


@pytest.mark.parametrize("change", [
    {"authority": "unknown"}, {"storage_allowed": False}, {"voice_clone_allowed": False},
    {"synthesis_allowed": False}, {"transcript_verified": False}, {"clean_reference": False},
    {"storage_allowed": 1}, {"attested_by": ""}, {"attested_at": "2026-09-07"},
    {"attested_at": "private-invalid-date"}, {"authority": "explicit_permission"},
    {"permission_evidence": "bad\x00evidence"}, {"unrelated": "private"},
])
def test_consent_is_explicit_and_failure_writes_no_reference(fixture, change):
    asset_store, root, audio, transcript = fixture
    with pytest.raises(SpeechAssetError, match="^voice_asset_consent_required$"):
        asset_store.register_reference(audio, transcript, consent=consent(**change))
    assert not list((root / "references").iterdir())


def test_explicit_permission_requires_retained_private_evidence(fixture):
    asset_store, _, audio, transcript = fixture
    evidence = "synthetic-local-permission-record"
    reference = asset_store.register_reference(audio, transcript, consent=consent(
        authority="explicit_permission", permission_evidence=evidence))
    assert reference.metadata["consent"]["permission_evidence"] == evidence
    assert evidence not in json.dumps(reference.public_metadata()) + repr(reference)


def test_reference_canonicalization_strips_ancillary_wav_metadata(fixture):
    _, _, audio, _ = fixture
    original = audio.read_bytes()
    marker = b"synthetic-private-ancillary"
    chunk = b"JUNK" + struct.pack("<I", len(marker)) + marker + b"\x00" * (len(marker) % 2)
    decorated = original + chunk
    decorated = decorated[:4] + struct.pack("<I", len(decorated) - 8) + decorated[8:]
    audio.write_bytes(decorated)
    reference = register(fixture)
    assert reference.audio_bytes == original
    assert marker not in reference.audio_bytes
    assert audio.read_bytes() == decorated
    assert reference.metadata["source_audio_sha256"] == hashlib.sha256(decorated).hexdigest()


@pytest.mark.parametrize("audio", [
    b"RIFF0000WAVE", wav_bytes(channels=2), wav_bytes(rate=4000),
    wav_bytes(frames=16000 * 61), wav_bytes()[:-1], wav_bytes(frames=0),
    wav_bytes()[:20] + struct.pack("<H", 3) + wav_bytes()[22:],
])
def test_invalid_reference_wav_is_rejected(fixture, audio):
    fixture[2].write_bytes(audio)
    with pytest.raises(SpeechAssetError, match="voice_asset_audio_invalid"):
        register(fixture)


@pytest.mark.parametrize("text", [b"", b" \n", b"\xff", b"secret\x00text", b"a" * (16 * 1024 + 1)])
def test_invalid_transcript_is_rejected(fixture, text):
    fixture[3].write_bytes(text)
    with pytest.raises(SpeechAssetError):
        register(fixture)


@pytest.mark.parametrize("git_kind", ["ordinary", "worktree", "bare"])
def test_private_root_cannot_be_inside_any_git_repository(tmp_path, git_kind):
    base = tmp_path.resolve() / "repository"
    base.mkdir()
    if git_kind == "ordinary":
        (base / ".git").mkdir()
        (base / ".gitignore").write_text("private/\n")
    elif git_kind == "worktree":
        (base / ".git").write_text("gitdir: synthetic-location\n")
    else:
        for name in ("HEAD", "config"):
            (base / name).touch()
        for name in ("objects", "refs"):
            (base / name).mkdir()
    target = base / "private" / "assets"
    with pytest.raises(SpeechAssetError, match="voice_asset_root_in_git"):
        SpeechAssetStore(target)
    assert not target.exists()


def test_root_rejects_relative_symlink_and_unsafe_permissions(tmp_path):
    base = tmp_path.resolve()
    with pytest.raises(SpeechAssetError, match="voice_asset_root_invalid"):
        SpeechAssetStore("relative-private-root")
    unsafe = base / "unsafe"
    unsafe.mkdir(mode=0o755)
    unsafe.chmod(0o755)
    with pytest.raises(SpeechAssetError, match="voice_asset_permissions"):
        SpeechAssetStore(unsafe)
    target = base / "target"
    target.mkdir(mode=0o700)
    link = base / "link"
    link.symlink_to(target, target_is_directory=True)
    for path in (link, link / "nested"):
        with pytest.raises(SpeechAssetError, match="voice_asset_root_invalid"):
            SpeechAssetStore(path)
    assert list(target.iterdir()) == []


@pytest.mark.parametrize("kind", ["root", "category", "asset"])
def test_new_git_marker_is_rejected_before_any_later_asset_access(fixture, kind):
    reference = register(fixture)
    target = {"root": fixture[1], "category": fixture[1] / "references", "asset": reference.audio_path.parent}[kind]
    (target / ".git").mkdir()
    with pytest.raises(SpeechAssetError, match="voice_asset_root_in_git"):
        fixture[0].load_reference(reference.reference_id)


def test_non_regular_source_is_rejected_without_blocking(fixture):
    source = fixture[2].with_name("synthetic-fifo")
    os.mkfifo(source)
    with pytest.raises(SpeechAssetError, match="voice_asset_file_invalid"):
        fixture[0].register_reference(source, fixture[3], consent=consent())


@pytest.mark.parametrize("kind", ["source", "source-parent", "category", "asset", "file"])
def test_symlinks_are_not_followed_at_any_asset_boundary(fixture, kind):
    asset_store, root, audio, transcript = fixture
    if kind.startswith("source"):
        if kind == "source":
            path = audio.with_name("source-link.wav")
            path.symlink_to(audio)
        else:
            path = audio.parent / "source-directory-link"
            path.symlink_to(audio.parent, target_is_directory=True)
            path = path / audio.name
        with pytest.raises(SpeechAssetError):
            asset_store.register_reference(path, transcript, consent=consent())
        return
    reference = register(fixture)
    target = {"category": root / "references", "asset": reference.audio_path.parent, "file": reference.audio_path}[kind]
    moved = target.with_name(target.name + "-preserved")
    target.rename(moved)
    target.symlink_to(moved, target_is_directory=moved.is_dir())
    with pytest.raises(SpeechAssetError):
        asset_store.load_reference(reference.reference_id)
    assert moved.exists()


@pytest.mark.parametrize("kind", ["category", "asset", "file", "hardlink"])
def test_load_rechecks_permissions_and_link_count(fixture, kind):
    reference = register(fixture)
    if kind == "hardlink":
        os.link(reference.audio_path, reference.audio_path.with_name("copy.wav"))
    else:
        target = {"category": fixture[1] / "references", "asset": reference.audio_path.parent, "file": reference.audio_path}[kind]
        target.chmod(0o640 if kind == "file" else 0o755)
    with pytest.raises(SpeechAssetError):
        fixture[0].load_reference(reference.reference_id)


@pytest.mark.parametrize("identity", ["../outside", "ref_" + "a" * 32, "/absolute", "a" * 64 + "/../outside"])
def test_identifiers_cannot_escape_the_store(fixture, identity):
    with pytest.raises(SpeechAssetError, match="voice_asset_id_invalid"):
        fixture[0].load_reference(identity)
    with pytest.raises(SpeechAssetError, match="voice_asset_id_invalid"):
        fixture[0].load_clone_prompt(identity)


def test_clone_prompt_roundtrip_is_deterministic_and_immutable(fixture, monkeypatch):
    asset_store, root, _, _ = fixture
    reference = register(fixture)
    prompt = clone(asset_store, reference)
    before = snapshot(root)
    observed = []
    real_load = np.load

    def safe_load(*arguments, **keywords):
        observed.append(keywords.get("allow_pickle"))
        return real_load(*arguments, **keywords)

    monkeypatch.setattr(np, "load", safe_load)
    loaded = asset_store.load_clone_prompt(prompt.prompt_digest)
    assert observed == [False, False]
    for name, value in arrays().items():
        np.testing.assert_array_equal(loaded.arrays[name], value)
        assert loaded.arrays[name].dtype == value.dtype
    assert loaded.metadata["ref_text"] == PRIVATE_TEXT
    assert loaded.metadata["model_revision"] == "synthetic-model-pin"
    loaded.arrays["ref_code"][:] = 0
    np.testing.assert_array_equal(asset_store.load_clone_prompt(prompt.prompt_digest).arrays["ref_code"], arrays()["ref_code"])
    with pytest.raises(SpeechAssetError, match="voice_asset_exists"):
        clone(asset_store, reference)
    assert snapshot(root) == before
    assert not list(root.glob("*/.staging-*"))
    exposed = json.dumps(prompt.public_metadata()) + repr(prompt)
    for private in (PRIVATE_TEXT, PRIVATE_ATTESTOR, str(root), "ref_code", "ref_spk_embedding"):
        assert private not in exposed


@pytest.mark.parametrize("bad", [
    {"ref_code": np.ones((2, 16), dtype=object)}, {"ref_code": np.ones((2, 16), dtype=np.int32)},
    {"ref_code": np.ones((2, 15), dtype=np.int64)}, {"ref_code": np.ones((4097, 16), dtype=np.int64)},
    {"ref_code": np.full((2, 16), -1, dtype=np.int64)}, {"ref_code": np.full((2, 16), 65536, dtype=np.int64)},
    {"ref_spk_embedding": np.array([float("nan")], dtype=np.float32)},
    {"ref_spk_embedding": np.array([float("inf")], dtype=np.float32)},
    {"ref_spk_embedding": np.ones((2, 2), dtype=np.float32)},
    {"ref_spk_embedding": np.ones(16385, dtype=np.float32)},
])
def test_clone_rejects_unbounded_or_unsafe_tensors(fixture, bad):
    reference = register(fixture)
    with pytest.raises(SpeechAssetError, match="voice_asset_tensor_invalid"):
        clone(fixture[0], reference, arrays={**arrays(), **bad})


@pytest.mark.parametrize("metadata", [
    {"x_vector_only_mode": True, "icl_mode": True, "ref_text": PRIVATE_TEXT},
    {"x_vector_only_mode": False, "icl_mode": False, "ref_text": PRIVATE_TEXT},
    {"x_vector_only_mode": False, "icl_mode": True, "ref_text": PRIVATE_TEXT.strip()},
    {"x_vector_only_mode": False, "icl_mode": True, "ref_text": PRIVATE_TEXT, "other": "private"},
])
def test_clone_metadata_requires_the_exact_verified_transcript(fixture, metadata):
    reference = register(fixture)
    with pytest.raises(SpeechAssetError, match="voice_asset_clone_metadata_invalid"):
        clone(fixture[0], reference, metadata=metadata)


@pytest.mark.parametrize("field", ["reference.wav", "transcript.txt", "manifest.json"])
def test_reference_tampering_fails_integrity_even_if_hash_fields_are_updated(fixture, field):
    reference = register(fixture)
    directory = reference.audio_path.parent
    manifest_path = directory / "manifest.json"
    manifest = json.loads(manifest_path.read_bytes())
    if field == "manifest.json":
        manifest["consent"]["attested_by"] = "modified-attestor"
    else:
        payload = wav_bytes(frames=320) if field.endswith("wav") else b"modified private transcript"
        (directory / field).write_bytes(payload)
        key = "audio_sha256" if field.endswith("wav") else "transcript_sha256"
        manifest[key] = hashlib.sha256(payload).hexdigest()
    manifest_path.write_bytes(implementation._json_bytes(manifest))
    with pytest.raises(SpeechAssetError, match="voice_asset_integrity"):
        fixture[0].load_reference(reference.reference_id)


@pytest.mark.parametrize("field", ["arrays.npz", "manifest.json"])
def test_clone_prompt_tampering_is_rejected(fixture, field):
    reference = register(fixture)
    prompt = clone(fixture[0], reference)
    path = fixture[1] / "prompts" / prompt.prompt_digest / field
    path.write_bytes(path.read_bytes() + b" ")
    with pytest.raises(SpeechAssetError, match="voice_asset_integrity"):
        fixture[0].load_clone_prompt(prompt.prompt_digest)


@pytest.mark.parametrize("kind", ["object", "compressed", "traversal", "duplicate", "declared-shape"])
def test_non_pickle_decoder_rejects_malicious_tensor_containers(kind, monkeypatch):
    # No pickle payload is executed or generated．The object header is sufficient．
    payload = io.BytesIO()
    if kind == "object":
        np.lib.format.write_array_header_1_0(payload, {"descr": "|O", "fortran_order": False, "shape": (1,)})
    elif kind == "declared-shape":
        np.lib.format.write_array_header_1_0(payload, {"descr": "<i8", "fortran_order": False, "shape": (1 << 50,)})
    else:
        np.lib.format.write_array(payload, arrays()["ref_code"], version=(1, 0), allow_pickle=False)
    container = io.BytesIO()
    compression = zipfile.ZIP_DEFLATED if kind == "compressed" else zipfile.ZIP_STORED
    with zipfile.ZipFile(container, "w", compression=compression) as archive:
        name = "../ref_code.npy" if kind == "traversal" else "ref_code.npy"
        archive.writestr(name, payload.getvalue())
        second = "ref_code.npy" if kind == "duplicate" else "ref_spk_embedding.npy"
        if kind == "duplicate":
            with pytest.warns(UserWarning, match="Duplicate name"):
                archive.writestr(second, payload.getvalue())
        else:
            archive.writestr(second, payload.getvalue())
    monkeypatch.setattr(np, "load", lambda *_a, **_kw: pytest.fail("unsafe input reached array load"))
    with pytest.raises(SpeechAssetError, match="voice_asset_tensor_invalid"):
        implementation._decode_arrays(container.getvalue())


def test_staging_failure_preserves_existing_assets_and_hides_private_error(fixture, monkeypatch):
    register(fixture)
    before = snapshot(fixture[1])
    real_write = implementation._write

    def fail_transcript(fd, name, data):
        if name == "transcript.txt":
            raise OSError("synthetic private path and transcript")
        real_write(fd, name, data)

    monkeypatch.setattr(implementation, "_write", fail_transcript)
    with pytest.raises(SpeechAssetError, match="^voice_asset_invalid$"):
        register(fixture)
    assert snapshot(fixture[1]) == before
    assert not list(fixture[1].glob("*/.staging-*"))


def test_atomic_publish_never_replaces_even_an_empty_destination(fixture, monkeypatch):
    real_rename = implementation._rename_exclusive
    destination = []

    def insert_destination(parent, old, new):
        os.mkdir(new, 0o700, dir_fd=parent)
        destination.append((new, os.stat(new, dir_fd=parent).st_ino))
        real_rename(parent, old, new)

    monkeypatch.setattr(implementation, "_rename_exclusive", insert_destination)
    with pytest.raises(SpeechAssetError, match="voice_asset_exists"):
        register(fixture)
    name, inode = destination[0]
    path = fixture[1] / "references" / name
    assert path.stat().st_ino == inode and not list(path.iterdir())
    assert not list(fixture[1].glob("*/.staging-*"))


def test_private_json_config_uses_the_same_safe_read_boundary(tmp_path):
    path = tmp_path.resolve() / "private-config.json"
    path.write_text('{"synthetic_config":true}', encoding="utf-8")
    path.chmod(0o600)
    assert read_private_json_file(path) == {"synthetic_config": True}
    assert read_private_json_file(str(path)) == {"synthetic_config": True}


@pytest.mark.parametrize("kind", ["relative", "symlink", "ancestor-symlink", "git", "permissions", "hardlink", "fifo", "duplicate", "body-limit", "non-object", "nan"])
def test_private_json_rejects_unsafe_paths_permissions_and_payloads(tmp_path, kind):
    base = tmp_path.resolve()
    path = base / "private-config.json"
    path.write_text('{"synthetic_config":true}', encoding="utf-8")
    path.chmod(0o600)
    if kind == "relative":
        path = "relative-private-config.json"
    elif kind == "symlink":
        link = base / "config-link.json"
        link.symlink_to(path)
        path = link
    elif kind == "ancestor-symlink":
        link = base / "parent-link"
        link.symlink_to(base, target_is_directory=True)
        path = link / path.name
    elif kind == "git":
        (base / ".git").mkdir()
    elif kind == "permissions":
        path.chmod(0o640)
    elif kind == "hardlink":
        os.link(path, base / "extra-link.json")
    elif kind == "fifo":
        path.unlink()
        os.mkfifo(path)
    else:
        path.write_bytes({"duplicate": b'{"value":1,"value":2}', "body-limit": b" " * (65536 + 1),
                          "non-object": b"[]", "nan": b'{"value":NaN}'}[kind])
    with pytest.raises(SpeechAssetError) as failure:
        read_private_json_file(path)
    assert str(base) not in str(failure.value)


@pytest.mark.parametrize("limit", [0, -1, True, 65537])
def test_private_json_has_a_finite_configured_limit(tmp_path, limit):
    with pytest.raises(SpeechAssetError, match="voice_asset_metadata_limit"):
        read_private_json_file(tmp_path.resolve() / "not-read.json", max_bytes=limit)
