import asyncio
import base64
import json
from pathlib import Path
import socket
import sys

import httpx
import pytest

from apps.speech.qwen import MODEL_REVISION, waveform_to_wav
from apps.speech.schemas import SpeechError, SpeechRequest
from apps.speech.service import ProcessWorker, SpeechService, create_app


PROFILE = {"voice_profile_id": "voice-1", "display_name": "Test voice", "provider": "qwen3-tts",
    "model_revision": MODEL_REVISION, "language": "ja-JP", "clone_prompt_digest": "a" * 64, "provenance_id": "prov_" + "b" * 32}


def request(**changes):
    return SpeechRequest.model_validate({"request_id": "request-1", "voice_profile_id": "voice-1", "model_revision": MODEL_REVISION,
        "clone_prompt_digest": "a" * 64, "speech_text": "確定した文です。", **changes})


class Connection:
    def __init__(self, disconnect=False):
        self.disconnect = disconnect

    async def is_disconnected(self):
        return self.disconnect


class FakeWorker:
    def __init__(self, *, delay=0, error=None):
        self.delay, self.error, self.calls, self.stops = delay, error, [], 0

    async def synthesize(self, speech, profile):
        self.calls.append(speech)
        try:
            await asyncio.sleep(self.delay)
        except asyncio.CancelledError:
            await self.stop()
            raise
        if self.error:
            raise self.error
        return waveform_to_wav([0.1] * 300, 24000, speech.volume)

    async def stop(self):
        self.stops += 1


def service(worker=None, **kwargs):
    return SpeechService({"default_profile_id": "voice-1", "profiles": [PROFILE]}, worker=worker or FakeWorker(), available=True, **kwargs)


async def collect(speech, connection=None):
    return [json.loads(item) async for item in speech.events(request(), connection or Connection())]


def test_audio_is_an_independent_wav_then_exact_completed_terminal():
    frames = asyncio.run(collect(service()))
    assert [item["type"] for item in frames] == ["audio", "completed"]
    assert base64.b64decode(frames[0]["wav_base64"])[:4] == b"RIFF"
    assert frames[-1] == {"type": "completed", "request_id": "request-1", "sequence": 1, "finish_reason": "stop"}


@pytest.mark.parametrize("mode", ["timeout", "disconnect", "cancel"])
def test_cancel_timeout_disconnect_stop_inflight_inference(mode):
    async def run():
        worker = FakeWorker(delay=10)
        speech = service(worker, request_timeout=0.03)
        if mode == "cancel":
            task = asyncio.create_task(collect(speech))
            await asyncio.sleep(0.01)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
        else:
            class InflightConnection(Connection):
                async def is_disconnected(self):
                    await asyncio.sleep(0)
                    return self.disconnect
            frames = await collect(speech, InflightConnection(mode == "disconnect"))
            assert frames == [{"type": "error", "request_id": "request-1", "error_code": "tts_canceled" if mode == "disconnect" else "tts_timeout"}]
        assert worker.stops >= 1
    asyncio.run(run())


def test_failure_is_body_free_and_never_completed():
    frames = asyncio.run(collect(service(FakeWorker(error=ValueError("PRIVATE transcript prompt embedding raw audio")))))
    assert frames == [{"type": "error", "request_id": "request-1", "error_code": "tts_failed"}]
    assert "PRIVATE" not in json.dumps(frames)


def test_catalog_and_http_validation_keep_private_data_out():
    async def run():
        worker = FakeWorker()
        app = create_app(service=service(worker))
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://127.0.0.1") as client:
            catalog = (await client.get("/v1/voice-profiles")).json()
            capabilities = catalog["profiles"][0]["capabilities"]
            assert capabilities["streaming_mode"] == "phrase" and set(capabilities["controls"]) == {"volume"}
            for body, code in [({**request().model_dump(), "speech_text": "PRIVATE " * 100}, "invalid_speech_text"),
                               ({**request().model_dump(), "pace": 2.0}, "unsupported_voice_control"),
                               ({**request().model_dump(), "clone_prompt_digest": "b" * 64}, "voice_profile_changed")]:
                result = await client.post("/v1/speech", json=body)
                assert result.status_code == 400 and result.json() == {"error_code": code}
                assert "PRIVATE" not in result.text
            result = await client.post("/v1/speech", json=request().model_dump(), headers={"Origin": "https://untrusted.example"})
            assert result.status_code == 400
            assert not worker.calls
            result = await client.post("/v1/speech", json=request().model_dump())
            assert result.status_code == 200 and result.headers["content-type"] == "application/x-ndjson"
            assert [json.loads(line)["type"] for line in result.text.splitlines()] == ["audio", "completed"]
    asyncio.run(run())


