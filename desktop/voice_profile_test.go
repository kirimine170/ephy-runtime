package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func customTestProfile() VoiceProfile {
	p := (&NativeVoiceTTS{voice: "Synthetic", locale: "ja-JP", osName: "darwin"}).Profile()
	p.VoiceProfileID = "synthetic-voice"
	p.Provider = "synthetic-tts"
	p.ModelRevision = "revision-1"
	p.ClonePromptDigest = strings.Repeat("a", 64)
	p.ProvenanceID = "synthetic-provenance"
	return p
}
func TestVoiceProfileRejectsUnsupportedAndPinsStyle(t *testing.T) {
	p := customTestProfile()
	for _, change := range []func(*SpeechStyle){func(s *SpeechStyle) { s.Affect = "happy" }, func(s *SpeechStyle) { s.PitchHint = 1 }, func(s *SpeechStyle) { s.Intensity = 0.5 }, func(s *SpeechStyle) { s.PauseStyle = "dramatic" }, func(s *SpeechStyle) { s.Interruptible = false }, func(s *SpeechStyle) { s.Volume = math.NaN() }, func(s *SpeechStyle) { s.Pace = 5 }} {
		s := p.DefaultStyle
		change(&s)
		if validateSpeechStyle(s, p) == nil {
			t.Fatal("unsupported control accepted")
		}
	}
	style := p.DefaultStyle
	style.Volume = 0
	provider := &syntheticSpeechProvider{profile: p}
	prepared, err := prepareProfile(provider, SpeechOptions{VoiceProfileID: p.VoiceProfileID, Style: &style})
	if err != nil {
		t.Fatal(err)
	}
	style.Volume = 1
	p.Capabilities.Controls["volume"] = VoiceControl{Type: "number", Max: 100}
	if prepared.style.Volume != 0 || prepared.profile.Capabilities.Controls["volume"].Max != 1 {
		t.Fatal("prepared identity mutated")
	}
	if _, err := prepareProfile(provider, SpeechOptions{VoiceProfileID: "missing"}); err == nil {
		t.Fatal("unknown profile fell back silently")
	}
}

type syntheticSpeechProvider struct {
	profile VoiceProfile
	stream  func(context.Context, SpeechRequest, func([]byte) error) error
}

func (p *syntheticSpeechProvider) Profile() VoiceProfile           { return p.profile }
func (p *syntheticSpeechProvider) Ready(ctx context.Context) error { return ctx.Err() }
func (p *syntheticSpeechProvider) Stream(context.Context, string, func([]byte) error) error {
	return errors.New("legacy stream must not be called")
}
func (p *syntheticSpeechProvider) StreamSpeech(ctx context.Context, r SpeechRequest, e func([]byte) error) error {
	if p.stream != nil {
		return p.stream(ctx, r, e)
	}
	return e(providerTestWAV())
}

func speechServer(t *testing.T, handler func(http.ResponseWriter, *http.Request, map[string]any)) *ServiceVoiceTTS {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		handler(w, r, request)
	}))
	t.Cleanup(server.Close)
	return &ServiceVoiceTTS{client: &speechServiceClient{bearerToken: strings.Repeat("s", 43), endpoint: server.URL, client: server.Client()}, profile: customTestProfile()}
}
func speechAudioFrame(id any) map[string]any {
	return map[string]any{"type": "audio", "request_id": id, "sequence": 1, "wav_base64": base64.StdEncoding.EncodeToString(providerTestWAV())}
}
func speechCompletedFrame(id any) map[string]any {
	return map[string]any{"type": "completed", "request_id": id, "sequence": 1, "finish_reason": "stop"}
}

