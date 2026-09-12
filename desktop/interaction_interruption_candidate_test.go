package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func candidateFixture(t *testing.T, provider VoiceASR) (*InteractionEngine, InteractionSnapshot) {
	t.Helper()
	e := NewInteractionEngine(provider, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	t.Cleanup(e.Close)
	v, err := e.StartVoiceSession("conversation")
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.Start(VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.turns[s.OperationID].snapshot.State = "SYNTHESIZING"
	e.mu.Unlock()
	return e, s
}
func awaitCandidateCanceled(t *testing.T, s *c02EngineSession) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for !s.canceled.Load() && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if !s.canceled.Load() {
		t.Fatal("candidate recognizer retained after cleanup")
	}
}
func assertCandidateReplyLive(t *testing.T, e *InteractionEngine, op string) {
	t.Helper()
	s, err := e.Snapshot(op)
	if err != nil || s.State != "SYNTHESIZING" || s.ErrorCode != "" || s.Transcript != "" {
		t.Fatalf("speculative input changed reply: %+v %v", s, err)
	}
	e.mu.Lock()
	live := e.turns[op].ctx.Err() == nil
	e.mu.Unlock()
	if !live {
		t.Fatal("candidate canceled reply generation")
	}
}
func TestInterruptionCandidateRecognitionNeverCommitsOrCancelsReply(t *testing.T) {
	p := &c02EngineProvider{}
	e, old := candidateFixture(t, p)
	c, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	native := p.sessions[0]
	for n, text := range []string{"private candidate partial", "private candidate revised"} {
		native.emit(native.update(n+1, "partial", text))
		got, err := e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, n+1, make([]byte, 320))
		if err != nil || got.Update == nil || got.Update.Transcript != text {
			t.Fatalf("candidate update: %+v %v", got, err)
		}
		assertCandidateReplyLive(t, e, old.OperationID)
	}
	if _, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000); err == nil {
		t.Fatal("second candidate accepted")
	}
	if err := e.CancelInterruptionCandidate(old.OperationID, c.CandidateID, "acknowledgement"); err != nil {
		t.Fatal(err)
	}
	awaitCandidateCanceled(t, native)
	assertCandidateReplyLive(t, e, old.OperationID)
	trace, _ := e.Trace(old.OperationID)
	data, _ := json.Marshal(trace)
	for _, value := range []string{"private candidate", "transcript", "pcm"} {
		if strings.Contains(string(data), value) {
			t.Fatal("candidate payload leaked into trace")
		}
	}
	if !strings.Contains(string(data), "interruption_candidate_acknowledgement") {
		t.Fatal("body-free rejection reason missing")
	}
}
func TestInterruptionCandidateNoiseFailureDoesNotFailOriginalTurn(t *testing.T) {
	for _, mode := range []string{"asr_failure", "invalid_metadata", "invalid_audio", "sequence", "too_much_audio"} {
		t.Run(mode, func(t *testing.T) {
			p := &c02EngineProvider{}
			e, old := candidateFixture(t, p)
			c, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
			if err != nil {
				t.Fatal(err)
			}
			native := p.sessions[0]
			switch mode {
			case "asr_failure":
				u := native.update(1, "failure", "")
				u.ErrorCode = "asr_failed"
				native.emit(u)
			case "invalid_metadata":
				u := native.update(1, "partial", "private")
				u.Provider = "invalid provider"
				native.emit(u)
			case "invalid_audio":
				if _, err = e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, 1, []byte{1}); err == nil {
					t.Fatal("odd PCM accepted")
				}
			case "sequence":
				if _, err = e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, 2, []byte{1, 2}); err == nil {
					t.Fatal("sequence skip accepted")
				}
			case "too_much_audio":
				if _, err = e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, 1, make([]byte, 64002)); err == nil {
					t.Fatal("PCM duration exceeded")
				}
			}
			awaitCandidateCanceled(t, native)
			assertCandidateReplyLive(t, e, old.OperationID)
		})
	}
}
func TestInterruptionCandidateIgnoresForeignAndStaleUpdates(t *testing.T) {
	p := &c02EngineProvider{}
	e, old := candidateFixture(t, p)
	c, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	native := p.sessions[0]
	u := native.update(5, "partial", "recognized speech")
	native.emit(u)
	foreign := native.update(6, "partial", "wrong turn")
	foreign.SegmentID = "another"
	native.emit(foreign)
	native.emit(native.update(4, "partial", "stale"))
	got, err := e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, 1, make([]byte, 320))
	if err != nil || got.Update == nil || *got.Update != u {
		t.Fatalf("identity fencing: %+v %v", got, err)
	}
	if err = e.CancelInterruptionCandidate(old.OperationID, c.CandidateID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	awaitCandidateCanceled(t, native)
	replacement, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.CancelInterruptionCandidate(old.OperationID, c.CandidateID, "noise"); err != nil {
		t.Fatal(err)
	}
	native.emit(native.update(8, "final", "late final"))
	got, err = e.AppendInterruptionCandidate(old.OperationID, replacement.CandidateID, 1, make([]byte, 320))
	if err != nil || got.Update != nil {
		t.Fatal("old cleanup/final affected replacement", err)
	}
	assertCandidateReplyLive(t, e, old.OperationID)
}
func TestInterruptionCandidateRequiresMatchingLiveContinuousResponse(t *testing.T) {
	p := &c02EngineProvider{}
	e, old := candidateFixture(t, p)
	for _, revision := range []int{0, 2} {
		if _, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), revision, 16000); err == nil {
			t.Fatal("wrong generation accepted")
		}
	}
	for _, rate := range []int{0, 7999, 48001} {
		if _, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, rate); err == nil {
			t.Fatal("bad sample rate accepted")
		}
	}
	e.mu.Lock()
	e.turns[old.OperationID].snapshot.State = "RECORDING"
	e.mu.Unlock()
	if _, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000); err == nil {
		t.Fatal("probe overlaps current utterance")
	}
	e.mu.Lock()
	e.turns[old.OperationID].snapshot.State = "SYNTHESIZING"
	e.mu.Unlock()
	c, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Cancel(old.OperationID); err != nil {
		t.Fatal(err)
	}
	awaitCandidateCanceled(t, p.sessions[0])
	if _, err = e.AppendInterruptionCandidate(old.OperationID, c.CandidateID, 1, make([]byte, 320)); err == nil {
		t.Fatal("canceled turn accepted candidate PCM")
	}
}

