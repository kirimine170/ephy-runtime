"""Immutable，consented voice assets outside Git．No payload belongs in diagnostics．"""

from __future__ import annotations

import contextlib
import ctypes
import errno
import hashlib
import io
import json
import math
import os
import re
import stat
import struct
import sys
import uuid
import zipfile
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping


MAX_AUDIO_BYTES = 8 << 20
MAX_TRANSCRIPT_BYTES = 16 << 10
MAX_JSON_BYTES = 64 << 10
MAX_PROMPT_BYTES = 8 << 20
MAX_REFERENCE_GROUP_SIZE = 4
_ID = re.compile(r"^ref_[0-9a-f]{64}$")
_PROVENANCE = re.compile(r"^prov_[0-9a-f]{32}$")
_DIGEST = re.compile(r"^[0-9a-f]{64}$")
_REVISION = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,199}$")
_DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
_READ = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK


class SpeechAssetError(ValueError):
    """A fixed code only，safe to map at the service boundary．"""


@dataclass(frozen=True)
class ReferenceAsset:
    reference_id: str
    provenance_id: str
    reference_digest: str
    audio_path: Path = field(repr=False)
    transcript_path: Path | None = field(repr=False)
    transcript: str | None = field(repr=False)
    metadata: dict[str, Any] = field(repr=False)
    audio_bytes: bytes = field(repr=False)

    def public_metadata(self) -> dict[str, str]:
        return {"reference_id": self.reference_id, "provenance_id": self.provenance_id,
                "reference_digest": self.reference_digest}


@dataclass(frozen=True)
class ClonePromptAsset:
    prompt_digest: str
    reference_id: str
    arrays: dict[str, Any] = field(repr=False)
    metadata: dict[str, Any] = field(repr=False)
    provenance_id: str = ""

    def public_metadata(self) -> dict[str, str]:
        return {"clone_prompt_digest": self.prompt_digest, "provenance_id": self.provenance_id,
                "provider": self.metadata["provider"], "model_revision": self.metadata["model_revision"]}


@dataclass(frozen=True)
class ReferenceGroupAsset:
    group_digest: str
    provenance_id: str
    references: tuple[ReferenceAsset, ...] = field(repr=False)

    def public_metadata(self) -> dict[str, str | int]:
        return {"reference_group_digest": self.group_digest, "provenance_id": self.provenance_id,
                "reference_count": len(self.references)}


def _fail(code: str = "voice_asset_invalid") -> None:
    raise SpeechAssetError(code)


def _digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _json_bytes(value: Any) -> bytes:
    try:
        encoded = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")
    except (TypeError, ValueError, UnicodeError):
        _fail()
    if len(encoded) > MAX_JSON_BYTES:
        _fail("voice_asset_metadata_limit")
    return encoded


def _json_read(data: bytes) -> dict[str, Any]:
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                _fail()
            result[key] = value
        return result
    try:
        value = json.loads(data, object_pairs_hook=unique, parse_constant=lambda _: _fail())
    except (ValueError, UnicodeError, RecursionError):
        _fail()
    if not isinstance(value, dict):
        _fail()
    return value


def _private(info: os.stat_result, directory: bool) -> None:
    wanted = 0o700 if directory else 0o600
    if ((not stat.S_ISDIR(info.st_mode) if directory else not stat.S_ISREG(info.st_mode))
            or stat.S_IMODE(info.st_mode) != wanted or info.st_uid != os.getuid()):
        _fail("voice_asset_permissions")


def _exists(fd: int, name: str) -> bool:
    try:
        os.stat(name, dir_fd=fd, follow_symlinks=False)
        return True
    except FileNotFoundError:
        return False


def _git_directory(fd: int) -> bool:
    # Covers worktrees (.git file)，ordinary repositories and bare repositories．
    return _exists(fd, ".git") or all(_exists(fd, name) for name in ("HEAD", "objects", "refs", "config"))


