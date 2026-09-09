import asyncio
import base64
from copy import deepcopy
import json
from pathlib import Path
import socket
import sys

import httpx
import pytest

from apps.speech.irodori import MODEL_REVISION as IRODORI_MODEL_REVISION
from apps.speech.qwen import MODEL_REVISION, waveform_to_wav
from apps.speech.schemas import (DEFAULT_STYLE, IRODORI_CAPABILITIES, QWEN_CAPABILITIES,
                                 SpeechError, SpeechRequest, public_profile)
from apps.speech.service import ProcessWorker, SpeechService, create_app


TOKEN = "synthetic-bearer-" + "s" * 32
AUTH = {"Authorization": "Bearer " + TOKEN}


def identity():
    return {"request_id": "request-1", "operation_id": "operation-1", "session_id": "session-1",
            "turn_id": "turn-1", "generation_revision": 1, "speech_unit_sequence": 1}


PROFILE = {"voice_profile_id": "voice-1", "display_name": "Test voice", "provider": "qwen3-tts",
    "model_revision": MODEL_REVISION, "language": "ja-JP", "clone_prompt_digest": "a" * 64, "provenance_id": "prov_" + "b" * 32}
IRODORI_PROFILE = {"voice_profile_id": "anime-voice", "display_name": "Anime experimental", "provider": "irodori-tts",
    "model_revision": IRODORI_MODEL_REVISION, "language": "ja-JP", "reference_group_digest": "c" * 64,
    "provenance_id": "prov_" + "d" * 32}


def request(**changes):
    return SpeechRequest.model_validate({"request_id": "request-1", "voice_profile_id": "voice-1", "model_revision": MODEL_REVISION,
        "operation_id": "operation-1", "session_id": "session-1", "turn_id": "turn-1", "generation_revision": 1, "speech_unit_sequence": 1,
        "clone_prompt_digest": "a" * 64, "speech_text": "確定した文です。", **changes})


def irodori_request(**changes):
    return SpeechRequest.model_validate({"request_id": "request-anime", "operation_id": "operation-1",
        "session_id": "session-1", "turn_id": "turn-1", "generation_revision": 1, "speech_unit_sequence": 1,
        "voice_profile_id": "anime-voice", "model_revision": IRODORI_MODEL_REVISION,
        "reference_group_digest": "c" * 64, "speech_text": "確定した文です。", **changes})


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
    assert frames[-1] == {"type": "completed", **identity(), "sequence": 1, "finish_reason": "stop"}


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
            assert frames == [{"type": "error", **identity(), "error_code": "tts_canceled" if mode == "disconnect" else "tts_timeout"}]
        assert worker.stops >= 1
    asyncio.run(run())


def test_failure_is_body_free_and_never_completed():
    frames = asyncio.run(collect(service(FakeWorker(error=ValueError("PRIVATE transcript prompt embedding raw audio")))))
    assert frames == [{"type": "error", **identity(), "error_code": "tts_failed"}]
    assert "PRIVATE" not in json.dumps(frames)


def test_catalog_and_http_validation_keep_private_data_out():
    async def run():
        worker = FakeWorker()
        app = create_app(service=service(worker), bearer_token=TOKEN)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://127.0.0.1", headers=AUTH) as client:
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
            assert result.status_code == 403
            assert not worker.calls
            result = await client.post("/v1/speech", json=request().model_dump())
            assert result.status_code == 200 and result.headers["content-type"] == "application/x-ndjson"
            assert [json.loads(line)["type"] for line in result.text.splitlines()] == ["audio", "completed"]
    asyncio.run(run())


def test_default_catalog_is_offline_without_importing_or_loading_model():
    catalog = SpeechService().catalog()
    assert catalog == {"default_profile_id": "", "profiles": [], "error_code": "tts_unavailable"}


