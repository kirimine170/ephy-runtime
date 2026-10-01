package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func testRecordedRecallLiteralCases() []struct {
	name  string
	texts []string
} {
	return []struct {
		name  string
		texts []string
	}{
		{"plain", []string{"The rehearsal starts at 10:00."}},
		{"parenthesis", []string{"Remember (Tuesday."}},
		{"bracket", []string{"Remember [Tuesday."}},
		{"backtick", []string{"Remember `Tuesday."}},
		{"code_fence", []string{"```text\nRemember Tuesday."}},
		{"backslash", []string{"Remember Tuesday\\"}},
		{"japanese_quote", []string{"Remember 「Tuesday."}},
		{"multiple_multiline", []string{"First line\nRemember (Tuesday.", "Remember [Wednesday.", "```text\nRemember `Thursday."}},
	}
}

func testRecordedRecallSources(texts []string) ([]recording.RecallSource, string) {
	sources := make([]recording.RecallSource, 0, len(texts))
	lines := make([]string, 0, len(texts))
	for index, text := range texts {
		source := testRecordedRecallSource()
		source.Text = text
		source.Event.EventID = fmt.Sprintf("synthetic-event-%d", index+1)
		sources = append(sources, source)
		lines = append(lines, fmt.Sprintf("%s [R%d]", text, index+1))
	}
	return sources, strings.Join(lines, "\n")
}

func TestFixedRecordedRecallPreservesMaterializedText(t *testing.T) {
	for _, test := range testRecordedRecallLiteralCases() {
		t.Run(test.name, func(t *testing.T) {
			sources, answer := testRecordedRecallSources(test.texts)
			store := scriptedRecaller{responses: [][]recording.RecallSource{sources, sources}}
			request := ChatRequest{RequestID: "synthetic-request", Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}
			var events []ChatStreamEvent
			ctx := context.WithValue(context.Background(), interactionChatEventKey{}, func(event ChatStreamEvent) {
				events = append(events, event)
			})
			calls, outputs := 0, 0
			response, err := generateRecordedRecall(ctx, request, "", func(ctx context.Context, req ChatRequest, token func(string)) (*ChatResponse, error) {
				calls++
				return fixedRecordedRecall(ctx, &store, req, token)
			}, func(progress GenerationProgress) {
				outputs++
				if len(events) != 1 || events[0].Kind != "sources" || len(events[0].Sources) != len(sources) {
					t.Error("materialized output preceded authorized sources")
				}
				if progress.CommittedText != answer || !reflect.DeepEqual(progress.SpeechUnits, []string{answer}) || !progress.Metadata.Complete {
					t.Errorf("saved text was parsed as Markdown: %+v", progress)
				}
			})
			if err != nil || response == nil || response.Answer != answer || calls != 1 || store.calls != 2 || outputs != 1 {
				t.Fatalf("fixed text was repaired, repeated, or truncated: %v, %+v, calls=%d, recalls=%d, outputs=%d", err, response, calls, store.calls, outputs)
			}
			if response.Generation == nil || !response.Generation.Complete || response.Generation.SegmentCount != 1 || response.Generation.ContinuationCount != 0 || response.FinishReason != "stop" {
				t.Fatalf("materialized answer was not one complete segment: %+v", response.Generation)
			}
			if len(response.Sources) != len(sources) || !reflect.DeepEqual(events[0].Sources, response.Sources) {
				t.Fatal("materialized answer lost sources")
			}
			for index, source := range sources {
				item := response.Sources[index]
				citation := item.KarteRecordV2
				if item.ChunkText != source.Text || item.Snippet != source.Text || item.SourceID != fmt.Sprintf("R%d", index+1) || citation == nil || !reflect.DeepEqual(citation.Target, source.Target) || !reflect.DeepEqual(citation.Event, source.Event) || citation.ConversationID != source.ConversationID || citation.TurnID != source.TurnID {
					t.Fatalf("materialized source bytes/binding changed: %+v", item)
				}
			}
		})
	}
}

func TestFixedRecordedRecallRejectsContinuationOrOtherScope(t *testing.T) {
	for _, test := range []struct{ scope, prefix string }{
		{"recorded_conversation", "already committed"},
		{"personal_context", ""},
		{"", ""},
	} {
		calls, outputs := 0, 0
		response, err := generateRecordedRecall(context.Background(), ChatRequest{SourceScope: test.scope}, test.prefix, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
			calls++
			return completedVoiceResponse("unexpected"), nil
		}, func(GenerationProgress) { outputs++ })
		if err == nil || response != nil || calls != 0 || outputs != 0 {
			t.Fatalf("materialized boundary accepted other scope/continuation: %+v, %v", test, err)
		}
	}
}

