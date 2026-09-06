package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real assembler, generation worker, playback ACKs and durable
// store together. The WAV and all conversation strings are synthetic fixtures.
func TestInteractionTraceLimitsAcrossMaximumGenerationRevisions(t *testing.T) {
	for _, ending := range []string{"COMPLETED", "INCOMPLETE", "CANCELED"} {
		t.Run(ending, func(t *testing.T) {
			const revisions, segments, chunks = 8, 3, 64
			var chatCalls, speechCalls atomic.Int32
			var requestMu sync.Mutex
			var requests []ChatRequest
			var late <-chan struct{}
			lastAudio := make(chan struct{}, 1)
			lateAudio := make(chan error, 1)
			lateChat := make(chan struct{}, 1)
			fixture := func(call int) string { return fmt.Sprintf("合成応答%02d。", call) }
			h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
				if text == "次の合成会話です。" {
					return emit(providerTestWAV())
				}
				call := int(speechCalls.Add(1))
				count := 3
				if call%segments == 0 {
					count = 2 // Eight chunks per revision, 64 for the whole operation.
				}
				for i := 0; i < count; i++ {
					if err := emit(providerTestWAV()); err != nil {
						return err
					}
				}
				if ending == "CANCELED" && call == revisions*segments {
					lastAudio <- struct{}{}
					<-ctx.Done()
					<-late
					lateAudio <- emit(providerTestWAV())
				}
				return nil
			}}, func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
				if request.SessionID == "trace-next-session" {
					token("次の合成会話です。")
					return completedVoiceResponse("次の合成会話です。"), nil
				}
				requestMu.Lock()
				requests = append(requests, request)
				requestMu.Unlock()
				call := int(chatCalls.Add(1))
				reportInteractionModel(ctx, "test-provider", "test-model", "test-config")
				text := fixture(call)
				token(text)
				if ending == "CANCELED" && call == revisions*segments {
					<-ctx.Done()
					<-late
					token("破棄される遅延合成文。")
					lateChat <- struct{}{}
					return completedVoiceResponse("破棄される遅延合成文。"), nil
				}
				if ending == "COMPLETED" && call == revisions*segments {
					response := completedVoiceResponse(text)
					response.Generation.CompletionTokens = 64
					response.Generation.CompletionTokensObserved = true
					return response, nil
				}
				return interactionGenerationLength(text+"未確定", 64), nil
			})
			releaseChannel, release := interactionGenerationRelease(t)
			late = releaseChannel
			limits := GenerationLimits{SegmentTokens: 64, MaxSegments: segments, MaxTotalTokens: 192, SoftTargetPercent: 75}
			start := h.start(t, "trace-limit-session", limits)
			var expected strings.Builder
			for revision := 1; revision <= revisions; revision++ {
				if revision > 1 {
					resumed, err := h.engine.Continue(start.OperationID)
					if err != nil {
						t.Fatal(err)
					}
					if resumed.OperationID != start.OperationID || resumed.TurnID != start.TurnID || resumed.SessionID != start.SessionID || resumed.GenerationRevision != revision {
						t.Fatal("resume changed the logical assistant turn")
					}
				}
				state := "INCOMPLETE"
				if revision == revisions {
					state = ending
					if ending == "CANCELED" {
						interactionGenerationReceive(t, lastAudio)
						// Wait for every queued chunk before cancel, so all 64 playback
						// start/stop facts participate in trace coalescing.
						awaitTraceLimitAudio(t, h, start.OperationID, chunks)
						if _, err := h.engine.Cancel(start.OperationID); err != nil {
							t.Fatal(err)
						}
					}
				}
				final := awaitInteraction(t, h.engine, start.OperationID, state)
				awaitTraceLimitTerminal(t, h, start.OperationID, revision, state)
				for segment := 1; segment <= segments; segment++ {
					expected.WriteString(fixture((revision-1)*segments + segment))
				}
				if final.LastAudioSequence != revision*8 || final.ResponsePlan == nil || final.ResponsePlan.Text != expected.String() {
					t.Fatal("audio sequence, committed prefix or pending-tail rollback was lost")
				}
				if final.Generation == nil || final.Generation.SegmentCount != segments || final.Generation.ContinuationCount != segments-1 || final.Generation.Complete != (state == "COMPLETED") {
					t.Fatal("generation limits or completion truth were lost")
				}
				assertTraceLimitPersistence(t, h, start.OperationID, revision, state)
			}
			if chatCalls.Load() != revisions*segments || speechCalls.Load() != revisions*segments {
				t.Fatal("test did not exercise every automatic continuation and speech unit")
			}
			if ending == "INCOMPLETE" {
				if _, err := h.engine.Continue(start.OperationID); err == nil || err.Error() != "generation_resume_limit" {
					t.Fatal("ninth generation revision was accepted")
				}
			}
			requestMu.Lock()
			captured := append([]ChatRequest(nil), requests...)
			requestMu.Unlock()
			for index, request := range captured {
				if len(request.Messages) != 1 || request.Messages[0].Content != "private-history-marker" || request.Prompt != "private-prompt-marker" {
					t.Fatal("continuation changed the original conversation input")
				}
				if index == 0 {
					continue
				}
				messages := request.generationMessages
				if len(messages) != 3 || messages[0].Role != "user" || messages[1].Role != "user" || messages[1].Content != request.Prompt || messages[2].Role != "assistant" {
					t.Fatal("continuation did not reuse one assistant entry and the original user input")
				}
			}
			if ending == "CANCELED" {
				marker := len(h.recorded())
				next := h.start(t, "trace-next-session", limits)
				release()
				if err := interactionGenerationReceive(t, lateAudio); !errors.Is(err, context.Canceled) {
					t.Fatalf("old audio callback escaped its operation fence: %v", err)
				}
				interactionGenerationReceive(t, lateChat)
				final := awaitInteraction(t, h.engine, next.OperationID, "COMPLETED")
				awaitTraceLimitTerminal(t, h, next.OperationID, 1, "COMPLETED")
				if final.ResponsePlan.Text != "次の合成会話です。" || final.LastAudioSequence != 1 {
					t.Fatal("old generation contaminated the next turn")
				}
				for _, event := range h.recorded()[marker:] {
					if event.OperationID == start.OperationID && (event.Kind == "audio" || event.Kind == "output" || event.Kind == "token") {
						t.Fatal("late old content reached the next turn")
					}
				}
				assertTraceLimitPersistence(t, h, start.OperationID, revisions, "CANCELED")
			}
			h.checkPlayback(t)
		})
	}
}