@contextlib.contextmanager
def _directory_path(path: Path, *, create: bool = False, private: bool = False, reject_git: bool = False):
    if not path.is_absolute() or ".." in path.parts:
        _fail("voice_asset_root_invalid")
    fd = os.open(path.anchor, _DIRECTORY)
    try:
        for part in path.parts[1:]:
            if reject_git and _git_directory(fd):
                _fail("voice_asset_root_in_git")
            try:
                following = os.open(part, _DIRECTORY, dir_fd=fd)
            except FileNotFoundError:
                if not create:
                    raise
                os.mkdir(part, 0o700, dir_fd=fd)
                following = os.open(part, _DIRECTORY, dir_fd=fd)
                os.fchmod(following, 0o700)
            os.close(fd)
            fd = following
        if reject_git and _git_directory(fd):
            _fail("voice_asset_root_in_git")
        if private:
            _private(os.fstat(fd), True)
        yield fd
    finally:
        os.close(fd)


@contextlib.contextmanager
def _child_directory(parent: int, name: str, *, create: bool = False):
    if create:
        try:
            os.mkdir(name, 0o700, dir_fd=parent)
        except FileExistsError:
            pass
    fd = os.open(name, _DIRECTORY, dir_fd=parent)
    try:
        _private(os.fstat(fd), True)
        if _git_directory(fd):
            _fail("voice_asset_root_in_git")
        yield fd
    finally:
        os.close(fd)


def _read(fd: int, name: str, limit: int, *, private: bool = True) -> bytes:
    file_fd = os.open(name, _READ, dir_fd=fd)
    try:
        info = os.fstat(file_fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            _fail("voice_asset_file_invalid")
        if private:
            _private(info, False)
            if info.st_nlink != 1:
                _fail("voice_asset_file_invalid")
        with os.fdopen(file_fd, "rb", closefd=False) as stream:
            data = stream.read(limit + 1)
        after = os.fstat(file_fd)
        if (len(data) > limit or len(data) != info.st_size
                or (after.st_mtime_ns, after.st_ctime_ns, after.st_size) != (info.st_mtime_ns, info.st_ctime_ns, info.st_size)):
            _fail("voice_asset_file_invalid")
        return data
    finally:
        os.close(file_fd)


def _source(path: Path, limit: int) -> bytes:
    if not path.is_absolute():
        _fail("voice_asset_source_invalid")
    with _directory_path(path.parent) as parent:
        return _read(parent, path.name, limit, private=False)


def read_private_json_file(path: Path | str, max_bytes: int = MAX_JSON_BYTES) -> dict[str, Any]:
    """Read inference config or consent without following links or exposing errors．"""
    try:
        if type(max_bytes) is not int or not 0 < max_bytes <= MAX_JSON_BYTES:
            _fail("voice_asset_metadata_limit")
        path = Path(path)
        with _directory_path(path.parent, reject_git=True) as directory:
            return _json_read(_read(directory, path.name, max_bytes))
    except SpeechAssetError:
        raise
    except Exception:
        _fail()


def _write(fd: int, name: str, data: bytes) -> None:
    file_fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=fd)
    try:
        os.fchmod(file_fd, 0o600)
        with os.fdopen(file_fd, "wb", closefd=False) as stream:
            stream.write(data)
            stream.flush()
            os.fsync(file_fd)
    finally:
        os.close(file_fd)


def _rename_exclusive(parent: int, old: str, new: str) -> None:
    # Atomic exclusive directory publication，including existing empty targets．
    libc = ctypes.CDLL(None, use_errno=True)
    symbol, flag = ("renameatx_np", 4) if sys.platform == "darwin" else ("renameat2", 1)
    rename = getattr(libc, symbol, None)
    if rename is None:
        _fail("voice_asset_atomic_publish_unavailable")
    rename.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    rename.restype = ctypes.c_int
    if rename(parent, old.encode(), parent, new.encode(), flag) != 0:
        if ctypes.get_errno() in (errno.EEXIST, errno.ENOTEMPTY):
            _fail("voice_asset_exists")
        _fail("voice_asset_publish_failed")


