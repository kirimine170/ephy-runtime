package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newCandidateTestEngine(t *testing.T) *InteractionEngine {
	e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		t.Error("unexpected model call")
		return nil, nil
	}, nil, t.TempDir())
	t.Cleanup(e.Close)
	return e
}

func TestCandidateStopRecordsInterruptedAndRejectsLateAck(t *testing.T) {
	e := newCandidateTestEngine(t)
	s, err := e.StartPreparedCandidate(VoiceTurnRequest{SessionID: "synthetic"}, "共有経験の検証です．")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := e.Snapshot(s.OperationID)
		if current.LastAudioSequence == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		if err := e.ObserveCandidateStop(s.OperationID, 1); err != nil {
			t.Fatal(err)
		}
		current, _ = e.Snapshot(s.OperationID)
		outcome, _ := recordedAssistant(current, current.State)
		if current.State != "CANCELED" || outcome.Playback != "interrupted" || current.SpeechUnits[0].State != "interrupted" {
			t.Fatal(current, outcome)
		}
		_ = e.Playback(s.OperationID, 1, "stopped")
		late, _ := e.Snapshot(s.OperationID)
		if late.SpeechUnits[0].State != "interrupted" {
			t.Fatal("late ACK invented completion")
		}
		return
	}
	t.Fatal("no audio")
}

func TestCandidateWaitExpiresAndFeedbackSuppressesNextCandidate(t *testing.T) {
	s := &reuseExperiment{engine: newCandidateTestEngine(t), token: "test-nonce", topic: "simple", revision: 1, speaking: true, suppressed: map[string]bool{}}
	s.pending = &reusePending{Candidate: reuseCandidate{Text: "候補です．"}, Revision: 1, Expires: time.Now().Add(time.Second)}
	s.speakLocked()
	if s.status != "wait" || s.active != "" {
		t.Fatal(s.status)
	}
	s.pending.Expires = time.Now().Add(-time.Second)
	s.speakLocked()
	if s.status != "discard" || s.pending != nil {
		t.Fatal(s.status)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:18880/control", strings.NewReader(`{"action":"not_now"}`))
	r.Header.Set("X-Ephy-Reuse", "test-nonce")
	w := httptest.NewRecorder()
	s.handler(w, r)
	if w.Code != 200 || !s.suppressed["simple"] {
		t.Fatal(w.Code)
	}
	s.observe("simple")
	if s.status != "suppressed" {
		t.Fatal(s.status)
	}
}

func TestCandidateEndpointRejectsCrossOriginAndUnknownFixture(t *testing.T) {
	s := &reuseExperiment{token: "nonce"}
	for _, test := range []struct {
		origin, body string
		code         int
	}{
		{"https://untrusted.invalid", `{"case":"simple"}`, 403},
		{"http://127.0.0.1:18880", `{"case":"user-memory"}`, 400},
		{"http://127.0.0.1:18880", `{"case":"simple","scope":"all"}`, 400},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18880/observe", strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-Ephy-Reuse", "nonce")
		w := httptest.NewRecorder()
		s.handler(w, r)
		if w.Code != test.code {
			t.Fatal(w.Code, test.code)
		}
	}
}
