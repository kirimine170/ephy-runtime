package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

// These are synthetic fixtures. The provider deliberately uses a different
// monotonic origin and can deliver callbacks after Cancel to test the boundary.
type asrTraceProvider struct {
	testVoiceASR
	session   *asrTraceSession
	openErr   error
	appendErr error
	openGate  <-chan struct{}
	opened    chan<- struct{}
}

type asrTraceSession struct {
	request    ASRSessionRequest
	onUpdate   func(ASRUpdate)
	final      ASRUpdate
	appendErr  error
	finishErr  error
	finishFunc func(context.Context) (ASRUpdate, error)
	onCancel   func()
	canceled   atomic.Bool
	cancelDone chan struct{}
	cancelOnce sync.Once
}

func (p *asrTraceProvider) OpenSession(_ context.Context, request ASRSessionRequest, onUpdate func(ASRUpdate)) (VoiceASRSession, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	p.session = &asrTraceSession{request: request, onUpdate: onUpdate, appendErr: p.appendErr, cancelDone: make(chan struct{})}
	if p.opened != nil {
		p.opened <- struct{}{}
	}
	if p.openGate != nil {
		<-p.openGate // Deliberately ignore ctx to exercise a late adapter handoff.
	}
	return p.session, nil
}

func (s *asrTraceSession) Append(ctx context.Context, _ int, _ []byte) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	return ctx.Err()
}

func (s *asrTraceSession) Finish(ctx context.Context) (ASRUpdate, error) {
	if s.finishFunc != nil {
		return s.finishFunc(ctx)
	}
	if ctx.Err() != nil {
		return ASRUpdate{}, ctx.Err()
	}
	if s.finishErr != nil {
		return ASRUpdate{}, s.finishErr
	}
	s.onUpdate(s.final)
	return s.final, nil
}

func (s *asrTraceSession) Cancel() {
	s.canceled.Store(true)
	s.cancelOnce.Do(func() { close(s.cancelDone) })
	if s.onCancel != nil {
		s.onCancel()
	}
}

func (s *asrTraceSession) update(revision int, phase, text string) ASRUpdate {
	update := ASRUpdate{
		OperationID: s.request.OperationID, SessionID: s.request.SessionID,
		TurnID: s.request.TurnID, SegmentID: s.request.SegmentID,
		Revision: revision, Phase: phase, Transcript: text,
		Provider: "synthetic-asr", ModelRevision: "synthetic-asr-v1",
		MonotonicMS: 10_000_000 + int64(revision),
	}
	if phase == "final" {
		update.StablePrefix = text
	}
	return update
}

func newASRTraceHarness(t *testing.T, provider *asrTraceProvider, tts testVoiceTTS, chat func(context.Context, ChatRequest, func(string)) (*ChatResponse, error)) *interactionGenerationHarness {
	t.Helper()
	h := newInteractionGenerationHarness(t, tts, chat)
	// Inject before Start. The dispatch goroutine has no operation to inspect yet.
	h.engine.asr = provider
	provider.testVoiceASR.transcribe = func(context.Context, []byte) (string, error) {
		return "", errors.New("legacy_asr_must_not_run")
	}
	return h
}

func beginASRTrace(t *testing.T, h *interactionGenerationHarness, partials int) (InteractionSnapshot, *asrTraceSession) {
	t.Helper()
	start, err := h.engine.Start(VoiceTurnRequest{SessionID: "synthetic-asr-session", InputKind: "microphone",
		Chat:             ChatRequest{Mode: "fast", MaxTokens: 64, Messages: []GatewayMessage{{Role: "user", Content: "private-asr-history-marker"}}},
		GenerationLimits: GenerationLimits{SegmentTokens: 64, MaxSegments: 3, MaxTotalTokens: 192, SoftTargetPercent: 75}})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := h.engine.BeginASR(start.OperationID, 16000)
	if err != nil || identity.OperationID != start.OperationID || identity.SessionID != start.SessionID || identity.TurnID != start.TurnID || identity.SegmentID == "" {
		t.Fatal("streaming ASR lost its operation identity", err)
	}
	if err := h.engine.AppendASRAudio(start.OperationID, 1, providerTestWAV()[44:]); err != nil {
		t.Fatal(err)
	}
	session := h.engine.asr.(*asrTraceProvider).session
	for revision := 1; revision <= partials; revision++ {
		session.onUpdate(session.update(revision, "partial", fmt.Sprintf("private-asr-partial-marker-%04d", revision)))
	}
	return start, session
}

