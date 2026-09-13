"""Pure reference group loading and temporary WAV assembly shared by TTS engines．"""

from __future__ import annotations
import array
from contextlib import contextmanager
import io
import os
from pathlib import Path
import sys
import tempfile
from typing import Any, Iterator
import wave
from .schemas import SpeechError


class ReferenceGroups:
    """The adapter owns config，group_loader and the bounded groups cache．"""

    def _group(self, digest: str, provenance_id: str) -> Any:
        if digest not in self.groups:
            if self.group_loader is not None:
                group = self.group_loader(digest)
            else:
                from packages.speech_assets import SpeechAssetStore

                group = SpeechAssetStore(
                    Path(self.config["asset_store_path"])
                ).load_reference_group(digest)
            if group.provenance_id != provenance_id or len(self.groups) >= 16:
                raise SpeechError("tts_asset_invalid")
            self.groups[digest] = group
        group = self.groups[digest]
        if group.provenance_id != provenance_id:
            raise SpeechError("tts_asset_invalid")
        return group

    @contextmanager
    def _reference_wav(self, group: Any) -> Iterator[str]:
        # Irodori v4.1 accepts one ref_wav．An ordered profile group is rendered
        # to one short，private，request-scoped WAV and unlinked immediately．
        try:
            target_rate = max(item.metadata["sample_rate"] for item in group.references)
            if not 8000 <= target_rate <= 48000:
                raise SpeechError("tts_asset_invalid")
            pieces: list[list[float]] = []
            for item in group.references:
                with wave.open(io.BytesIO(item.audio_bytes), "rb") as source:
                    pcm = array.array("h", source.readframes(source.getnframes()))
                    if sys.byteorder != "little":
                        pcm.byteswap()
                    samples = [sample / 32768.0 for sample in pcm]
                    rate = source.getframerate()
                if rate != target_rate:
                    count = max(1, round(len(samples) * target_rate / rate))
                    if len(samples) == 1:
                        samples = samples * count
                    else:
                        scale = (len(samples) - 1) / max(1, count - 1)
                        resampled = []
                        for index in range(count):
                            position = index * scale
                            left = min(int(position), len(samples) - 1)
                            right = min(left + 1, len(samples) - 1)
                            fraction = position - left
                            resampled.append(
                                samples[left] * (1 - fraction)
                                + samples[right] * fraction
                            )
                        samples = resampled
                pieces.append(samples)
                pieces.append([0.0] * round(target_rate * 0.05))
            combined = [sample for piece in pieces[:-1] for sample in piece]
            if not 0 < len(combined) <= target_rate * 30:
                raise SpeechError("tts_asset_invalid")
            root = Path(self.config["temporary_path"])
            if not root.is_absolute() or not root.is_dir() or root.is_symlink():
                raise SpeechError("tts_asset_invalid")
            handle = tempfile.NamedTemporaryFile(
                prefix="ref-", suffix=".wav", dir=root, delete=False
            )
            path = Path(handle.name)
            os.fchmod(handle.fileno(), 0o600)
            with wave.open(handle, "wb") as output:
                output.setnchannels(1)
                output.setsampwidth(2)
                output.setframerate(target_rate)
                values = array.array(
                    "h",
                    (round(max(-1.0, min(1.0, float(v))) * 32767) for v in combined),
                )
                if sys.byteorder != "little":
                    values.byteswap()
                output.writeframes(values.tobytes())
            handle.close()
        except SpeechError:
            raise
        except Exception:
            raise SpeechError("tts_asset_invalid") from None
        try:
            yield str(path)
        finally:
            try:
                path.unlink(missing_ok=True)
            except Exception:
                pass
