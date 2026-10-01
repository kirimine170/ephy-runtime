# Speech-evidence suppression prototype

This is an experimental source patch, not a deployment or a demonstrated fix
for a particular microphone recording. The input source is ephy-runtime commit
`32ef71f495945ca5a577ecd8fe3251b49cf251f3`. Existing uncommitted Windows adapter
changes were unavailable and must be reconciled separately.

## Behavior

- Keep the existing Silero probability threshold of 0.5 and the existing 64 ms
  voiced minimum. Replace lifetime accumulation with evidence in one local run.
- Permit at most 32 ms of non-voiced frames within onset evidence. A longer gap
  clears the unconfirmed run, so distant spikes no longer authorize decoding.
- Count actual samples in a padded terminal frame. Padding cannot contribute
  speech time or move the processed-audio clock beyond the supplied input.
- `activity.audio_ms` now reports processed VAD samples, not all buffered PCM;
  an unprocessed partial frame can lag input by up to 511 samples (under 32 ms).
- Preserve confirmed speech through later pauses. A new Session starts empty.
- Use the same evidence latch for partial and final decoding. Without evidence,
  preserve the existing `no_speech` terminal and matching `done` ACK.
- Preserve decoder errors as errors. An empty decode after speech remains
  `asr_empty_result`. Invalid VAD values are `asr_failed`.
- Keep all transcript content eligible, including the genuine spoken phrase
  “ご視聴ありがとうございました”. There is no phrase blacklist, RMS floor,
  minimum word count, added Whisper threshold flag, or new model.

The 64 ms value preserves the old effective two-frame minimum. The 32 ms bridge
is a provisional policy choice, not a calibrated acoustic threshold. Adjacent
or closely spaced false positives may still pass; speech followed by long
silence can still produce decoder hallucinations. Short/quiet Japanese recall
and the user's actual root cause have not been measured.

## Conversation boundary

The optional engine patch checks an Activity-capable provider's positive
`HasSpeech` observation after Finish/final agreement and before
`acceptTranscript`. A contradictory nonempty final fails with
`asr_stream_invalid`; it is not relabeled as a normal `no_speech` result.

Current pinned Whisper emits ordered activity callbacks before final/done.
Only Whisper is the production Activity-capable provider identified in this
source path; native macOS ASR does not advertise Activity. Other/new providers
advertising Activity must honor this ordering before adopting the guard.
Text, transcript replay, batch ASR, and non-Activity streaming are unchanged.

`acceptTranscript` remains unchanged: recording Store.Begin (`user_final`),
Chat.Prompt, transcript events, and generation still occur there. Cancellation,
identity/revision checks, terminal/done ACK handling and model loading are not
changed. No helper wire fields or capabilities are added.

## Validation performed in the isolated Linux prototype

- GCC 14.2 C++17 with warnings-as-errors: policy tests and existing resampler pass.
- Policy coverage: sustained low probabilities, isolated and repeated separated
  spikes, short local voiced evidence, brief gaps, later pauses, valid terminal
  sample counts, invalid probability/count rejection, reset/next-session state,
  and 4,096 exhaustive 12-frame sequences.
- AddressSanitizer/UBSan: policy and resampler tests, with LeakSanitizer disabled
  because this execution environment uses ptrace. Leak detection is not passed.
- Static scope checks confirm existing close/ACK, consume/identity/cancel,
  decoder handling, adapter, and platform builder sections are unchanged.
- Repository validator passes on the selected source snapshot with all its
  required repository-shape files. This is not a complete repository test run.

Go regression tests are supplied but NOT executed: this environment has no Go
toolchain or gofmt. They cover rejected finals producing no canonical input,
prompt change, transcript event, resident action or LLM request; next-session
recovery; duplicate-final adoption exactly once; real phrase/short-answer
permissiveness; failure/no-speech separation; and exempt input paths.

The complete C++ worker was NOT built or run. No models were loaded, downloaded
or installed. No microphone or audio playback was opened, no new recordings
were made, and no network inference ran. Frontend and installed-helper suites
were NOT run. Python pytest is unavailable; no dependencies were installed.

## Before adoption

1. Review the two patches against the actual Windows working tree. Do not copy
   the pinned whole files over unrelated local edits. The helper builder remains
   Apple Silicon/Metal-specific in the pinned source and is not replaced here.
2. Run `gofmt` on the new Go test, then `go test -race ./...` from desktop.
   Include `TestActivityASR*`, `TestASREvidenceGuard*`, `TestWhisper*`, existing
   cancellation/next-turn tests, and recording regression tests.
3. Build the real platform helper with its existing pinned Whisper/Silero assets
   and run CTest; verify terminal/done ordering and cancellation with the actual
   helper. Do not enable new models or capture without authorization.
4. Use already authorized fixtures, or separately consented recordings, to
   compare silence, fan/keyboard/click noise, distant spikes, soft/brief はい and
   いや, negation, brief pauses, and genuine ご視聴ありがとうございました.
   Test 16/44.1/48 kHz, finish-tail boundaries, long silence, and cancel/restart.
   Count false finals and missed speech separately; do not accept a synthetic
   probability test as proof of acoustic accuracy.

Until these checks pass, the thresholds and Go guard remain review candidates.
