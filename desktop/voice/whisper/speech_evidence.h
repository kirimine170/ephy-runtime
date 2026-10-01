#pragma once
#include <cmath>
#include <cstddef>

// Model-free interpretation of ordered 16 kHz Silero frames. This policy does
// not infer speech from loudness or transcript content. The onset constants
// are experimental until evaluated with consented real helper fixtures.
class SpeechEvidence {
public:
    static constexpr std::size_t sample_rate = 16000;
    static constexpr std::size_t frame_samples = 512;
    // Preserve the old two-frame (64 ms) voiced minimum, but require evidence
    // in one local run. One frame of uncertainty may bridge a brief pause.
    static constexpr std::size_t min_voiced_samples = 2 * frame_samples;
    static constexpr std::size_t max_gap_samples = frame_samples;
    static constexpr float speech_threshold = .5f;

    // valid_samples excludes zero padding in the final Silero frame. Invalid
    // probabilities/counts are a decoder/input error, never evidence of silence.
    bool observe(float probability, std::size_t valid_samples) {
        if (!std::isfinite(probability) || probability < 0 || probability > 1 ||
            valid_samples == 0 || valid_samples > frame_samples) return false;
        probability_ = probability;
        audio_samples_ += valid_samples;
        speaking_ = probability >= speech_threshold;
        if (speaking_) {
            speech_samples_ += valid_samples;
            last_speech_sample_ = audio_samples_;
            candidate_voiced_samples_ += valid_samples;
            gap_samples_ = 0;
            if (candidate_voiced_samples_ >= min_voiced_samples) has_speech_ = true;
        } else if (candidate_voiced_samples_ != 0) {
            gap_samples_ += valid_samples;
            if (gap_samples_ > max_gap_samples) {
                candidate_voiced_samples_ = 0;
                gap_samples_ = 0;
            }
        }
        return true;
    }

    void reset() { *this = SpeechEvidence{}; }
    bool has_speech() const { return has_speech_; }
    bool speaking() const { return speaking_; }
    float probability() const { return probability_; }
    std::size_t audio_samples() const { return audio_samples_; }
    // Monotonic activity metrics include unconfirmed VAD positives. Only the
    // has_speech latch authorizes decoding, so distant spikes cannot combine.
    std::size_t speech_samples() const { return speech_samples_; }
    std::size_t last_speech_sample() const { return last_speech_sample_; }

private:
    std::size_t audio_samples_ = 0;
    std::size_t speech_samples_ = 0;
    std::size_t last_speech_sample_ = 0;
    std::size_t candidate_voiced_samples_ = 0;
    std::size_t gap_samples_ = 0;
    float probability_ = 0;
    bool speaking_ = false;
    bool has_speech_ = false;
};