@pytest.mark.parametrize("volume", [0, 0.5, 1])
def test_catalog_preserves_supported_profile_default_volume(volume):
    speech = SpeechService({"default_profile_id": "voice-1", "profiles": [
        {**PROFILE, "default_style": {"volume": volume}},
    ]}, worker=FakeWorker(), available=True)
    assert speech.catalog()["profiles"][0]["default_style"] == {**DEFAULT_STYLE, "volume": volume}


@pytest.mark.parametrize("controls,expected_volume", [({}, 0.5), ({"volume": 1.0}, 1.0), ({"volume": 0}, 0)])
def test_http_omitted_controls_use_profile_defaults_and_explicit_values_win(controls, expected_volume):
    async def run():
        worker = FakeWorker()
        speech = SpeechService({"default_profile_id": "voice-1", "profiles": [
            {**PROFILE, "default_style": {"volume": 0.5}},
        ]}, worker=worker, available=True)
        body = {**request().model_dump(exclude_unset=True), **controls}
        assert ("volume" in body) == ("volume" in controls)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=create_app(service=speech, bearer_token=TOKEN)), base_url="http://127.0.0.1", headers=AUTH) as client:
            response = await client.post("/v1/speech", json=body)
        assert response.status_code == 200
        assert [json.loads(line)["type"] for line in response.text.splitlines()] == ["audio", "completed"]
        assert len(worker.calls) == 1 and worker.calls[0].volume == expected_volume
    asyncio.run(run())


@pytest.mark.parametrize("style", [
    {"affect": "angry"}, {"pace": 2.0}, {"intensity": 0.5}, {"pitch_hint": 1.0},
    {"pause_style": "long"}, {"interruptible": False}, {"volume": -1}, {"volume": 1.1},
    {"volume": True}, {"volume": float("nan")}, {"volume": float("inf")},
    {"volume": "0.5"}, {"unknown": "PRIVATE"}, {"speech_text": "PRIVATE"}, None, [],
])
def test_profile_rejects_unsupported_or_invalid_defaults_without_private_details(style):
    with pytest.raises(SpeechError) as error:
        public_profile({**PROFILE, "default_style": style}, available=True)
    assert str(error.value) == "unsupported_voice_control"


@pytest.mark.parametrize("capabilities", [
    {**QWEN_CAPABILITIES, "controls": {**QWEN_CAPABILITIES["controls"], "affect": {"type": "enum", "values": ["angry"]}}},
    {**QWEN_CAPABILITIES, "streaming_mode": "codec"},
    {**QWEN_CAPABILITIES, "streaming": 1},
    {**QWEN_CAPABILITIES, "controls": {"volume": {"type": "number", "min": 0, "max": True, "step": 0.05}}},
    {**QWEN_CAPABILITIES, "unknown": "PRIVATE"}, {}, None,
])
def test_profile_rejects_capabilities_that_the_provider_does_not_implement(capabilities):
    with pytest.raises(SpeechError) as error:
        public_profile({**PROFILE, "capabilities": capabilities}, available=True)
    assert str(error.value) == "unsupported_voice_control"


@pytest.mark.parametrize("claims", [
    {"available": True}, {"available": False}, {"error_code": "tts_unavailable"},
    {"error_code": "PRIVATE"}, {"unknown": "PRIVATE"},
])
def test_profile_configuration_cannot_claim_service_availability_or_unknown_fields(claims):
    with pytest.raises(SpeechError) as error:
        public_profile({**PROFILE, **claims}, available=True)
    assert str(error.value) == "invalid_speech_text"


