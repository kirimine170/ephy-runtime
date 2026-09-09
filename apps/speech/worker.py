"""Private worker．The parent kills it to cancel opaque third-party generation．"""

from __future__ import annotations

import base64
import json
import os
import sys

from .schemas import MAX_REQUEST_BYTES, SpeechError, error_code, parse_speech_request
from .service import encode_frame


def run_worker() -> int:
    source = sys.stdin.buffer
    output = os.fdopen(os.dup(sys.stdout.fileno()), "wb", buffering=0)
    # Third-party imports and inference can print arbitrary input/path details．
    # Keep their output away from both the protocol pipe and persistent logs．
    sink = open(os.devnull, "w")
    os.dup2(sink.fileno(), 1)
    os.dup2(sink.fileno(), 2)
    sys.stdout = sink
    sys.stderr = sink
    try:
        line = source.readline(64 * 1024 + 1)
        config = json.loads(line)
        if not line.endswith(b"\n") or set(config) != {"type", "provider", "config"} or config["type"] != "configure":
            return 1
        if config["provider"] == "qwen3-tts":
            from .qwen import QwenAdapter
            adapter = QwenAdapter(config["config"])
        elif config["provider"] == "irodori-tts":
            from .irodori import IrodoriAdapter
            adapter = IrodoriAdapter(config["config"])
        else:
            return 1
        output.write(encode_frame({"type": "ready"}))
        output.flush()
        while True:
            line = source.readline(2 * MAX_REQUEST_BYTES + 1)
            if not line:
                return 0
            request_id = "invalid"
            try:
                if not line.endswith(b"\n") or len(line) > 2 * MAX_REQUEST_BYTES:
                    raise SpeechError("invalid_speech_text")
                frame = json.loads(line)
                if set(frame) != {"type", "request", "profile"} or frame["type"] != "synthesize":
                    raise SpeechError("invalid_speech_text")
                request = parse_speech_request(frame["request"])
                request_id = request.request_id
                wav = adapter.synthesize(request, frame["profile"])
                result = {"type": "result", "request_id": request_id, "wav_base64": base64.b64encode(wav).decode("ascii")}
            except Exception as exc:
                result = {"type": "error", "request_id": request_id, "error_code": error_code(exc)}
            output.write(encode_frame(result))
            output.flush()
    except BaseException:
        return 1