func TestSpeechServiceRequiresTerminalAndRejectsProtocolDamage(t *testing.T) {
	for _, kind := range []string{"complete", "missing-terminal", "missing-newline", "wrong-identity", "wrong-sequence", "after-terminal", "length", "private-error", "invalid-audio"} {
		t.Run(kind, func(t *testing.T) {
			provider := speechServer(t, func(w http.ResponseWriter, r *http.Request, request map[string]any) {
				id := request["request_id"]
				encoder := json.NewEncoder(w)
				audio := speechAudioFrame(id)
				if kind == "wrong-identity" {
					audio["request_id"] = "stale"
				}
				if kind == "wrong-sequence" {
					audio["sequence"] = 2
				}
				if kind == "invalid-audio" {
					audio["wav_base64"] = "AAAA"
				}
				if kind == "private-error" {
					encoder.Encode(map[string]any{"type": "error", "request_id": id, "error_code": "private prompt reftext raw audio path"})
					return
				}
				encoder.Encode(audio)
				if kind == "missing-terminal" {
					return
				}
				terminal := speechCompletedFrame(id)
				if kind == "length" {
					terminal["finish_reason"] = "length"
				}
				if kind == "missing-newline" {
					b, _ := json.Marshal(terminal)
					w.Write(b)
					return
				}
				encoder.Encode(terminal)
				if kind == "after-terminal" {
					encoder.Encode(audio)
				}
			})
			calls := 0
			err := provider.Stream(context.Background(), "確定した文です．", func([]byte) error { calls++; return nil })
			if kind == "complete" {
				if err != nil || calls != 1 {
					t.Fatalf("successful stream: %v %d", err, calls)
				}
			} else if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("invalid terminal accepted/leaked: %v", err)
			}
		})
	}
}
func TestSpeechServiceBoundedUnicodeAndEarlyPlayback(t *testing.T) {
	firstDelivered := make(chan struct{})
	calls := 0
	var serverCalls atomic.Int32
	provider := speechServer(t, func(w http.ResponseWriter, r *http.Request, request map[string]any) {
		text := request["speech_text"].(string)
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 180 || text == "" {
			t.Error("invalid bounded phrase")
		}
		if request["model_revision"] != "revision-1" || request["clone_prompt_digest"] != strings.Repeat("a", 64) {
			t.Error("profile pin lost")
		}
		encoder := json.NewEncoder(w)
		encoder.Encode(speechAudioFrame(request["request_id"]))
		w.(http.Flusher).Flush()
		if serverCalls.Add(1) == 1 {
			select {
			case <-firstDelivered:
			case <-time.After(time.Second):
				t.Error("waited for complete WAV response before playback")
			}
		}
		encoder.Encode(speechCompletedFrame(request["request_id"]))
	})
	err := provider.Stream(context.Background(), strings.Repeat(strings.Repeat("界", 179)+"。", 88)+strings.Repeat("界", 160), func([]byte) error {
		if calls == 0 {
			close(firstDelivered)
		}
		calls++
		return nil
	})
	if err != nil || calls != 89 {
		t.Fatalf("bounded split: %v chunks=%d", err, calls)
	}
	err = provider.Stream(context.Background(), strings.Repeat("界", 16000), func([]byte) error { t.Error("unbroken oversized phrase reached audio"); return nil })
	if err == nil || err.Error() != "invalid_speech_text" || calls != 89 {
		t.Fatal("oversized unbroken phrase was synthesized")
	}
}
func TestSpeechServiceCancelDisconnectsAndDropsLateAudio(t *testing.T) {
	disconnected := make(chan struct{})
	provider := speechServer(t, func(w http.ResponseWriter, r *http.Request, request map[string]any) {
		json.NewEncoder(w).Encode(speechAudioFrame(request["request_id"]))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	})
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := provider.Stream(ctx, "確定文です．次の文です．", func([]byte) error { calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel fence: %v %d", err, calls)
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("HTTP worker not canceled")
	}
}
func TestNativeSpeechControlsChangeActualProcessAndPCM(t *testing.T) {
	probes, synthesis := 0, 0
	p := &NativeVoiceTTS{voice: "Kyoko", locale: "ja-JP", osName: "darwin", executable: "/usr/bin/say", tempRoot: t.TempDir()}
	p.run = func(_ context.Context, _ string, args []string, input []byte) ([]byte, []byte, error) {
		if len(args) == 2 {
			probes++
			return []byte("Kyoko ja_JP # synthetic"), nil, nil
		}
		synthesis++
		if !strings.Contains(strings.Join(args, " "), "-r 262") || strings.Contains(strings.Join(args, " "), "確定") {
			t.Error("pace not applied or text in argv")
		}
		wav := providerTestWAV()
		binary.LittleEndian.PutUint16(wav[44:46], 1000)
		return nil, nil, os.WriteFile(args[3], wav, 0600)
	}
	style := defaultSpeechStyle()
	style.Pace = 1.5
	style.Volume = 0.5
	err := p.StreamSpeech(context.Background(), SpeechRequest{SpeechStyle: style, VoiceProfileID: "macos-kyoko", SpeechText: "確定文です．次です．"}, func(wav []byte) error {
		if binary.LittleEndian.Uint16(wav[44:46]) != 500 {
			t.Error("volume was ignored")
		}
		return nil
	})
	entries, _ := os.ReadDir(p.tempRoot)
	if err != nil || probes != 1 || synthesis != 2 || len(entries) != 0 {
		t.Fatalf("native controls/probes/cleanup: %v %d %d", err, probes, synthesis)
	}
}
func TestSpeechProfileContinuationHistoryAndTracePrivacy(t *testing.T) {
	var mu sync.Mutex
	var requests []SpeechRequest
	segments := 0
	p := &syntheticSpeechProvider{profile: customTestProfile(), stream: func(ctx context.Context, r SpeechRequest, emit func([]byte) error) error {
		mu.Lock()
		requests = append(requests, r)
		mu.Unlock()
		return emit(providerTestWAV())
	}}
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, r ChatRequest, onToken func(string)) (*ChatResponse, error) {
		segments++
		if segments == 1 {
			onToken("## 春\n花が咲きます。桃")
			return interactionGenerationLength("## 春\n花が咲きます。桃", 64), nil
		}
		return completedVoiceResponse("夏は暑いです。秋は涼しいです。冬は寒いです。"), nil
	})
	h.engine.tts = p
	style := p.profile.DefaultStyle
	style.Volume = 0.5
	s, err := h.engine.Start(VoiceTurnRequest{SessionID: "profile-turn", InputKind: "transcript", Speech: SpeechOptions{VoiceProfileID: p.profile.VoiceProfileID, Style: &style}, Chat: ChatRequest{MaxTokens: 64}})
	if err != nil {
		t.Fatal(err)
	}
	style.Volume = 1
	if err := h.engine.Commit(s.OperationID, nil, "synthetic request marker"); err != nil {
		t.Fatal(err)
	}
	complete := awaitInteraction(t, h.engine, s.OperationID, "COMPLETED")
	mu.Lock()
	defer mu.Unlock()
	if len(requests) < 2 || complete.Generation.ContinuationCount != 1 {
		t.Fatalf("continuation not exercised: %d %+v", len(requests), complete.Generation)
	}
	for _, r := range requests {
		if r.Volume != 0.5 || r.VoiceProfileID != p.profile.VoiceProfileID || strings.ContainsAny(r.SpeechText, "#桃") {
			t.Fatalf("unsafe or mutable speech request: %+v", r)
		}
	}
	stored, readErr := os.ReadFile(filepath.Join(h.store, s.OperationID+".json"))
	if readErr != nil || len(stored) > 256<<10 {
		t.Fatal("profile continuation trace was not durably saved")
	}
	h.engine.mu.Lock()
	events := append([]InteractionTraceEvent(nil), h.engine.turns[s.OperationID].events...)
	h.engine.mu.Unlock()
	body, _ := json.Marshal(events)
	for _, secret := range []string{"synthetic request marker", "花が", "ref_audio", "speaker_embedding", "speech_text", "provenance"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private speech material entered trace")
		}
	}
	if len(events) > 128 {
		t.Fatal("trace budget exceeded")
	}
	found := false
	for _, e := range events {
		if e.Name == "tts_completed" {
			found = e.ProviderID == "synthetic-tts" && e.ModelID == "revision-1"
		}
	}
	if !found {
		t.Fatal(fmt.Sprint("prepared provider identity missing"))
	}
}

