package main

import (
	"context"
	"testing"
	"time"
)

func TestPreparedCandidateUsesExistingDeliveryWithoutUserQuestion(t *testing.T) {
	engine := NewInteractionEngine(testVoiceASR{transcribe: func(context.Context, []byte) (string, error) { t.Error("candidate called ASR"); return "", nil }}, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		t.Error("candidate called LLM again")
		return nil, nil
	}, func(e InteractionEvent) {
		if e.Kind == "transcript" {
			t.Error("candidate created ASR final")
		}
	}, t.TempDir())
	defer engine.Close()
	snapshot, err := engine.StartPreparedCandidate(VoiceTurnRequest{SessionID: "synthetic-participation"}, "わたしも，桜の下のおにぎりを覚えています．")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := engine.Snapshot(snapshot.OperationID)
		if s.LastAudioSequence > 0 {
			if s.Transcript != "" {
				t.Fatal("observation invented a question")
			}
			if err = engine.Playback(s.OperationID, 1, "started"); err != nil {
				t.Fatal(err)
			}
			if err = engine.Playback(s.OperationID, 1, "stopped"); err != nil {
				t.Fatal(err)
			}
		}
		if s.State == "COMPLETED" {
			if len(s.SpeechUnits) != 1 || s.SpeechUnits[0].State != "completed" {
				t.Fatal(s)
			}
			outcome, complete := recordedAssistant(s, s.State)
			if !complete || outcome.Playback != "completed" {
				t.Fatal(outcome)
			}
			return
		}
		if s.State == "FAILED" {
			t.Fatal(s.ErrorCode)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("candidate did not complete")
}
