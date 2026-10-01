#include "speech_evidence.h"
#include <cassert>
#include <iostream>
#include <limits>

static void frame(SpeechEvidence& evidence, float probability,
                  std::size_t samples = SpeechEvidence::frame_samples) {
    assert(evidence.observe(probability, samples));
    assert(evidence.speech_samples() <= evidence.audio_samples());
    assert(evidence.last_speech_sample() <= evidence.audio_samples());
}

static void silence_and_spikes() {
    SpeechEvidence s;
    // A whole 60-second input cannot accumulate low-probability noise.
    for (int i = 0; i < 1875; ++i) frame(s, .49f);
    assert(!s.has_speech() && s.speech_samples() == 0);
    assert(s.audio_samples() == 60 * SpeechEvidence::sample_rate);
    s.reset();
    frame(s, .99f);
    assert(!s.has_speech());
    frame(s, .01f);
    frame(s, .01f);
    frame(s, .99f);
    // This input incorrectly latched the old lifetime two-frame counter.
    assert(s.speech_samples() == 1024 && !s.has_speech());
    for (int i = 0; i < 300; ++i) {
        frame(s, .01f); frame(s, .01f); frame(s, .99f);
    }
    assert(!s.has_speech());
}

static void short_speech_and_pause() {
    SpeechEvidence s;
    frame(s, .5f);
    assert(!s.has_speech());
    frame(s, .5f);
    assert(s.has_speech() && s.speech_samples() == 1024);
    s.reset();
    frame(s, .8f); frame(s, .1f); frame(s, .8f);
    assert(s.has_speech() && s.speech_samples() == 1024);
    assert(s.last_speech_sample() == 1536);
    // Once genuine local evidence is established, a pause must not revoke it.
    for (int i = 0; i < 100; ++i) frame(s, 0);
    assert(s.has_speech() && !s.speaking());
    assert(s.last_speech_sample() == 1536 && s.probability() == 0);
    // No PCM amplitude, RMS floor, word count or phrase blacklist is involved.
}

static void terminal_valid_samples() {
    SpeechEvidence s;
    frame(s, .9f);
    frame(s, .9f, 1);
    assert(s.audio_samples() == 513 && s.speech_samples() == 513);
    assert(s.last_speech_sample() == 513 && !s.has_speech());
    s.reset();
    frame(s, .9f); frame(s, .9f, 511);
    assert(s.speech_samples() == 1023 && !s.has_speech());
    s.reset();
    frame(s, .9f); frame(s, .9f);
    frame(s, .1f, 7);
    assert(s.has_speech() && s.audio_samples() == 1031);
    assert(s.speech_samples() == 1024 && s.last_speech_sample() == 1024);
    // Silence padding cannot supply the missing part of a voiced frame.
    s.reset();
    frame(s, .99f, 17);
    assert(!s.has_speech() && s.speech_samples() == 17);
}

static void gap_boundaries() {
    SpeechEvidence s;
    frame(s, .9f); frame(s, 0, 511); frame(s, 0, 1); frame(s, .9f);
    assert(s.has_speech());
    s.reset();
    frame(s, .9f); frame(s, 0, 512); frame(s, 0, 1); frame(s, .9f);
    assert(!s.has_speech());
    frame(s, .9f);
    assert(s.has_speech());
}

static void errors_and_reset() {
    SpeechEvidence s;
    frame(s, .8f);
    for (float invalid : {-1.f, 1.001f, std::numeric_limits<float>::infinity(),
                          -std::numeric_limits<float>::infinity(),
                          std::numeric_limits<float>::quiet_NaN()}) {
        assert(!s.observe(invalid, 512));
        assert(s.audio_samples() == 512 && s.speech_samples() == 512);
        assert(s.probability() == .8f && !s.has_speech());
    }
    assert(!s.observe(.9f, 0) && !s.observe(.9f, 513));
    frame(s, .8f);
    assert(s.has_speech());
    s.reset();
    assert(!s.has_speech() && !s.speaking() && s.probability() == 0);
    assert(s.audio_samples() == 0 && s.speech_samples() == 0 && s.last_speech_sample() == 0);
    frame(s, .9f);
    assert(!s.has_speech());
    // A new session is independent even when the preceding one had speech.
    SpeechEvidence next;
    for (int i = 0; i < 100; ++i) frame(next, 0);
    assert(!next.has_speech());
}

static void exhaustive_full_frame_sequences() {
    // Independent reference: two voiced full frames at distance <= 2 in the
    // sequence suffice; two silent frames between positives must break onset.
    for (unsigned mask = 0; mask < (1U << 12); ++mask) {
        SpeechEvidence s;
        bool expected = false;
        for (unsigned i = 0; i < 12; ++i) {
            bool voiced = (mask & (1U << i)) != 0;
            if (voiced && ((i > 0 && (mask & (1U << (i - 1)))) ||
                           (i > 1 && (mask & (1U << (i - 2)))))) expected = true;
            frame(s, voiced ? .9f : .1f);
            assert(s.has_speech() == expected);
        }
    }
}

int main() {
    silence_and_spikes();
    short_speech_and_pause();
    terminal_valid_samples();
    gap_boundaries();
    errors_and_reset();
    exhaustive_full_frame_sequences();
    std::cout << "speech-evidence: 6 groups, 4096 exhaustive sequences PASS\n";
    std::cout << "Model-free probability fixtures only; acoustic/Japanese recall is unmeasured.\n";
}
