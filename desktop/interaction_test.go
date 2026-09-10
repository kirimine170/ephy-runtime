package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testVoiceASR struct {
	ready      func(context.Context) error
	transcribe func(context.Context, []byte) (string, error)
}

func (a testVoiceASR) Ready(ctx context.Context) error {
	if a.ready != nil {
		return a.ready(ctx)
	}
	return nil
}
func (a testVoiceASR) Transcribe(ctx context.Context, b []byte) (string, error) {
	if a.transcribe != nil {
		return a.transcribe(ctx, b)
	}
	return "private speech", nil
}

type testVoiceTTS struct {
	ready  func(context.Context) error
	stream func(context.Context, string, func([]byte) error) error
}

func (a testVoiceTTS) Ready(ctx context.Context) error {
	if a.ready != nil {
		return a.ready(ctx)
	}
	return nil
}
func (a testVoiceTTS) Stream(ctx context.Context, s string, emit func([]byte) error) error {
	if a.stream != nil {
		return a.stream(ctx, s, emit)
	}
	return emit(testVoiceWAV())
}
func testVoiceWAV() []byte {
	b := make([]byte, 48)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 40)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 4)
	return b
}
func testVoiceChat(ctx context.Context, r ChatRequest, token func(string)) (*ChatResponse, error) {
	reportInteractionModel(ctx, "test-provider", "test/model", "test-config")
	token("private answer")
	return completedVoiceResponse("private answer"), nil
}
func completedVoiceResponse(text string) *ChatResponse {
	return &ChatResponse{Answer: text, FinishReason: "stop", Generation: &GenerationMetadata{SchemaVersion: 2, FinishReason: "stop", ProviderFinishReason: "stop", TerminalSSE: true, DoneReceived: true, Complete: true}}
}
func awaitInteraction(t *testing.T, e *InteractionEngine, op, state string) InteractionSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, err := e.Snapshot(op)
		if err != nil {
			t.Fatal(err)
		}
		if s.State == state {
			return s
		}
		if interactionTerminal(s.State) {
			t.Fatalf("wanted %s, got %+v", state, s)
		}
		time.Sleep(time.Millisecond)
	}
	s, _ := e.Snapshot(op)
	t.Fatalf("waiting for %s: %+v", state, s)
	return s
}
func startTestInteraction(t *testing.T, e *InteractionEngine) InteractionSnapshot {
	t.Helper()
	s, err := e.Start(VoiceTurnRequest{SessionID: "test-session", Chat: ChatRequest{Mode: "fast", Prompt: "private prompt", Messages: []GatewayMessage{{Role: "user", Content: "private history"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInteractionStreamsAndCompletesOnlyAfterEveryPlaybackAck(t *testing.T) {
	var mu sync.Mutex
	var events []InteractionEvent
	var engine *InteractionEngine
	delivered := make(chan InteractionEvent, 4)
	finish := make(chan struct{})
	tts := testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
		if err := emit(testVoiceWAV()); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
		}
		return emit(testVoiceWAV())
	}}
	engine = NewInteractionEngine(testVoiceASR{}, tts, testVoiceChat, func(event InteractionEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		if event.Kind == "audio" {
			delivered <- event
		}
	}, t.TempDir())
	defer engine.Close()
	s := startTestInteraction(t, engine)
	if s.State != "RECORDING" {
		t.Fatal(s)
	}
	if _, err := engine.Start(VoiceTurnRequest{}); err == nil {
		t.Fatal("overlapping operation accepted")
	}
	if err := engine.Commit(s.OperationID, testVoiceWAV(), ""); err != nil {
		t.Fatal(err)
	}
	audio1 := <-delivered
	if audio1.Sequence != 1 || audio1.OperationID != s.OperationID {
		t.Fatal(audio1)
	}
	if err := engine.Playback(s.OperationID, 1, "stopped"); err == nil {
		t.Fatal("stop before start accepted")
	}
	if err := engine.Playback(s.OperationID, 1, "started"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Playback(s.OperationID, 1, "stopped"); err != nil {
		t.Fatal(err)
	}
	playing := awaitInteraction(t, engine, s.OperationID, "PLAYING")
	if playing.ResponsePlan == nil || !playing.ResponsePlan.Interruptible || playing.ResponsePlan.VoiceHint.Pace != 1 {
		t.Fatal(playing)
	}
	close(finish)
	audio2 := <-delivered
	if audio2.Sequence != 2 {
		t.Fatal(audio2)
	}
	engine.Playback(s.OperationID, 2, "started")
	engine.Playback(s.OperationID, 2, "stopped")
	done := awaitInteraction(t, engine, s.OperationID, "COMPLETED")
	if done.Transcript != "private speech" {
		t.Fatal(done)
	}
	trace, err := engine.Trace(s.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	validation := ValidateTrace(trace)
	if !validation.Valid {
		t.Fatal(validation)
	}
	if _, ok := validation.LatenciesMS["first_audio"]; !ok {
		t.Fatal(validation)
	}
	for _, event := range trace {
		if event.Name == "llm_first_token" && (event.ModelID != "test/model" || event.ProviderID != "test-provider") {
			t.Fatal(event)
		}
	}
	for _, event := range trace {
		if event.Name == "turn_completed" && event.Status != "COMPLETED" {
			t.Fatal(event)
		}
	}
	canceled, err := engine.Cancel(s.OperationID)
	if err != nil || canceled.State != "COMPLETED" {
		t.Fatal(canceled, err)
	}
	request, err := engine.GetRequest(s.OperationID)
	if err != nil || request.Chat.Prompt != "private speech" || request.Chat.SessionMode != "voice" || len(request.Chat.Messages) != 1 {
		t.Fatal(request, err)
	}
	request.Chat.Messages[0].Content = "mutated"
	fresh, _ := engine.GetRequest(s.OperationID)
	if fresh.Chat.Messages[0].Content == "mutated" {
		t.Fatal("request leaked mutable history")
	}
	// The returned plan must also be detached from the live snapshot.
	done.ResponsePlan.Text = "mutated"
	freshSnapshot, _ := engine.Snapshot(s.OperationID)
	if freshSnapshot.ResponsePlan.Text == "mutated" {
		t.Fatal("snapshot leaked mutable plan")
	}
}

func TestInteractionCancelEveryActiveStateAndDropLateCallbacks(t *testing.T) {
	for _, state := range []string{"RECORDING", "TRANSCRIBING", "THINKING", "SYNTHESIZING", "PLAYING"} {
		t.Run(state, func(t *testing.T) {
			canceled := make(chan struct{}, 1)
			entered := make(chan struct{}, 1)
			late := make(chan struct{})
			var engine *InteractionEngine
			var mu sync.Mutex
			lateData := 0
			wait := func(ctx context.Context) { entered <- struct{}{}; <-ctx.Done(); canceled <- struct{}{}; <-late }
			asr := testVoiceASR{}
			tts := testVoiceTTS{}
			chat := testVoiceChat
			switch state {
			case "TRANSCRIBING":
				asr.transcribe = func(ctx context.Context, _ []byte) (string, error) { wait(ctx); return "late transcript", nil }
			case "THINKING":
				chat = func(ctx context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
					wait(ctx)
					emit("late token")
					return &ChatResponse{Answer: "late answer"}, nil
				}
			case "SYNTHESIZING":
				tts.stream = func(ctx context.Context, _ string, emit func([]byte) error) error {
					wait(ctx)
					return emit(testVoiceWAV())
				}
			case "PLAYING":
				tts.stream = func(ctx context.Context, _ string, emit func([]byte) error) error {
					if err := emit(testVoiceWAV()); err != nil {
						return err
					}
					wait(ctx)
					return emit(testVoiceWAV())
				}
			}
			engine = NewInteractionEngine(asr, tts, chat, func(event InteractionEvent) {
				if event.Kind == "audio" {
					engine.Playback(event.OperationID, event.Sequence, "started")
				}
				if event.Text == "late transcript" || event.Text == "late token" {
					mu.Lock()
					lateData++
					mu.Unlock()
				}
			}, "")
			defer engine.Close()
			s := startTestInteraction(t, engine)
			if state != "RECORDING" {
				if err := engine.Commit(s.OperationID, testVoiceWAV(), ""); err != nil {
					t.Fatal(err)
				}
			}
			awaitInteraction(t, engine, s.OperationID, state)
			if state != "RECORDING" {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("stage never entered")
				}
			}
			snapshot, err := engine.Cancel(s.OperationID)
			if err != nil || snapshot.State != "CANCELED" {
				t.Fatal(snapshot, err)
			}
			repeated, err := engine.Cancel(s.OperationID)
			if err != nil || repeated.State != "CANCELED" {
				t.Fatal(repeated, err)
			}
			if state != "RECORDING" {
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("stage context not canceled")
				}
			}
			close(late)
			time.Sleep(10 * time.Millisecond)
			final, _ := engine.Snapshot(s.OperationID)
			if final.State != "CANCELED" {
				t.Fatal(final)
			}
			mu.Lock()
			if lateData != 0 {
				t.Error("late callback emitted after cancellation")
			}
			mu.Unlock()
			trace, _ := engine.Trace(s.OperationID)
			counts := map[string]int{}
			for _, ev := range trace {
				counts[ev.Name]++
			}
			if counts["cancel_requested"] != 1 || counts["cancel_acknowledged"] != 1 {
				t.Fatal(counts)
			}
			if err := engine.Commit(s.OperationID, testVoiceWAV(), ""); err == nil {
				t.Fatal("terminal operation restarted")
			}
		})
	}
}

func TestInteractionStageTimeoutsAndFailures(t *testing.T) {
	for _, stage := range []string{"asr", "llm", "tts", "playback"} {
		for _, failure := range []string{"timeout", "failed"} {
			t.Run(stage+"_"+failure, func(t *testing.T) {
				broken := func(ctx context.Context) error {
					if failure == "failed" {
						return errors.New("PRIVATE provider error with credentials")
					}
					<-ctx.Done()
					return ctx.Err()
				}
				asr := testVoiceASR{}
				tts := testVoiceTTS{}
				chat := testVoiceChat
				switch stage {
				case "asr":
					asr.transcribe = func(ctx context.Context, _ []byte) (string, error) { return "", broken(ctx) }
				case "llm":
					chat = func(ctx context.Context, _ ChatRequest, _ func(string)) (*ChatResponse, error) {
						return nil, broken(ctx)
					}
				case "tts":
					tts.stream = func(ctx context.Context, _ string, _ func([]byte) error) error { return broken(ctx) }
				}
				var engine *InteractionEngine
				engine = NewInteractionEngine(asr, tts, chat, func(event InteractionEvent) {
					if stage == "playback" && failure == "failed" && event.Kind == "audio" {
						engine.Playback(event.OperationID, event.Sequence, "failed")
					}
				}, "")
				defer engine.Close()
				engine.Timeouts = InteractionTimeouts{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
				s := startTestInteraction(t, engine)
				engine.Commit(s.OperationID, testVoiceWAV(), "")
				final := awaitInteraction(t, engine, s.OperationID, "FAILED")
				if final.ErrorCode != stage+"_"+failure {
					t.Fatal(final)
				}
				trace, _ := engine.Trace(s.OperationID)
				encoded, _ := json.Marshal(trace)
				if strings.Contains(string(encoded), "PRIVATE") {
					t.Fatal("provider error escaped trace boundary")
				}
			})
		}
	}
}

func TestInteractionRejectsInvalidOrLateAudioAndIsolatesTurns(t *testing.T) {
	var engine *InteractionEngine
	audio := make(chan InteractionEvent, 4)
	engine = NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			audio <- event
		}
	}, "")
	defer engine.Close()
	old := startTestInteraction(t, engine)
	engine.Commit(old.OperationID, nil, "first replay")
	oldChunk := <-audio
	engine.Cancel(old.OperationID)
	next := startTestInteraction(t, engine)
	engine.Commit(next.OperationID, nil, "second replay")
	nextChunk := <-audio
	if oldChunk.OperationID == nextChunk.OperationID || oldChunk.TraceID == nextChunk.TraceID || oldChunk.TurnID == nextChunk.TurnID {
		t.Fatal("turn identifiers reused")
	}
	if err := engine.Playback(old.OperationID, 1, "started"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Playback(next.OperationID, 999, "started"); err == nil {
		t.Fatal("unknown sequence accepted")
	}
	current, _ := engine.Snapshot(next.OperationID)
	if current.State != "SYNTHESIZING" {
		t.Fatal("old ack advanced new turn", current)
	}
	engine.Playback(next.OperationID, 1, "started")
	engine.Playback(next.OperationID, 1, "stopped")
	awaitInteraction(t, engine, next.OperationID, "COMPLETED")
	trace, _ := engine.Trace(next.OperationID)
	for _, event := range trace {
		if event.Name == "endpoint_commit" && event.Source != "transcript" {
			t.Fatal(event)
		}
	}
	invalid := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{stream: func(_ context.Context, _ string, emit func([]byte) error) error { return emit([]byte("bad audio")) }}, testVoiceChat, nil, "")
	defer invalid.Close()
	s := startTestInteraction(t, invalid)
	invalid.Commit(s.OperationID, nil, "replay")
	failed := awaitInteraction(t, invalid, s.OperationID, "FAILED")
	if failed.ErrorCode != "tts_invalid_audio" {
		t.Fatal(failed)
	}
}

func TestInteractionTraceRetentionAndNoConversationPersistence(t *testing.T) {
	dir := t.TempDir()
	var engine *InteractionEngine
	irodori := &syntheticSpeechProvider{profile: irodoriTestProfile()}
	engine = NewInteractionEngine(testVoiceASR{}, irodori, testVoiceChat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			engine.Playback(event.OperationID, event.Sequence, "started")
			engine.Playback(event.OperationID, event.Sequence, "stopped")
		}
	}, dir)
	defer engine.Close()
	var first string
	// 200 complete synthetic Irodori-contract turns exercise the complete loop and playback acks.
	for i := 0; i < 200; i++ {
		s := startTestInteraction(t, engine)
		if i == 0 {
			first = s.OperationID
		}
		if err := engine.Commit(s.OperationID, testVoiceWAV(), ""); err != nil {
			t.Fatal(err)
		}
		awaitInteraction(t, engine, s.OperationID, "COMPLETED")
		trace, _ := engine.Trace(s.OperationID)
		if !ValidateTrace(trace).Valid {
			t.Fatal("incomplete synthetic trace", i)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, first+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private speech", "private prompt", "private history", "private answer", "UklGR", "\"audio\"", "\"prompt\"", "\"transcript\"", "\"response\""} {
		if strings.Contains(string(data), private) {
			t.Fatalf("persisted conversation content: %s", private)
		}
	}
	for i := 0; i < 76; i++ {
		s := startTestInteraction(t, engine)
		engine.Cancel(s.OperationID)
	}
	engine.mu.Lock()
	count := len(engine.turns)
	engine.mu.Unlock()
	if count > maxInteractionTurns {
		t.Fatal("unbounded in-memory turns", count)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxInteractionTurns {
		t.Fatal("incorrect disk retention", len(entries))
	}
	if _, err := engine.GetRequest(first); err == nil {
		t.Fatal("expired request remains in memory")
	}
	if _, err := engine.Trace("../../outside"); err == nil {
		t.Fatal("trace path traversal accepted")
	}
	oldPath := filepath.Join(dir, "operation_old.json")
	if err := os.WriteFile(oldPath, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(oldPath, old, old)
	notes := filepath.Join(dir, "notes.json")
	os.WriteFile(notes, []byte("keep"), 0600)
	os.Chtimes(notes, old, old)
	if err := engine.store.prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old trace retained", err)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatal("unrelated file removed")
	}
}

func TestInteractionTraceValidationDetectsMissingCriticalFacts(t *testing.T) {
	validation := ValidateTrace([]InteractionTraceEvent{{Name: "user_speech_start"}, {Name: "turn_completed"}})
	if validation.Valid || len(validation.Missing) == 0 {
		t.Fatal(validation)
	}
	if !ValidateTrace([]InteractionTraceEvent{{Name: "user_speech_start"}, {Name: "cancel_requested"}, {Name: "cancel_acknowledged"}}).Valid {
		t.Fatal("complete cancellation trace rejected")
	}
}

func TestInteractionCoalescesLongPlaybackTraceWithoutLosingCriticalFacts(t *testing.T) {
	var engine *InteractionEngine
	tts := testVoiceTTS{stream: func(_ context.Context, _ string, emit func([]byte) error) error {
		for i := 0; i < 64; i++ {
			if err := emit(testVoiceWAV()); err != nil {
				return err
			}
		}
		return nil
	}}
	engine = NewInteractionEngine(testVoiceASR{}, tts, testVoiceChat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			engine.Playback(event.OperationID, event.Sequence, "started")
			engine.Playback(event.OperationID, event.Sequence, "stopped")
		}
	}, t.TempDir())
	defer engine.Close()
	s := startTestInteraction(t, engine)
	engine.Commit(s.OperationID, nil, "replay")
	awaitInteraction(t, engine, s.OperationID, "COMPLETED")
	trace, _ := engine.Trace(s.OperationID)
	if len(trace) > 128 || !ValidateTrace(trace).Valid {
		t.Fatal(len(trace), ValidateTrace(trace))
	}
}

