#!/usr/bin/env python3
"""Loopback-only review server that never exposes the unblinding key．"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import mimetypes
import os
from pathlib import Path
import re
import tempfile
from urllib.parse import unquote, urlsplit

RUNTIME = Path(__file__).resolve().parents[2]
if str(RUNTIME) not in __import__("sys").path:
    __import__("sys").path.insert(0, str(RUNTIME))

from scripts.voice_ab.common import load_corpus, read_json, validate_vote


TOKEN = re.compile(r"^[A-Za-z0-9_-]{32,128}$")


def atomic_private_json(path: Path, value: object) -> None:
    body = json.dumps(value, ensure_ascii=False, indent=2).encode("utf-8") + b"\n"
    descriptor, temporary = tempfile.mkstemp(prefix=".votes-", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "wb") as target:
            target.write(body)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


class ReviewServer(ThreadingHTTPServer):
    def __init__(self, address: tuple[str, int], root: Path, token: str, corpus: dict):
        super().__init__(address, Handler)
        self.review_root = root / "review"
        self.votes_path = root / "votes.json"
        self.token = token
        self.case_ids = {item["id"] for item in corpus["cases"]}
        self.case_order = {item["id"]: index for index, item in enumerate(corpus["cases"])}
        self.vote_lock = __import__("threading").Lock()

    def votes(self) -> list[dict]:
        if not self.votes_path.exists():
            return []
        value = read_json(self.votes_path)
        votes = value.get("votes")
        if value.get("schema_version") != 1 or not isinstance(votes, list):
            raise ValueError("invalid_votes")
        return [validate_vote(item, self.case_ids) for item in votes]

    def save(self, vote: dict) -> int:
        with self.vote_lock:
            votes = [item for item in self.votes() if item["case_id"] != vote["case_id"]]
            votes.append(vote)
            votes.sort(key=lambda item: self.case_order.get(item["case_id"], 10_000))
            value = {"schema_version": 1, "suite_id": "c0-3-2-voice-provider-ab-v1",
                     "updated_at": datetime.now(timezone.utc).isoformat(), "votes": votes}
            if len(votes) == len(self.case_ids):
                value["completed_at"] = value["updated_at"]
            atomic_private_json(self.votes_path, value)
            return len(votes)


class Handler(BaseHTTPRequestHandler):
    server: ReviewServer

    def log_message(self, *_: object) -> None:
        return

    def send_headers(self, status: int, content_type: str, length: int) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(length))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; media-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
        self.end_headers()

    def body(self, status: int, content_type: str, body: bytes) -> None:
        self.send_headers(status, content_type, len(body))
        self.wfile.write(body)

    def route(self) -> str | None:
        path = unquote(urlsplit(self.path).path)
        prefix = "/" + self.server.token + "/"
        if not path.startswith(prefix):
            return None
        return path[len(prefix):] or "index.html"

    def do_GET(self) -> None:
        route = self.route()
        if route is None:
            self.body(404, "text/plain", b"not found")
            return
        if route == "api/state":
            body = json.dumps({"votes": self.server.votes()}, ensure_ascii=False).encode("utf-8")
            self.body(200, "application/json; charset=utf-8", body)
            return
        relative = Path(route)
        if relative.is_absolute() or ".." in relative.parts:
            self.body(404, "text/plain", b"not found")
            return
        target = self.server.review_root / relative
        if not target.is_file() or target.is_symlink() or self.server.review_root not in target.resolve().parents:
            self.body(404, "text/plain", b"not found")
            return
        kind = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
        self.body(200, kind, target.read_bytes())

    def do_POST(self) -> None:
        if self.route() != "api/vote" or self.headers.get("Content-Type") != "application/json":
            self.body(404, "text/plain", b"not found")
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 1 <= length <= 16_000:
                raise ValueError("invalid_vote")
            vote = validate_vote(json.loads(self.rfile.read(length)), self.server.case_ids)
            count = self.server.save(vote)
            body = json.dumps({"saved": True, "count": count}).encode("ascii")
            self.body(200, "application/json", body)
        except Exception:
            self.body(400, "application/json", b'{"error":"invalid_vote"}')


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--corpus", type=Path, default=Path(__file__).with_name("corpus.json"))
    parser.add_argument("--port", type=int, default=8791)
    args = parser.parse_args()
    token = (args.root / "server-token").read_text(encoding="ascii")
    if not TOKEN.fullmatch(token) or not 1024 <= args.port <= 65535:
        raise ValueError("invalid_server_configuration")
    server = ReviewServer(("127.0.0.1", args.port), args.root, token, load_corpus(args.corpus))
    print(f"http://127.0.0.1:{args.port}/{token}/", flush=True)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
