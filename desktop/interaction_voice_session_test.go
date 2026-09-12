package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceSessionEpochAndOwnerCancellation(t *testing.T) {
	p := &c02EngineProvider{}
	e := NewInteractionEngine(p, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	v, err := e.StartVoiceSession("conversation")
	if err != nil {
		t.Fatal(err)
	}
	req := VoiceTurnRequest{SessionID: "conversation", VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch}
	s, err := e.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.BeginASR(s.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	oldASR := e.turns[s.OperationID].asr
	parent := e.voiceSession.ctx
	e.mu.Unlock()
	if _, err = e.Cancel(s.OperationID); err != nil {
		t.Fatal(err)
	}
	if parent.Err() != nil {
		t.Fatal("answer cancellation ended the input owner")
	}
	if oldASR.ctx.Err() == nil {
		t.Fatal("discarded ASR remained live")
	}
	second, err := e.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.BeginASR(second.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Cancel(s.OperationID); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	nextASR := e.turns[second.OperationID].asr
	e.mu.Unlock()
	if nextASR.ctx.Err() != nil {
		t.Fatal("old cancellation killed the next ASR")
	}
	paused, err := e.ChangeVoiceSession(v.ID, v.Epoch, "pause")
	if err != nil || paused.State != "paused" {
		t.Fatal(paused, err)
	}
	if parent.Err() == nil || nextASR.ctx.Err() == nil {
		t.Fatal("pause did not cancel input")
	}
	if _, err = e.Start(req); err == nil {
		t.Fatal("paused epoch accepted a turn")
	}
	resumed, err := e.ChangeVoiceSession(v.ID, v.Epoch, "resume")
	if err != nil || resumed.Epoch != v.Epoch+1 {
		t.Fatal(resumed, err)
	}
	if _, err = e.Start(req); err == nil {
		t.Fatal("old epoch accepted a delayed start")
	}
	if _, err = e.ChangeVoiceSession(v.ID, v.Epoch, "end"); err == nil {
		t.Fatal("old end canceled resumed session")
	}
	req.VoiceSessionEpoch = resumed.Epoch
	latest, err := e.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ChangeVoiceSession(v.ID, resumed.Epoch, "end"); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := e.Snapshot(latest.OperationID)
	if snapshot.State != "CANCELED" {
		t.Fatal(snapshot)
	}
	if _, err = e.Start(req); err == nil {
		t.Fatal("stopped session accepted a turn")
	}
}

func TestVoiceSessionFourFinalOnlyTurnsWithoutStableNotifications(t *testing.T) {
	p := &c02EngineProvider{}
	var calls atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(ctx context.Context, r ChatRequest, token func(string)) (*ChatResponse, error) {
		calls.Add(1)
		if r.Prompt != "はい" {
			t.Error("unconfirmed text reached LLM")
		}
		return completedVoiceResponse("```\nno spoken units\n```"), nil
	}, nil, t.TempDir())
	defer e.Close()
	v, err := e.StartVoiceSession("conversation")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		s, err := e.Start(VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.BeginASR(s.OperationID, 16000); err != nil {
			t.Fatal(err)
		}
		if err = e.AppendASRAudio(s.OperationID, 1, make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
		session := p.sessions[i]
		session.emit(session.update(1, "partial", "えっと"))
		final := session.update(2, "final", "はい")
		session.emit(final)
		if calls.Load() != int32(i) {
			t.Fatal("early final called LLM")
		}
		if err = e.EndASR(s.OperationID); err != nil {
			t.Fatal(err)
		}
		session.result <- c02SessionResult{update: final}
		awaitInteraction(t, e, s.OperationID, "COMPLETED")
		if err = e.EndASR(s.OperationID); err == nil {
			t.Fatal("duplicate endpoint accepted")
		}
		session.emit(final)
		if calls.Load() != int32(i+1) {
			t.Fatal("duplicate or missing final")
		}
		trace, _ := e.Trace(s.OperationID)
		endpoints, finals := 0, 0
		for _, event := range trace {
			if event.Name == "endpoint_commit" {
				endpoints++
			}
			if event.Name == "asr_final" {
				finals++
			}
		}
		if endpoints != 1 || finals != 1 {
			t.Fatal("endpoint/final was not unique", endpoints, finals)
		}
	}
}

func TestVoiceSessionLateFinalAfterPauseNeverCallsLLM(t *testing.T) {
	p := &c02EngineProvider{}
	var calls atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		calls.Add(1)
		return completedVoiceResponse("answer"), nil
	}, nil, t.TempDir())
	defer e.Close()
	v, _ := e.StartVoiceSession("conversation")
	s, _ := e.Start(VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch})
	if _, err := e.BeginASR(s.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	if err := e.AppendASRAudio(s.OperationID, 1, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	session := p.sessions[0]
	if err := e.EndASR(s.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ChangeVoiceSession(v.ID, v.Epoch, "pause"); err != nil {
		t.Fatal(err)
	}
	final := session.update(1, "final", "late")
	session.emit(final)
	session.result <- c02SessionResult{update: final}
	time.Sleep(10 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("late final reached LLM")
	}
	e.Close()
	if _, err := e.ChangeVoiceSession(v.ID, v.Epoch, "resume"); err == nil {
		t.Fatal("closed engine resumed")
	}
}

func TestVoiceSessionIdleTraceDoesNotClaimUserSpeech(t *testing.T) {
	e := NewInteractionEngine(&c02EngineProvider{}, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	v, _ := e.StartVoiceSession("conversation")
	s, err := e.Start(VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.BeginASR(s.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Cancel(s.OperationID); err != nil {
		t.Fatal(err)
	}
	trace, _ := e.Trace(s.OperationID)
	for _, event := range trace {
		if event.Name == "user_speech_start" || event.Name == "endpoint_commit" {
			t.Fatal("idle created speech event")
		}
	}
	if result := ValidateTrace(trace); !result.Valid {
		t.Fatal(result)
	}
}