type unavailableCandidateProvider struct{ testVoiceASR }

func (p unavailableCandidateProvider) OpenSession(context.Context, ASRSessionRequest, func(ASRUpdate)) (VoiceASRSession, error) {
	return nil, errors.New("asr_unavailable")
}
func TestInterruptionCandidateUnavailableAndInvalidBridgePCMLeaveReplyRunning(t *testing.T) {
	e, old := candidateFixture(t, unavailableCandidateProvider{})
	if _, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000); err == nil {
		t.Fatal("unavailable probe accepted")
	}
	assertCandidateReplyLive(t, e, old.OperationID)
	app := &App{interaction: e}
	for _, encoded := range []string{"!", base64.StdEncoding.EncodeToString(make([]byte, maxASRPCMChunkBytes+1))} {
		if _, err := app.AppendInteractionInterruptionCandidate(old.OperationID, "invalid", 1, encoded); err == nil {
			t.Fatal("bad bridge PCM accepted")
		}
		assertCandidateReplyLive(t, e, old.OperationID)
	}
}
func TestInterruptionCandidateSharesASRGateAndReleasesBeforeNextTurn(t *testing.T) {
	p := &delayedASRCancelProvider{c02EngineProvider: &c02EngineProvider{}, cancelStarted: make(chan struct{}), releaseCancel: make(chan struct{}), opened: make(chan struct{}, 1)}
	e, old := candidateFixture(t, p)
	c, err := e.BeginInterruptionCandidate(old.OperationID, interactionID("candidate_"), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.CancelInterruptionCandidate(old.OperationID, c.CandidateID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	interactionGenerationReceive(t, p.cancelStarted)
	if _, err = e.Cancel(old.OperationID); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	request := e.turns[old.OperationID].request
	e.mu.Unlock()
	next, err := e.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() { _, err := e.BeginASR(next.OperationID, 16000); ready <- err }()
	select {
	case <-p.opened:
		t.Fatal("ASR overlaps candidate cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(p.releaseCancel)
	if err := interactionGenerationReceive(t, ready); err != nil {
		t.Fatal(err)
	}
	interactionGenerationReceive(t, p.opened)
}

type delayedCandidateStartup struct {
	testVoiceASR
	entered chan struct{}
	release chan struct{}
	session chan *c02EngineSession
}

func (p *delayedCandidateStartup) OpenSession(_ context.Context, req ASRSessionRequest, emit func(ASRUpdate)) (VoiceASRSession, error) {
	close(p.entered)
	<-p.release
	s := &c02EngineSession{req: req, emit: emit, result: make(chan c02SessionResult, 1)}
	p.session <- s
	return s, nil
}
func TestInterruptionCandidateCanBeCanceledBeforeStartupReturns(t *testing.T) {
	p := &delayedCandidateStartup{entered: make(chan struct{}), release: make(chan struct{}), session: make(chan *c02EngineSession, 1)}
	e, old := candidateFixture(t, p)
	started := make(chan error, 1)
	go func() {
		_, err := e.BeginInterruptionCandidate(old.OperationID, "known-before-startup", 1, 16000)
		started <- err
	}()
	interactionGenerationReceive(t, p.entered)
	if err := e.CancelInterruptionCandidate(old.OperationID, "known-before-startup", "expired"); err != nil {
		t.Fatal(err)
	}
	if err := interactionGenerationReceive(t, started); err == nil {
		t.Fatal("canceled pending startup returned live session")
	}
	assertCandidateReplyLive(t, e, old.OperationID)
	close(p.release)
	native := interactionGenerationReceive(t, p.session)
	awaitCandidateCanceled(t, native)
	assertCandidateReplyLive(t, e, old.OperationID)
}

func TestInterruptionCandidateTimingIsSeparateFromLocalStopAndBounded(t *testing.T) {
	e, old := candidateFixture(t, &c02EngineProvider{})
	e.mu.Lock()
	request := e.turns[old.OperationID].request
	e.mu.Unlock()
	r := InteractionInterruption{OperationID: old.OperationID, SessionID: old.SessionID, TurnID: old.TurnID, GenerationRevision: 1, VoiceSessionID: request.VoiceSessionID, VoiceSessionEpoch: request.VoiceSessionEpoch, LocalStopMS: 2, CandidateMS: 640}
	for _, invalid := range []int{-1, 2001} {
		bad := r
		bad.CandidateMS = invalid
		if _, err := e.Interrupt(bad); err == nil {
			t.Fatal("invalid candidate timing accepted")
		}
	}
	result, err := e.Interrupt(r)
	if err != nil {
		t.Fatal(err)
	}
	if result.Interruption == nil || result.Interruption.CandidateMS != 640 {
		t.Fatal("candidate latency missing")
	}
	trace, err := e.Trace(old.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	metrics := ValidateTrace(trace).LatenciesMS
	if metrics["interruption_candidate"] != 640 || metrics["local_stop"] != 2 {
		t.Fatal("recognition wait hidden inside local stop metric")
	}
}
