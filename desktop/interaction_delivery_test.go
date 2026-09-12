package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInteractionSharedDeliveryFixture(t *testing.T) {
	data, err := os.ReadFile("../schemas/karte-ephy/v2/fixtures/runtime-delivery.scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string              `json:"name"`
			Snapshot InteractionSnapshot `json:"snapshot"`
			Chunks   []struct {
				Sequence int    `json:"sequence"`
				UnitID   string `json:"unit_id"`
				State    string `json:"state"`
			} `json:"chunks"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			expected := cloneInteractionSnapshot(scenario.Snapshot)
			turn := &interactionTurn{snapshot: cloneInteractionSnapshot(scenario.Snapshot), chunks: map[int]*playbackChunk{}}
			for _, c := range scenario.Chunks {
				turn.chunks[c.Sequence] = &playbackChunk{unitID: c.UnitID, started: c.State != "queued", stopped: c.State == "completed", interrupted: c.State == "interrupted"}
			}
			for i := range turn.snapshot.SpeechUnits {
				turn.snapshot.SpeechUnits[i].State = "unknown"
				turn.snapshot.SpeechUnits[i].PlaybackStarted = false
			}
			(&InteractionEngine{}).refreshSpeechUnitsLocked(turn)
			if !reflect.DeepEqual(turn.snapshot.SpeechUnits, expected.SpeechUnits) {
				t.Fatalf("unit outcomes differ: %+v", turn.snapshot.SpeechUnits)
			}
			if len(expected.SpeechUnits) > 0 {
				turn.snapshot.SpeechUnits[0].AudioSequences[0] = 99
				if expected.SpeechUnits[0].AudioSequences[0] == 99 {
					t.Fatal("snapshot alias")
				}
			}
		})
	}
}

func TestInteractionInterruptionPreservesGenerationAndNextASRDespiteLateProviders(t *testing.T) {
	for _, phase := range []string{"llm_wait", "tts_wait", "body"} {
		t.Run(phase, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			entered, late := make(chan struct{}, 1), make(chan error, 1)
			p := &c02EngineProvider{}
			chat := func(ctx context.Context, request ChatRequest, token func(string)) (*ChatResponse, error) {
				if phase == "llm_wait" {
					entered <- struct{}{}
					<-release
					token("late private answer")
					late <- ctx.Err()
				}
				return completedVoiceResponse("春の説明です。"), nil
			}
			tts := testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
				if phase == "body" {
					if err := emit(providerTestWAV()); err != nil {
						return err
					}
					if err := emit(providerTestWAV()); err != nil {
						return err
					}
				}
				entered <- struct{}{}
				<-release
				late <- emit(providerTestWAV())
				return nil
			}}
			e := NewInteractionEngine(p, tts, chat, nil, t.TempDir())
			defer e.Close()
			v, err := e.StartVoiceSession("conversation")
			if err != nil {
				t.Fatal(err)
			}
			req := VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch, InputKind: "transcript"}
			first, err := e.Start(req)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Commit(first.OperationID, nil, "四季を説明して"); err != nil {
				t.Fatal(err)
			}
			interactionGenerationReceive(t, entered)
			if phase != "llm_wait" {
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					s, _ := e.Snapshot(first.OperationID)
					if s.Generation != nil && s.Generation.Complete {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			interrupt := InteractionInterruption{VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch, SessionID: first.SessionID, TurnID: first.TurnID, OperationID: first.OperationID, GenerationRevision: 1, LocalStopMS: 3}
			if phase == "body" {
				interrupt.Playback = []InteractionPlaybackObservation{{Sequence: 1, State: "completed"}, {Sequence: 2, State: "interrupted"}}
			}
			done := make(chan InteractionSnapshot, 1)
			go func() {
				s, err := e.Interrupt(interrupt)
				if err != nil {
					t.Error(err)
				}
				done <- s
			}()
			stopped := interactionGenerationReceive(t, done) // Provider deliberately still blocked．
			if stopped.State != "CANCELED" || stopped.Generation.Complete != (phase != "llm_wait") {
				t.Fatalf("lost independent generation completion: %+v", stopped)
			}
			if phase == "body" && (len(stopped.SpeechUnits) != 1 || stopped.SpeechUnits[0].State != "interrupted" || stopped.SpeechUnits[0].SynthesisComplete) {
				t.Fatal("invented whole-unit completion", stopped.SpeechUnits)
			}
			req.InputKind = "microphone"
			next, err := e.Start(req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.BeginASR(next.OperationID, 16000); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Interrupt(interrupt); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			nextASR := e.turns[next.OperationID].asr
			parent := e.voiceSession.ctx
			e.mu.Unlock()
			if parent.Err() != nil || nextASR.ctx.Err() != nil {
				t.Fatal("old cancel killed new input")
			}
			// Mutating any identity must leave the new operation untouched．
			for _, mutate := range []func(*InteractionInterruption){func(r *InteractionInterruption) { r.SessionID = "wrong" }, func(r *InteractionInterruption) { r.TurnID = "wrong" }, func(r *InteractionInterruption) { r.OperationID = next.OperationID }, func(r *InteractionInterruption) { r.GenerationRevision++ }, func(r *InteractionInterruption) { r.VoiceSessionEpoch++ }, func(r *InteractionInterruption) { r.VoiceSessionID = "wrong" }} {
				wrong := interrupt
				mutate(&wrong)
				if _, err = e.Interrupt(wrong); err == nil {
					t.Fatal("stale identity accepted")
				}
			}
			trace, err := e.Trace(first.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if ValidateTrace(trace).LatenciesMS["local_stop"] != 3 {
				t.Fatal("missing local stop measurement")
			}
			raw, err := os.ReadFile(filepath.Join(e.store.dir, first.OperationID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var decoded []map[string]any
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, event := range decoded {
				if event["speech_units"] != nil || event["response_plan"] != nil {
					t.Fatal("text persisted in trace")
				}
			}
			// Let the ignored cancellation finish while the next ASR is still open．
			release <- struct{}{}
			if err := interactionGenerationReceive(t, late); !errors.Is(err, context.Canceled) {
				t.Fatal("late provider escaped cancellation", err)
			}
			if nextASR.ctx.Err() != nil {
				t.Fatal("late provider killed next ASR")
			}
		})
	}
}

func TestInteractionSpeechUnitRequiresProducerSealAndEveryNaturalEnd(t *testing.T) {
	h := newInteractionGenerationHarness(t, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
		if err := emit(providerTestWAV()); err != nil {
			return err
		}
		return emit(providerTestWAV())
	}}, testVoiceChat)
	first := h.start(t, "units", GenerationLimits{})
	final := awaitInteraction(t, h.engine, first.OperationID, "COMPLETED")
	if len(final.SpeechUnits) != 1 || final.SpeechUnits[0].State != "completed" || len(final.SpeechUnits[0].AudioSequences) != 2 || !final.SpeechUnits[0].SynthesisComplete {
		t.Fatal(final.SpeechUnits)
	}
	h.checkPlayback(t)
}

func TestInteractionSuccessfulSpeechSealSurvivesCancellationLockRace(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "all_natural", true: "partial_interrupted"}[interrupted], func(t *testing.T) {
			e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
			defer e.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			turn := &interactionTurn{ctx: ctx, cancel: cancel, created: time.Now(),
				snapshot: InteractionSnapshot{OperationID: "op_seal", SessionID: "session", TurnID: "turn", GenerationRevision: 1, State: "PLAYING"},
				chunks:   map[int]*playbackChunk{1: {started: true, stopped: true}, 2: {started: true, stopped: !interrupted, interrupted: interrupted}},
			}
			e.mu.Lock()
			e.turns[turn.snapshot.OperationID] = turn
			unitID := e.registerSpeechUnitLocked(turn, 1, "自然終了を確認する合成文．")
			turn.snapshot.SpeechUnits[0].AudioSequences = []int{1, 2}
			// The producer has returned successfully，but its bookkeeping waits
			// for the engine lock while cancellation wins that same lock．
			waiting, sealed := make(chan struct{}), make(chan struct{})
			go func() {
				close(waiting)
				e.sealSpeechUnit(turn, 1, unitID)
				close(sealed)
			}()
			<-waiting
			e.cancelTurnLocked(turn)
			e.mu.Unlock()
			interactionGenerationReceive(t, sealed)
			got, err := e.Snapshot(turn.snapshot.OperationID)
			if err != nil || got.State != "CANCELED" || ctx.Err() != context.Canceled || !got.SpeechUnits[0].SynthesisComplete {
				t.Fatalf("successful producer close lost after cancel: %+v / %v", got, err)
			}
			want := "completed"
			if interrupted {
				want = "interrupted"
			}
			if got.SpeechUnits[0].State != want {
				t.Fatalf("producer close invented playback: %+v", got.SpeechUnits)
			}
		})
	}
}

func TestInteractionSpeechSealRejectsStaleTurnRevisionAndUnit(t *testing.T) {
	for _, stale := range []string{"turn", "revision", "unit_revision", "unit_id"} {
		t.Run(stale, func(t *testing.T) {
			turn := &interactionTurn{snapshot: InteractionSnapshot{OperationID: "op_seal", GenerationRevision: 2, State: "CANCELED",
				SpeechUnits: []InteractionSpeechUnit{{UnitID: "unit_current", GenerationRevision: 2}}}, chunks: map[int]*playbackChunk{}}
			e := &InteractionEngine{turns: map[string]*interactionTurn{"op_seal": turn}}
			revision, id := 2, "unit_current"
			switch stale {
			case "turn":
				e.turns["op_seal"] = &interactionTurn{}
			case "revision":
				revision = 1
			case "unit_revision":
				turn.snapshot.SpeechUnits[0].GenerationRevision = 1
			case "unit_id":
				id = "unit_old"
			}
			e.sealSpeechUnit(turn, revision, id)
			if turn.snapshot.SpeechUnits[0].SynthesisComplete {
				t.Fatal("stale producer close reached another unit or revision")
			}
		})
	}
}

type delayedASRCancelProvider struct {
	*c02EngineProvider
	cancelStarted chan struct{}
	releaseCancel chan struct{}
	opened        chan struct{}
}
type delayedASRCancelSession struct {
	VoiceASRSession
	provider *delayedASRCancelProvider
}

func (s *delayedASRCancelSession) Cancel() {
	close(s.provider.cancelStarted)
	<-s.provider.releaseCancel
	s.VoiceASRSession.Cancel()
}
func (p *delayedASRCancelProvider) OpenSession(ctx context.Context, req ASRSessionRequest, emit func(ASRUpdate)) (VoiceASRSession, error) {
	s, err := p.c02EngineProvider.OpenSession(ctx, req, emit)
	if len(p.sessions) == 1 {
		return &delayedASRCancelSession{VoiceASRSession: s, provider: p}, err
	}
	p.opened <- struct{}{}
	return s, err
}
func TestInteractionASRProviderSessionsNeverOverlapDuringDelayedCleanup(t *testing.T) {
	p := &delayedASRCancelProvider{c02EngineProvider: &c02EngineProvider{}, cancelStarted: make(chan struct{}), releaseCancel: make(chan struct{}), opened: make(chan struct{}, 1)}
	e := NewInteractionEngine(p, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	v, err := e.StartVoiceSession("conversation")
	if err != nil {
		t.Fatal(err)
	}
	req := VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch}
	old, err := e.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.BeginASR(old.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Cancel(old.OperationID); err != nil {
		t.Fatal(err)
	}
	interactionGenerationReceive(t, p.cancelStarted)
	next, err := e.Start(req)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() { _, err := e.BeginASR(next.OperationID, 16000); ready <- err }()
	select {
	case <-p.opened:
		t.Fatal("second ASR opened before old cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(p.releaseCancel)
	if err := interactionGenerationReceive(t, ready); err != nil {
		t.Fatal(err)
	}
	interactionGenerationReceive(t, p.opened)
	if _, err = e.Cancel(old.OperationID); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	alive := e.turns[next.OperationID].asr.ctx.Err() == nil
	e.mu.Unlock()
	if !alive {
		t.Fatal("repeated old cancel ended new ASR")
	}
}

func TestInteractionInputHandoffTimingIsBoundedAndFenced(t *testing.T) {
	e := NewInteractionEngine(&c02EngineProvider{}, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	defer e.Close()
	v, err := e.StartVoiceSession("timing")
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.Start(VoiceTurnRequest{SessionID: v.ConversationID, VoiceSessionID: v.ID, VoiceSessionEpoch: v.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.BeginASR(s.OperationID, 16000); err != nil {
		t.Fatal(err)
	}
	timing := InteractionInputHandoffTiming{ASRReadyMS: 40, DrainedMS: 48, BufferedAudioMS: 72}
	if e.RecordInputHandoff(s.OperationID, 2, timing) == nil {
		t.Fatal("stale revision accepted")
	}
	if e.RecordInputHandoff(s.OperationID, 1, InteractionInputHandoffTiming{DrainedMS: 5001}) == nil {
		t.Fatal("unbounded timing accepted")
	}
	if err = e.RecordInputHandoff(s.OperationID, 1, timing); err != nil {
		t.Fatal(err)
	}
	if err = e.RecordInputHandoff(s.OperationID, 1, timing); err != nil {
		t.Fatal(err)
	}
	trace, _ := e.Trace(s.OperationID)
	count := 0
	for _, event := range trace {
		if event.Name == "input_handoff_ready" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate handoff timing")
	}
	if _, err = e.Cancel(s.OperationID); err != nil {
		t.Fatal(err)
	}
	trace, _ = e.Trace(s.OperationID)
	metrics := ValidateTrace(trace).LatenciesMS
	if metrics["input_handoff_asr_ready"] != 40 || metrics["input_handoff_drained"] != 48 {
		t.Fatal("timing not retained")
	}
	if e.RecordInputHandoff(s.OperationID, 1, timing) == nil {
		t.Fatal("terminal callback accepted")
	}
}