func TestMaterializedRecordedRecallDenialAndCancellationExposeNothing(t *testing.T) {
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
			events, outputs := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, interactionChatEventKey{}, func(ChatStreamEvent) { events++ })
			request := ChatRequest{Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}
			response, err := generateRecordedRecall(ctx, request, "", func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
				if test.canceled {
					cancel()
				}
				return fixedRecordedRecall(ctx, &test.store, request, token)
			}, func(GenerationProgress) { outputs++ })
			if err == nil || response != nil || events != 0 || outputs != 0 {
				t.Fatalf("materialized denial/cancellation disclosed output: %v, %+v, events=%d, outputs=%d", err, response, events, outputs)
			}
		})
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
		for _, literal := range testRecordedRecallLiteralCases() {
			for _, denied := range []bool{false, true} {
				name := inputKind + "/" + literal.name + "/authorized"
				if denied {
					name = inputKind + "/" + literal.name + "/revoked"
				}
				t.Run(name, func(t *testing.T) {
					sources, answer := testRecordedRecallSources(literal.texts)
					store := scriptedRecaller{responses: [][]recording.RecallSource{sources, sources}}
					adapterCalls := 0
					speechTexts := make(chan string, 1)
					if denied {
						store.errOn = 2
					}
					sourcesDelivered := make(chan struct{}, 1)
					speechGate, releaseSpeech := interactionGenerationRelease(t)
					defer releaseSpeech()
					h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
						select {
						case speechTexts <- text:
						case <-ctx.Done():
							return ctx.Err()
						}
						select {
						case <-speechGate:
							return emit(testVoiceWAV())
						case <-ctx.Done():
							return ctx.Err()
						}
					}}, func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
						adapterCalls++
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
							if event.Chat.RequestID != start.OperationID || len(event.Chat.Sources) != len(sources) {
								t.Fatalf("interaction source event lost identity: %+v", event)
							}
							for index, source := range sources {
								item := event.Chat.Sources[index]
								citation := item.KarteRecordV2
								if item.ChunkText != source.Text || item.Snippet != source.Text || item.SourceID != fmt.Sprintf("R%d", index+1) || citation == nil || !reflect.DeepEqual(citation.Target, source.Target) || !reflect.DeepEqual(citation.Event, source.Event) || citation.ConversationID != source.ConversationID || citation.TurnID != source.TurnID || item.SourceType != "karte_record_v2" || item.TrustLevel != "local_untrusted" {
									t.Fatalf("interaction source event lost literal text/v2 binding: %+v", item)
								}
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
					if store.calls != 2 || adapterCalls != 1 {
						t.Fatalf("interaction attempted recall repair/continuation: recalls=%d, adapter=%d", store.calls, adapterCalls)
					}
					if denied {
						if sourceEvents != 0 || outputEvents != 0 || audioEvents != 0 || (done.ResponsePlan != nil && done.ResponsePlan.Text != "") {
							t.Fatalf("revoked interaction disclosed saved content: %+v", done)
						}
						return
					}
					if done.ResponsePlan == nil || done.ResponsePlan.Text != answer || done.Generation == nil || !done.Generation.Complete || done.Generation.FinishReason != "stop" || done.Generation.SegmentCount != 1 || done.Generation.ContinuationCount != 0 || done.ErrorCode != "" || outputEvents == 0 || audioEvents == 0 {
						t.Fatalf("recall interaction did not complete after playback: %+v", done)
					}
					if sourceEvents != 1 {
						t.Fatal("interaction did not deliver citations to the UI")
					}
					if text := interactionGenerationReceive(t, speechTexts); text != answer {
						t.Fatalf("materialized speech text was parsed as Markdown: %q", text)
					}
				})
			}
		}
	}
}

