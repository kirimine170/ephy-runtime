package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Explicit opt-in: uses the already running local Gateway/model and installed
// macOS voice．It never opens the microphone or speakers，and downloads nothing．
func TestInteractionInstalledGatewayAndTTS(t *testing.T) {
	if os.Getenv("EPHY_INTERACTION_INTEGRATION") != "1" || runtime.GOOS != "darwin" {
		t.Skip("opt in to local Gateway/model and macOS TTS integration")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.workspaceRoot = root
	events := make(chan InteractionEvent, 256)
	engine := NewInteractionEngine(testVoiceASR{transcribe: func(context.Context, []byte) (string, error) {
		t.Error("transcript replay must not access the microphone/ASR")
		return "", nil
	}}, NewNativeVoiceTTS(), app.chatWithContext, func(event InteractionEvent) { events <- event }, t.TempDir())
	defer engine.Close()
	snapshot, err := engine.Start(VoiceTurnRequest{SessionID: "synthetic-installed-c0", InputKind: "transcript", Chat: ChatRequest{Mode: "fast", SessionID: "synthetic-installed-c0", SessionMode: "voice", Messages: []GatewayMessage{{Role: "user", Content: "これは合成テストです．一文だけで答えてください．"}, {Role: "assistant", Content: "わかりました．"}}, Stream: true, Temperature: 0.2, MaxTokens: 64, ProviderID: "gateway-router", ModelID: "route:fast", ConfigurationID: "synthetic-c0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Commit(snapshot.OperationID, nil, "日本語で短く挨拶してください．"); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	audioBytes, chunks := 0, 0
	for {
		select {
		case <-deadline.C:
			t.Fatal("installed integration timeout")
		case event := <-events:
			if event.Kind == "audio" {
				data, err := base64.StdEncoding.DecodeString(event.AudioBase64)
				if err != nil || len(data) < 44 {
					t.Fatal("invalid installed TTS audio")
				}
				audioBytes += len(data)
				chunks++
				// Synthetic playback acknowledgements，not physical audio output．
				if err = engine.Playback(snapshot.OperationID, event.Sequence, "started"); err != nil {
					t.Fatal(err)
				}
				if err = engine.Playback(snapshot.OperationID, event.Sequence, "stopped"); err != nil {
					t.Fatal(err)
				}
			}
			if event.Kind != "state" || event.Snapshot == nil || !interactionTerminal(event.Snapshot.State) {
				continue
			}
			if event.Snapshot.State != "COMPLETED" {
				t.Fatalf("installed integration failed: %s", event.Snapshot.ErrorCode)
			}
			trace, err := engine.Trace(snapshot.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			validation := ValidateTrace(trace)
			if !validation.Valid || chunks == 0 {
				t.Fatalf("incomplete trace or audio: %+v", validation)
			}
			actualRoute := false
			for _, event := range trace {
				if event.Name == "llm_first_token" && event.ProviderID != "gateway-router" {
					actualRoute = true
				}
			}
			if !actualRoute {
				t.Fatal("running Gateway did not report the selected provider/model configuration")
			}
			result, _ := json.Marshal(map[string]any{"latencies_ms": validation.LatenciesMS, "chunks": chunks, "audio_bytes": audioBytes, "microphone_tested": false, "speaker_tested": false, "playback_ack": "synthetic", "trace": trace})
			t.Log(string(result))
			return
		}
	}
}