func TestInteractionStorageFailureHasExactlyOneTerminalOutcome(t *testing.T) {
	dir := t.TempDir()
	var engine *InteractionEngine
	var mu sync.Mutex
	terminals := []string{}
	engine = NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			engine.Playback(event.OperationID, event.Sequence, "started")
			engine.Playback(event.OperationID, event.Sequence, "stopped")
		}
		if event.Kind == "state" && interactionTerminal(event.Snapshot.State) {
			mu.Lock()
			terminals = append(terminals, event.Snapshot.State)
			mu.Unlock()
		}
	}, dir)
	defer engine.Close()
	s := startTestInteraction(t, engine)
	// Replacing the owned trace directory with a file makes persistence fail
	// deterministically, including when tests run with elevated permissions.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("storage unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	engine.Commit(s.OperationID, nil, "replay")
	final := awaitInteraction(t, engine, s.OperationID, "FAILED")
	if final.ErrorCode != "trace_storage_failed" {
		t.Fatal(final)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ready := len(terminals) > 0
		mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(terminals) != 1 || terminals[0] != "FAILED" {
		t.Fatal(terminals)
	}
	trace, _ := engine.Trace(s.OperationID)
	for _, event := range trace {
		if event.Name == "turn_completed" {
			t.Fatal("false completion in trace")
		}
	}
}