func TestFixedRecordedRecallCompletedSnapshotRecoversSourcesWithoutAudio(t *testing.T) {
	for _, texts := range [][]string{{"Remember (Tuesday."}, {}} {
		sources, answer := testRecordedRecallSources(texts)
		if len(texts) == 0 {
			answer = "No authorized saved conversation matched."
		}
		store := scriptedRecaller{responses: [][]recording.RecallSource{sources, sources}}
		dispatchGate, releaseDispatch := interactionGenerationRelease(t)
		defer releaseDispatch()
		dispatchEntered := make(chan struct{}, 1)
		h := newInteractionGenerationHarness(t, testVoiceTTS{ready: func(context.Context) error {
			return errors.New("tts_unavailable")
		}}, func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
			return fixedRecordedRecall(ctx, &store, request, token)
		})
		emit := h.engine.emit
		h.engine.emit = func(event InteractionEvent) {
			select {
			case dispatchEntered <- struct{}{}:
			default:
			}
			<-dispatchGate
			emit(event)
		}
		request := VoiceTurnRequest{SessionID: "synthetic-session", InputKind: "text",
			Chat: ChatRequest{Prompt: "When does the rehearsal start?", SourceScope: "recorded_conversation"}}
		start, err := h.engine.Start(request)
		if err != nil {
			t.Fatal(err)
		}
		interactionGenerationReceive(t, dispatchEntered)
		if err := h.engine.Commit(start.OperationID, nil, request.Chat.Prompt); err != nil {
			t.Fatal(err)
		}
		// No speech gate: the optional readout fails immediately, and the
		// operation completes before any queued source/output can be delivered.
		done := awaitInteraction(t, h.engine, start.OperationID, "COMPLETED")
		if done.ResponsePlan == nil || done.ResponsePlan.Text != answer || done.SpeechErrorCode != "tts_unavailable" || done.LastAudioSequence != 0 || done.Sources == nil || len(done.Sources) != len(sources) || store.calls != 2 {
			t.Fatalf("completed no-audio answer lost its citation recovery: %+v", done)
		}
		encoded, err := json.Marshal(done)
		if err != nil || (len(sources) == 0 && !strings.Contains(string(encoded), `"sources":[]`)) {
			t.Fatalf("no-match recovery did not preserve an empty source array: %v, %s", err, encoded)
		}
		if len(sources) > 0 {
			if done.Sources[0].ChunkText != sources[0].Text || done.Sources[0].KarteRecordV2 == nil || !reflect.DeepEqual(done.Sources[0].KarteRecordV2.Target, sources[0].Target) {
				t.Fatal("completed recovery changed the authorized source")
			}
			done.Sources[0].ChunkText = "mutated"
			done.Sources[0].KarteRecordV2.Target.DocID = "mutated"
			fresh, err := h.engine.Snapshot(start.OperationID)
			if err != nil || fresh.Sources[0].ChunkText != sources[0].Text || fresh.Sources[0].KarteRecordV2.Target.DocID != sources[0].Target.DocID {
				t.Fatal("snapshot citation data was not deeply copied")
			}
		}
		releaseDispatch()
		h.awaitDeliveredTerminal(t, start.OperationID, "COMPLETED")
		found := false
		for _, event := range h.recorded() {
			if event.Kind == "chat" || event.Kind == "output" || event.Kind == "audio" {
				t.Fatal("test failed to exercise dropped live-event recovery")
			}
			if event.Kind == "state" && event.Snapshot.State == "COMPLETED" {
				found = event.Snapshot.ResponsePlan.Text == answer && event.Snapshot.Sources != nil && len(event.Snapshot.Sources) == len(sources)
			}
		}
		if !found {
			t.Fatal("completed event did not carry answer and authorized citations")
		}
	}
}

func TestRecordedRecallSourcesCloneAndTerminalBoundaries(t *testing.T) {
	source := SearchItem{HeadingPath: []string{"heading"}, Tags: []string{"tag"}, KarteRecordV2: &KarteRecordV2Citation{Target: testRecordedRecallSource().Target}}
	cloned := cloneInteractionSnapshot(InteractionSnapshot{Sources: []SearchItem{source}})
	cloned.Sources[0].HeadingPath[0] = "changed"
	cloned.Sources[0].Tags[0] = "changed"
	cloned.Sources[0].KarteRecordV2.Target.DocID = "changed"
	if source.HeadingPath[0] != "heading" || source.Tags[0] != "tag" || source.KarteRecordV2.Target.DocID != "synthetic-doc" {
		t.Fatal("snapshot retained a mutable source alias")
	}
	for _, state := range []string{"FAILED", "CANCELED", "INCOMPLETE"} {
		h := newInteractionGenerationHarness(t, testVoiceTTS{}, testVoiceChat)
		start := startTestInteraction(t, h.engine)
		h.engine.mu.Lock()
		turn := h.engine.turns[start.OperationID]
		turn.recallSources = []SearchItem{source}
		turn.snapshot.Sources = []SearchItem{source}
		if state == "CANCELED" {
			turn.snapshot.State = "CANCELING"
		}
		h.engine.finishLocked(turn, state, "turn_failed", "synthetic_failure")
		pending := turn.recallSources
		h.engine.mu.Unlock()
		done, err := h.engine.Snapshot(start.OperationID)
		if err != nil || done.State != state || done.Sources != nil || pending != nil {
			t.Fatalf("unsuccessful terminal promoted citation data: %+v, %v", done, err)
		}
	}
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, _ ChatRequest, _ func(string)) (*ChatResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := startTestInteraction(t, h.engine)
	h.engine.mu.Lock()
	turn := h.engine.turns[start.OperationID]
	h.engine.finishLocked(turn, "INCOMPLETE", "turn_incomplete", "incomplete_response")
	// Even a stale retained value must never cross the next revision boundary.
	turn.recallSources = []SearchItem{source}
	turn.snapshot.Sources = []SearchItem{source}
	h.engine.mu.Unlock()
	resumed, err := h.engine.Continue(start.OperationID)
	if err != nil || resumed.GenerationRevision != start.GenerationRevision+1 || resumed.Sources != nil {
		t.Fatalf("continuation promoted a prior revision's sources: %+v, %v", resumed, err)
	}
	h.engine.mu.Lock()
	pending := h.engine.turns[start.OperationID].recallSources
	h.engine.mu.Unlock()
	if pending != nil {
		t.Fatal("continuation retained private sources from the prior revision")
	}
}
