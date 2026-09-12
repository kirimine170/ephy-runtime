package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit local fixtures only．This opt-in test opens no microphone or speaker．
func TestWhisperInstalledEngine(t *testing.T) {
	if os.Getenv("EPHY_WHISPER_INSTALLED") != "1" {
		t.Skip("opt-in local Whisper fixtures，no microphone or playback")
	}
	if os.Getenv("EPHY_ASR_PROVIDER") != "whisper-cpp" {
		t.Fatal("explicit Whisper provider required")
	}
	path := os.Getenv("EPHY_ASR_FIXTURE_MANIFEST")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture manifest missing")
	}
	var manifest struct {
		Samples []struct {
			Audio string `json:"audio"`
			Kind  string `json:"kind"`
		}
	}
	if json.Unmarshal(data, &manifest) != nil || len(manifest.Samples) == 0 {
		t.Fatal("invalid fixture manifest")
	}
	wav, err := os.ReadFile(filepath.Join(filepath.Dir(path), manifest.Samples[0].Audio))
	if err != nil {
		t.Fatal("fixture audio missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, ok := NewConfiguredVoiceASR(detectWorkspaceRoot()).(*WhisperVoiceASR)
	if !ok {
		t.Fatal("provider selection failed")
	}
	defer p.Close()
	begin := time.Now()
	for {
		r, err := p.Readiness(ctx)
		if err != nil {
			t.Fatal("readiness failed", asrStreamError(err))
		}
		if r.CanStart {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("warmup timed out")
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Logf("warmup_ms=%d provider=whisper-cpp model=%s microphone_opened=false audio_played=false", time.Since(begin).Milliseconds(), p.config.ModelID)
	text, err := p.Transcribe(ctx, wav)
	if err != nil || text == "" {
		t.Fatal("manual WAV path failed", asrStreamError(err))
	}
	rate, pcm := voicePCMFromWAV(wav)
	var chatCalls atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		chatCalls.Add(1)
		return completedVoiceResponse("```go\nfixture\n```"), nil
	}, nil, t.TempDir())
	defer e.Close()
	for index := 0; index < 8; index++ {
		snapshot := startTestInteraction(t, e)
		if _, err = e.BeginASR(snapshot.OperationID, rate); err != nil {
			t.Fatal("engine OpenSession failed", asrStreamError(err))
		}
		if index%3 == 0 {
			if err = e.AppendASRAudio(snapshot.OperationID, 1, make([]byte, rate*2)); err != nil {
				t.Fatal(err)
			}
			if err = e.EndASR(snapshot.OperationID); err != nil {
				t.Fatal(err)
			}
			s := awaitInteraction(t, e, snapshot.OperationID, "CANCELED")
			if s.InputOutcome != "no_speech" || s.Transcript != "" {
				t.Fatal("silence became Conversation")
			}
		} else if index%3 == 1 {
			for sequence, offset := 1, 0; offset < len(pcm); sequence++ {
				end := min(offset+16000, len(pcm))
				if err = e.AppendASRAudio(snapshot.OperationID, sequence, pcm[offset:end]); err != nil {
					t.Fatal(err)
				}
				offset = end
			}
			if err = e.EndASR(snapshot.OperationID); err != nil {
				t.Fatal(err)
			}
			s := awaitInteraction(t, e, snapshot.OperationID, "COMPLETED")
			if s.Transcript == "" {
				t.Fatal("voice lost verified final")
			}
		} else {
			if err = e.AppendASRAudio(snapshot.OperationID, 1, pcm[:min(len(pcm), rate)]); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Cancel(snapshot.OperationID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if chatCalls.Load() != 3 {
		t.Fatal("LLM called from partial，silence or cancellation", chatCalls.Load())
	}
	r, err := p.Readiness(ctx)
	if err != nil || !r.CanStart {
		t.Fatal("model stopped across session transitions", r)
	}
	t.Logf("engine_turns=8 canonical_final=3 no_speech=3 cancel=2 llm_calls=%d elapsed_ms=%d", chatCalls.Load(), time.Since(begin).Milliseconds())
}
