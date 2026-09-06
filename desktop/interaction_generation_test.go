package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func interactionGenerationLength(text string, tokens int) *ChatResponse {
	return &ChatResponse{Answer: text, FinishReason: "length", Generation: &GenerationMetadata{
		SchemaVersion: 2, FinishReason: "length", ProviderFinishReason: "length",
		TerminalSSE: true, DoneReceived: true, CompletionTokens: tokens, CompletionTokensObserved: true,
	}}
}

type interactionGenerationHarness struct {
	engine *InteractionEngine
	mu     sync.Mutex
	events []InteractionEvent
	errors []error
	store  string
}

func TestInteractionGenerationTransportFailureKeepsSpecificFixedCode(t *testing.T) {
	for _, reason := range []string{"transport_eof", "timeout"} {
		t.Run(reason, func(t *testing.T) {
			h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				return &ChatResponse{FinishReason: reason, Generation: &GenerationMetadata{SchemaVersion: 2, FinishReason: reason, ProviderFinishReason: "unknown"}}, errors.New("private transport body must not leave adapter")
			})
			s := h.start(t, "transport-failure", GenerationLimits{})
			failed := awaitInteraction(t, h.engine, s.OperationID, "FAILED")
			if failed.ErrorCode != "llm_"+reason || failed.Generation.FinishReason != reason || failed.Generation.Complete {
				t.Fatal("transport failure lost fixed terminal code")
			}
		})
	}
}

func newInteractionGenerationHarness(t *testing.T, tts testVoiceTTS, chat func(context.Context, ChatRequest, func(string)) (*ChatResponse, error)) *interactionGenerationHarness {
	t.Helper()
	h := &interactionGenerationHarness{store: t.TempDir()}
	h.engine = NewInteractionEngine(testVoiceASR{}, tts, chat, func(event InteractionEvent) {
		h.mu.Lock()
		h.events = append(h.events, event)
		h.mu.Unlock()
		if event.Kind == "audio" {
			for _, phase := range []string{"started", "stopped"} {
				if err := h.engine.Playback(event.OperationID, event.Sequence, phase); err != nil {
					h.mu.Lock()
					h.errors = append(h.errors, err)
					h.mu.Unlock()
				}
			}
		}
	}, h.store)
	h.engine.Timeouts.LLM = 2 * time.Second
	h.engine.Timeouts.TTS = time.Second
	t.Cleanup(h.engine.Close)
	return h
}

func (h *interactionGenerationHarness) start(t *testing.T, session string, limits GenerationLimits) InteractionSnapshot {
	t.Helper()
	snapshot, err := h.engine.Start(VoiceTurnRequest{SessionID: session, InputKind: "transcript", GenerationLimits: limits,
		Chat: ChatRequest{Mode: "fast", MaxTokens: 64, ConfigurationID: "original-config", Messages: []GatewayMessage{{Role: "user", Content: "private-history-marker"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.engine.Commit(snapshot.OperationID, nil, "private-prompt-marker"); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (h *interactionGenerationHarness) recorded() []InteractionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]InteractionEvent(nil), h.events...)
}

func (h *interactionGenerationHarness) checkPlayback(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.errors) != 0 {
		t.Fatal(h.errors)
	}
}

func (h *interactionGenerationHarness) awaitDeliveredTerminal(t *testing.T, operation, state string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, event := range h.recorded() {
			if event.Kind == "state" && event.OperationID == operation && event.Snapshot.State == state {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal state was not dispatched")
}

func interactionGenerationReceive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("generation synchronization timed out")
		var zero T
		return zero
	}
}

func interactionGenerationRelease(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	channel := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(channel) }) }
	t.Cleanup(release)
	return channel, release
}

func TestInteractionGenerationPlaysConfirmedSentenceDuringContinuationWithoutCompleting(t *testing.T) {
	entered := make(chan struct{}, 1)
	speech := make(chan string, 8)
	var count atomic.Int32
	var releaseChannel chan struct{}
	chat := func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
		if count.Add(1) == 1 {
			token("春には花が咲きます。桃")
			return interactionGenerationLength("春には花が咲きます。桃", 64), nil
		}
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseChannel:
		}
		token("夏は暑く，秋には紅葉し，冬には雪が降ります。")
		return completedVoiceResponse("夏は暑く，秋には紅葉し，冬には雪が降ります。"), nil
	}
	h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(_ context.Context, text string, emit func([]byte) error) error {
		speech <- text
		return emit(testVoiceWAV())
	}}, chat)
	var release func()
	releaseChannel, release = interactionGenerationRelease(t)
	start := h.start(t, "generation-session", GenerationLimits{})
	interactionGenerationReceive(t, entered)
	if text := interactionGenerationReceive(t, speech); text != "春には花が咲きます。" {
		t.Fatalf("unconfirmed fragment reached speech adapter: %q", text)
	}
	playing := awaitInteraction(t, h.engine, start.OperationID, "PLAYING")
	if playing.ResponsePlan == nil || strings.Contains(playing.ResponsePlan.Text, "桃") {
		t.Fatal("unconfirmed text entered the response plan")
	}
	trace, err := h.engine.Trace(start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range trace {
		if event.Name == "llm_completed" || event.Name == "turn_completed" {
			t.Fatal("completion emitted while continuation was pending")
		}
	}
	release()
	done := awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
	if done.Generation == nil || !done.Generation.Complete || done.Generation.SegmentCount != 2 || done.Generation.ContinuationCount != 1 {
		t.Fatalf("unexpected generation terminal: %+v", done.Generation)
	}
	for _, season := range []string{"春", "夏", "秋", "冬"} {
		if !strings.Contains(done.ResponsePlan.Text, season) {
			t.Fatalf("missing season %s", season)
		}
	}
	if strings.Contains(done.ResponsePlan.Text, "桃") {
		t.Fatal("unfinished word survived the assembled terminal")
	}
	h.checkPlayback(t)
}