func TestSpeechResponseBudgetDoesNotManufactureEOF(t *testing.T) {
	for _, extra := range []string{"", "extra-frame"} {
		reader := &speechResponseReader{reader: strings.NewReader("completed\n" + extra), remaining: len("completed\n")}
		_, err := io.ReadAll(reader)
		if extra == "" && err != nil {
			t.Fatal(err)
		}
		if extra != "" && (err == nil || err == io.EOF) {
			t.Fatal("size boundary hid a post-terminal frame")
		}
	}
}

func TestSpeechProfileCancelDoesNotEnterNextTurn(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	late := make(chan error, 1)
	var calls atomic.Int32
	p := &syntheticSpeechProvider{profile: customTestProfile(), stream: func(ctx context.Context, r SpeechRequest, emit func([]byte) error) error {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			<-release
			late <- emit(providerTestWAV())
			return ctx.Err()
		}
		return emit(providerTestWAV())
	}}
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		return completedVoiceResponse("確定した文です。"), nil
	})
	h.engine.tts = p
	first := h.start(t, "profile-cancel", GenerationLimits{})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("speech did not start")
	}
	if _, err := h.engine.Cancel(first.OperationID); err != nil {
		t.Fatal(err)
	}
	second := h.start(t, "profile-next", GenerationLimits{})
	complete := awaitInteraction(t, h.engine, second.OperationID, "COMPLETED")
	close(release)
	select {
	case err := <-late:
		if err == nil {
			t.Fatal("late audio accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("late callback did not finish")
	}
	if complete.LastAudioSequence != 1 {
		t.Fatal("old audio entered next turn")
	}
	for _, e := range h.recorded() {
		if e.Kind == "audio" && e.OperationID == first.OperationID {
			t.Fatal("canceled audio was published")
		}
	}
}

func TestSpeechCatalogRefreshPinsActiveProfileAndCachesReadiness(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := customTestProfile()
		if calls.Add(1) > 1 {
			p.ModelRevision = "revision-2"
		}
		json.NewEncoder(w).Encode(VoiceProfileCatalog{Profiles: []VoiceProfile{p}, DefaultProfileID: p.VoiceProfileID})
	}))
	defer server.Close()
	r := NewVoiceTTSRegistry()
	r.remote = &speechServiceClient{bearerToken: strings.Repeat("s", 43), endpoint: server.URL, client: server.Client()}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); r.Profiles(context.Background()) }()
	}
	group.Wait()
	prepared, err := r.PrepareSpeech(SpeechOptions{VoiceProfileID: "synthetic-voice"})
	if err != nil || calls.Load() != 1 {
		t.Fatalf("catalog singleflight: %v %d", err, calls.Load())
	}
	r.mu.Lock()
	r.checked = time.Time{}
	r.mu.Unlock()
	r.Profiles(context.Background())
	next, err := r.PrepareSpeech(SpeechOptions{VoiceProfileID: "synthetic-voice"})
	if err != nil || next.profile.ModelRevision != "revision-2" || prepared.profile.ModelRevision != "revision-1" {
		t.Fatal("active profile mutated after refresh")
	}
	server.Close()
	r.mu.Lock()
	r.checked = time.Time{}
	r.mu.Unlock()
	catalog := r.Profiles(context.Background())
	if catalog.ErrorCode == "" || len(catalog.Profiles) != 1 {
		t.Fatal("offline service lost native fallback")
	}
	if _, err := r.PrepareSpeech(SpeechOptions{VoiceProfileID: "synthetic-voice"}); err == nil {
		t.Fatal("offline custom profile silently fell back")
	}
}

