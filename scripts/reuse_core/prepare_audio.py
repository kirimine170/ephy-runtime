"""Explicit setup only；pin/build native assets and derive a private comparison config．"""

from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import urllib.request

from apps.speech.audiocpp import (
    SOURCE_REVISION,
    MODEL_REVISION,
    MODEL_SHA256,
    MODEL_SIZE,
    ABI_VERSION,
    STYLE_MAPPING_REVISION,
    AudioCppABI,
    verified_file,
)


def digest(path):
    with Path(path).open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--assets", required=True, type=Path)
    p.add_argument("--source-config", required=True, type=Path)
    p.add_argument("--output-config", required=True, type=Path)
    p.add_argument("--download-model", action="store_true")
    args = p.parse_args()
    assets = args.assets.resolve()
    output = args.output_config.resolve()
    repository = Path(__file__).resolve().parents[2]
    if assets.is_relative_to(repository) or output.is_relative_to(repository):
        p.error("assets and private config must stay outside the repository")
    if output == args.source_config.resolve():
        p.error("preserve the baseline config")
    assets.mkdir(parents=True, exist_ok=True, mode=0o700)
    source = assets / "audio.cpp"
    build = assets / "audio-build"
    if not source.exists():
        subprocess.run(
            [
                "git",
                "clone",
                "--filter=blob:none",
                "--no-checkout",
                "https://github.com/0xShug0/audio.cpp.git",
                str(source),
            ],
            check=True,
        )
        subprocess.run(
            ["git", "-C", str(source), "checkout", "--detach", SOURCE_REVISION],
            check=True,
        )
        subprocess.run(
            ["git", "-C", str(source), "submodule", "update", "--init", "--recursive"],
            check=True,
        )
    revision = subprocess.check_output(
        ["git", "-C", str(source), "rev-parse", "HEAD"], text=True
    ).strip()
    dirty = subprocess.check_output(
        ["git", "-C", str(source), "status", "--porcelain"], text=True
    )
    if revision != SOURCE_REVISION or dirty:
        raise ValueError("pinned_clean_source_required")
    subprocess.run(
        [
            "cmake",
            "-S",
            str(source),
            "-B",
            str(build),
            "-DCMAKE_BUILD_TYPE=Release",
            "-DAUDIOCPP_BUILD_C_API=ON",
            "-DENGINE_ENABLE_METAL=ON",
            "-DENGINE_ENABLE_OPENMP=OFF",
        ],
        check=True,
    )
    subprocess.run(
        ["cmake", "--build", str(build), "--target", "audiocpp", "-j", "6"], check=True
    )
    library = (build / "bin/libaudiocpp.dylib").resolve(strict=True)
    # This pinned source reports "dev"；do not relabel it as v0.7.4．
    AudioCppABI(library, "dev")
    model = assets / "irodori-tts-v4.1-anime-q8_0.gguf"
    if not model.exists() and args.download_model:
        url = f"https://huggingface.co/audio-cpp/audio.cpp-gguf/resolve/{MODEL_REVISION}/Irodori-TTS-v4-Small-GGUF/irodori-tts-v4.1-anime-q8_0.gguf"
        temporary = model.with_suffix(".download")
        try:
            urllib.request.urlretrieve(url, temporary)
            verified_file(temporary, MODEL_SHA256, MODEL_SIZE)
            temporary.replace(model)
        finally:
            temporary.unlink(missing_ok=True)
    verified_file(model, MODEL_SHA256, MODEL_SIZE)
    dependencies = sorted({path.resolve() for path in build.rglob("*.dylib")})
    manifest = {
        "source_revision": SOURCE_REVISION,
        "build_version": "dev",
        "abi_version": ABI_VERSION,
        "library_sha256": digest(library),
        "cmake": {
            "ENGINE_ENABLE_METAL": True,
            "ENGINE_ENABLE_OPENMP": False,
            "AUDIOCPP_BUILD_C_API": True,
        },
        "dependencies": [
            {"path": str(path), "sha256": digest(path)} for path in dependencies
        ],
    }
    manifest_path = assets / "build-manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    config = json.loads(args.source_config.read_text())
    profile = next(
        dict(value)
        for value in config["profiles"]
        if value["voice_profile_id"] == "voice-irodori-anime-exp"
    )
    baseline = config["providers"]["irodori-tts"]
    provider = {
        key: baseline[key]
        for key in ("asset_store_path", "seed", "num_steps", "cfg")
        if key in baseline
    }
    provider.update(
        source_revision=SOURCE_REVISION,
        model_revision=MODEL_REVISION,
        model_path=str(model),
        model_sha256=MODEL_SHA256,
        library_path=str(library),
        library_sha256=digest(library),
        build_manifest_path=str(manifest_path),
        build_manifest_sha256=digest(manifest_path),
        build_version="dev",
        abi_version=ABI_VERSION,
        backend="metal",
        codec_backend="same",
        threads=4,
        style_mapping_revision=STYLE_MAPPING_REVISION,
    )
    config["providers"]["irodori-audiocpp"] = provider
    profile.update(
        voice_profile_id="voice-irodori-audiocpp-exp",
        provider="irodori-audiocpp",
        display_name="Irodori Anime audio.cpp experiment",
        model_revision=MODEL_REVISION,
    )
    config["profiles"] = [
        value
        for value in config["profiles"]
        if value["voice_profile_id"] != profile["voice_profile_id"]
    ] + [profile]
    output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    output.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
    output.chmod(0o600)
    print(f"Prepared private config: {output}")


if __name__ == "__main__":
    main()
