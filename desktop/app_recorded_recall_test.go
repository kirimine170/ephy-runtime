package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"desktop/recording"
)

type scriptedRecaller struct {
	responses [][]recording.RecallSource
	errOn     int
	calls     int
}

func (s *scriptedRecaller) Recall(_ context.Context, query string, _ int) ([]recording.RecallSource, error) {
	if query != "rehearsal" {
		return nil, errors.New("wrong literal search term")
	}
	s.calls++
	if s.calls == s.errOn {
		return nil, errors.New("permission_blocked")
	}
	return s.responses[s.calls-1], nil
}

func testRecordedRecallSource() recording.RecallSource {
	return recording.RecallSource{
		Target:         recording.Target{DocID: "synthetic-doc", Revision: 2, SHA256: strings.Repeat("a", 64)},
		Event:          recording.EventRef{EventID: "synthetic-event", EventRevision: 1},
		Text:           "The rehearsal starts at 10:00.",
		ConversationID: "synthetic-conversation",
		TurnID:         "synthetic-turn",
	}
}

func TestFixedRecordedRecallCitationAndOutputAuthorization(t *testing.T) {
	source := testRecordedRecallSource()
	for _, test := range []struct {
		name       string
		store      scriptedRecaller
		wantOutput bool
	}{
		{"authorized", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {source}}}, true},
		{"revoked_before_output", scriptedRecaller{responses: [][]recording.RecallSource{{source}}, errOn: 2}, false},
		{"changed_before_output", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := ""
			var events []ChatStreamEvent
			ctx := context.WithValue(context.Background(), interactionChatEventKey{}, func(event ChatStreamEvent) {
				if output != "" {
					t.Error("sources arrived after answer exposure")
				}
				events = append(events, event)
			})
			request := ChatRequest{RequestID: "synthetic-request", Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}
			response, err := fixedRecordedRecall(ctx, &test.store, request, func(text string) { output += text })
			if test.store.calls != 2 {
				t.Fatalf("expected authorization checks before generation and output: %d", test.store.calls)
			}
			if !test.wantOutput {
				if err == nil || response != nil || output != "" || len(events) != 0 {
					t.Fatalf("revoked or changed record disclosed: %v, %+v, %q, %+v", err, response, output, events)
				}
				return
			}
			if err != nil || response == nil || output != "The rehearsal starts at 10:00. [R1]" || len(response.Sources) != 1 || response.Sources[0].ChunkID != "karte-v2:synthetic-doc:2:synthetic-event" {
				t.Fatalf("missing current citation: %v, %+v, %q", err, response, output)
			}
			if len(events) != 1 || events[0].Kind != "sources" || events[0].RequestID != request.RequestID || !reflect.DeepEqual(events[0].Sources, response.Sources) {
				t.Fatalf("interaction lost authorized sources: %+v", events)
			}
			citation := response.Sources[0].KarteRecordV2
			if citation == nil || !reflect.DeepEqual(citation.Target, source.Target) || !reflect.DeepEqual(citation.Event, source.Event) || citation.ConversationID != source.ConversationID || citation.TurnID != source.TurnID || response.Sources[0].SourceType != "karte_record_v2" || response.Sources[0].TrustLevel != "local_untrusted" {
				t.Fatalf("incomplete v2 citation binding: %+v", citation)
			}
			encoded, encodeErr := json.Marshal(response.Sources[0])
			var transported SearchItem
			if encodeErr != nil || json.Unmarshal(encoded, &transported) != nil || transported.KarteRecordV2 == nil || transported.KarteRecordV2.Target.SHA256 != citation.Target.SHA256 || transported.KarteRecordV2.Event.EventRevision != citation.Event.EventRevision {
				t.Fatalf("v2 citation lost across response JSON: %v, %s", encodeErr, encoded)
			}
		})
	}
}