func TestInteractionGenerationCancelContinuationAndSessionSwitchDropOldTextAndAudio(t *testing.T) {
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 2)
	lateAudio := make(chan error, 1)
	var oldCalls atomic.Int32
	var late chan struct{}
	h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
		if err := emit(testVoiceWAV()); err != nil {
			return err
		}
		if strings.Contains(text, "旧世代") {
			<-ctx.Done()
			<-late
			lateAudio <- emit(testVoiceWAV())
		}
		return nil
	}}, func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
		if request.SessionID == "next-session" {
			token("新しい会話です。")
			return completedVoiceResponse("新しい会話です。"), nil
		}
		if oldCalls.Add(1) == 1 {
			token("旧世代の確定文です。桃")
			return interactionGenerationLength("旧世代の確定文です。桃", 64), nil
		}
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-late
		token("旧世代の遅延回答です。")
		return completedVoiceResponse("旧世代の遅延回答です。"), nil
	})
	var release func()
	late, release = interactionGenerationRelease(t)
	start := h.start(t, "old-session", GenerationLimits{})
	interactionGenerationReceive(t, entered)
	awaitInteraction(t, h.engine, start.OperationID, "PLAYING")
	if final, err := h.engine.Cancel(start.OperationID); err != nil || final.State != "CANCELED" {
		t.Fatal(final, err)
	}
	interactionGenerationReceive(t, canceled)
	h.awaitDeliveredTerminal(t, start.OperationID, "CANCELED")
	marker := len(h.recorded())
	next := h.start(t, "next-session", GenerationLimits{})
	release()
	if err := interactionGenerationReceive(t, lateAudio); !errors.Is(err, context.Canceled) {
		t.Fatalf("old audio callback was accepted: %v", err)
	}
	done := awaitInteraction(t, h.engine, next.OperationID, "COMPLETED")
	if done.ResponsePlan.Text != "新しい会話です。" {
		t.Fatal("old content entered new conversation")
	}
	for _, event := range h.recorded()[marker:] {
		if event.OperationID == start.OperationID && (event.Kind == "audio" || event.Kind == "output" || event.Kind == "token") {
			t.Fatalf("late old generation event delivered: %s", event.Kind)
		}
	}
	h.checkPlayback(t)
}