func TestSpeechCatalogDefaultRemainsSelectedWhenUnavailable(t *testing.T) {
	for _, mode := range []string{"available", "unavailable", "missing"} {
		t.Run(mode, func(t *testing.T) {
			profile := customTestProfile()
			profile.Available = mode != "unavailable"
			catalog := VoiceProfileCatalog{DefaultProfileID: profile.VoiceProfileID, Profiles: []VoiceProfile{profile}}
			if mode == "missing" {
				catalog.Profiles = nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { json.NewEncoder(w).Encode(catalog) }))
			defer server.Close()
			t.Setenv("EPHY_TTS_ENDPOINT", server.URL)
			t.Setenv("EPHY_TTS_BEARER_TOKEN", strings.Repeat("s", 43))
			r := NewVoiceTTSRegistry()
			if _, err := r.PrepareSpeech(SpeechOptions{}); err == nil {
				t.Fatal("unresolved service default fell back")
			}
			got := r.Profiles(context.Background())
			if got.DefaultProfileID != profile.VoiceProfileID {
				t.Fatal("service default lost")
			}
			prepared, err := r.PrepareSpeech(SpeechOptions{})
			if mode == "available" {
				if err != nil || prepared.profile.VoiceProfileID != profile.VoiceProfileID {
					t.Fatalf("default not prepared: %v", err)
				}
			} else if err == nil || err.Error() != "voice_profile_unavailable" {
				t.Fatal("unavailable default fell back")
			}
			if _, err := r.PrepareSpeech(SpeechOptions{VoiceProfileID: "macos-kyoko"}); err != nil {
				t.Fatal("explicit native selection lost", err)
			}
		})
	}
}

func TestSpeechServiceBearerIsRequiredAndNeverExposed(t *testing.T) {
	token := strings.Repeat("s", 43)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing bearer")
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error_code":"PRIVATE `+token+`"}`)
	}))
	defer server.Close()
	t.Setenv("EPHY_TTS_ENDPOINT", server.URL)
	for _, value := range []string{"", "short", token + "\n", token} {
		t.Setenv("EPHY_TTS_BEARER_TOKEN", value)
		c := newSpeechServiceClient()
		for _, path := range []string{"/health", "/v1/voice-profiles", "/v1/speech"} {
			_, err := c.request(context.Background(), http.MethodGet, path, nil)
			if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unsafe bearer error")
			}
		}
	}
	if calls.Load() != 3 {
		t.Fatal("invalid bearer reached transport")
	}
}

func TestSpeechServiceBearerNeverFollowsRedirectOrNonLoopbackEndpoint(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	t.Setenv("EPHY_TTS_BEARER_TOKEN", strings.Repeat("s", 43))
	for _, endpoint := range []string{redirect.URL, "http://example.com:8767", "http://192.0.2.1:8767", "http://localhost:8767", "http://user@127.0.0.1:8767", target.URL + "?secret=x"} {
		t.Setenv("EPHY_TTS_ENDPOINT", endpoint)
		if _, err := newSpeechServiceClient().request(context.Background(), http.MethodGet, "/health", nil); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("bearer forwarded by redirect")
	}
}