func TestInteractionASRTraceSurvivesRevisionFloodAndMaximumGeneration(t *testing.T) {
	const partials, revisions, segments = 1536, 8, 3
	const finalText = "private-asr-final-marker 確定文。"
	var chatCalls, speechCalls atomic.Int32
	var requestsMu sync.Mutex
	var requests []ChatRequest
	h := newASRTraceHarness(t, &asrTraceProvider{}, testVoiceTTS{stream: func(_ context.Context, _ string, emit func([]byte) error) error {
		count := 3
		if speechCalls.Add(1)%segments == 0 {
			count = 2
		}
		for i := 0; i < count; i++ {
			if err := emit(providerTestWAV()); err != nil {
				return err
			}
		}
		return nil
	}}, func(_ context.Context, request ChatRequest, onToken func(string)) (*ChatResponse, error) {
		requestsMu.Lock()
		requests = append(requests, request)
		requestsMu.Unlock()
		call := int(chatCalls.Add(1))
		text := fmt.Sprintf("合成ASR応答%02d。", call)
		onToken(text)
		if call == revisions*segments {
			response := completedVoiceResponse(text)
			response.Thinking = "private-asr-reasoning-marker"
			return response, nil
		}
		return interactionGenerationLength(text+"未確定", 64), nil
	})
	// C0.3 uses the same maximal trace workload through a pinned profile and SpeechRequest．
	legacyTTS := h.engine.tts
	h.engine.tts = &syntheticSpeechProvider{profile: customTestProfile(), stream: func(ctx context.Context, request SpeechRequest, emit func([]byte) error) error {
		if request.VoiceProfileID != "synthetic-voice" || request.SpeechStyle != defaultSpeechStyle() {
			return errors.New("invalid_voice_profile")
		}
		return legacyTTS.Stream(ctx, request.SpeechText, emit)
	}}
	start, session := beginASRTrace(t, h, partials)
	session.final = session.update(partials+1, "final", finalText)
	// Providers may finalize before the user's endpoint, then repeat that final
	// in both the Finish callback and return value. It must enter history once.
	session.onUpdate(session.final)
	snapshot, err := h.engine.Snapshot(start.OperationID)
	if err != nil || snapshot.Transcript != "" || chatCalls.Load() != 0 {
		t.Fatal("ASR hypothesis entered the conversation before the endpoint", err)
	}
	trace, err := h.engine.Trace(start.OperationID)
	if err != nil || len(trace) > 12 {
		t.Fatal("partial revisions produced unbounded durable trace events", err)
	}
	for _, event := range trace {
		if event.Name == "asr_final" || event.Name == "llm_requested" {
			t.Fatal("ASR or LLM finalized before the endpoint")
		}
	}
	if err := h.engine.EndASR(start.OperationID); err != nil {
		t.Fatal(err)
	}
	for revision := 1; revision <= revisions; revision++ {
		if revision > 1 {
			if _, err := h.engine.Continue(start.OperationID); err != nil {
				t.Fatal(err)
			}
		}
		state := "INCOMPLETE"
		if revision == revisions {
			state = "COMPLETED"
		}
		final := awaitInteraction(t, h.engine, start.OperationID, state)
		awaitTraceLimitTerminal(t, h, start.OperationID, revision, state)
		if final.Transcript != finalText || final.TurnID != start.TurnID || final.LastAudioSequence != revision*8 {
			t.Fatal("ASR final or generation continuation identity changed")
		}
		assertTraceLimitPersistence(t, h, start.OperationID, revision, state)
		metadata := assertASRTracePrivacyAndReload(t, h, start.OperationID)
		if metadata.RevisionCount != partials+1 || metadata.CharacterCount != utf8.RuneCountInString(finalText) || metadata.Provider != "synthetic-asr" || metadata.ModelRevision != "synthetic-asr-v1" {
			t.Fatal("ASR revision count, final character count or provider identity was lost")
		}
		if metadata.FirstAudioMS == nil || metadata.FirstPartialMS == nil || metadata.FirstStableMS == nil || metadata.FinalMS == nil || metadata.FinalizationMS == nil {
			t.Fatal("observed ASR timings were lost")
		}
		if *metadata.FirstStableMS != *metadata.FinalMS || *metadata.FirstPartialMS > *metadata.FinalMS {
			t.Fatal("Apple-style partial results were marked stable before final")
		}
		for _, timing := range []*int64{metadata.FirstAudioMS, metadata.FirstPartialMS, metadata.FirstStableMS, metadata.FinalMS, metadata.FinalizationMS} {
			if *timing < 0 || *timing >= 10_000_000 {
				t.Fatal("provider clock origin was mistaken for Runtime latency")
			}
		}
	}
	if chatCalls.Load() != revisions*segments || speechCalls.Load() != revisions*segments {
		t.Fatal("test did not exercise all generation continuations and audio chunks")
	}
	requestsMu.Lock()
	captured := append([]ChatRequest(nil), requests...)
	requestsMu.Unlock()
	for index, request := range captured {
		if request.Prompt != finalText || len(request.Messages) != 1 || request.Messages[0].Content != "private-asr-history-marker" {
			t.Fatal("partial ASR text entered the LLM request or history")
		}
		if index > 0 && (len(request.generationMessages) != 3 || request.generationMessages[1].Content != finalText || request.generationMessages[2].Role != "assistant") {
			t.Fatal("continuation created a second ASR user turn")
		}
	}
	trace, _ = h.engine.Trace(start.OperationID)
	names := map[string]int{}
	for _, event := range trace {
		names[event.Name]++
	}
	for _, name := range []string{"asr_started", "asr_first_partial", "asr_first_stable", "asr_final", "turn_completed"} {
		if names[name] != 1 {
			t.Fatalf("ASR flood lost or duplicated critical event %s", name)
		}
	}
	h.checkPlayback(t)
}