def _publish(parent: int, name: str, files: Mapping[str, bytes]) -> None:
    staging = ".staging-" + uuid.uuid4().hex
    os.mkdir(staging, 0o700, dir_fd=parent)
    published = False
    try:
        with _child_directory(parent, staging) as stage:
            for filename, data in files.items():
                _write(stage, filename, data)
            os.fsync(stage)
        _rename_exclusive(parent, staging, name)
        published = True
        os.fsync(parent)
    finally:
        if not published:
            # Only this invocation's exclusive staging directory is disposable．
            with _child_directory(parent, staging) as stage:
                for filename in files:
                    if _exists(stage, filename):
                        os.unlink(filename, dir_fd=stage)
            os.rmdir(staging, dir_fd=parent)


def _consent(value: Mapping[str, Any], *, transcript_required: bool = True) -> dict[str, Any]:
    allowed = {"authority", "storage_allowed", "voice_clone_allowed", "synthesis_allowed", "transcript_verified", "clean_reference", "attested_by", "attested_at", "permission_evidence"}
    if not isinstance(value, Mapping) or set(value) - allowed:
        _fail("voice_asset_consent_required")
    result = dict(value)
    if result.get("authority") not in ("owned", "explicit_permission"):
        _fail("voice_asset_consent_required")
    for key in ("storage_allowed", "voice_clone_allowed", "synthesis_allowed", "clean_reference"):
        if result.get(key) is not True:
            _fail("voice_asset_consent_required")
    if result.get("transcript_verified") is not transcript_required:
        _fail("voice_asset_consent_required")
    for key, limit in (("attested_by", 200), ("attested_at", 80), ("permission_evidence", 4096)):
        text = result.get(key, "")
        if not isinstance(text, str) or len(text) > limit or "\x00" in text:
            _fail("voice_asset_consent_required")
        if key != "permission_evidence" and not text.strip():
            _fail("voice_asset_consent_required")
    if result["authority"] == "explicit_permission" and not result.get("permission_evidence", "").strip():
        _fail("voice_asset_consent_required")
    try:
        if datetime.fromisoformat(result["attested_at"].replace("Z", "+00:00")).tzinfo is None:
            _fail("voice_asset_consent_required")
    except ValueError:
        _fail("voice_asset_consent_required")
    return result