def test_profile_capabilities_and_defaults_are_independent_copies():
    expected_caps, expected_style = deepcopy(QWEN_CAPABILITIES), deepcopy(DEFAULT_STYLE)
    config = {**PROFILE, "default_style": {**DEFAULT_STYLE, "volume": 0.5}, "capabilities": deepcopy(QWEN_CAPABILITIES)}
    # JSON integer bounds express the same numeric range as float bounds．
    config["capabilities"]["controls"]["volume"].update(min=0, max=1)
    original = deepcopy(config)
    first = public_profile(config, available=True)
    second = public_profile(PROFILE, available=False)
    assert first["default_style"]["volume"] == 0.5 and first["available"] is True and "error_code" not in first
    assert second["available"] is False and second["error_code"] == "tts_unavailable"
    first["default_style"]["volume"] = 0.2
    first["capabilities"]["controls"]["volume"]["max"] = 100
    first["capabilities"]["controls"]["affect"] = {"type": "enum", "values": ["angry"]}
    assert config == original
    assert second["default_style"] == DEFAULT_STYLE == expected_style
    assert second["capabilities"] == QWEN_CAPABILITIES == expected_caps
    assert public_profile(PROFILE, available=True)["capabilities"] == expected_caps


def test_irodori_profile_exposes_only_bounded_controls_and_cannot_be_default():
    public = public_profile(IRODORI_PROFILE, available=True)
    assert public["provider"] == "irodori-tts" and public["clone_prompt_digest"] == ""
    assert public["reference_group_digest"] == "c" * 64
    assert public["capabilities"] == IRODORI_CAPABILITIES
    assert set(public["capabilities"]["controls"]["affect"]["values"]) == {
        "neutral", "warm", "cheerful", "cute", "sleepy", "concerned"}
    with pytest.raises(SpeechError, match="invalid_voice_config"):
        SpeechService({"default_profile_id": "anime-voice", "profiles": [IRODORI_PROFILE]},
                      worker=FakeWorker(), available=True)


def test_irodori_http_style_and_reference_identity_are_provider_scoped():
    async def run():
        worker = FakeWorker()
        speech = SpeechService({"default_profile_id": "", "profiles": [PROFILE, IRODORI_PROFILE]},
                               worker=worker, available=True)
        body = irodori_request(affect="cute", intensity=1.25, pace=0.85, pause_style="deliberate").model_dump()
        async with httpx.AsyncClient(transport=httpx.ASGITransport(
                app=create_app(service=speech, bearer_token=TOKEN)),
                base_url="http://127.0.0.1", headers=AUTH) as client:
            response = await client.post("/v1/speech", json=body)
            assert response.status_code == 200
            assert [json.loads(line)["type"] for line in response.text.splitlines()] == ["audio", "completed"]
            damaged = await client.post("/v1/speech", json={**body, "reference_group_digest": "e" * 64})
            assert damaged.status_code == 400 and damaged.json() == {"error_code": "voice_profile_changed"}
            arbitrary = await client.post("/v1/speech", json={**body, "affect": "PRIVATE free prompt"})
            assert arbitrary.status_code == 400 and arbitrary.json() == {"error_code": "unsupported_voice_control"}
        assert len(worker.calls) == 1 and worker.calls[0].affect == "cute"
    asyncio.run(run())


@pytest.mark.parametrize("profile", [{}, {**PROFILE, "clone_prompt_digest": None}])
def test_malformed_profile_identity_returns_only_a_fixed_error(profile):
    with pytest.raises(SpeechError) as error:
        public_profile(profile, available=True)
    assert str(error.value) == "invalid_speech_text"


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


def test_worker_process_is_reused_per_provider_and_replaced_on_switch(tmp_path):
    async def run():
        worker = ProcessWorker({"providers": {"qwen3-tts": {}, "irodori-tts": {}}},
                               command=fake_worker_command(tmp_path))
        try:
            assert await worker.synthesize(irodori_request(), IRODORI_PROFILE)
            anime_pid = worker.process.pid
            assert await worker.synthesize(irodori_request(request_id="request-anime-2"), IRODORI_PROFILE)
            assert worker.process.pid == anime_pid
            assert await worker.synthesize(request(), PROFILE)
            assert worker.process.pid != anime_pid and worker.provider == "qwen3-tts"
        finally:
            await worker.stop()
        assert worker.process is None and worker.temporary is None
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
        server = uvicorn.Server(uvicorn.Config(create_app(service=service(worker), bearer_token=TOKEN), log_level="critical", access_log=False))
        serving = asyncio.create_task(server.serve(sockets=[listener]))
        try:
            async with asyncio.timeout(2):
                while not server.started:
                    await asyncio.sleep(0.005)
            async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{port}", headers=AUTH) as client:
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

