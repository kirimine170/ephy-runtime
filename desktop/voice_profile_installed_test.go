package main

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// Explicit opt-in，installed Kyoko synthesis only．No microphone or reference clone．
func TestC03InstalledNativeSpeechControls(t *testing.T) {
	if os.Getenv("EPHY_C03_INSTALLED") != "1" || runtime.GOOS != "darwin" {
		t.Skip("installed speech opt-in")
	}
	p := NewNativeVoiceTTS()
	p.tempRoot = t.TempDir()
	style := defaultSpeechStyle()
	style.Pace = 0.8
	style.Volume = 0.6
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	chunks := 0
	duration := time.Duration(0)
	first := time.Duration(0)
	err := p.StreamSpeech(ctx, SpeechRequest{SpeechStyle: style, SpeechText: "音声の動作確認です．次の文です．", VoiceProfileID: p.Profile().VoiceProfileID}, func(wav []byte) error {
		if chunks == 0 {
			first = time.Since(started)
		}
		chunks++
		d, ok := voiceWAVDuration(wav, 60)
		if !ok {
			t.Error("invalid installed WAV")
		}
		duration += d
		entries, _ := os.ReadDir(p.tempRoot)
		if len(entries) != 0 {
			t.Error("WAV remained at callback")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if chunks != 2 {
		t.Fatal("phrase split not used")
	}
	t.Logf("provider=macos-say chunks=%d first_chunk_ms=%d validated_audio_ms=%d total_ms=%d temporary_wav=0 microphone=false playback=false", chunks, first.Milliseconds(), duration.Milliseconds(), time.Since(started).Milliseconds())
}