def test_default_catalog_is_offline_without_importing_or_loading_model():
    catalog = SpeechService().catalog()
    assert catalog == {"default_profile_id": "", "profiles": [], "error_code": "tts_unavailable"}


def fake_worker_command(tmp_path: Path, mode="valid"):
    audio = base64.b64encode(waveform_to_wav([0.1] * 100, 24000, 1)).decode()
    script = tmp_path / "worker.py"
    script.write_text("""import json,sys,time
json.loads(sys.stdin.readline())
print(json.dumps({'type':'ready'}),flush=True)
for line in sys.stdin:
 frame=json.loads(line)
 request=frame['request']
 if MODE=='blocked':time.sleep(30)
 if MODE=='delayed':time.sleep(0.15)
 if MODE=='malformed':print('PRIVATE raw diagnostic',flush=True)
 elif MODE=='identity':print(json.dumps({'type':'result','request_id':'old-request','wav_base64':AUDIO}),flush=True)
 elif MODE=='eof':sys.exit(0)
 else:print(json.dumps({'type':'result','request_id':request['request_id'],'wav_base64':AUDIO}),flush=True)
""".replace("MODE", repr(mode)).replace("AUDIO", repr(audio)))
    return [sys.executable, str(script)]


def test_real_worker_process_is_reused_serially(tmp_path):
    async def run():
        worker = ProcessWorker({}, command=fake_worker_command(tmp_path))
        try:
            assert await worker.synthesize(request(), PROFILE)
            pid = worker.process.pid
            assert await worker.synthesize(request(request_id="request-2"), PROFILE)
            assert worker.process.pid == pid
        finally:
            await worker.stop()
        assert worker.process is None
    asyncio.run(run())


@pytest.mark.parametrize("mode", ["malformed", "identity", "eof"])
def test_invalid_worker_frames_are_fixed_errors_and_worker_is_discarded(tmp_path, mode):
    async def run():
        worker = ProcessWorker({}, command=fake_worker_command(tmp_path, mode))
        with pytest.raises(SpeechError, match="tts_stream_eof"):
            await worker.synthesize(request(), PROFILE)
        assert worker.process is None
    asyncio.run(run())


def test_cancel_kills_process_and_following_request_spawns_clean_worker(tmp_path):
    async def run():
        worker = ProcessWorker({}, command=fake_worker_command(tmp_path, "blocked"))
        task = asyncio.create_task(worker.synthesize(request(), PROFILE))
        await asyncio.sleep(0.05)
        old = worker.process
        assert old is not None
        with pytest.raises(SpeechError, match="tts_busy"):
            await worker.synthesize(request(request_id="busy"), PROFILE)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert old.returncode is not None and worker.process is None
        worker.command = fake_worker_command(tmp_path)
        try:
            assert await worker.synthesize(request(request_id="next-request"), PROFILE)
            assert worker.process.pid != old.pid
        finally:
            await worker.stop()
    asyncio.run(run())


def test_canceled_before_start_contender_cannot_stop_the_worker_owner(tmp_path):
    async def run():
        worker = ProcessWorker({}, command=fake_worker_command(tmp_path, "delayed"))
        owner = asyncio.create_task(worker.synthesize(request(request_id="owner"), PROFILE))
        try:
            async with asyncio.timeout(1):
                while worker.process is None:
                    await asyncio.sleep(0.001)
            process = worker.process
            frames = await collect(service(worker), Connection(disconnect=True))
            assert frames[-1]["error_code"] == "tts_canceled"
            assert await owner
            assert worker.process is process and process.returncode is None
        finally:
            await worker.stop()
    asyncio.run(run())


def test_actual_http_disconnect_terminates_the_owned_worker(tmp_path):
    import uvicorn

    async def run():
        worker = ProcessWorker({}, command=fake_worker_command(tmp_path, "blocked"))
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
        server = uvicorn.Server(uvicorn.Config(create_app(service=service(worker)), log_level="critical", access_log=False))
        serving = asyncio.create_task(server.serve(sockets=[listener]))
        try:
            async with asyncio.timeout(2):
                while not server.started:
                    await asyncio.sleep(0.005)
            async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{port}") as client:
                async with client.stream("POST", "/v1/speech", json=request().model_dump()) as response:
                    assert response.status_code == 200
                    async with asyncio.timeout(2):
                        while worker.process is None:
                            await asyncio.sleep(0.005)
                    process = worker.process
                # Close a real TCP response while the model process is blocked．
                async with asyncio.timeout(2):
                    while process.returncode is None or worker.process is not None:
                        await asyncio.sleep(0.005)
        finally:
            server.should_exit = True
            await asyncio.wait_for(serving, timeout=3)
            listener.close()
    asyncio.run(run())