func TestFixedRecordedRecallCompletesThroughGenerationAssembler(t *testing.T) {
	source := testRecordedRecallSource()
	for _, test := range []struct {
		name    string
		sources []recording.RecallSource
		answer  string
	}{
		{"matched", []recording.RecallSource{source}, "The rehearsal starts at 10:00. [R1]"},
		{"no_match", []recording.RecallSource{}, "No authorized saved conversation matched."},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedRecaller{responses: [][]recording.RecallSource{test.sources, test.sources}}
			request := ChatRequest{RequestID: "synthetic-request", Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}
			var events []ChatStreamEvent
			ctx := context.WithValue(context.Background(), interactionChatEventKey{}, func(event ChatStreamEvent) {
				events = append(events, event)
			})
			var committed string
			response, err := assembleGeneration(ctx, request, GenerationLimits{}, "", func(ctx context.Context, req ChatRequest, token func(string)) (*ChatResponse, error) {
				return fixedRecordedRecall(ctx, &store, req, token)
			}, func(progress GenerationProgress) {
				if len(events) != 1 || events[0].Kind != "sources" || len(events[0].Sources) != len(test.sources) {
					t.Error("answer/speech exposed before its authorized sources")
				}
				committed = progress.CommittedText
			})
			if err != nil || response == nil || response.Answer != test.answer || committed != test.answer || response.FinishReason != "stop" || store.calls != 2 {
				t.Fatalf("fixed answer failed real assembly: %v, %+v, committed=%q, calls=%d", err, response, committed, store.calls)
			}
			generation := response.Generation
			if generation == nil || !generation.Complete || !generation.TerminalSSE || !generation.DoneReceived || generation.FinishReason != "stop" || generation.SegmentCount != 1 || generation.ContinuationCount != 0 {
				t.Fatalf("local terminal framing was lost: %+v", generation)
			}
			if _, err := time.Parse(time.RFC3339Nano, generation.TerminalSSEAt); err != nil || generation.CompletionTokensObserved || generation.ReasoningTokens != nil || generation.ReasoningTokenSource != "unavailable" {
				t.Fatalf("local answer claimed provider usage or omitted terminal time: %+v", generation)
			}
			if len(events) != 1 || events[0].RequestID != request.RequestID || !reflect.DeepEqual(events[0].Sources, response.Sources) {
				t.Fatalf("assembler lost source bindings: %+v, %+v", events, response.Sources)
			}
		})
	}
}

func TestFixedRecordedRecallDeniedOrCanceledHasNoGenerationOutput(t *testing.T) {
	source := testRecordedRecallSource()
	for _, test := range []struct {
		name     string
		store    scriptedRecaller
		canceled bool
	}{
		{"denied", scriptedRecaller{errOn: 1}, false},
		{"revoked", scriptedRecaller{responses: [][]recording.RecallSource{{source}}, errOn: 2}, false},
		{"changed", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {}}}, false},
		{"canceled", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {source}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			events, progress := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, interactionChatEventKey{}, func(ChatStreamEvent) { events++ })
			request := ChatRequest{RequestID: "synthetic-request", Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}
			response, err := assembleGeneration(ctx, request, GenerationLimits{}, "", func(ctx context.Context, req ChatRequest, token func(string)) (*ChatResponse, error) {
				if test.canceled {
					cancel()
				}
				return fixedRecordedRecall(ctx, &test.store, req, token)
			}, func(GenerationProgress) { progress++ })
			if err == nil || response == nil || response.Answer != "" || len(response.Sources) != 0 || events != 0 || progress != 0 || response.Generation.Complete {
				t.Fatalf("unauthorized/canceled generation disclosed output: %v, %+v, sources=%d, progress=%d", err, response, events, progress)
			}
		})
	}
}

