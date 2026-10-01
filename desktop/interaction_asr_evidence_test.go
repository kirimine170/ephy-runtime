package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func evidenceUpdate(s *c02EngineSession, revision int, phase, text string) ASRUpdate {
	u := s.update(revision, phase, text)
	u.ModelRevision = "evidence-test-v1"
	return u
}

func evidenceActivity(s *c02EngineSession, revision int, hasSpeech bool) ASRUpdate {
	u := evidenceUpdate(s, revision, "activity", "")
	// These are protocol fixtures, not acoustic estimates. The engine consumes
	// the provider's evidence decision; the C++ policy owns onset thresholds.
	u.Activity = &ASRAudioActivity{AudioMS: 10, LastSpeechMS: 10, SpeechMS: 10,
		Probability: .9, Speaking: true, HasSpeech: hasSpeech}
	return u
}

func TestActivityASRRejectsUnevidencedFinalBeforeConversation(t *testing.T) {
	for _, kind := range []string{"missing-activity", "silence", "unconfirmed-spike", "foreign-evidence"} {
		t.Run(kind, func(t *testing.T) {
			p := &activityEngineProvider{}
			store, home := newAppRecordingStore(t)
			var calls, transcripts, residentInputs atomic.Int32
			e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				calls.Add(1)
				return completedVoiceResponse("```go\nfixture\n```"), nil
			}, func(ev InteractionEvent) {
				if ev.Kind == "transcript" {
					transcripts.Add(1)
				}
			}, t.TempDir())
			defer e.Close()
			e.recorder = store
			e.residentInput = func(InteractionSnapshot, VoiceTurnRequest, string) bool {
				residentInputs.Add(1)
				return false
			}
			s, session := c02Start(t, e, &p.c02EngineProvider)
			before, err := e.GetRequest(s.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "missing-activity" {
				u := evidenceActivity(session, 1, false)
				if kind == "silence" {
					u.Activity = &ASRAudioActivity{AudioMS: 10}
				}
				if kind == "foreign-evidence" {
					u.Activity.HasSpeech = true
					u.SegmentID = "another-segment"
				}
				session.emit(u)
			}
			if err := e.EndASR(s.OperationID); err != nil {
				t.Fatal(err)
			}
			final := evidenceUpdate(session, 2, "final", "ご視聴ありがとうございました")
			session.emit(final)
			session.result <- c02SessionResult{update: final}
			got := awaitInteraction(t, e, s.OperationID, "FAILED")
			after, err := e.GetRequest(s.OperationID)
			if err != nil || after.Chat.Prompt != before.Chat.Prompt {
				t.Fatal("unverified final changed the LLM prompt", err)
			}
			if got.ErrorCode != "asr_stream_invalid" || got.InputOutcome == "no_speech" || got.Transcript != "" || calls.Load() != 0 || transcripts.Load() != 0 || residentInputs.Load() != 0 {
				t.Fatal("contradictory final crossed the acceptance boundary", got)
			}
			if events := readSpoolEvents(t, home); len(events) != 0 {
				t.Fatal("unverified final created a canonical recording", events)
			}
			trace, err := e.Trace(s.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range trace {
				if event.Name == "asr_final" || event.Name == "llm_requested" {
					t.Fatal("unverified final entered generation", event.Name)
				}
			}
			// Failure must release the ASR slot and fence late old-session data.
			next, nextSession := c02Start(t, e, &p.c02EngineProvider)
			session.emit(evidenceActivity(session, 3, true))
			e.mu.Lock()
			leaked := e.turns[next.OperationID].asr.speechObserved
			e.mu.Unlock()
			if leaked || !session.canceled.Load() {
				t.Fatal("old-session evidence or ownership crossed into the next turn")
			}
			nextSession.emit(evidenceActivity(nextSession, 1, true))
			if err := e.EndASR(next.OperationID); err != nil {
				t.Fatal(err)
			}
			nextSession.result <- c02SessionResult{update: evidenceUpdate(nextSession, 2, "final", "はい")}
			awaitInteraction(t, e, next.OperationID, "COMPLETED")
			if calls.Load() != 1 {
				t.Fatal("next input did not recover exactly once")
			}
		})
	}
}