def _clean_wav(audio: bytes) -> tuple[bytes, dict[str, int]]:
    if len(audio) < 44 or len(audio) > MAX_AUDIO_BYTES or audio[:4] != b"RIFF" or audio[8:12] != b"WAVE" or struct.unpack_from("<I", audio, 4)[0] + 8 != len(audio):
        _fail("voice_asset_audio_invalid")
    rate, pcm = None, None
    offset = 12
    while offset < len(audio):
        if offset + 8 > len(audio):
            _fail("voice_asset_audio_invalid")
        kind, size = struct.unpack_from("<4sI", audio, offset)
        start = offset + 8
        if size > len(audio) - start:
            _fail("voice_asset_audio_invalid")
        if kind == b"fmt ":
            if rate is not None or size < 16:
                _fail("voice_asset_audio_invalid")
            form, channels, rate, byte_rate, align, bits = struct.unpack_from("<HHIIHH", audio, start)
            if form != 1 or channels != 1 or bits != 16 or align != 2 or not 8000 <= rate <= 48000 or byte_rate != rate * 2:
                _fail("voice_asset_audio_invalid")
        elif kind == b"data":
            if pcm is not None or not size or size % 2:
                _fail("voice_asset_audio_invalid")
            pcm = audio[start:start + size]
        offset = start + size + size % 2
        if offset > len(audio):
            _fail("voice_asset_audio_invalid")
    if rate is None or pcm is None or len(pcm) > rate * 2 * 60:
        _fail("voice_asset_audio_invalid")
    header = struct.pack("<4sI4s4sIHHIIHH4sI", b"RIFF", len(pcm) + 36, b"WAVE", b"fmt ", 16, 1, 1, rate, rate * 2, 2, 16, b"data", len(pcm))
    return header + pcm, {"sample_rate": rate, "sample_count": len(pcm) // 2}


def _transcript(data: bytes) -> str:
    try:
        text = data.decode("utf-8")
    except UnicodeError:
        _fail("voice_asset_transcript_invalid")
    if not text.strip() or "\x00" in text:
        _fail("voice_asset_transcript_invalid")
    return text


def _numpy():
    try:
        import numpy
    except ImportError:
        _fail("voice_asset_numpy_unavailable")
    return numpy


def _arrays(value: Mapping[str, Any]) -> dict[str, Any]:
    np = _numpy()
    if not isinstance(value, Mapping) or set(value) != {"ref_code", "ref_spk_embedding"}:
        _fail("voice_asset_tensor_invalid")
    code, embedding = value["ref_code"], value["ref_spk_embedding"]
    if (not isinstance(code, np.ndarray) or code.dtype != np.dtype("int64") or code.ndim != 2
            or not 1 <= code.shape[0] <= 4096 or code.shape[1] != 16
            or not isinstance(embedding, np.ndarray) or embedding.dtype != np.dtype("float32")
            or embedding.ndim != 1 or not 1 <= embedding.shape[0] <= 16384):
        _fail("voice_asset_tensor_invalid")
    if np.any(code < 0) or np.any(code > 65535) or not np.all(np.isfinite(embedding)):
        _fail("voice_asset_tensor_invalid")
    return {key: value[key].copy(order="C") for key in sorted(value)}


def _encode_arrays(value: Mapping[str, Any]) -> bytes:
    np = _numpy()
    destination = io.BytesIO()
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_STORED) as archive:
        for key, array in _arrays(value).items():
            payload = io.BytesIO()
            np.lib.format.write_array(payload, array, version=(1, 0), allow_pickle=False)
            member = zipfile.ZipInfo(key + ".npy", date_time=(1980, 1, 1, 0, 0, 0))
            member.external_attr = 0o600 << 16
            archive.writestr(member, payload.getvalue())
    data = destination.getvalue()
    if len(data) > MAX_PROMPT_BYTES:
        _fail("voice_asset_tensor_limit")
    return data


def _decode_arrays(data: bytes) -> dict[str, Any]:
    np = _numpy()
    result = {}
    with zipfile.ZipFile(io.BytesIO(data), "r") as archive:
        members = archive.infolist()
        if len(members) != 2 or {item.filename for item in members} != {"ref_code.npy", "ref_spk_embedding.npy"}:
            _fail("voice_asset_tensor_invalid")
        for member in members:
            if member.compress_type != zipfile.ZIP_STORED or member.file_size > MAX_PROMPT_BYTES or member.flag_bits & 1:
                _fail("voice_asset_tensor_invalid")
            payload = archive.read(member)
            header = io.BytesIO(payload)
            if np.lib.format.read_magic(header) != (1, 0):
                _fail("voice_asset_tensor_invalid")
            shape, fortran, dtype = np.lib.format.read_array_header_1_0(header)
            if fortran or dtype.hasobject or dtype.kind not in "if" or math.prod(shape) * dtype.itemsize != len(payload) - header.tell():
                _fail("voice_asset_tensor_invalid")
            result[member.filename[:-4]] = np.load(io.BytesIO(payload), allow_pickle=False)
    return _arrays(result)