func TestFixedRecordedRecallInteractionCompletesWithCitations(t *testing.T) {
	for _, inputKind := range []string{"text", "transcript", "microphone"} {
		for _, denied := range []bool{false, true} {
			name := inputKind + "/authorized"
			if denied {
				name = inputKind + "/revoked"
			}
			t.Run(name, func(t *testing.T) {
				source := testRecordedRecallSource()
				store := scriptedRecaller{responses: [][]recording.RecallSource{{source}, {source}}}
				if denied {
					store.errOn = 2
				}
				sourcesDelivered := make(chan struct{}, 1)
				speechGate, releaseSpeech := interactionGenerationRelease(t)
				defer releaseSpeech()
				h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, _ string, emit func([]byte) error) error {
					select {
					case <-speechGate:
						return emit(testVoiceWAV())
					case <-ctx.Done():
						return ctx.Err()
					}
				}}, func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
					if request.SourceScope != "recorded_conversation" {
						return nil, errors.New("unexpected_source_scope")
					}
					return fixedRecordedRecall(ctx, &store, request, token)
				})
				// Install before Start queues any events. Preserve the harness's
				// recording/playback callback, and observe actual source delivery
				// before allowing the speech stub to finish the operation.
				emit := h.engine.emit
				h.engine.emit = func(event InteractionEvent) {
					emit(event)
					if event.Kind == "chat" && event.Chat != nil && event.Chat.Kind == "sources" {
						select {
						case sourcesDelivered <- struct{}{}:
						default:
						}
					}
				}
				request := VoiceTurnRequest{SessionID: "synthetic-session", InputKind: inputKind,
					Chat: ChatRequest{Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}}
				h.engine.asr = testVoiceASR{transcribe: func(context.Context, []byte) (string, error) {
					return request.Chat.Prompt, nil
				}}
				start, err := h.engine.Start(request)
				if err != nil {
					t.Fatal(err)
				}
				var audio []byte
				transcript := request.Chat.Prompt
				if inputKind == "microphone" {
					audio, transcript = testVoiceWAV(), ""
				}
				if err := h.engine.Commit(start.OperationID, audio, transcript); err != nil {
					t.Fatal(err)
				}
				if !denied {
					select {
					case <-sourcesDelivered:
						releaseSpeech()
					case <-time.After(time.Second):
						t.Fatal("interaction did not deliver sources before speech completion")
					}
				}
				state := "COMPLETED"
				if denied {
					state = "FAILED"
				}
				done := awaitInteraction(t, h.engine, start.OperationID, state)
				h.awaitDeliveredTerminal(t, start.OperationID, state)
				h.checkPlayback(t)
				sourceEvents, outputEvents, audioEvents := 0, 0, 0
				for _, event := range h.recorded() {
					if event.Kind == "chat" && event.Chat != nil && event.Chat.Kind == "sources" {
						sourceEvents++
						if event.Chat.RequestID != start.OperationID || len(event.Chat.Sources) != 1 {
							t.Fatalf("interaction source event lost identity: %+v", event)
						}
						item := event.Chat.Sources[0]
						citation := item.KarteRecordV2
						if citation == nil || !reflect.DeepEqual(citation.Target, source.Target) || !reflect.DeepEqual(citation.Event, source.Event) || citation.ConversationID != source.ConversationID || citation.TurnID != source.TurnID || item.SourceType != "karte_record_v2" || item.TrustLevel != "local_untrusted" {
							t.Fatalf("interaction source event lost v2 binding: %+v", item)
						}
					}
					if event.Kind == "output" || event.Kind == "audio" {
						if sourceEvents != 1 {
							t.Fatal("interaction displayed/spoke answer before citation event")
						}
						if event.Kind == "output" {
							outputEvents++
						} else {
							audioEvents++
						}
					}
				}
				if store.calls != 2 {
					t.Fatalf("interaction did not reauthorize exactly once: %d", store.calls)
				}
				if denied {
					if sourceEvents != 0 || outputEvents != 0 || audioEvents != 0 || (done.ResponsePlan != nil && done.ResponsePlan.Text != "") {
						t.Fatalf("revoked interaction disclosed saved content: %+v", done)
					}
					return
				}
				if done.ResponsePlan == nil || done.ResponsePlan.Text != "The rehearsal starts at 10:00. [R1]" || done.Generation == nil || !done.Generation.Complete || done.Generation.FinishReason != "stop" || done.ErrorCode != "" || outputEvents == 0 || audioEvents == 0 {
					t.Fatalf("recall interaction did not complete after playback: %+v", done)
				}
				if sourceEvents != 1 {
					t.Fatal("interaction did not deliver citations to the UI")
				}
			})
		}
	}
}
