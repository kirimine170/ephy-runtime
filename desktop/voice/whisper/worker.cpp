#include "whisper.h"
#include "json.hpp"
#include "resampler.h"
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <csignal>
#include <iostream>
#include <memory>
#include <mutex>
#include <regex>
#include <thread>

using json = nlohmann::json;
using Clock = std::chrono::steady_clock;
static constexpr size_t frame_limit=96*1024;
static constexpr size_t text_limit=16*1024;
static std::mutex output_mutex;

static void output(const json& value) {
    const auto line=value.dump();
    if(line.size()>frame_limit) std::exit(2);
    std::lock_guard<std::mutex> lock(output_mutex);
    std::cout<<line<<'\n'<<std::flush;
    if(!std::cout) std::exit(0);
}
[[noreturn]] static void fatal(const char* code) {
    output({{"type","fatal"},{"protocol",1},{"error_code",code}});
    std::exit(2);
}
static std::vector<uint8_t> decode64(const std::string& text) {
    static const std::string alphabet="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    if(text.empty() || text.size()%4 || text.size()>87384) throw std::runtime_error("invalid_audio");
    std::vector<uint8_t> result;
    for(size_t i=0;i<text.size();i+=4) {
        uint32_t value=0; int padding=0;
        for(size_t j=0;j<4;++j) {
            const char c=text[i+j];
            if(c=='=') {
                if(i+4!=text.size() || j<2) throw std::runtime_error("invalid_audio");
                ++padding; value<<=6;
            } else {
                const auto index=alphabet.find(c);
                if(index==std::string::npos || padding) throw std::runtime_error("invalid_audio");
                value=(value<<6)|uint32_t(index);
            }
        }
        if((padding==1 && (value&255)) || (padding==2 && (value&65535))) throw std::runtime_error("invalid_audio");
        result.push_back(uint8_t(value>>16));
        if(padding<2) result.push_back(uint8_t(value>>8));
        if(!padding) result.push_back(uint8_t(value));
    }
    if(result.empty() || result.size()>65536 || result.size()%2) throw std::runtime_error("invalid_audio");
    return result;
}
static bool valid_id(const std::string& value) {
    static const std::regex pattern("^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$");
    return std::regex_match(value,pattern);
}
static std::string trim(std::string value) {
    const auto first=value.find_first_not_of(" \r\n\t");
    if(first==std::string::npos) return {};
    return value.substr(first,value.find_last_not_of(" \r\n\t")-first+1);
}

struct Session {
    json identity;
    Resampler resampler;
    std::vector<float> pcm;
    std::atomic<bool> canceled{false}, finish{false};
    int sequence=0,revision=0, speech_frames=0,step_ms=500;
    size_t vad_offset=0,last_activity=0,last_speech=0,last_decode=0, bytes=0;
    bool terminal=false, had_speech=false, partial=true;
    float probability=0;
    std::string text;
    Clock::time_point started=Clock::now();
    Clock::time_point decoded_at=Clock::time_point::min();
    Session(const json& start,int default_step=500):resampler(start.at("sample_rate").get<int>()) {
        partial=start.value("partial",true);
        step_ms=start.value("step_ms",default_step);
        if(step_ms<100 || step_ms>1500) throw std::runtime_error("asr_stream_invalid");
        for(const char* key:{"operation_id","session_id","turn_id","segment_id"}) {
            auto value=start.at(key).get<std::string>();
            if(!valid_id(value)) throw std::runtime_error("asr_stream_invalid");
            identity[key]=value;
        }
    }
};

class Worker {
    whisper_context* model=nullptr;
    whisper_vad_context* vad=nullptr;
    std::string revision;
    int threads,step_ms;
    std::mutex mutex;
    std::condition_variable changed;
    std::shared_ptr<Session> active;
    json last_terminal_identity;
    bool ending=false;
    std::thread inference;

