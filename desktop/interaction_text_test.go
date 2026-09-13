package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTextInteractionSharesOneAnswerAndRecordingWithoutASR(t *testing.T) {
	for _, speechFails := range []bool{false, true} {
		name := "played"
		if speechFails {
			name = "speech-failed"
		}
		t.Run(name, func(t *testing.T) {
			store, home := newAppRecordingStore(t)
			var generated atomic.Int32
			speechAttempted := make(chan struct{})
			answer := "合成テストの一文目です．合成テストの二文目です．"
			h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
				select {
				case <-speechAttempted:
				default:
					close(speechAttempted)
				}
				if speechFails {
					return errors.New("private speech provider detail")
				}
				return emit(testVoiceWAV())
			}}, func(ctx context.Context, req ChatRequest, token func(string)) (*ChatResponse, error) {
				generated.Add(1)
				if req.SessionMode != "default" || req.Mode != "work" || req.MaxTokens != 4096 || req.Temperature != 0.2 || !reflect.DeepEqual(req.Messages, []GatewayMessage{{Role: "assistant", Content: "合成履歴"}}) {
					t.Error("typed settings or history changed")
				}
				before := readSpoolEvents(t, home)
				if len(before) != 1 || before[0].InputKind != "text" || before[0].ASR != nil {
					t.Error("typed user input must be recorded once without ASR")
				}
				token("合成テストの一文目です．")
				select {
				case <-speechAttempted:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				token("合成テストの二文目です．")
				return completedVoiceResponse(answer), nil
			})
			h.engine.asr = nil
			h.engine.recorder = store
			request := VoiceTurnRequest{SessionID: store.Snapshot().Settings.ConversationID, InputKind: "text", GenerationLimits: GenerationLimits{MaxSegments: 1}, Chat: ChatRequest{Mode: "work", Prompt: "合成入力", MaxTokens: 4096, Temperature: 0.2, Messages: []GatewayMessage{{Role: "assistant", Content: "合成履歴"}}}}
			app := NewApp()
			app.interaction, app.recorder = h.engine, store
			start, err := app.StartTextInteraction(request)
			if err != nil {
				t.Fatal(err)
			}
			done := awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
			if generated.Load() != 1 || done.ResponsePlan.Text != answer || done.InputKind != "text" || !done.Generation.Complete {
				t.Fatal("typed answer was lost or duplicated")
			}
			if speechFails != (done.SpeechErrorCode == "tts_failed") {
				t.Fatal("wrong optional speech result")
			}
			events := readSpoolEvents(t, home)
			playback := "completed"
			if speechFails {
				playback = "failed"
			}
			if len(events) != 2 || events[1].Text != answer || events[1].Assistant.Generation != "completed" || events[1].Assistant.Playback != playback {
				t.Fatalf("wrong recording outcome: %+v", events)
			}
			trace, err := h.engine.Trace(start.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if validation := ValidateTrace(trace); !validation.Valid {
				t.Fatalf("invalid typed trace: %+v", validation)
			}
			for _, event := range trace {
				if strings.HasPrefix(event.Name, "asr_") || strings.HasPrefix(event.Name, "user_speech_") {
					t.Fatal("typed input claimed microphone or ASR")
				}
			}
			h.checkPlayback(t)
		})
	}
}
