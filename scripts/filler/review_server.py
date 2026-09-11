#!/usr/bin/env python3
"""Local C0.4 human review．Only explicit votes approve immutable candidate hashes．"""
from __future__ import annotations
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import sys
import threading

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from packages.speech_assets import read_private_json_file
from scripts.filler.prepare import CANDIDATES, candidate_kind, candidate_max_duration_ms


def serve(bundle: Path, port: int) -> None:
    token = secrets.token_urlsafe(24)
    lock = threading.Lock()
    manifest_path = bundle / "manifest.json"
    manifest = read_private_json_file(manifest_path)
    audio_paths = {a["file"]: bundle / a["file"] for a in manifest["assets"] if a["candidate"] in CANDIDATES and a["file"] == a["candidate"] + ".wav"}
    audio_paths["review_answer.wav"] = bundle / "review_answer.wav"
    files = {"": (Path(__file__).with_name("review.html"), "text/html; charset=utf-8"),
             "review.js": (Path(__file__).with_name("review.js"), "text/javascript"),
             **{name: (path, "audio/wav") for name, path in audio_paths.items()},
             **{name: (ROOT / "desktop/frontend/src" / name, "text/javascript") for name in ("fillerController.js", "fillerTiming.js", "fillerBargeIn.js")}}

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # URLs，votes and audio are not HTTP access logs．

        def reply(self, status, body=b"", mime="application/json"):
            self.send_response(status)
            self.send_header("Content-Type", mime)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; media-src 'self' blob:; frame-ancestors 'none'")
            self.end_headers()
            self.wfile.write(body)

        def route(self):
            if self.headers.get("Host") != f"127.0.0.1:{self.server.server_port}":
                return None
            prefix = f"/{token}/"
            return self.path[len(prefix):] if self.path.startswith(prefix) else None

        def do_GET(self):
            route = self.route()
            if route == "manifest":
                with lock:
                    public = [{"candidate": a["candidate"], "kind": candidate_kind(a["candidate"]), "phrase": CANDIDATES[a["candidate"]], "file": a["file"],
                               "duration_ms": a["duration_ms"], "approved": a["approved"]} for a in manifest["assets"]]
                return self.reply(200, json.dumps(public, ensure_ascii=False).encode())
            if route not in files:
                return self.reply(404)
            path, mime = files[route]
            return self.reply(200, path.read_bytes(), mime)

        def do_POST(self):
            if self.route() != "review" or self.headers.get("Origin") != f"http://127.0.0.1:{self.server.server_port}":
                return self.reply(403)
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 < length <= 512:
                    return self.reply(400)
                vote = json.loads(self.rfile.read(length))
                if set(vote) != {"candidate", "approved"} or vote["candidate"] not in CANDIDATES or type(vote["approved"]) is not bool:
                    return self.reply(400)
                with lock:
                    # Reject external profile／asset replacement during review．
                    current = read_private_json_file(manifest_path)
                    if current != manifest:
                        return self.reply(409)
                    for asset in manifest["assets"]:
                        if asset["candidate"] == vote["candidate"]:
                            digest = hashlib.sha256(audio_paths[asset["file"]].read_bytes()).hexdigest()
                            if digest != asset["sha256"] or not 150 <= asset["duration_ms"] <= candidate_max_duration_ms(asset["candidate"]):
                                return self.reply(409)
                            asset["approved"] = vote["approved"]
                    # Review never changes live enablement or default voice．
                    temporary = bundle / (".review-" + secrets.token_hex(8))
                    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, "wb") as f:
                        f.write((json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode())
                    os.replace(temporary, manifest_path)
                return self.reply(200, b'{"saved":true}')
            except (ValueError, KeyError, OSError):
                return self.reply(400)

    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"http://127.0.0.1:{server.server_port}/{token}/", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--port", type=int, default=8794)
    args = parser.parse_args()
    serve(args.bundle.resolve(), args.port)