    void emit(Session& session,const char* phase,const std::string& text="",const char* error="") {
        if(++session.revision>2048) fatal("asr_stream_invalid");
        json event=session.identity;
        event.update({{"type","update"},{"protocol",1},{"provider","whisper-cpp"},
            {"model_revision",revision},{"revision",session.revision},{"phase",phase},
            {"transcript",text},{"stable_prefix",std::string(phase)=="final"?text:""},
            {"error_code",error},{"monotonic_ms",std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now()-session.started).count()}});
        if(std::string(phase)=="activity") {
            event["activity"]={{"audio_ms",session.pcm.size()/16},{"last_speech_ms",session.last_speech/16},
                {"speech_ms",session.speech_frames*32},{"probability",session.probability},
                {"speaking",session.probability>=.5f},{"has_speech",session.had_speech}};
        }
        output(event);
    }
    void close(Session& session,const char* phase,const std::string& text="",const char* error="") {
        if(session.terminal) return;
        emit(session,phase,text,error);
        json ack=session.identity;
        ack.update({{"type","done"},{"protocol",1},{"phase",phase}});
        output(ack); session.terminal=true; last_terminal_identity=session.identity;
    }
    void activity(Session& s,bool flush) {
        while(s.vad_offset+512<=s.pcm.size() || (flush && s.vad_offset<s.pcm.size())) {
            float frame[512]{};
            const size_t count=std::min(size_t(512),s.pcm.size()-s.vad_offset);
            std::copy_n(s.pcm.data()+s.vad_offset,count,frame);
            if(!whisper_vad_detect_speech_no_reset(vad,frame,512)) fatal("asr_failed");
            const int n=whisper_vad_n_probs(vad);
            if(n<1) fatal("asr_failed");
            s.probability=whisper_vad_probs(vad)[n-1];
            if(!std::isfinite(s.probability)) fatal("asr_failed");
            s.vad_offset+=count;
            if(s.probability>=.5f) {
                ++s.speech_frames; s.last_speech=s.vad_offset;
                if(s.speech_frames>=2) s.had_speech=true;
            }
        }
        if(s.pcm.size()-s.last_activity>=1600 || flush) {
            s.last_activity=s.pcm.size(); emit(s,"activity");
        }
    }
    std::string transcribe(const std::vector<float>& audio,const std::shared_ptr<Session>& s,bool final,int& result) {
        auto params=whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
        params.n_threads=threads; params.language="ja"; params.translate=false;
        params.no_context=true; params.no_timestamps=true; params.single_segment=!final;
        params.print_special=false; params.print_progress=false; params.print_realtime=false; params.print_timestamps=false;
        params.temperature=0; params.temperature_inc=0; params.greedy.best_of=1;
        params.suppress_blank=true; params.suppress_nst=true;
        struct DecodeControl { Session* session; bool final; } control{s.get(),final};
        params.abort_callback=[](void* p){
            const auto& c=*static_cast<DecodeControl*>(p);
            return c.session->canceled.load() || (!c.final && c.session->finish.load());
        };
        params.abort_callback_user_data=&control;
        result=whisper_full(model,params,audio.data(),int(audio.size()));
        if(result!=0) return {};
        std::string text;
        for(int i=0;i<whisper_full_n_segments(model);++i) {
            const char* part=whisper_full_get_segment_text(model,i);
            if(part) text+=part;
            if(text.size()>text_limit) { result=-1; return {}; }
        }
        return trim(text);
    }
    void work() {
        std::unique_lock<std::mutex> lock(mutex);
        while(!ending) {
            changed.wait_for(lock,std::chrono::milliseconds(20));
            if(ending || !active) continue;
            auto s=active;
            if(s->canceled) { close(*s,"canceled","","asr_canceled"); active.reset(); continue; }
            if(!s->finish && (!s->partial || s->pcm.size()<6400 || !s->had_speech || s->last_decode>=s->last_speech+3200 || s->pcm.size()-s->last_decode<size_t(s->step_ms*16)
                || (s->last_decode && Clock::now()-s->decoded_at<std::chrono::milliseconds(s->step_ms)))) continue;
            if(s->finish && !s->had_speech) { close(*s,"no_speech"); active.reset(); continue; }
            const bool final=s->finish;
            // Final sees the whole bounded utterance．Partials are revisable windows．
            const size_t begin=final?0:(s->pcm.size()>8*16000?s->pcm.size()-8*16000:0);
            std::vector<float> audio(s->pcm.begin()+begin,s->pcm.end());
            s->last_decode=s->pcm.size();
            s->decoded_at=Clock::now();
            lock.unlock();
            int result=0;
            const auto text=transcribe(audio,s,final,result);
            lock.lock();
            if(ending) break;
            if(s->canceled) { close(*s,"canceled","","asr_canceled"); active.reset(); continue; }
            // A sealed input supersedes its in-flight partial．Abort that work
            // at the backend boundary and prioritize the whole-utterance final．
            if(!final && s->finish) { changed.notify_one(); continue; }
            if(result!=0) { close(*s,"failure","","asr_failed"); active.reset(); continue; }
            if(final) {
                if(text.empty()) close(*s,"failure","","asr_empty_result");
                else close(*s,"final",text);
                active.reset();
            } else if(!s->finish && text!=s->text && !text.empty()) { s->text=text; emit(*s,"partial",text); }
            // Finish received during a partial decode is handled immediately．
            if(active && active->finish) changed.notify_one();
        }
    }
