package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type c02SessionResult struct {
	update ASRUpdate
	err    error
}
type c02EngineSession struct {
	req      ASRSessionRequest
	emit     func(ASRUpdate)
	result   chan c02SessionResult
	canceled atomic.Bool
}

func (s *c02EngineSession) Append(ctx context.Context, seq int, pcm []byte) error { return ctx.Err() }
func (s *c02EngineSession) Finish(ctx context.Context) (ASRUpdate, error) {
	select {
	case r := <-s.result:
		return r.update, r.err
	case <-ctx.Done():
		return ASRUpdate{}, ctx.Err()
	}
}
func (s *c02EngineSession) Cancel() { s.canceled.Store(true) }
func (s *c02EngineSession) update(revision int, phase, text string) ASRUpdate {
	u := ASRUpdate{OperationID: s.req.OperationID, SessionID: s.req.SessionID, TurnID: s.req.TurnID, SegmentID: s.req.SegmentID, Revision: revision, Phase: phase, Transcript: text, Provider: "mock-asr", ModelRevision: "mock/v1", MonotonicMS: int64(revision)}
	if phase == "final" {
		u.StablePrefix = text
	}
	return u
}

type c02EngineProvider struct {
	testVoiceASR
	sessions []*c02EngineSession
}

func (p *c02EngineProvider) OpenSession(ctx context.Context, req ASRSessionRequest, emit func(ASRUpdate)) (VoiceASRSession, error) {
	s := &c02EngineSession{req: req, emit: emit, result: make(chan c02SessionResult, 1)}
	p.sessions = append(p.sessions, s)
	return s, nil
}

