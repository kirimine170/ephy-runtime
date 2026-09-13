"""Real Pipecat pipeline with synthetic frames and Ephy epoch/delivery adapters．"""

import asyncio
from dataclasses import dataclass
import json
from pathlib import Path
import time
from loguru import logger

logger.remove()  # No transcript/debug export．
from pipecat.frames.frames import (
    StartFrame,
    EndFrame,
    InterruptionFrame,
    TranscriptionFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
    LLMContextFrame,
    OutputAudioRawFrame,
)
from pipecat.processors.frame_processor import FrameProcessor
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.processors.aggregators.llm_response_universal import (
    LLMContextAggregatorPair,
    LLMUserAggregatorParams,
)
from pipecat.turns.user_turn_strategies import ExternalUserTurnStrategies
from pipecat.pipeline.pipeline import Pipeline
from pipecat.pipeline.worker import PipelineWorker, PipelineParams
from pipecat.workers.runner import WorkerRunner


@dataclass
class EpochAudio(OutputAudioRawFrame):
    epoch: int = 1
    sequence: int = 1


class DeliveryBoundary(FrameProcessor):
    """Runtime epoch/ACK stays authoritative；Pipecat owns its frame queue．"""

    def __init__(self):
        super().__init__()
        self.epoch = 1
        self.frames = []
        self.dropped = 0
        self.interruptions = 0
        self.contexts = 0
        self.ready = asyncio.Event()
        self.changed = asyncio.Event()

    async def process_frame(self, frame, direction):
        await super().process_frame(frame, direction)
        if isinstance(frame, StartFrame):
            self.ready.set()
        if isinstance(frame, LLMContextFrame):
            self.contexts += 1
            self.changed.set()
        if isinstance(frame, InterruptionFrame):
            self.epoch += 1
            self.interruptions += 1
            self.changed.set()
        if isinstance(frame, EpochAudio):
            if frame.epoch != self.epoch:
                self.dropped += 1
                self.changed.set()
                return
            self.frames.append(
                {
                    "generation_revision": frame.epoch,
                    "speech_unit_sequence": frame.sequence,
                    "state": "unknown",
                }
            )
            self.changed.set()
        await self.push_frame(frame, direction)

    def ack(self, epoch, sequence, state):
        for row in self.frames:
            if (
                row["generation_revision"] == epoch
                and row["speech_unit_sequence"] == sequence
            ):
                if row["state"] == "unknown" and state in {"completed", "interrupted"}:
                    row["state"] = state
                return
        raise ValueError("stale_ack")


async def wait_until(predicate):
    async with asyncio.timeout(5):
        while not predicate():
            await asyncio.sleep(0.01)


async def run(output):
    boundary = DeliveryBoundary()
    aggregators = LLMContextAggregatorPair(
        LLMContext([]),
        user_params=LLMUserAggregatorParams(
            user_turn_strategies=ExternalUserTurnStrategies(enable_interruptions=False)
        ),
    )
    worker = PipelineWorker(
        Pipeline([aggregators.user(), boundary]),
        params=PipelineParams(),
        enable_tracing=False,
        enable_rtvi=False,
        enable_turn_tracking=False,
        idle_timeout_secs=None,
    )
    runner = WorkerRunner(handle_sigint=False, handle_sigterm=False)
    await runner.add_workers(worker)
    job = asyncio.create_task(runner.run())
    try:
        await asyncio.wait_for(boundary.ready.wait(), 5)
        await worker.queue_frame(UserStartedSpeakingFrame())
        await asyncio.sleep(0.03)
        await worker.queue_frame(
            TranscriptionFrame(
                text="検証用の通常発話です．",
                user_id="fixture-hana",
                timestamp="2026-09-13T00:00:00Z",
                finalized=True,
            )
        )
        await asyncio.sleep(0.03)
        await worker.queue_frame(UserStoppedSpeakingFrame())
        await wait_until(lambda: boundary.contexts >= 1)
        await worker.queue_frame(
            EpochAudio(
                audio=b"\0\0" * 160,
                sample_rate=16000,
                num_channels=1,
                epoch=1,
                sequence=1,
            )
        )
        await wait_until(lambda: len(boundary.frames) == 1)
        # Ephy classifies a backchannel without emitting an interruption frame．
        before = boundary.interruptions
        await asyncio.sleep(0.03)
        backchannel_ok = before == boundary.interruptions
        boundary.ack(1, 1, "completed")
        start = time.monotonic()
        await worker.queue_frame(InterruptionFrame())
        await wait_until(lambda: boundary.epoch == 2)
        interruption_ms = (time.monotonic() - start) * 1000
        await worker.queue_frame(
            EpochAudio(
                audio=b"\0\0" * 160,
                sample_rate=16000,
                num_channels=1,
                epoch=1,
                sequence=2,
            )
        )
        await wait_until(lambda: boundary.dropped == 1)
        await worker.queue_frame(
            EpochAudio(
                audio=b"\0\0" * 160,
                sample_rate=16000,
                num_channels=1,
                epoch=2,
                sequence=1,
            )
        )
        await wait_until(lambda: len(boundary.frames) == 2)
        boundary.ack(2, 1, "interrupted")
        result = {
            "synthetic": True,
            "pipecat_revision": "f67c18afddbfb0609991cd6830355713baaad01b",
            "final_contexts": boundary.contexts,
            "backchannel_preserved": backchannel_ok,
            "stale_audio_dropped": boundary.dropped,
            "interruption_frame_ms": interruption_ms,
            "delivery": boundary.frames,
            "real_audio": False,
            "physical_playback_stop_measured": False,
            "note": "Ephy external turn decision and epoch/ACK adapter remain necessary．",
        }
        assert boundary.contexts == 1 and boundary.dropped == 1 and backchannel_ok
        print(json.dumps(result), flush=True)
        Path(output).write_text(json.dumps(result, indent=2) + "\n")
    finally:
        await worker.queue_frame(EndFrame())
        await asyncio.wait_for(job, 10)


if __name__ == "__main__":
    import argparse

    p = argparse.ArgumentParser()
    p.add_argument("--output", required=True)
    asyncio.run(run(p.parse_args().output))