public:
    Worker(const std::string& model_path,const std::string& vad_path,std::string model_revision,int n_threads,int update_ms)
        :revision(std::move(model_revision)),threads(n_threads),step_ms(update_ms) {
        auto model_params=whisper_context_default_params();
        model_params.use_gpu=true; model_params.flash_attn=true;
        model=whisper_init_from_file_with_params(model_path.c_str(),model_params);
        if(!model) fatal("asr_model_load_failed");
        auto vad_params=whisper_vad_default_context_params();
        vad_params.n_threads=1; vad_params.use_gpu=false;
        vad=whisper_vad_init_from_file_with_params(vad_path.c_str(),vad_params);
        if(!vad) fatal("asr_model_load_failed");
        // Warm the actual encoder and decoder before announcing readiness．
        auto warm=std::make_shared<Session>(json{{"operation_id","warmup"},{"session_id","warmup"},
            {"turn_id","warmup"},{"segment_id","warmup"},{"sample_rate",16000}});
        int status=0; transcribe(std::vector<float>(16000,0),warm,false,status);
        if(status!=0) fatal("asr_model_load_failed");
        inference=std::thread([this]{work();});
        output({{"type","ready"},{"protocol",1},{"provider","whisper-cpp"},{"model_revision",revision},
            {"sample_rate",16000},{"capabilities",{{"activity",true},{"partial",true},{"no_speech",true}}},{"step_ms",step_ms}});
    }
    ~Worker() {
        { std::lock_guard<std::mutex> lock(mutex); ending=true; if(active) active->canceled=true; }
        changed.notify_one(); if(inference.joinable()) inference.join();
        whisper_vad_free(vad); whisper_free(model);
    }
    void consume(const json& frame) {
        std::lock_guard<std::mutex> lock(mutex);
        if(frame.at("protocol")!=1) throw std::runtime_error("asr_stream_invalid");
        const std::string type=frame.at("type");
        if(type=="start") {
            if(active) throw std::runtime_error("asr_stream_invalid");
            active=std::make_shared<Session>(frame,step_ms); whisper_vad_reset_state(vad);
            if(active->identity==last_terminal_identity) throw std::runtime_error("asr_stream_invalid");
            json ack=active->identity; ack.update({{"type","started"},{"protocol",1}}); output(ack); return;
        }
        // A cancel can cross an already-sent terminal ACK on the pipe．It is
        // idempotent for that identity and cannot touch the next session．
        if(type=="cancel" && !last_terminal_identity.is_null()) {
            bool previous=true;
            for(const char* key:{"operation_id","session_id","turn_id","segment_id"})
                if(frame.at(key)!=last_terminal_identity[key]) previous=false;
            if(previous) return;
        }
        if(!active) throw std::runtime_error("asr_stream_invalid");
        auto& s=*active;
        for(const char* key:{"operation_id","session_id","turn_id","segment_id"})
            if(frame.at(key)!=s.identity[key]) throw std::runtime_error("asr_stream_invalid");
        if(type=="cancel") { s.canceled=true; changed.notify_one(); return; }
        if(s.finish || s.canceled) throw std::runtime_error("asr_stream_invalid");
        if(type=="audio") {
            const int sequence=frame.at("sequence");
            if(sequence!=s.sequence+1) throw std::runtime_error("asr_stream_invalid");
            const auto pcm=decode64(frame.at("pcm_base64").get<std::string>());
            auto converted=s.resampler.append(pcm); s.bytes+=pcm.size(); ++s.sequence;
            s.pcm.insert(s.pcm.end(),converted.begin(),converted.end()); activity(s,false);
        } else if(type=="finish") {
            if(!s.bytes) throw std::runtime_error("invalid_audio");
            auto tail=s.resampler.append({},true); s.pcm.insert(s.pcm.end(),tail.begin(),tail.end());
            activity(s,true); s.finish=true;
        } else throw std::runtime_error("asr_stream_invalid");
        changed.notify_one();
    }
};

int main(int argc,char** argv) {
    std::signal(SIGPIPE,SIG_IGN);
    whisper_log_set([](ggml_log_level,const char*,void*){},nullptr);
    ggml_log_set([](ggml_log_level,const char*,void*){},nullptr);
    try {
        std::string model,vad,revision; int threads=4,step=500;
        for(int i=1;i<argc;++i) {
            const std::string option=argv[i];
            if(i+1>=argc) fatal("invalid_voice_config");
            const std::string value=argv[++i];
            if(option=="--model") model=value;
            else if(option=="--vad") vad=value;
            else if(option=="--model-revision") revision=value;
            else if(option=="--threads") threads=std::stoi(value);
            else if(option=="--step-ms") step=std::stoi(value);
            else fatal("invalid_voice_config");
        }
        if(model.empty() || model.front()!='/' || vad.empty() || vad.front()!='/' || !valid_id(revision)
            || threads<1 || threads>16 || step<100 || step>1500) fatal("invalid_voice_config");
        Worker worker(model,vad,revision,threads,step);
        std::string line; char c;
        while(std::cin.get(c)) {
            if(c=='\n') {
                if(line.empty()) fatal("asr_stream_invalid");
                worker.consume(json::parse(line)); line.clear();
            } else {
                if(line.size()>=frame_limit) fatal("asr_stream_invalid");
                line.push_back(c);
            }
        }
        if(!line.empty()) fatal("asr_stream_invalid");
        return 0;
    } catch(const std::runtime_error& error) {
        const std::string code=error.what();
        fatal(code=="invalid_audio"?"invalid_audio":"asr_stream_invalid");
    } catch(...) { fatal("asr_stream_invalid"); }
}