func c02Start(t *testing.T, e *InteractionEngine, p *c02EngineProvider) (InteractionSnapshot, *c02EngineSession) {
	t.Helper()
	s := startTestInteraction(t, e)
	if _, err := e.BeginASR(s.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	if err := e.AppendASRAudio(s.OperationID, 1, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	return s, p.sessions[len(p.sessions)-1]
}

func TestStreamingASRFinalOnlyAfterEndpointAndVerifiedFinish(t *testing.T) {
	p := &c02EngineProvider{}
	requests := make(chan ChatRequest, 1)
	var mu sync.Mutex
	var transcripts []string
	e := NewInteractionEngine(p, testVoiceTTS{}, func(ctx context.Context, r ChatRequest, token func(string)) (*ChatResponse, error) {
		requests <- r
		return completedVoiceResponse("```go\ncode\n```"), nil
	}, func(ev InteractionEvent) {
		if ev.Kind == "transcript" {
			mu.Lock()
			transcripts = append(transcripts, ev.Text)
			mu.Unlock()
		}
	}, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, p)
	for i, text := range []string{"private provisional 桃", "private revision 桃の花"} {
		session.emit(session.update(i+1, "partial", text))
	}
	stable := session.update(3, "stable", "private stable 日本の四季")
	stable.StablePrefix = "private stable "
	session.emit(stable)
	final := session.update(4, "final", "private stable 四季を説明して")
	session.emit(final)
	snapshot, _ := e.Snapshot(s.OperationID)
	request, _ := e.GetRequest(s.OperationID)
	if snapshot.State != "RECORDING" || snapshot.Transcript != "" || request.Chat.Prompt != "private prompt" {
		t.Fatal("hypothesis entered Conversation before endpoint")
	}
	select {
	case <-requests:
		t.Fatal("LLM called before endpoint")
	default:
	}
	if err := e.EndASR(s.OperationID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
		t.Fatal("LLM called before verified finish")
	default:
	}
	session.result <- c02SessionResult{update: final}
	select {
	case request := <-requests:
		if request.Prompt != final.Transcript || request.SessionMode != "voice" || len(request.Messages) != 1 {
			t.Fatal("final/history integration changed")
		}
	case <-time.After(time.Second):
		t.Fatal("final not forwarded")
	}
	awaitInteraction(t, e, s.OperationID, "COMPLETED")
	trace, _ := e.Trace(s.OperationID)
	encoded, _ := json.Marshal(trace)
	for _, private := range []string{"private provisional", "private revision", "private stable", "private final", "private prompt", "private history", "四季を説明して", "pcm_base64", "transcript"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("trace privacy leak %q", private)
		}
	}
	var metadata *ASRMetadata
	for _, ev := range trace {
		if ev.Name == "asr_final" {
			metadata = ev.ASR
		}
	}
	if metadata == nil || metadata.RevisionCount != 4 || metadata.FirstPartialMS == nil || metadata.FirstStableMS == nil || metadata.FinalMS == nil || metadata.FinalizationMS == nil {
		t.Fatalf("missing body-free measurements: %+v", metadata)
	}
	mu.Lock()
	count := len(transcripts)
	mu.Unlock()
	// Completed state delivery may still be queued，but the transcript is queued first．
	if count > 1 {
		t.Fatal("duplicate final Conversation message")
	}
}

func TestStreamingASRIdentityRevisionFencingAndCancelNextTurn(t *testing.T) {
	p := &c02EngineProvider{}
	var calls atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(ctx context.Context, r ChatRequest, token func(string)) (*ChatResponse, error) {
		calls.Add(1)
		return completedVoiceResponse("```\ncode\n```"), nil
	}, nil, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, p)
	session.emit(session.update(2, "partial", "current"))
	for _, mutate := range []func(*ASRUpdate){func(u *ASRUpdate) { u.OperationID = "old-operation" }, func(u *ASRUpdate) { u.SessionID = "old-session" }, func(u *ASRUpdate) { u.TurnID = "old-turn" }, func(u *ASRUpdate) { u.SegmentID = "old-segment" }, func(u *ASRUpdate) { u.Revision = 1 }} {
		u := session.update(3, "final", "stale-private")
		mutate(&u)
		session.emit(u)
	}
	e.mu.Lock()
	metadata := cloneASRMetadata(&e.turns[s.OperationID].asr.metadata)
	e.mu.Unlock()
	if metadata.RevisionCount != 1 || metadata.FinalMS != nil {
		t.Fatal("old identity/revision accepted")
	}
	if _, err := e.Cancel(s.OperationID); err != nil {
		t.Fatal(err)
	}
	next, newSession := c02Start(t, e, p)
	session.emit(session.update(4, "final", "old text must not enter next turn"))
	if err := e.AppendASRAudio(s.OperationID, 2, []byte{0, 0}); err == nil {
		t.Fatal("old audio accepted")
	}
	snapshot, _ := e.Snapshot(next.OperationID)
	if snapshot.Transcript != "" || snapshot.State != "RECORDING" || calls.Load() != 0 {
		t.Fatal("old final crossed operation boundary")
	}
	final := newSession.update(1, "final", "new current final")
	newSession.result <- c02SessionResult{update: final}
	if err := e.EndASR(next.OperationID); err != nil {
		t.Fatal(err)
	}
	awaitInteraction(t, e, next.OperationID, "COMPLETED")
	if calls.Load() != 1 {
		t.Fatal("unexpected generation count")
	}
}

