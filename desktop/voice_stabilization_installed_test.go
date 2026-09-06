package main

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// Opt-in installed-provider checks never open a microphone or play audio．
// Only readiness states，process counts，durations，and cleanup facts are logged．
func TestC01StabilizationInstalledReadinessAndTTS(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("EPHY_C01_STABILIZATION_INSTALLED") != "1" {
		t.Skip("opt-in macOS readiness and synthesis，without microphone or playback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	asr := NewNativeVoiceASR(detectWorkspaceRoot())
	asrProbes := 0
	asrRun := asr.run
	asr.run = func(ctx context.Context, executable string, args []string, input []byte) ([]byte, []byte, error) {
		if len(input) != 0 || len(args) == 0 || args[0] != "--check" {
			t.Fatal("installed preflight attempted recognition")
		}
		asrProbes++
		return asrRun(ctx, executable, args, input)
	}
	first, firstErr := asr.Readiness(ctx)
	second, secondErr := asr.Readiness(ctx)
	if first.CanStart {
		if firstErr != nil || secondErr != nil || first != second || asrProbes != 1 {
			t.Fatalf("readiness cache state=%s probes=%d", first.State, asrProbes)
		}
	} else if firstErr == nil || secondErr == nil || second.CanStart {
		t.Fatal("unavailable installed ASR reported ready")
	}
	t.Logf("installed ASR state=%s can_start=%t error_code=%s probes=%d microphone_opened=false", first.State, first.CanStart, first.ErrorCode, asrProbes)

	tts := NewNativeVoiceTTS()
	tts.tempRoot = t.TempDir()
	checks, syntheses, chunks := 0, 0, 0
	ttsRun := tts.run
	tts.run = func(ctx context.Context, executable string, args []string, input []byte) ([]byte, []byte, error) {
		if len(args) == 2 {
			checks++
		} else {
			syntheses++
		}
		return ttsRun(ctx, executable, args, input)
	}
	for i := 0; i < 2; i++ {
		if err := tts.Ready(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var totalDuration time.Duration
	err := tts.Stream(ctx, "テストです．音声の準備を確認しています．", func(wav []byte) error {
		chunks++
		duration, valid := voiceWAVDuration(wav, 60)
		if !valid {
			t.Fatal("installed synthesis violated WAV contract")
		}
		totalDuration += duration
		entries, err := os.ReadDir(tts.tempRoot)
		if err != nil || len(entries) != 0 {
			t.Fatal("temporary WAV remained at callback")
		}
		return nil
	})
	if err != nil || checks != 1 || chunks != 2 || syntheses != 2 {
		t.Fatalf("TTS error=%v readiness_processes=%d syntheses=%d chunks=%d", err, checks, syntheses, chunks)
	}
	t.Logf("installed TTS readiness_processes=%d syntheses=%d chunks=%d validated_duration_ms=%d temporary_wavs=0 playback_started=false", checks, syntheses, chunks, totalDuration.Milliseconds())
}