func TestInteractionASRTraceIncompleteRecognitionKeepsUnknownTimingsAndPrivateErrors(t *testing.T) {
	for _, ending := range []string{"cancel", "failure", "timeout"} {
		t.Run(ending, func(t *testing.T) {
			var calls atomic.Int32
			h := newASRTraceHarness(t, &asrTraceProvider{}, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				calls.Add(1)
				return completedVoiceResponse("next synthetic response"), nil
			})
			start, session := beginASRTrace(t, h, 1536)
			state, code := "FAILED", "asr_failed"
			if ending == "cancel" {
				state, code = "CANCELED", ""
				if _, err := h.engine.Cancel(start.OperationID); err != nil {
					t.Fatal(err)
				}
			} else {
				session.finishErr = errors.New("private-asr-error-marker /private/synthetic/transcript")
				if ending == "timeout" {
					session.finishErr = context.DeadlineExceeded
					code = "asr_timeout"
				}
				if err := h.engine.EndASR(start.OperationID); err != nil {
					t.Fatal(err)
				}
			}
			final := awaitInteraction(t, h.engine, start.OperationID, state)
			awaitTraceLimitTerminal(t, h, start.OperationID, 1, state)
			if final.ErrorCode != code || final.Transcript != "" || calls.Load() != 0 {
				t.Fatal("failed recognition became a transcript or leaked an error")
			}
			metadata := assertASRTracePrivacyAndReload(t, h, start.OperationID)
			if metadata.RevisionCount != 1536 || metadata.FirstPartialMS == nil || metadata.FirstAudioMS == nil || metadata.FirstStableMS != nil || metadata.FinalMS != nil || metadata.FinalizationMS != nil {
				t.Fatal("unobserved final timings became successful measurements")
			}
			interactionGenerationReceive(t, session.cancelDone)
			if !session.canceled.Load() {
				t.Fatal("terminal recognition retained its adapter session")
			}
			before, _ := h.engine.Trace(start.OperationID)
			next := h.start(t, "next-asr-session", GenerationLimits{})
			session.onUpdate(session.update(1537, "partial", "private-asr-late-partial-marker"))
			session.onUpdate(session.update(1538, "final", "private-asr-late-final-marker"))
			lateFailure := session.update(1539, "failure", "")
			lateFailure.ErrorCode = "asr_failed"
			session.onUpdate(lateFailure)
			nextFinal := awaitInteraction(t, h.engine, next.OperationID, "COMPLETED")
			if nextFinal.Transcript != "private-prompt-marker" || calls.Load() != 1 {
				t.Fatal("late ASR final contaminated the next turn")
			}
			after, _ := h.engine.Trace(start.OperationID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("late ASR callback changed the terminated trace")
			}
			h.checkPlayback(t)
		})
	}
}