func TestStreamingASRRejectsMissingFinalAndInvalidStream(t *testing.T) {
	for _, kind := range []string{"eof-after-final", "partial-finish", "timeout", "invalid-utf8", "backward-time", "empty-final", "foreign-final", "metadata-change", "revision-limit", "failure-private"} {
		t.Run(kind, func(t *testing.T) {
			p := &c02EngineProvider{}
			var calls atomic.Int32
			e := NewInteractionEngine(p, testVoiceTTS{}, func(ctx context.Context, r ChatRequest, token func(string)) (*ChatResponse, error) {
				calls.Add(1)
				return nil, nil
			}, nil, t.TempDir())
			e.Timeouts.ASR = 10 * time.Millisecond
			defer e.Close()
			s, session := c02Start(t, e, p)
			partial := session.update(1, "partial", "private hypothesis")
			session.emit(partial)
			final := session.update(2, "final", "private final")
			switch kind {
			case "eof-after-final":
				session.emit(final)
				session.result <- c02SessionResult{err: errors.New("asr_stream_eof")}
			case "partial-finish":
				session.result <- c02SessionResult{update: partial}
			case "timeout":
			case "invalid-utf8":
				final.Transcript = string([]byte{255})
				final.StablePrefix = ""
				session.emit(final)
			case "backward-time":
				final.MonotonicMS = 0
				session.emit(final)
			case "empty-final":
				final.Transcript = " "
				final.StablePrefix = ""
				session.emit(final)
			case "foreign-final":
				final.SessionID = "foreign"
				session.result <- c02SessionResult{update: final}
			case "metadata-change":
				final.ModelRevision = "another"
				session.emit(final)
			case "revision-limit":
				final.Revision = maxASRRevisions + 1
				session.emit(final)
			case "failure-private":
				final.Phase = "failure"
				final.Transcript = ""
				final.StablePrefix = ""
				final.ErrorCode = "private provider exception"
				session.emit(final)
			}
			snapshot, _ := e.Snapshot(s.OperationID)
			if snapshot.State == "RECORDING" {
				if err := e.EndASR(s.OperationID); err != nil {
					t.Fatal(err)
				}
			}
			snapshot = awaitInteraction(t, e, s.OperationID, "FAILED")
			if calls.Load() != 0 || snapshot.Transcript != "" {
				t.Fatal("unverified final promoted")
			}
			trace, _ := e.Trace(s.OperationID)
			encoded, _ := json.Marshal(trace)
			if strings.Contains(string(encoded), "private") {
				t.Fatal("provider content escaped into trace")
			}
			if kind == "timeout" && snapshot.ErrorCode != "asr_timeout" {
				t.Fatal(snapshot.ErrorCode)
			}
			if kind == "eof-after-final" && snapshot.ErrorCode != "asr_stream_eof" {
				t.Fatal(snapshot.ErrorCode)
			}
		})
	}
}

func TestStreamingASRPCMInputBoundsAndDuplicateEndpoint(t *testing.T) {
	for _, kind := range []string{"sequence", "odd", "empty", "oversize", "total", "duplicate-end", "batch-mixed"} {
		t.Run(kind, func(t *testing.T) {
			p := &c02EngineProvider{}
			e := NewInteractionEngine(p, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
			defer e.Close()
			s, _ := c02Start(t, e, p)
			pcm, seq := []byte{0, 0}, 2
			switch kind {
			case "sequence":
				seq = 1
			case "odd":
				pcm = []byte{1}
			case "empty":
				pcm = nil
			case "oversize":
				pcm = make([]byte, maxASRPCMChunkBytes+2)
			case "total":
				e.mu.Lock()
				e.turns[s.OperationID].asr.bytes = 16000 * 2 * 60
				e.mu.Unlock()
			case "duplicate-end":
				if err := e.EndASR(s.OperationID); err != nil {
					t.Fatal(err)
				}
				if err := e.EndASR(s.OperationID); err == nil {
					t.Fatal("duplicate endpoint accepted")
				}
				return
			case "batch-mixed":
				if err := e.Commit(s.OperationID, testVoiceWAV(), ""); err == nil {
					t.Fatal("batch and stream mixed")
				}
				return
			}
			if err := e.AppendASRAudio(s.OperationID, seq, pcm); err == nil {
				t.Fatal("invalid PCM accepted")
			}
			awaitInteraction(t, e, s.OperationID, "FAILED")
		})
	}
}

func TestStreamingASREmptyHypothesisDoesNotClaimFirstVisiblePartial(t *testing.T) {
	p := &c02EngineProvider{}
	e := NewInteractionEngine(p, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, p)
	session.emit(session.update(1, "partial", ""))
	session.emit(session.update(2, "partial", " \n"))
	e.mu.Lock()
	before := cloneASRMetadata(&e.turns[s.OperationID].asr.metadata)
	e.mu.Unlock()
	if before.RevisionCount != 2 || before.FirstPartialMS != nil {
		t.Fatal("empty hypotheses became an observed first partial")
	}
	session.emit(session.update(3, "partial", "visible hypothesis"))
	e.mu.Lock()
	after := cloneASRMetadata(&e.turns[s.OperationID].asr.metadata)
	e.mu.Unlock()
	if after.RevisionCount != 3 || after.FirstPartialMS == nil {
		t.Fatal("first visible partial was not measured")
	}
}

type activityEngineProvider struct{ c02EngineProvider }

func (p *activityEngineProvider) Capabilities() ASRCapabilities {
	return ASRCapabilities{Partial: true, Activity: true, NoSpeech: true}
}

func TestASRNoSpeechDoesNotCreateConversationAndNextInputRemainsIndependent(t *testing.T) {
	p := &activityEngineProvider{}
	var calls atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		calls.Add(1)
		return completedVoiceResponse("```code```"), nil
	}, nil, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, &p.c02EngineProvider)
	if err := e.EndASR(s.OperationID); err != nil {
		t.Fatal(err)
	}
	final := session.update(1, "no_speech", "")
	session.emit(final)
	session.result <- c02SessionResult{update: final}
	got := awaitInteraction(t, e, s.OperationID, "CANCELED")
	if got.InputOutcome != "no_speech" || got.Transcript != "" || got.ErrorCode != "" || calls.Load() != 0 {
		t.Fatal("no-speech became conversation or failure", got)
	}
	next, newSession := c02Start(t, e, &p.c02EngineProvider)
	session.emit(session.update(2, "final", "stale"))
	got, _ = e.Snapshot(next.OperationID)
	if got.Transcript != "" || got.State != "RECORDING" || newSession.canceled.Load() {
		t.Fatal("old no-speech affected next session")
	}
	trace, err := e.Trace(s.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, event := range trace {
		if event.Name == "asr_no_speech" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("no-speech outcome missing from metadata")
	}
}

