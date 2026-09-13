"""Prepare explicit synthetic memories and build a pinned Karte context receiver．

The receiver calls Karte's existing Context V1 processor unchanged．No search
implementation，private memory copy，or alternate conversation UI is introduced．
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess

from scripts.build_recording_control import KARTE_REVISION, pinned_source


WRAPPER = r'''package main
import (
 "context"
 "flag"
 "fmt"
 "karte/internal/contextcore"
 "os"
 "os/signal"
 "path/filepath"
 "syscall"
 "time"
)
func main() {
 root := flag.String("data-root", "", "Explicit synthetic Karte data root")
 flag.Parse()
 if *root == "" { os.Exit(2) }
 marker, err := os.ReadFile(filepath.Join(*root, ".resident-fixture.json"))
 if err != nil || string(marker) != "{\"kind\":\"ephy-resident-synthetic-karte\",\"version\":1}\n" {
  fmt.Fprintln(os.Stderr, "synthetic_fixture_marker_required"); os.Exit(2)
 }
 processor, err := contextcore.NewProcessor(*root)
 if err != nil { fmt.Fprintln(os.Stderr, "karte_context_init_failed"); os.Exit(1) }
 ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
 defer cancel()
 ticker := time.NewTicker(100 * time.Millisecond)
 defer ticker.Stop()
 fmt.Println("karte_context_ready")
 for {
  if _, err := processor.ProcessPending(20); err != nil {
   fmt.Fprintln(os.Stderr, "karte_context_process_failed"); os.Exit(1)
  }
  select { case <-ctx.Done(): return; case <-ticker.C: }
 }
}
'''
MARKER = '{"kind":"ephy-resident-synthetic-karte","version":1}\n'


def _new_or_same(path, data, mode=0o600):
    if path.exists():
        if path.is_symlink() or path.read_bytes() != data:
            raise ValueError("Existing fixture/config differs；preserve it and choose another state root．")
        return
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with path.open("xb") as handle:
        os.fchmod(handle.fileno(), mode)
        handle.write(data)


def prepare(state: Path) -> dict:
    import yaml
    state = state.resolve()
    if any((p / ".git").exists() for p in (state, *state.parents)):
        raise ValueError("Fixture state must be outside Git repositories．")
    data_root = state / "karte"
    marker = data_root / ".resident-fixture.json"
    if not marker.exists() and (data_root / "content").exists() and any((data_root / "content").rglob("*")):
        raise ValueError("Never populate an existing memory workspace with synthetic fixtures．")
    _new_or_same(marker, MARKER.encode())
    fixture = json.loads(Path(__file__).with_name("fixtures.json").read_text())
    # This example uses the selected single-user owner ID used by resident UI．
    # The named fixture characters are fictional；they are not speaker detection．
    participants = ["owner"]
    grants = {"scope": {"projects": ["reuse-fixture"], "tags": ["synthetic"], "sensitivity_ceiling": "internal"},
              "participants": participants, "documents": [], "model_mode": "work"}
    files = []
    for item in fixture["documents"]:
        metadata = {"doc_id": item["id"], "title": item["query"], "project": "reuse-fixture",
                    "kind": "memory", "tags": ["synthetic"], "sensitivity": "internal"}
        text = "---\n" + yaml.safe_dump(metadata, allow_unicode=True, sort_keys=False) + "---\n" + item["body"] + "\n"
        path = data_root / "content/projects/reuse-fixture/memory" / (item["id"] + ".md")
        _new_or_same(path, text.encode())
        sha = hashlib.sha256(text.encode()).hexdigest()
        files.append({"doc_id": item["id"], "sha256": sha})
        if item["share"]:
            grants["documents"].append({"doc_id": item["id"], "sha256": sha, "participants": participants})
    policy = {"protocol_version": "1.0", "actors": {"ephy": {
        "sensitivity_ceiling": "internal", "projects": ["reuse-fixture"],
        "allowed_tags": ["synthetic"], "capabilities": ["search", "read"]},
        "human": {"sensitivity_ceiling": "internal", "projects": ["reuse-fixture"]}}}
    _new_or_same(data_root / ".mdsys/context/v1/policy.json", (json.dumps(policy, indent=2) + "\n").encode())
    _new_or_same(state / "participation-grants.json", (json.dumps(grants, indent=2) + "\n").encode())
    proof = {"synthetic": True, "karte_revision": KARTE_REVISION, "documents": files,
             "participants": participants, "example_observation": "公園のピクニック"}
    _new_or_same(state / "fixture-manifest.json", (json.dumps(proof, ensure_ascii=False, indent=2) + "\n").encode())
    return proof


def build(karte_root: Path, output: Path) -> dict:
    output = output.absolute()
    output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if output.exists():
        proof = json.loads(output.with_suffix(".provenance.json").read_text())
        if (proof.get("source_revision") != KARTE_REVISION
                or proof.get("wrapper_sha256") != hashlib.sha256(WRAPPER.encode()).hexdigest()
                or proof.get("executable_sha256") != hashlib.sha256(output.read_bytes()).hexdigest()):
            raise ValueError("Existing receiver provenance differs．")
        return proof
    with pinned_source(karte_root) as source:
        wrapper = source / "cmd/ephy-resident-context/main.go"
        wrapper.parent.mkdir()
        wrapper.write_text(WRAPPER)
        subprocess.run(["go", "build", "-buildvcs=false", "-ldflags=-w -s", "-o", str(output),
                        "./cmd/ephy-resident-context"], cwd=source, check=True,
                       env={**os.environ, "GOCACHE": str(output.parent / "karte-context-build-cache")})
    if platform.system() == "Darwin":
        subprocess.run(["codesign", "--force", "--sign", "-", "--identifier",
                        "com.ephy.resident-karte-context", str(output)], check=True)
    proof = {"source_revision": KARTE_REVISION, "source_dirty": False,
             "wrapper_sha256": hashlib.sha256(WRAPPER.encode()).hexdigest(),
             "executable_sha256": hashlib.sha256(output.read_bytes()).hexdigest()}
    _new_or_same(output.with_suffix(".provenance.json"), (json.dumps(proof, indent=2) + "\n").encode())
    return proof


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--karte-root", type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps({"fixture": prepare(args.state),
                      "receiver": build(args.karte_root, args.state / "karte-context-fixture")}, ensure_ascii=False, indent=2))
