"""Foreground owner of only this experiment's child processes and state．"""

import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def main():
    p = argparse.ArgumentParser()
    p.add_argument(
        "--mode", choices=["baseline", "selected", "audio", "combined"], default="audio"
    )
    p.add_argument("--config", required=True)
    p.add_argument("--binary", required=True)
    p.add_argument("--speech-python")
    p.add_argument("--state", required=True)
    args = p.parse_args()
    state = Path(args.state).resolve()
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    for port in (18867, 18868, 18880):
        with socket.socket() as probe:
            probe.settimeout(0.2)
            if probe.connect_ex(("127.0.0.1", port)) == 0:
                raise RuntimeError("reuse_port_in_use")
        with socket.socket() as available:
            # A stopped mode can leave TIME_WAIT sockets．An active listener
            # is rejected above；the service bind is still authoritative．
            available.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            available.bind(("127.0.0.1", port))
    token = secrets.token_urlsafe(32)
    env = {
        k: v
        for k, v in os.environ.items()
        if k
        not in {
            "KARTE_DATA_DIR",
            "EPHY_RECORDING_HOME",
            "EPHY_TTS_BEARER_TOKEN",
            "EPHY_TTS_ENDPOINT",
        }
    }
    env.update(
        EPHY_REUSE_ENGINE="pydantic"
        if args.mode in {"selected", "combined"}
        else "fixed",
        EPHY_RUNTIME_ROOT=str(ROOT),
        EPHY_REUSE_PYTHON=sys.executable,
        EPHY_REUSE_STATE=str(state),
        EPHY_REUSE_PROFILE="voice-irodori-audiocpp-exp"
        if args.mode in {"audio", "combined"}
        else "voice-irodori-anime-exp",
        EPHY_TTS_ENDPOINT="http://127.0.0.1:18867",
        EPHY_TTS_BEARER_TOKEN=token,
        OTEL_SDK_DISABLED="true",
        PYDANTIC_AI_NO_BANNER="1",
        TMPDIR=str(state),
    )
    children = []

    def cleanup():
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=3)
        # No process-name matching or shared inference shutdown．

    def stop(*_):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)
    try:
        with (state / "speech.log").open("ab") as log:
            speech = subprocess.Popen(
                [
                    args.speech_python or sys.executable,
                    "-m",
                    "apps.speech",
                    "serve",
                    "--config",
                    str(Path(args.config).resolve()),
                    "--port",
                    "18867",
                ],
                cwd=ROOT,
                env=env,
                stdout=log,
                stderr=log,
            )
            children.append(speech)
        for _ in range(100):
            if speech.poll() is not None:
                raise RuntimeError("speech_service_exited")
            try:
                request = urllib.request.Request(
                    "http://127.0.0.1:18867/v1/voice-profiles",
                    headers={"Authorization": "Bearer " + token},
                )
                with urllib.request.urlopen(request, timeout=0.2) as response:
                    json.load(response)
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("speech_start_timeout")
        with (state / "candidate.log").open("ab") as log:
            candidate = subprocess.Popen(
                [
                    sys.executable,
                    "-m",
                    "uvicorn",
                    "scripts.reuse_core.candidate_service:app",
                    "--host",
                    "127.0.0.1",
                    "--port",
                    "18868",
                    "--no-access-log",
                    "--log-level",
                    "error",
                ],
                cwd=ROOT,
                env=env,
                stdout=log,
                stderr=log,
            )
            children.append(candidate)
        for _ in range(100):
            if candidate.poll() is not None:
                raise RuntimeError("candidate_service_exited")
            try:
                with socket.create_connection(("127.0.0.1", 18868), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("candidate_start_timeout")
        app = subprocess.Popen(
            [str(Path(args.binary).resolve()), "--reuse-experiment"], cwd=ROOT, env=env
        )
        children.append(app)
        (state / "owned-pids.json").write_text(
            json.dumps(
                {
                    "launcher": os.getpid(),
                    "speech": speech.pid,
                    "candidate": candidate.pid,
                    "app": app.pid,
                }
            )
        )
        print("Open http://127.0.0.1:18880 · stop with Ctrl-C", flush=True)
        return app.wait()
    except KeyboardInterrupt:
        return 0
    finally:
        cleanup()
        (state / "owned-pids.json").unlink(missing_ok=True)


if __name__ == "__main__":
    raise SystemExit(main())