func TestActivityASRAcceptsEvidencedFinalOnceWithoutPhraseFiltering(t *testing.T) {
	for _, text := range []string{"はい", "いや", "ご視聴ありがとうございました"} {
		t.Run(text, func(t *testing.T) {
			p := &activityEngineProvider{}
			store, home := newAppRecordingStore(t)
			var calls atomic.Int32
			transcripts := make(chan string, 8)
			chatRelease := make(chan struct{})
			var releaseOnce sync.Once
			releaseChat := func() { releaseOnce.Do(func() { close(chatRelease) }) }
			e := NewInteractionEngine(p, testVoiceTTS{}, func(ctx context.Context, req ChatRequest, _ func(string)) (*ChatResponse, error) {
				calls.Add(1)
				if req.Prompt != text {
					t.Error("evidenced final was changed")
				}
				// Keep the turn live until the queued transcript is observed.
				// Production dispatch intentionally fences events after completion.
				select {
				case <-chatRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return completedVoiceResponse("```go\nfixture\n```"), nil
			}, func(ev InteractionEvent) {
				if ev.Kind == "transcript" {
					transcripts <- ev.Text
				}
			}, t.TempDir())
			defer func() {
				releaseChat()
				e.Close()
			}()
			e.recorder = store
			s, session := c02Start(t, e, &p.c02EngineProvider)
			session.emit(evidenceActivity(session, 1, true))
			if err := e.EndASR(s.OperationID); err != nil {
				t.Fatal(err)
			}
			final := evidenceUpdate(session, 2, "final", text)
			session.emit(final)
			session.emit(final)
			session.result <- c02SessionResult{update: final}
			select {
			case got := <-transcripts:
				if got != text {
					t.Fatal("wrong transcript event")
				}
			case <-time.After(time.Second):
				t.Fatal("missing transcript event")
			}
			releaseChat()
			got := awaitInteraction(t, e, s.OperationID, "COMPLETED")
			session.emit(final)
			if got.Transcript != text || calls.Load() != 1 {
				t.Fatal("evidenced final was lost or duplicated", got)
			}
			select {
			case <-transcripts:
				t.Fatal("duplicate transcript event")
			default:
			}
			users := 0
			for _, event := range readSpoolEvents(t, home) {
				if event.Type == "user_final" {
					users++
					if event.Text != text {
						t.Fatal("canonical text changed")
					}
				}
			}
			if users != 1 {
				t.Fatal("expected exactly one canonical user_final", users)
			}
		})
	}
}

func TestActivityASRNoSpeechAndDecoderFailureRemainDistinct(t *testing.T) {
	for _, decoderFailed := range []bool{false, true} {
		p := &activityEngineProvider{}
		var calls atomic.Int32
		e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
			calls.Add(1)
			return nil, nil
		}, nil, t.TempDir())
		s, session := c02Start(t, e, &p.c02EngineProvider)
		if err := e.EndASR(s.OperationID); err != nil {
			t.Fatal(err)
		}
		state := "CANCELED"
		if decoderFailed {
			state = "FAILED"
			session.result <- c02SessionResult{err: errors.New("asr_failed")}
		} else {
			session.result <- c02SessionResult{update: evidenceUpdate(session, 1, "no_speech", "")}
		}
		got := awaitInteraction(t, e, s.OperationID, state)
		if calls.Load() != 0 || got.Transcript != "" || (decoderFailed && (got.ErrorCode != "asr_failed" || got.InputOutcome == "no_speech")) || (!decoderFailed && (got.ErrorCode != "" || got.InputOutcome != "no_speech")) {
			t.Fatal("no-speech and decoder failure were conflated", got)
		}
		e.Close()
	}
}

func TestASREvidenceGuardExemptsNonActivityTextAndReplay(t *testing.T) {
	for _, kind := range []string{"non-activity", "text", "transcript"} {
		t.Run(kind, func(t *testing.T) {
			p := &activityEngineProvider{}
			var provider VoiceASR = p
			if kind == "non-activity" {
				provider = &p.c02EngineProvider
			}
			var calls atomic.Int32
			e := NewInteractionEngine(provider, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				calls.Add(1)
				return completedVoiceResponse("```go\nfixture\n```"), nil
			}, nil, t.TempDir())
			defer e.Close()
			var s InteractionSnapshot
			const text = "ご視聴ありがとうございました"
			if kind == "non-activity" {
				var session *c02EngineSession
				s, session = c02Start(t, e, &p.c02EngineProvider)
				if err := e.EndASR(s.OperationID); err != nil {
					t.Fatal(err)
				}
				session.result <- c02SessionResult{update: evidenceUpdate(session, 1, "final", text)}
			} else {
				var err error
				s, err = e.Start(VoiceTurnRequest{SessionID: "evidence-exemption", InputKind: kind, Chat: ChatRequest{Mode: "fast"}})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.Commit(s.OperationID, nil, text); err != nil {
					t.Fatal(err)
				}
			}
			got := awaitInteraction(t, e, s.OperationID, "COMPLETED")
			if calls.Load() != 1 || got.Transcript != text {
				t.Fatal("evidence guard affected an exempt input", got)
			}
		})
	}
}