func awaitTraceLimitTerminal(t *testing.T, h *interactionGenerationHarness, operation string, revision int, state string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range h.recorded() {
			if event.Kind == "state" && event.OperationID == operation && event.GenerationRevision == revision && event.Snapshot.State == state {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("revision terminal was not dispatched")
}

func awaitTraceLimitAudio(t *testing.T, h *interactionGenerationHarness, operation string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stopped := 0
		for _, event := range h.recorded() {
			if event.OperationID == operation && event.Kind == "trace" && event.Trace.Name == "audio_play_stopped" {
				stopped++
			}
		}
		if stopped == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("maximum playback ACKs were not dispatched")
}

func assertTraceLimitPersistence(t *testing.T, h *interactionGenerationHarness, operation string, revision int, state string) {
	t.Helper()
	trace, err := h.engine.Trace(operation)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) > 128 || !ValidateTrace(trace).Valid {
		t.Fatalf("trace exceeded its bound or lost critical facts: events=%d validation=%+v", len(trace), ValidateTrace(trace))
	}
	critical := func(events []InteractionTraceEvent) []InteractionTraceEvent {
		var result []InteractionTraceEvent
		for _, event := range events {
			if event.Name != "audio_play_started" && event.Name != "audio_play_stopped" {
				result = append(result, event)
			}
		}
		return result
	}
	var published []InteractionTraceEvent
	audioCount, completed := 0, 0
	for _, event := range h.recorded() {
		if event.OperationID != operation {
			continue
		}
		if event.Kind == "trace" {
			published = append(published, *event.Trace)
			if event.Trace.Name == "turn_completed" {
				completed++
			}
		}
		if event.Kind == "audio" {
			audioCount++
			if event.Sequence != audioCount || event.GenerationRevision != (audioCount-1)/8+1 {
				t.Fatal("audio sequence or revision was reused")
			}
		}
	}
	if !reflect.DeepEqual(critical(trace), critical(published)) {
		t.Fatal("coalescing removed or rewrote a critical event")
	}
	playbackEndpoints := func(events []InteractionTraceEvent) [2]string {
		var endpoints [2]string
		for _, event := range events {
			if event.Name == "audio_play_started" && endpoints[0] == "" {
				endpoints[0] = event.EventID
			}
			if event.Name == "audio_play_stopped" {
				endpoints[1] = event.EventID
			}
		}
		return endpoints
	}
	if playbackEndpoints(trace) != playbackEndpoints(published) {
		t.Fatal("coalescing changed the first or last playback timing fact")
	}
	expectedCompleted := 0
	if state == "COMPLETED" {
		expectedCompleted = 1
	}
	if audioCount != revision*8 || completed != expectedCompleted {
		t.Fatal("audio coverage or single assistant completion invariant failed")
	}
	if revision == 8 && (len(published) <= 128 || len(trace) != 128) {
		t.Fatalf("coalescing was not exercised at its limit: published=%d stored=%d", len(published), len(trace))
	}
	for rev := 1; rev <= revision; rev++ {
		names := map[string]int{}
		for _, event := range trace {
			if event.GenerationRevision == rev {
				names[event.Name]++
			}
		}
		for _, name := range []string{"llm_requested", "llm_first_token", "tts_requested"} {
			if names[name] != 1 {
				t.Fatalf("revision %d lost %s", rev, name)
			}
		}
		terminal := "turn_incomplete"
		if rev == revision {
			terminal = map[string]string{"COMPLETED": "turn_completed", "INCOMPLETE": "turn_incomplete", "CANCELED": "cancel_acknowledged"}[state]
		}
		if names[terminal] != 1 {
			t.Fatalf("revision %d lost its terminal event", rev)
		}
	}
	file := filepath.Join(h.store, operation+".json")
	data, err := os.ReadFile(file)
	if err != nil || len(data) > 256<<10 {
		t.Fatalf("durable trace exceeds the reader bound or was not saved: bytes=%d err=%v", len(data), err)
	}
	for _, secret := range []string{"private-prompt-marker", "private-history-marker", "合成応答", "未確定", base64.StdEncoding.EncodeToString(providerTestWAV())} {
		if strings.Contains(string(data), secret) {
			t.Fatal("content entered the durable trace")
		}
	}
	// A fresh Engine has no in-memory turn, so Trace must use the real store reader.
	reopened := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, h.store)
	defer reopened.Close()
	reloaded, err := reopened.Trace(operation)
	if err != nil || !reflect.DeepEqual(trace, reloaded) {
		t.Fatal("durable trace did not round-trip through a fresh engine", err)
	}
	if revision == 8 {
		t.Logf("terminal=%s revisions=%d audio=%d published=%d retained=%d critical=%d stored_bytes=%d", state, revision, audioCount, len(published), len(trace), len(critical(trace)), len(data))
	}
}