class SpeechAssetStore:
    """Inference-private API．Only explicit public_metadata methods are exportable．"""

    def __init__(self, root: Path | str | None = None):
        default = Path.home() / "Library" / "Application Support" / "Ephy" / "voice-assets" if sys.platform == "darwin" else Path.home() / ".local" / "share" / "Ephy" / "voice-assets"
        self._root = Path(root if root is not None else os.environ.get("EPHY_VOICE_ASSET_ROOT", default))
        try:
            with _directory_path(self._root, create=True, private=True, reject_git=True) as directory:
                for category in ("references", "prompts", "groups"):
                    with _child_directory(directory, category, create=True):
                        pass
        except SpeechAssetError:
            raise
        except Exception:
            _fail("voice_asset_root_invalid")

    @contextlib.contextmanager
    def _category(self, category: str):
        with _directory_path(self._root, private=True, reject_git=True) as root:
            with _child_directory(root, category) as directory:
                yield directory

    def register_reference(self, reference_path: Path | str, transcript_path: Path | str, *, consent: Mapping[str, Any]) -> ReferenceAsset:
        try:
            permission = _consent(consent)
            source = _source(Path(reference_path), MAX_AUDIO_BYTES)
            audio, measurements = _clean_wav(source)
            transcript_bytes = _source(Path(transcript_path), MAX_TRANSCRIPT_BYTES)
            _transcript(transcript_bytes)
            provenance_id = "prov_" + uuid.uuid4().hex
            manifest = {"schema_version": 1, "provenance_id": provenance_id,
                        "source_audio_sha256": _digest(source), "audio_sha256": _digest(audio),
                        "transcript_sha256": _digest(transcript_bytes), "consent": permission,
                        "imported_at": datetime.now(timezone.utc).isoformat(), **measurements}
            reference_id = "ref_" + _digest(_json_bytes(manifest))
            manifest["reference_id"] = reference_id
            with self._category("references") as directory:
                _publish(directory, reference_id, {"reference.wav": audio, "transcript.txt": transcript_bytes, "manifest.json": _json_bytes(manifest)})
            return self.load_reference(reference_id)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def register_irodori_reference(self, reference_path: Path | str, *, consent: Mapping[str, Any]) -> ReferenceAsset:
        """Register audio-only conditioning for Irodori，which does not consume a transcript．"""
        try:
            permission = _consent(consent, transcript_required=False)
            source = _source(Path(reference_path), MAX_AUDIO_BYTES)
            audio, measurements = _clean_wav(source)
            provenance_id = "prov_" + uuid.uuid4().hex
            manifest = {"schema_version": 2, "provenance_id": provenance_id,
                        "source_audio_sha256": _digest(source), "audio_sha256": _digest(audio),
                        "conditioning_provider": "irodori-tts", "transcript_mode": "not_consumed",
                        "consent": permission, "imported_at": datetime.now(timezone.utc).isoformat(),
                        **measurements}
            reference_id = "ref_" + _digest(_json_bytes(manifest))
            manifest["reference_id"] = reference_id
            with self._category("references") as directory:
                _publish(directory, reference_id, {"reference.wav": audio, "manifest.json": _json_bytes(manifest)})
            return self.load_reference(reference_id)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def load_reference(self, reference_id: str) -> ReferenceAsset:
        try:
            if not isinstance(reference_id, str) or not _ID.fullmatch(reference_id):
                _fail("voice_asset_id_invalid")
            with self._category("references") as directory, _child_directory(directory, reference_id) as asset:
                manifest_bytes = _read(asset, "manifest.json", MAX_JSON_BYTES)
                manifest = _json_read(manifest_bytes)
                audio = _read(asset, "reference.wav", MAX_AUDIO_BYTES)
                transcript_bytes = (_read(asset, "transcript.txt", MAX_TRANSCRIPT_BYTES)
                                    if manifest.get("schema_version") == 1 else None)
            common = {"schema_version", "reference_id", "provenance_id", "source_audio_sha256",
                      "audio_sha256", "consent", "imported_at", "sample_rate", "sample_count"}
            version_one = common | {"transcript_sha256"}
            version_two = common | {"conditioning_provider", "transcript_mode"}
            if (set(manifest) not in (version_one, version_two) or manifest["schema_version"] not in (1, 2)
                    or (manifest["schema_version"] == 1 and set(manifest) != version_one)
                    or (manifest["schema_version"] == 2 and set(manifest) != version_two)
                    or manifest["reference_id"] != reference_id
                    or not _PROVENANCE.fullmatch(manifest["provenance_id"])
                    or not _DIGEST.fullmatch(manifest["source_audio_sha256"])
                    or manifest["audio_sha256"] != _digest(audio)):
                _fail("voice_asset_integrity")
            if manifest["schema_version"] == 1:
                if transcript_bytes is None or manifest["transcript_sha256"] != _digest(transcript_bytes):
                    _fail("voice_asset_integrity")
                transcript = _transcript(transcript_bytes)
                transcript_path: Path | None = self._root / "references" / reference_id / "transcript.txt"
                _consent(manifest["consent"])
            else:
                if (manifest["conditioning_provider"] != "irodori-tts"
                        or manifest["transcript_mode"] != "not_consumed"):
                    _fail("voice_asset_integrity")
                transcript = None
                transcript_path = None
                _consent(manifest["consent"], transcript_required=False)
            if "ref_" + _digest(_json_bytes({key: value for key, value in manifest.items() if key != "reference_id"})) != reference_id:
                _fail("voice_asset_integrity")
            clean, measurements = _clean_wav(audio)
            if clean != audio or any(manifest[key] != value for key, value in measurements.items()):
                _fail("voice_asset_integrity")
            base = self._root / "references" / reference_id
            return ReferenceAsset(reference_id, manifest["provenance_id"], _digest(manifest_bytes),
                                  base / "reference.wav", transcript_path, transcript, manifest, audio)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def store_reference_group(self, reference_ids: list[str] | tuple[str, ...]) -> ReferenceGroupAsset:
        """Publish an immutable ordered group of already consented reference clips．"""
        try:
            if (not isinstance(reference_ids, (list, tuple)) or
                    not 1 <= len(reference_ids) <= MAX_REFERENCE_GROUP_SIZE or
                    len(set(reference_ids)) != len(reference_ids)):
                _fail("voice_asset_group_invalid")
            references = tuple(self.load_reference(reference_id) for reference_id in reference_ids)
            identities = [{"reference_id": item.reference_id,
                           "reference_digest": item.reference_digest,
                           "provenance_id": item.provenance_id} for item in references]
            provenance_id = "prov_" + _digest(_json_bytes({"references": identities}))[:32]
            manifest = {"schema_version": 1, "provenance_id": provenance_id,
                        "references": identities}
            manifest_bytes = _json_bytes(manifest)
            digest = _digest(manifest_bytes)
            with self._category("groups") as directory:
                _publish(directory, digest, {"manifest.json": manifest_bytes})
            return self.load_reference_group(digest)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def load_reference_group(self, group_digest: str) -> ReferenceGroupAsset:
        try:
            if not isinstance(group_digest, str) or not _DIGEST.fullmatch(group_digest):
                _fail("voice_asset_id_invalid")
            with self._category("groups") as directory, _child_directory(directory, group_digest) as asset:
                manifest_bytes = _read(asset, "manifest.json", MAX_JSON_BYTES)
                manifest = _json_read(manifest_bytes)
            if (_digest(manifest_bytes) != group_digest or set(manifest) != {"schema_version", "provenance_id", "references"}
                    or manifest["schema_version"] != 1 or not _PROVENANCE.fullmatch(manifest["provenance_id"])
                    or not isinstance(manifest["references"], list)
                    or not 1 <= len(manifest["references"]) <= MAX_REFERENCE_GROUP_SIZE):
                _fail("voice_asset_integrity")
            references: list[ReferenceAsset] = []
            seen: set[str] = set()
            for identity in manifest["references"]:
                if (not isinstance(identity, dict)
                        or set(identity) != {"reference_id", "reference_digest", "provenance_id"}
                        or identity["reference_id"] in seen):
                    _fail("voice_asset_integrity")
                reference = self.load_reference(identity["reference_id"])
                if (identity["reference_digest"] != reference.reference_digest
                        or identity["provenance_id"] != reference.provenance_id):
                    _fail("voice_asset_integrity")
                seen.add(reference.reference_id)
                references.append(reference)
            identities = [{"reference_id": item.reference_id,
                           "reference_digest": item.reference_digest,
                           "provenance_id": item.provenance_id} for item in references]
            expected_provenance = "prov_" + _digest(_json_bytes({"references": identities}))[:32]
            if manifest["provenance_id"] != expected_provenance:
                _fail("voice_asset_integrity")
            return ReferenceGroupAsset(group_digest, manifest["provenance_id"], tuple(references))
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def store_clone_prompt(self, reference_id: str, *, provider: str, model_revision: str, tokenizer_revision: str, arrays: Mapping[str, Any], metadata: Mapping[str, Any]) -> ClonePromptAsset:
        try:
            reference = self.load_reference(reference_id)
            if reference.transcript is None:
                _fail("voice_asset_clone_metadata_invalid")
            for value in (provider, model_revision, tokenizer_revision):
                if not isinstance(value, str) or not _REVISION.fullmatch(value) or "://" in value or value.startswith("/"):
                    _fail("voice_asset_model_revision_invalid")
            if (set(metadata) != {"x_vector_only_mode", "icl_mode", "ref_text"}
                    or metadata["x_vector_only_mode"] is not False or metadata["icl_mode"] is not True
                    or metadata["ref_text"] != reference.transcript):
                _fail("voice_asset_clone_metadata_invalid")
            payload = _encode_arrays(arrays)
            manifest = {"schema_version": 1, "reference_id": reference_id, "reference_digest": reference.reference_digest,
                        "provenance_id": reference.provenance_id, "provider": provider, "model_revision": model_revision,
                        "tokenizer_revision": tokenizer_revision, "arrays_sha256": _digest(payload), "metadata": dict(metadata)}
            manifest_bytes = _json_bytes(manifest)
            digest = _digest(manifest_bytes)
            with self._category("prompts") as directory:
                _publish(directory, digest, {"arrays.npz": payload, "manifest.json": manifest_bytes})
            return self.load_clone_prompt(digest)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()

    def load_clone_prompt(self, prompt_digest: str) -> ClonePromptAsset:
        try:
            if not isinstance(prompt_digest, str) or not _DIGEST.fullmatch(prompt_digest):
                _fail("voice_asset_id_invalid")
            with self._category("prompts") as directory, _child_directory(directory, prompt_digest) as asset:
                manifest_bytes = _read(asset, "manifest.json", MAX_JSON_BYTES)
                manifest = _json_read(manifest_bytes)
                payload = _read(asset, "arrays.npz", MAX_PROMPT_BYTES)
            if (set(manifest) != {"schema_version", "reference_id", "reference_digest", "provenance_id", "provider", "model_revision", "tokenizer_revision", "arrays_sha256", "metadata"}
                    or _digest(manifest_bytes) != prompt_digest or manifest["schema_version"] != 1 or _digest(payload) != manifest["arrays_sha256"]):
                _fail("voice_asset_integrity")
            reference = self.load_reference(manifest["reference_id"])
            if manifest["reference_digest"] != reference.reference_digest or manifest["provenance_id"] != reference.provenance_id:
                _fail("voice_asset_integrity")
            metadata = manifest["metadata"]
            if (set(metadata) != {"x_vector_only_mode", "icl_mode", "ref_text"} or metadata["ref_text"] != reference.transcript
                    or metadata["x_vector_only_mode"] is not False or metadata["icl_mode"] is not True):
                _fail("voice_asset_clone_metadata_invalid")
            for key in ("provider", "model_revision", "tokenizer_revision"):
                value = manifest[key]
                if not isinstance(value, str) or not _REVISION.fullmatch(value) or "://" in value:
                    _fail("voice_asset_model_revision_invalid")
            public_identity = {key: manifest[key] for key in ("provider", "model_revision", "tokenizer_revision")}
            return ClonePromptAsset(prompt_digest, reference.reference_id, _decode_arrays(payload), {**metadata, **public_identity}, reference.provenance_id)
        except SpeechAssetError:
            raise
        except Exception:
            _fail()