func TestInteractionCopiesAndBoundsRequestsAndExpiresOnRead(t *testing.T) {
	e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	request := VoiceTurnRequest{InputKind: "transcript", Chat: ChatRequest{Messages: []GatewayMessage{{Role: "user", Content: "original"}}, Tags: []string{"original"}}}
	s, err := e.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Chat.Messages[0].Content = "mutated"
	request.Chat.Tags[0] = "mutated"
	got, err := e.GetRequest(s.OperationID)
	if err != nil || got.Chat.Messages[0].Content != "original" || got.Chat.Tags[0] != "original" {
		t.Fatal(got, err)
	}
	if err := e.Commit(s.OperationID, nil, strings.Repeat("x", 16<<10+1)); err == nil {
		t.Fatal("oversized transcript accepted")
	}
	e.Cancel(s.OperationID)
	trace, _ := e.Trace(s.OperationID)
	if trace[0].Source != "transcript" {
		t.Fatal(trace[0])
	}
	e.mu.Lock()
	e.turns[s.OperationID].created = time.Now().Add(-8 * 24 * time.Hour)
	e.mu.Unlock()
	if _, err := e.GetRequest(s.OperationID); err == nil {
		t.Fatal("expired request returned")
	}
	if _, err := e.Snapshot(s.OperationID); err == nil {
		t.Fatal("expired snapshot returned")
	}
	if _, err := e.Trace(s.OperationID); err == nil {
		t.Fatal("expired trace returned")
	}
	request.Chat.Prompt = strings.Repeat("x", 16<<10+1)
	if _, err := e.Start(request); err == nil {
		t.Fatal("oversized request accepted")
	}
	request.Chat.Prompt = ""
	request.InputKind = "arbitrary"
	if _, err := e.Start(request); err == nil {
		t.Fatal("arbitrary source accepted")
	}
}

