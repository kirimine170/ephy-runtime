#pragma once
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <stdexcept>
#include <vector>

// Bounded，stateful windowed-sinc conversion．The sample clock uses integers，
// so 44.1 kHz conversion cannot drift at arbitrary PCM chunk boundaries．
class Resampler {
    static constexpr int output_rate = 16000;
    static constexpr int radius = 32;
    int rate;
    size_t next = 0;
    bool ended = false;
    std::vector<float> input;
public:
    explicit Resampler(int sample_rate) : rate(sample_rate) {
        if (rate < 8000 || rate > 48000) throw std::runtime_error("invalid_audio");
    }
    std::vector<float> append(const std::vector<uint8_t>& bytes, bool finish = false) {
        if (ended || bytes.size() % 2 || input.size() + bytes.size()/2 > size_t(rate)*60)
            throw std::runtime_error("invalid_audio");
        for (size_t i=0;i<bytes.size();i+=2) {
            const auto sample = int16_t(uint16_t(bytes[i]) | uint16_t(bytes[i+1])<<8);
            input.push_back(float(sample)/32768.0f);
        }
        ended = finish;
        const size_t count = (input.size()*output_rate + rate-1)/rate;
        std::vector<float> result;
        const double cutoff = .45*std::min(1.0, double(output_rate)/rate);
        constexpr double pi = 3.14159265358979323846;
        while (next < count) {
            const int64_t clock = int64_t(next)*rate;
            const int64_t center = clock/output_rate;
            if (!finish && rate != output_rate && center+radius >= int64_t(input.size())) break;
            if (rate == output_rate) { result.push_back(input[next++]); continue; }
            const double position = double(clock)/output_rate;
            double sum=0, weight=0;
            for (int64_t j=center-radius+1;j<=center+radius;++j) {
                const double delta = double(j)-position;
                const double sinc = std::abs(delta)<1e-12 ? 2*cutoff : std::sin(2*pi*cutoff*delta)/(pi*delta);
                const double window = .5+.5*std::cos(pi*delta/radius);
                const double coefficient = sinc*window;
                // Edge extension preserves DC and introduces no leading zero gap．
                const size_t index = size_t(std::clamp<int64_t>(j,0,int64_t(input.size())-1));
                sum += input[index]*coefficient; weight += coefficient;
            }
            result.push_back(float(sum/weight)); ++next;
        }
        return result;
    }
};