@pytest.mark.parametrize('token', ['', 'short', 'x' * 129, 'x' * 43 + '\n', '非公開' * 20])
def test_http_service_fails_closed_without_valid_secret(token, monkeypatch):
    monkeypatch.setenv('EPHY_TTS_BEARER_TOKEN', token)
    with pytest.raises(SpeechError, match='^invalid_voice_config$'):
        create_app(service=service())


def test_http_authentication_covers_all_routes_before_body_or_worker(caplog):
    async def run():
        worker = FakeWorker()
        app = create_app(service=service(worker), bearer_token=TOKEN)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://127.0.0.1') as client:
            for path in ['/health', '/v1/voice-profiles', '/v1/speech', '/docs', '/unknown']:
                for headers in [{}, {'Authorization': 'Bearer wrong'}, {'Authorization': 'Basic ' + TOKEN},
                                [('Authorization', 'Bearer ' + TOKEN), ('Authorization', 'Bearer ' + TOKEN)]]:
                    result = await client.post(path, headers=headers, content=b'PRIVATE body')
                    assert result.status_code == 401
                    assert result.json() == {'error_code': 'tts_unauthorized'}
                    assert result.headers['cache-control'] == 'no-store'
                    assert TOKEN not in result.text and 'PRIVATE' not in result.text
            for path in ['/health', '/v1/voice-profiles', '/v1/speech']:
                for extra in [{'Origin': 'http://127.0.0.1'}, {'Origin': 'null'}, {'Host': 'attacker.example'}, {'Host': '192.0.2.1'}]:
                    result = await client.get(path, headers={**AUTH, **extra})
                    assert result.status_code == 403
                    assert result.json() == {'error_code': 'tts_forbidden'}
            assert not worker.calls
            for path in ['/health', '/v1/voice-profiles']:
                result = await client.get(path, headers=AUTH)
                assert result.status_code == 200 and result.headers['cache-control'] == 'no-store'
            result = await client.post('/v1/speech', headers=AUTH, json=request().model_dump())
            assert result.status_code == 200
            assert len(worker.calls) == 1
    asyncio.run(run())
    assert TOKEN not in caplog.text and 'PRIVATE body' not in caplog.text


def test_serve_keeps_loopback_and_disables_request_logging(monkeypatch):
    import uvicorn
    from apps.speech.__main__ import main
    seen = {}
    monkeypatch.setenv('EPHY_TTS_BEARER_TOKEN', TOKEN)
    monkeypatch.setattr(sys, 'argv', ['speech', 'serve', '--port', '8767'])
    monkeypatch.setattr(uvicorn, 'run', lambda app, **kwargs: seen.update(kwargs))
    assert main() == 0
    assert seen['host'] == '127.0.0.1'
    assert seen['access_log'] is False and seen['log_level'] == 'critical'


def test_inference_child_does_not_inherit_http_secret(tmp_path, monkeypatch):
    monkeypatch.setenv('EPHY_TTS_BEARER_TOKEN', TOKEN)
    script = tmp_path / 'auth_worker.py'
    script.write_text('import os,sys,json\n'
                      'json.loads(sys.stdin.readline())\n'
                      'assert "EPHY_TTS_BEARER_TOKEN" not in os.environ\n'
                      'print(json.dumps({"type":"ready"}),flush=True)\n'
                      'sys.stdin.read()\n')
    async def run():
        worker = ProcessWorker({}, command=[sys.executable, str(script)])
        try:
            await worker._start("qwen3-tts")
        finally:
            await worker.stop()
    asyncio.run(run())