func TestASRActivityDoesNotOverwritePartialOrCommitText(t *testing.T) {
	p := &activityEngineProvider{}
	e := NewInteractionEngine(p, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, &p.c02EngineProvider)
	session.emit(session.update(1, "partial", "小さな声"))
	a := session.update(2, "activity", "")
	a.Activity = &ASRAudioActivity{AudioMS: 10, LastSpeechMS: 10, SpeechMS: 10, Probability: .9, HasSpeech: true, Speaking: true}
	session.emit(a)
	e.mu.Lock()
	metadata := e.turns[s.OperationID].asr.metadata
	last := e.turns[s.OperationID].asr.lastRevision
	e.mu.Unlock()
	got, _ := e.Snapshot(s.OperationID)
	if metadata.CharacterCount != 4 || metadata.VADSpeechMS != 10 || last != 2 || got.Transcript != "" || got.State != "RECORDING" {
		t.Fatal("activity changed hypothesis or canonical input", metadata, got)
	}
}

func TestASRCaptureMetadataAndEndpointReasonExcludeDeviceIdentity(t *testing.T) {
	p := &c02EngineProvider{}
	e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		return completedVoiceResponse("```go\nfixture\n```"), nil
	}, nil, t.TempDir())
	defer e.Close()
	s, session := c02Start(t, e, p)
	yes, no := true, false
	m := ASRCaptureMetadata{ContextSampleRate: 48000, TrackSampleRate: 44100, ChannelCount: 1, EchoCancellation: &yes, NoiseSuppression: &yes, AutoGainControl: &no}
	if err := e.RecordASRCapture(s.OperationID, m); err != nil {
		t.Fatal(err)
	}
	yes = false
	if err := e.EndASRWithReason(s.OperationID, "silence"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	metadata := cloneASRMetadata(&e.turns[s.OperationID].asr.metadata)
	e.mu.Unlock()
	if metadata.EndpointReason != "silence" || metadata.Capture == nil || !*metadata.Capture.EchoCancellation || metadata.InputSampleRate != 16000 {
		t.Fatal("capture metadata missing or aliased", metadata)
	}
	session.result <- c02SessionResult{update: session.update(1, "final", "確定")}
	awaitInteraction(t, e, s.OperationID, "COMPLETED")
	if err := e.RecordASRCapture(s.OperationID, m); err == nil {
		t.Fatal("late capture metadata changed closed turn")
	}
}
