package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// An explicit network/synthesis smoke check，not microphone or audible playback
// acceptance．It uses the existing Work route and keeps all generated content
// in memory．Only typed timing and generation metadata are logged．
type c1WorkMetadataTransport struct{ t *testing.T }

func (p c1WorkMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		p.t.Logf("gateway_http_status=%d", response.StatusCode)
	}
	return response, err
}

func TestC1Step2InstalledWorkGenerationAndSynthesis(t *testing.T) {
	if os.Getenv("EPHY_C1_STEP2_WORK_INSTALLED") != "1" || runtime.GOOS != "darwin" {
		t.Skip("opt-in existing Work route and native synthesis，no microphone or playback")
	}
	app := NewApp()
	if os.Getenv("EPHY_C1_STEP2_ISOLATED_GATEWAY") == "1" {
		app.baseURL = "http://127.0.0.1:18000"
	}
	app.httpClient.Transport = c1WorkMetadataTransport{t: t}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started := time.Now()
	units := []string{}
	request := ChatRequest{SessionID: "c1-step2-synthetic-work", SessionMode: "voice", Mode: "work", Prompt: "日本の冬の特徴だけを，日本語で短い二文で説明してください．", MaxTokens: 512, Temperature: .2, Stream: true, SourceScope: "all"}
	response, err := assembleGeneration(ctx, request, GenerationLimits{}, "", app.chatWithContext, func(p GenerationProgress) { units = append(units, p.SpeechUnits...) })
	llmMS := time.Since(started).Milliseconds()
	if err != nil || response == nil || response.Generation == nil {
		t.Fatalf("Work generation failed: %v", err)
	}
	for label, stamp := range map[string]string{"first_raw_delta_ms": response.Generation.FirstRawDeltaAt, "first_visible_content_ms": response.Generation.FirstVisibleContentAt} {
		if at, parseErr := time.Parse(time.RFC3339Nano, stamp); parseErr == nil {
			t.Logf("%s=%d", label, at.Sub(started).Milliseconds())
		}
	}
	metadata, _ := json.Marshal(response.Generation)
	t.Logf("work_generation_ms=%d generation=%s microphone=false playback=false", llmMS, metadata)
	if !response.Generation.Complete || !strings.Contains(response.Answer, "冬") || len(units) == 0 {
		t.Fatal("Work response did not complete the synthetic winter request within existing limits")
	}
	p := NewNativeVoiceTTS()
	p.tempRoot = t.TempDir()
	synthesisStart := time.Now()
	var firstMS int64 = -1
	chunks := 0
	duration := time.Duration(0)
	for _, unit := range units {
		if err = p.Stream(ctx, unit, func(wav []byte) error {
			if firstMS < 0 {
				firstMS = time.Since(synthesisStart).Milliseconds()
			}
			chunks++
			d, ok := voiceWAVDuration(wav, 60)
			if !ok {
				t.Error("invalid synthesized WAV")
			}
			duration += d
			return nil
		}); err != nil {
			t.Fatalf("native_synthesis_error=%s", err)
		}
	}
	t.Logf("route=work profile=macos-say synthesis_first_chunk_ms=%d synthesis_total_ms=%d speech_units=%d wav_chunks=%d validated_audio_ms=%d microphone=false playback=false", firstMS, time.Since(synthesisStart).Milliseconds(), len(units), chunks, duration.Milliseconds())
}
