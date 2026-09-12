#include "resampler.h"
#include <cassert>
#include <iostream>

static std::vector<uint8_t> tone(int rate, int frequency, double amplitude=.5) {
    std::vector<uint8_t> data;
    for (int n=0;n<rate;++n) {
        const auto value=int16_t(std::lround(amplitude*32767*std::sin(6.283185307179586*frequency*n/rate)));
        data.push_back(uint8_t(value)); data.push_back(uint8_t(uint16_t(value)>>8));
    }
    return data;
}
static double rms(const std::vector<float>& data) {
    double sum=0;
    for (size_t i=200;i<data.size()-200;++i) sum+=data[i]*data[i];
    return std::sqrt(sum/(data.size()-400));
}
int main() {
    for (const int rate:{8000,16000,22050,44100,48000}) {
        auto data=tone(rate,1000);
        Resampler one(rate), chunks(rate);
        const auto expected=one.append(data,true);
        std::vector<float> actual;
        for(size_t i=0;i<data.size();i+=514) {
            const size_t end=std::min(i+514,data.size());
            auto part=chunks.append(std::vector<uint8_t>(data.begin()+i,data.begin()+end));
            actual.insert(actual.end(),part.begin(),part.end());
        }
        auto tail=chunks.append({},true); actual.insert(actual.end(),tail.begin(),tail.end());
        assert(expected.size()==16000 && actual==expected);
        assert(std::abs(rms(actual)-.35355)<.005);
        if(rate>16000) {
            Resampler high(rate);
            assert(rms(high.append(tone(rate,10000),true))<.01);
        }
        bool rejected=false;
        try { chunks.append({0,0}); } catch (...) { rejected=true; }
        assert(rejected);
    }
    Resampler silence(48000);
    auto out=silence.append(std::vector<uint8_t>(48002,0),true);
    assert(out.size()==8001);
    for(float f:out) assert(f==0);
    bool rejected=false;
    try { Resampler(48000).append({0}); } catch (...) { rejected=true; }
    assert(rejected);
    std::cout<<"resampler: chunk identity，sample counts，passband，alias rejection，bounds PASS\n";
}
