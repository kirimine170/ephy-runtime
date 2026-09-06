"""Private reference and tensor assets for an inference-side speech adapter．"""

from .store import ClonePromptAsset, ReferenceAsset, SpeechAssetError, SpeechAssetStore, read_private_json_file

__all__ = ["ClonePromptAsset", "ReferenceAsset", "SpeechAssetError", "SpeechAssetStore", "read_private_json_file"]