func TestInteractionASRTraceBridgeScrubsProviderStartupAndAppendErrors(t *testing.T) {
	for _, phase := range []string{"startup", "append"} {
		t.Run(phase, func(t *testing.T) {
			provider := &asrTraceProvider{}
			secret := errors.New("private-asr-error-marker /private/synthetic/transcript")
			if phase == "startup" {
				provider.openErr = secret
			} else {
				provider.appendErr = secret
			}
			var calls atomic.Int32
			h := newASRTraceHarness(t, provider, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				calls.Add(1)
				return nil, errors.New("unexpected_generation")
			})
			app := &App{interaction: h.engine}
			start := startTestInteraction(t, h.engine)
			_, err := app.BeginInteractionASR(start.OperationID, 16000)
			if phase == "append" {
				if err != nil {
					t.Fatal(err)
				}
				err = app.AppendInteractionAudio(start.OperationID, 1, base64.StdEncoding.EncodeToString(providerTestWAV()[44:]))
			}
			if err == nil || err.Error() != "asr_failed" {
				t.Fatal("bridge did not return the fixed ASR error code")
			}
			final := awaitInteraction(t, h.engine, start.OperationID, "FAILED")
			if final.ErrorCode != "asr_failed" || final.Transcript != "" || calls.Load() != 0 {
				t.Fatal("provider error crossed the public failure boundary")
			}
			assertASRTracePrivacyAndReload(t, h, start.OperationID)
		})
	}
}

func TestInteractionASRTraceReadsLegacySchemasWithoutInventingMeasurements(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("schema%d", version), func(t *testing.T) {
			operation := fmt.Sprintf("operation_legacy_%d", version)
			var events []InteractionTraceEvent
			for index, name := range []string{"user_speech_start", "cancel_requested", "cancel_acknowledged"} {
				events = append(events, InteractionTraceEvent{
					SchemaVersion: version, EventID: fmt.Sprintf("event_legacy_%d_%d", version, index),
					TraceID: "trace_legacy", SessionID: "session_legacy", TurnID: "turn_legacy", OperationID: operation,
					Name: name, Source: "microphone", Timestamp: "2026-09-06T00:00:00Z", MonotonicMS: int64(index),
					Status: "CANCELED", ProviderID: "desktop", ModelID: "interaction", ConfigurationID: "legacy",
				})
			}
			data, err := json.Marshal(events)
			if err != nil || strings.Contains(string(data), `"asr"`) {
				t.Fatal("legacy fixture unexpectedly included the new metadata")
			}
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, operation+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			engine := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, directory)
			defer engine.Close()
			loaded, err := engine.Trace(operation)
			if err != nil || !reflect.DeepEqual(events, loaded) || !ValidateTrace(loaded).Valid {
				t.Fatal("legacy trace was rejected or rewritten", err)
			}
			for _, event := range loaded {
				if event.ASR != nil {
					t.Fatal("legacy trace acquired unobserved ASR measurements")
				}
			}
		})
	}
}