func TestInteractionGenerationIncompleteResumesSameTurnWithRevisionAndMonotonicAudio(t *testing.T) {
	var count atomic.Int32
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		reportInteractionModel(ctx, "provider", "model", "configuration")
		if count.Add(1) == 1 {
			token("春は花の季節です。桃")
			return interactionGenerationLength("春は花の季節です。桃", 64), nil
		}
		token("夏は暑く，秋は紅葉，冬は雪の季節です。")
		return completedVoiceResponse("夏は暑く，秋は紅葉，冬は雪の季節です。"), nil
	})
	limits := GenerationLimits{SegmentTokens: 64, MaxSegments: 1, MaxTotalTokens: 64, SoftTargetPercent: 75}
	start := h.start(t, "resume-session", limits)
	first := awaitInteraction(t, h.engine, start.OperationID, "INCOMPLETE")
	if first.Generation.Complete || first.Generation.FinishReason != "length" || first.LastAudioSequence != 1 {
		t.Fatal(first)
	}
	resumed, err := h.engine.Continue(start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.OperationID != start.OperationID || resumed.TurnID != start.TurnID || resumed.SessionID != start.SessionID || resumed.GenerationRevision != 2 || resumed.LastAudioSequence != 1 {
		t.Fatal("continuation changed logical identity or audio base")
	}
	done := awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
	if done.LastAudioSequence != 2 || !strings.HasPrefix(done.ResponsePlan.Text, first.ResponsePlan.Text) {
		t.Fatal("resumed answer lost its committed prefix or audio sequence")
	}
	request, err := h.engine.GetRequest(start.OperationID)
	if err != nil || len(request.Chat.Messages) != 1 || request.Chat.Messages[0].Content != "private-history-marker" || request.Chat.Prompt != "private-prompt-marker" {
		t.Fatal("continuation mutated stored history or added a synthetic user turn")
	}
	trace, err := h.engine.Trace(start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	configurations := map[string]bool{}
	for _, event := range trace {
		if event.Name == "turn_completed" {
			completed++
		}
		if event.Name == "llm_first_token" {
			configurations[event.ConfigurationID] = true
		}
	}
	if completed != 1 || len(configurations) != 1 {
		t.Fatalf("completion count=%d configuration identities=%d", completed, len(configurations))
	}
	audio := []InteractionEvent{}
	for _, event := range h.recorded() {
		if event.Kind == "audio" {
			audio = append(audio, event)
		}
	}
	if len(audio) != 2 || audio[0].Sequence != 1 || audio[0].GenerationRevision != 1 || audio[1].Sequence != 2 || audio[1].GenerationRevision != 2 {
		t.Fatal("audio sequence or generation revision was reused")
	}
	h.checkPlayback(t)
}

func TestInteractionGenerationResumedTTSWithoutNewAudioFailsInsteadOfHanging(t *testing.T) {
	var calls, spoken atomic.Int32
	h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(_ context.Context, _ string, emit func([]byte) error) error {
		if spoken.Add(1) == 1 {
			return emit(testVoiceWAV())
		}
		return nil
	}}, func(_ context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		if calls.Add(1) == 1 {
			token("春です。桃")
			return interactionGenerationLength("春です。桃", 64), nil
		}
		token("夏です。")
		return completedVoiceResponse("夏です。"), nil
	})
	start := h.start(t, "empty-audio-session", GenerationLimits{SegmentTokens: 64, MaxSegments: 1, MaxTotalTokens: 64})
	awaitInteraction(t, h.engine, start.OperationID, "INCOMPLETE")
	if _, err := h.engine.Continue(start.OperationID); err != nil {
		t.Fatal(err)
	}
	failed := awaitInteraction(t, h.engine, start.OperationID, "FAILED")
	if failed.ErrorCode != "tts_empty_audio" || failed.GenerationRevision != 2 || failed.LastAudioSequence != 1 {
		t.Fatal(failed)
	}
}

func TestInteractionGenerationTTSFailureCancelsUpstreamImmediately(t *testing.T) {
	canceled := make(chan struct{}, 1)
	h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(context.Context, string, func([]byte) error) error {
		return errors.New("tts_unavailable")
	}}, func(ctx context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		token("最初の確定文です。")
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	})
	start := h.start(t, "tts-failure-session", GenerationLimits{})
	interactionGenerationReceive(t, canceled)
	failed := awaitInteraction(t, h.engine, start.OperationID, "FAILED")
	if failed.ErrorCode != "tts_unavailable" {
		t.Fatal(failed)
	}
}

func TestInteractionGenerationTTSBudgetDoesNotIncludeIdleContinuationWait(t *testing.T) {
	var calls atomic.Int32
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		if calls.Add(1) == 1 {
			token("春です。桃")
			return interactionGenerationLength("春です。桃", 64), nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		token("夏です。")
		return completedVoiceResponse("夏です。"), nil
	})
	h.engine.Timeouts.TTS = 80 * time.Millisecond
	start := h.start(t, "idle-session", GenerationLimits{})
	done := awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
	if !done.Generation.Complete || done.Generation.ContinuationCount != 1 {
		t.Fatal(done.Generation)
	}
	h.checkPlayback(t)
}

func TestInteractionGenerationTraceStoresTerminalFactsWithoutContent(t *testing.T) {
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(_ context.Context, _ ChatRequest, token func(string)) (*ChatResponse, error) {
		text := "private-answer-marker。"
		token(text)
		response := completedVoiceResponse(text)
		response.Thinking = "private-reasoning-marker"
		response.Generation.CompletionTokens = 12
		response.Generation.CompletionTokensObserved = true
		reasoning := 0
		response.Generation.ReasoningTokens = &reasoning
		return response, nil
	})
	start := h.start(t, "privacy-session", GenerationLimits{})
	awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
	trace, err := h.engine.Trace(start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-prompt-marker", "private-history-marker", "private-answer-marker", "private-reasoning-marker", base64.StdEncoding.EncodeToString(testVoiceWAV())} {
		if strings.Contains(string(data), secret) {
			t.Fatal("content entered in-memory trace")
		}
	}
	if err := filepath.WalkDir(h.store, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		stored, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(stored), "private-") || strings.Contains(string(stored), base64.StdEncoding.EncodeToString(testVoiceWAV())) {
			t.Fatal("content entered durable trace")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var terminal *GenerationMetadata
	for _, event := range trace {
		if event.Name == "turn_completed" {
			if event.SchemaVersion != 3 || event.GenerationRevision != 1 {
				t.Fatal("terminal trace lost schema or revision")
			}
			terminal = event.Generation
		}
	}
	if terminal == nil || terminal.FinishReason != "stop" || terminal.CompletionTokens != 12 || !terminal.TerminalSSE || !terminal.DoneReceived {
		t.Fatal("terminal facts were lost")
	}
}