func TestInteractionRejectsSymlinkTraceDirectory(t *testing.T) {
	parent := t.TempDir()
	target := t.TempDir()
	path := filepath.Join(parent, "trace-link")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, path)
	defer e.Close()
	if _, err := e.Start(VoiceTurnRequest{}); err == nil {
		t.Fatal("symlink trace store accepted")
	}
}

func TestInteractionRouteIdentityPreservesRequestConfigurationAndIsStable(t *testing.T) {
	var engine *InteractionEngine
	chat := func(ctx context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		reportInteractionModel(ctx, "same-provider", "same/model", "same-route-config")
		token("answer")
		// Repeated route facts must combine with the original request digest,
		// never with the previously combined digest.
		reportInteractionModel(ctx, "same-provider", "same/model", "same-route-config")
		return completedVoiceResponse("answer"), nil
	}
	engine = NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, chat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			engine.Playback(event.OperationID, event.Sequence, "started")
			engine.Playback(event.OperationID, event.Sequence, "stopped")
		}
	}, "")
	defer engine.Close()
	a := NewApp()
	a.interaction = engine
	var configurations []string
	for _, temperature := range []float64{0.2, 0.8, 0.2} {
		start, err := a.StartInteraction(VoiceTurnRequest{SessionID: "same-session", InputKind: "transcript", Chat: ChatRequest{Mode: "fast", Temperature: temperature, MaxTokens: 128}})
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.Commit(start.OperationID, nil, "same input"); err != nil {
			t.Fatal(err)
		}
		awaitInteraction(t, engine, start.OperationID, "COMPLETED")
		trace, err := engine.Trace(start.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		var requested, first, completed string
		for _, event := range trace {
			switch event.Name {
			case "llm_requested":
				requested = event.ConfigurationID
			case "llm_first_token":
				first = event.ConfigurationID
			case "llm_completed":
				completed = event.ConfigurationID
				if event.ModelID != "same/model" {
					t.Fatal(event)
				}
			}
		}
		if first == "" || first != completed || completed == requested || completed == "same-route-config" {
			t.Fatal("request/route configuration was lost or unstable", requested, first, completed)
		}
		configurations = append(configurations, completed)
	}
	if configurations[0] == configurations[1] {
		t.Fatal("different temperatures collapsed into one trace configuration", configurations)
	}
	if configurations[0] != configurations[2] {
		t.Fatal("same request/route configuration is not deterministic", configurations)
	}
}