func TestInteractionASRTraceCancelReclaimsLateStartupSession(t *testing.T) {
	entered := make(chan struct{}, 1)
	provider := &asrTraceProvider{opened: entered}
	h := newASRTraceHarness(t, provider, testVoiceTTS{}, testVoiceChat)
	gate, release := interactionGenerationRelease(t)
	provider.openGate = gate
	start := startTestInteraction(t, h.engine)
	returned := make(chan error, 1)
	go func() {
		_, err := h.engine.BeginASR(start.OperationID, 16000)
		returned <- err
	}()
	interactionGenerationReceive(t, entered)
	if _, err := h.engine.Cancel(start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := interactionGenerationReceive(t, returned); err == nil || err.Error() != "asr_canceled" {
		t.Fatal("startup did not return a fixed cancellation before the adapter finished")
	}
	before, _ := h.engine.Trace(start.OperationID)
	release()
	interactionGenerationReceive(t, provider.session.cancelDone)
	after, _ := h.engine.Trace(start.OperationID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("late session attachment changed the terminal trace")
	}
	assertASRTracePrivacyAndReload(t, h, start.OperationID)
}

func TestInteractionASRTraceCanceledFinishCannotEnterNextTurn(t *testing.T) {
	var calls atomic.Int32
	h := newASRTraceHarness(t, &asrTraceProvider{}, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		calls.Add(1)
		return completedVoiceResponse("next synthetic response"), nil
	})
	gate, release := interactionGenerationRelease(t)
	entered, returned := make(chan struct{}, 1), make(chan struct{}, 1)
	start, session := beginASRTrace(t, h, 1)
	lateFinal := session.update(2, "final", "private-asr-late-final-marker")
	session.finishFunc = func(context.Context) (ASRUpdate, error) {
		entered <- struct{}{}
		<-gate // Ignore cancellation, as a faulty adapter might.
		session.onUpdate(lateFinal)
		returned <- struct{}{}
		return lateFinal, nil
	}
	if err := h.engine.EndASR(start.OperationID); err != nil {
		t.Fatal(err)
	}
	interactionGenerationReceive(t, entered)
	if _, err := h.engine.Cancel(start.OperationID); err != nil {
		t.Fatal(err)
	}
	before, _ := h.engine.Trace(start.OperationID)
	next := h.start(t, "next-asr-session", GenerationLimits{})
	release()
	interactionGenerationReceive(t, returned)
	final := awaitInteraction(t, h.engine, next.OperationID, "COMPLETED")
	if calls.Load() != 1 || final.Transcript != "private-prompt-marker" {
		t.Fatal("canceled Finish returned a second conversation input")
	}
	after, _ := h.engine.Trace(start.OperationID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("canceled Finish rewrote the old trace")
	}
	assertASRTracePrivacyAndReload(t, h, start.OperationID)
}

func TestInteractionASRTraceSessionTerminationDoesNotWaitForAdapterCleanup(t *testing.T) {
	h := newASRTraceHarness(t, &asrTraceProvider{}, testVoiceTTS{}, testVoiceChat)
	gate, _ := interactionGenerationRelease(t)
	start, session := beginASRTrace(t, h, 1)
	session.onCancel = func() { <-gate }
	// Terminate the session context without canceling the operation. This is the
	// same watcher path as the finite session deadline, without a 95-second sleep.
	h.engine.mu.Lock()
	h.engine.turns[start.OperationID].asr.cancel()
	h.engine.mu.Unlock()
	interactionGenerationReceive(t, session.cancelDone)
	final := awaitInteraction(t, h.engine, start.OperationID, "FAILED")
	if final.ErrorCode != "asr_canceled" || final.Transcript != "" {
		t.Fatal("session failure waited for external adapter cleanup")
	}
	assertASRTracePrivacyAndReload(t, h, start.OperationID)
}

func assertASRTracePrivacyAndReload(t *testing.T, h *interactionGenerationHarness, operation string) *ASRMetadata {
	t.Helper()
	trace, err := h.engine.Trace(operation)
	if err != nil || len(trace) > 128 || !ValidateTrace(trace).Valid {
		t.Fatal("ASR trace exceeded its event bound or lost critical facts", err)
	}
	var metadata *ASRMetadata
	for _, event := range trace {
		if event.ASR != nil {
			if event.SchemaVersion != 3 {
				t.Fatal("ASR metadata used the wrong trace schema")
			}
			metadata = event.ASR
		}
	}
	if metadata == nil {
		t.Fatal("terminal ASR metadata was not retained")
	}
	data, err := os.ReadFile(filepath.Join(h.store, operation+".json"))
	if err != nil || len(data) > 256<<10 {
		t.Fatal("ASR trace was not durably saved within the reader byte bound", err)
	}
	memory, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range [][]byte{data, memory} {
		for _, secret := range []string{"private-asr-", "合成ASR応答", "未確定", "/private/synthetic/", base64.StdEncoding.EncodeToString(providerTestWAV()), base64.StdEncoding.EncodeToString(providerTestWAV()[44:])} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("ASR content entered trace metadata")
			}
		}
	}
	reopened := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, h.store)
	defer reopened.Close()
	reloaded, err := reopened.Trace(operation)
	if err != nil || !reflect.DeepEqual(trace, reloaded) {
		t.Fatal("ASR trace did not round-trip through a new Engine", err)
	}
	return metadata
}
