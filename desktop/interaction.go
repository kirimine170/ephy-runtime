package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Voice adapters must observe cancellation and return stable error codes only.
type VoiceASR interface {
	Ready(context.Context) error
	Transcribe(context.Context, []byte) (string, error)
}
type VoiceTTS interface {
	Ready(context.Context) error
	Stream(context.Context, string, func([]byte) error) error
}
type voiceIdentity interface {
	Identity() (provider, model, configuration string)
}
type VoiceTurnRequest struct {
	VoiceSessionID    string           `json:"voice_session_id,omitempty"`
	VoiceSessionEpoch uint64           `json:"voice_session_epoch,omitempty"`
	SessionID         string           `json:"session_id"`
	InputKind         string           `json:"input_kind,omitempty"`
	Chat              ChatRequest      `json:"chat"`
	GenerationLimits  GenerationLimits `json:"generation_limits"`
	Speech            SpeechOptions    `json:"speech"`
}
type VoiceHint struct {
	Pace   float64 `json:"pace"`
	Volume float64 `json:"volume"`
}
type ResponsePlan struct {
	Text          string    `json:"text"`
	DialogueAct   string    `json:"dialogue_act"`
	Affect        string    `json:"affect"`
	VoiceHint     VoiceHint `json:"voice_hint"`
	Interruptible bool      `json:"interruptible"`
}
type InteractionSnapshot struct {
	TraceID            string                         `json:"trace_id"`
	SessionID          string                         `json:"session_id"`
	TurnID             string                         `json:"turn_id"`
	OperationID        string                         `json:"operation_id"`
	State              string                         `json:"state"`
	Transcript         string                         `json:"transcript,omitempty"`
	ResponsePlan       *ResponsePlan                  `json:"response_plan,omitempty"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	Generation         *GenerationMetadata            `json:"generation,omitempty"`
	GenerationRevision int                            `json:"generation_revision"`
	LastAudioSequence  int                            `json:"last_audio_sequence"`
	InputHandoff       *InteractionInputHandoffTiming `json:"input_handoff,omitempty"`
	Interruption       *InteractionInterruptionTiming `json:"interruption,omitempty"`
	SpeechUnits        []InteractionSpeechUnit        `json:"speech_units"`
}
type InteractionEvent struct {
	Kind               string                 `json:"kind"`
	TraceID            string                 `json:"trace_id"`
	SessionID          string                 `json:"session_id"`
	TurnID             string                 `json:"turn_id"`
	OperationID        string                 `json:"operation_id"`
	Snapshot           *InteractionSnapshot   `json:"snapshot,omitempty"`
	Text               string                 `json:"text,omitempty"`
	AudioBase64        string                 `json:"audio_base64,omitempty"`
	Sequence           int                    `json:"sequence,omitempty"`
	SpeechUnitID       string                 `json:"speech_unit_id,omitempty"`
	Trace              *InteractionTraceEvent `json:"trace,omitempty"`
	GenerationRevision int                    `json:"generation_revision"`
	ASR                *ASRUpdate             `json:"asr,omitempty"`
}

// Playback is a finite start/ACK margin，added to the validated WAV duration．
type InteractionTimeouts struct{ ASR, LLM, TTS, Playback time.Duration }
type playbackChunk struct {
	started, stopped     bool
	interrupted          bool
	unitID               string
	duration             time.Duration
	waitingAt, startedAt time.Time
}
type interactionTurn struct {
	fillerIdentityReady    bool
	fillerSetupServed      bool
	fillerSampleSaved      bool
	fillerScope            string
	fillerTraceCount       int
	snapshot               InteractionSnapshot
	request                VoiceTurnRequest
	ctx                    context.Context
	cancel                 context.CancelFunc
	created                time.Time
	events                 []InteractionTraceEvent
	chunks                 map[int]*playbackChunk
	producerDone           bool
	generationDone         bool
	playbackTimer          *time.Timer
	playbackDeadline       time.Time
	playbackGeneration     uint64
	tokenCount, audioBytes int
	source                 string
	requestConfigurationID string
	asr                    *interactionASRSession
	speech                 *preparedSpeech
}
type InteractionEngine struct {
	mu           sync.Mutex
	voiceSession *voiceSession
	asr          VoiceASR
	tts          VoiceTTS
	chat         func(context.Context, ChatRequest, func(string)) (*ChatResponse, error)
	emit         func(InteractionEvent)
	turns        map[string]*interactionTurn
	active       string
	closed       bool
	events       chan InteractionEvent
	done         chan struct{}
	asrWake      chan struct{}
	asrSlot      chan struct{}
	store        *interactionTraceStore
	// Configure before starting the first operation.
	Timeouts InteractionTimeouts
}

var interactionIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var interactionMetadataID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:@-]{0,127}$`)

const maxInteractionTurns = 256
const maxInteractionEvents = 128
const maxInteractionAge = 7 * 24 * time.Hour

func NewInteractionEngine(asr VoiceASR, tts VoiceTTS, chat func(context.Context, ChatRequest, func(string)) (*ChatResponse, error), emit func(InteractionEvent), storeDir string) *InteractionEngine {
	e := &InteractionEngine{asr: asr, tts: tts, chat: chat, emit: emit, turns: map[string]*interactionTurn{}, events: make(chan InteractionEvent, 1024), done: make(chan struct{}), store: newInteractionTraceStore(storeDir), Timeouts: InteractionTimeouts{30 * time.Second, 90 * time.Second, 60 * time.Second, 30 * time.Second}}
	e.asrWake = make(chan struct{}, 1)
	e.asrSlot = make(chan struct{}, 1)
	go e.dispatch()
	return e
}
func interactionID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func interactionTerminal(state string) bool {
	return state == "COMPLETED" || state == "CANCELED" || state == "FAILED" || state == "INCOMPLETE"
}
func (e *InteractionEngine) dispatch() {
	for {
		select {
		case <-e.done:
			return
		case <-e.asrWake:
			e.dispatchASR()
		case event := <-e.events:
			e.mu.Lock()
			turn := e.turns[event.OperationID]
			live := turn != nil && !interactionTerminal(turn.snapshot.State) && event.GenerationRevision == turn.snapshot.GenerationRevision
			current := turn != nil && event.GenerationRevision == turn.snapshot.GenerationRevision
			e.mu.Unlock()
			if !current || ((event.Kind == "audio" || event.Kind == "token" || event.Kind == "transcript" || event.Kind == "output") && !live) {
				continue
			}
			if e.emit != nil {
				e.emit(event)
			}
		}
	}
}
func (e *InteractionEngine) eventLocked(t *interactionTurn, kind string) InteractionEvent {
	s := t.snapshot
	return InteractionEvent{Kind: kind, TraceID: s.TraceID, SessionID: s.SessionID, TurnID: s.TurnID, OperationID: s.OperationID, GenerationRevision: s.GenerationRevision}
}

// A generation attempt emits at most 128 traces, 64 committed output events,
// 64 audio chunks and bounded state events. Start/Continue reserve 512 queue slots,
// so producers never block
// under the state mutex, including when the UI calls Playback synchronously.
func (e *InteractionEngine) queueLocked(event InteractionEvent) { e.events <- event }
func cloneInteractionSnapshot(s InteractionSnapshot) InteractionSnapshot {
	s.Generation = cloneGenerationMetadata(s.Generation)
	if s.InputHandoff != nil {
		copy := *s.InputHandoff
		s.InputHandoff = &copy
	}
	if s.Interruption != nil {
		copy := *s.Interruption
		s.Interruption = &copy
	}
	s.SpeechUnits = append([]InteractionSpeechUnit(nil), s.SpeechUnits...)
	for i := range s.SpeechUnits {
		s.SpeechUnits[i].AudioSequences = append([]int(nil), s.SpeechUnits[i].AudioSequences...)
	}
	if s.ResponsePlan != nil {
		p := *s.ResponsePlan
		s.ResponsePlan = &p
	}
	return s
}
func (e *InteractionEngine) transitionLocked(t *interactionTurn, state string) error {
	old := t.snapshot.State
	allowed := map[string]string{"IDLE": "RECORDING", "RECORDING": "TRANSCRIBING", "TRANSCRIBING": "THINKING", "THINKING": "SYNTHESIZING", "SYNTHESIZING": "PLAYING", "PLAYING": "COMPLETED", "CANCELING": "CANCELED"}
	if interactionTerminal(old) || (allowed[old] != state && state != "CANCELING" && state != "FAILED" && state != "INCOMPLETE" && !(state == "COMPLETED" && old == "THINKING" && t.generationDone)) {
		return errors.New("invalid_interaction_transition")
	}
	t.snapshot.State = state
	ev := e.eventLocked(t, "state")
	snapshot := cloneInteractionSnapshot(t.snapshot)
	ev.Snapshot = &snapshot
	e.queueLocked(ev)
	return nil
}
func (e *InteractionEngine) pruneLocked() {
	now := time.Now()
	for id, t := range e.turns {
		if interactionTerminal(t.snapshot.State) && now.Sub(t.created) > maxInteractionAge {
			delete(e.turns, id)
		}
	}
	for len(e.turns) >= maxInteractionTurns {
		var oldest string
		var at time.Time
		for id, t := range e.turns {
			if interactionTerminal(t.snapshot.State) && (oldest == "" || t.created.Before(at)) {
				oldest, at = id, t.created
			}
		}
		if oldest == "" {
			break
		}
		delete(e.turns, oldest)
	}
}
func (e *InteractionEngine) Start(request VoiceTurnRequest) (InteractionSnapshot, error) {
	if request.InputKind == "" {
		request.InputKind = "microphone"
	}
	if request.InputKind != "microphone" && request.InputKind != "transcript" {
		return InteractionSnapshot{}, errors.New("invalid_input_kind")
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > 1<<20 || len(request.Chat.Prompt) > 16<<10 || len(request.Chat.Messages) > 128 || len(request.Chat.Tags) > 128 {
		return InteractionSnapshot{}, errors.New("voice_request_too_large")
	}
	for _, m := range request.Chat.Messages {
		if len(m.Content) > 16<<10 {
			return InteractionSnapshot{}, errors.New("voice_request_too_large")
		}
	}
	request.Chat.Messages = append([]GatewayMessage(nil), request.Chat.Messages...)
	request.Chat.Tags = append([]string(nil), request.Chat.Tags...)
	// Copy caller-owned style before validating and pinning it to this operation．
	if request.Speech.Style != nil {
		style := *request.Speech.Style
		request.Speech.Style = &style
	}
	prepared, err := prepareSpeechProvider(e.tts, request.Speech)
	if err != nil {
		return InteractionSnapshot{}, err
	}
	if request.SessionID == "" {
		request.SessionID = interactionID("session_")
	}
	if !interactionIdentifier.MatchString(request.SessionID) {
		return InteractionSnapshot{}, errors.New("invalid_session_id")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return InteractionSnapshot{}, errors.New("interaction_closed")
	}
	if !e.validVoiceSessionLocked(request) {
		return InteractionSnapshot{}, errors.New("stale_voice_session")
	}
	if e.active != "" {
		return InteractionSnapshot{}, errors.New("interaction_busy")
	}
	if len(e.events) > 512 {
		return InteractionSnapshot{}, errors.New("interaction_consumer_slow")
	}
	if e.asr == nil || e.tts == nil || e.chat == nil {
		return InteractionSnapshot{}, errors.New("voice_unavailable")
	}
	if e.store.err != nil {
		return InteractionSnapshot{}, errors.New("trace_storage_unavailable")
	}
	// Readiness is checked by the bridge before microphone acquisition and again
	// in each bounded stage, so Start never performs blocking provider work.
	e.pruneLocked()
	ctx, cancel := context.WithCancel(context.Background())
	op := interactionID("operation_")
	t := &interactionTurn{snapshot: InteractionSnapshot{TraceID: interactionID("trace_"), SessionID: request.SessionID, TurnID: interactionID("turn_"), OperationID: op, State: "IDLE", GenerationRevision: 1}, request: request, ctx: ctx, cancel: cancel, created: time.Now(), chunks: map[int]*playbackChunk{}, source: request.InputKind, requestConfigurationID: request.Chat.ConfigurationID}
	t.speech = prepared
	e.turns[op] = t
	e.active = op
	if request.VoiceSessionID != "" {
		e.voiceSession.snapshot.State = "listening"
	}
	if request.VoiceSessionID == "" {
		e.traceLocked(t, "user_speech_start", "")
	} else {
		e.traceLocked(t, "listening_started", "")
	}
	_ = e.transitionLocked(t, "RECORDING")
	return cloneInteractionSnapshot(t.snapshot), nil
}
func (e *InteractionEngine) Commit(op string, audio []byte, transcript string) error {
	if len(audio) > 8<<20 || len(transcript) > 16<<10 {
		return errors.New("voice_input_too_large")
	}
	transcript = strings.TrimSpace(transcript)
	if len(audio) == 0 && transcript == "" {
		return errors.New("empty_voice_input")
	}
	e.mu.Lock()
	t := e.turns[op]
	if t == nil {
		e.mu.Unlock()
		return errors.New("operation_not_found")
	}
	if t.snapshot.State != "RECORDING" || t.asr != nil {
		e.mu.Unlock()
		return errors.New("invalid_interaction_transition")
	}
	if transcript != "" {
		t.source = "transcript"
	}
	e.traceLocked(t, "user_speech_end", "")
	e.traceLocked(t, "endpoint_commit", "")
	_ = e.transitionLocked(t, "TRANSCRIBING")
	e.mu.Unlock()
	data := append([]byte(nil), audio...)
	go e.run(t, data, transcript)
	return nil
}

// runStage bounds providers even if an adapter incorrectly ignores context.
func runInteractionStage[T any](ctx context.Context, timeout time.Duration, work func(context.Context) (T, error)) (T, error) {
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	stage, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() { v, err := work(stage); done <- result{v, err} }()
	select {
	case <-stage.Done():
		var zero T
		return zero, stage.Err()
	case r := <-done:
		if stage.Err() != nil {
			var zero T
			return zero, stage.Err()
		}
		return r.value, r.err
	}
}
func (e *InteractionEngine) liveLocked(t *interactionTurn) bool {
	return e.turns[t.snapshot.OperationID] == t && !interactionTerminal(t.snapshot.State) && t.ctx.Err() == nil
}

type interactionModelKey struct{}

func reportInteractionModel(ctx context.Context, provider, model, configuration string) {
	if observer, ok := ctx.Value(interactionModelKey{}).(func(string, string, string)); ok {
		observer(provider, model, configuration)
	}
}
func (e *InteractionEngine) run(t *interactionTurn, audio []byte, transcript string) {
	e.mu.Lock()
	if !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	e.traceLocked(t, "asr_started", "")
	e.mu.Unlock()
	result, err := runInteractionStage(t.ctx, e.Timeouts.ASR, func(ctx context.Context) (string, error) {
		if transcript != "" {
			return transcript, nil
		}
		if err := e.asr.Ready(ctx); err != nil {
			return "", err
		}
		return e.asr.Transcribe(ctx, audio)
	})
	if err != nil {
		e.stageFailure(t, "asr", err)
		return
	}
	e.acceptTranscript(t, result)
}

// Both transcript replay and a verified streaming final enter Conversation here．
// Interim ASR hypotheses never mutate ChatRequest，snapshot.Transcript or history．
func (e *InteractionEngine) acceptTranscript(t *interactionTurn, result string) {
	result = strings.TrimSpace(result)
	if result == "" || len(result) > 16<<10 {
		e.stageFailure(t, "asr", errors.New("asr_empty_result"))
		return
	}
	e.mu.Lock()
	if !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	t.snapshot.Transcript = result
	t.request.Chat.Prompt = result
	t.request.Chat.RequestID = t.snapshot.OperationID
	t.request.Chat.SessionID = t.snapshot.SessionID
	t.request.Chat.SessionMode = "voice"
	e.traceLocked(t, "asr_final", "")
	ev := e.eventLocked(t, "transcript")
	ev.Text = result
	e.queueLocked(ev)
	_ = e.transitionLocked(t, "THINKING")
	e.mu.Unlock()
	e.runGeneration(t, "")
}

// A single operation context owns the assembler and speech consumer. Only
// committed utterance units cross this queue; speculative suffixes stay private.
func (e *InteractionEngine) runGeneration(t *interactionTurn, prefix string) {
	e.mu.Lock()
	if !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	ctx, revision := t.ctx, t.snapshot.GenerationRevision
	operationID, sessionID, turnID := t.snapshot.OperationID, t.snapshot.SessionID, t.snapshot.TurnID
	firstSequence := t.snapshot.LastAudioSequence
	req, limits := t.request.Chat, t.request.GenerationLimits
	requestConfigurationID := t.requestConfigurationID
	prepared := t.speech
	e.traceLocked(t, "llm_requested", "")
	e.mu.Unlock()
	type speechTask struct{ id, text string }
	speech := make(chan speechTask, 64)
	type speechResult struct {
		units int
		err   error
	}
	speechDone := make(chan speechResult, 1)
	go func() {
		units := 0
		remaining := e.Timeouts.TTS
		for {
			var task speechTask
			select {
			case <-ctx.Done():
				speechDone <- speechResult{units: units, err: ctx.Err()}
				return
			case next, ok := <-speech:
				if !ok {
					speechDone <- speechResult{units: units}
					return
				}
				task = next
			}
			unitID, text := task.id, task.text
			started := time.Now()
			_, err := runInteractionStage(ctx, remaining, func(ttsCtx context.Context) (bool, error) {
				provider := e.tts
				if prepared != nil {
					provider = prepared.provider
				}
				if units == 0 {
					if err := provider.Ready(ttsCtx); err != nil {
						return false, err
					}
				}
				emit := func(chunk []byte) error { return e.emitSpeechUnitAudio(t, revision, ttsCtx, unitID, chunk) }
				var err error
				if prepared != nil {
					err = prepared.provider.StreamSpeech(ttsCtx, SpeechRequest{SpeechStyle: prepared.style, SpeechText: text,
						VoiceProfileID: prepared.profile.VoiceProfileID, OperationID: operationID,
						SessionID: sessionID, TurnID: turnID, GenerationRevision: revision, SpeechUnitSequence: units + 1}, emit)
				} else {
					err = provider.Stream(ttsCtx, text, emit)
				}
				if err == nil {
					// Record the provider result before the stage wrapper can choose
					// cancellation over an already successful synthesis result．
					e.sealSpeechUnit(t, revision, unitID)
				}
				return err == nil, err
			})
			remaining -= time.Since(started)
			if err != nil {
				// Fail immediately so a stopped consumer cannot leave the LLM producer
				// waiting on a full speech queue. Idle waits do not spend the TTS budget.
				e.stageFailure(t, "tts", err)
				speechDone <- speechResult{units: units, err: err}
				return
			}
			units++
		}
	}()
	response, err := runInteractionStage(ctx, e.Timeouts.LLM, func(llmCtx context.Context) (*ChatResponse, error) {
		defer close(speech)
		llmCtx = context.WithValue(llmCtx, interactionModelKey{}, func(provider, model, configuration string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if !e.generationLiveLocked(t, revision, llmCtx) {
				return
			}
			oldProvider, oldModel, oldConfig := t.request.Chat.ProviderID, t.request.Chat.ModelID, t.request.Chat.ConfigurationID
			if interactionIdentifier.MatchString(provider) {
				t.request.Chat.ProviderID = provider
			}
			if interactionMetadataID.MatchString(model) {
				t.request.Chat.ModelID = model
			}
			if interactionIdentifier.MatchString(configuration) {
				combined := sha256.Sum256([]byte(requestConfigurationID + "\x00" + configuration))
				t.request.Chat.ConfigurationID = hex.EncodeToString(combined[:16])
			}
			if !t.fillerIdentityReady {
				t.fillerIdentityReady = true
				e.traceLocked(t, "llm_identity_ready", "")
			} else if oldProvider != t.request.Chat.ProviderID || oldModel != t.request.Chat.ModelID || oldConfig != t.request.Chat.ConfigurationID {
				e.traceLocked(t, "llm_identity_changed", "")
			}
		})
		chat := func(segmentCtx context.Context, segmentReq ChatRequest, onToken func(string)) (*ChatResponse, error) {
			return e.chat(segmentCtx, segmentReq, func(token string) {
				e.mu.Lock()
				live := e.generationLiveLocked(t, revision, segmentCtx)
				if live && token != "" {
					if t.tokenCount == 0 {
						e.traceLocked(t, "llm_first_token", "")
					}
					t.tokenCount++
				}
				e.mu.Unlock()
				if live {
					onToken(token)
				}
			})
		}
		return assembleGeneration(llmCtx, req, limits, prefix, chat, func(progress GenerationProgress) {
			e.mu.Lock()
			if !e.generationLiveLocked(t, revision, llmCtx) {
				e.mu.Unlock()
				return
			}
			t.snapshot.ResponsePlan = responsePlan(progress.CommittedText)
			t.snapshot.Generation = cloneGenerationMetadata(&progress.Metadata)
			if len(t.snapshot.SpeechUnits)+len(progress.SpeechUnits) > 256 {
				e.failLocked(t, "tts_audio_limit")
				e.mu.Unlock()
				return
			}
			tasks := make([]speechTask, 0, len(progress.SpeechUnits))
			for _, text := range progress.SpeechUnits {
				tasks = append(tasks, speechTask{id: e.registerSpeechUnitLocked(t, revision, text), text: text})
			}
			event := e.eventLocked(t, "output")
			snapshot := cloneInteractionSnapshot(t.snapshot)
			event.Snapshot = &snapshot
			e.queueLocked(event)
			if len(progress.SpeechUnits) > 0 && t.snapshot.State == "THINKING" {
				_ = e.transitionLocked(t, "SYNTHESIZING")
				e.traceLocked(t, "tts_requested", "")
			}
			e.mu.Unlock()
			for _, task := range tasks {
				select {
				case speech <- task:
				case <-llmCtx.Done():
					return
				}
			}
		})
	})
	if err != nil {
		e.mu.Lock()
		if e.generationLiveLocked(t, revision, ctx) && response != nil {
			t.snapshot.Generation = cloneGenerationMetadata(response.Generation)
			t.snapshot.ResponsePlan = responsePlan(response.Answer)
		}
		e.mu.Unlock()
		e.stageFailure(t, "llm", err)
		return
	}
	if response == nil || response.Generation == nil || len(response.Answer) > 128<<10 {
		e.stageFailure(t, "llm", errors.New("llm_empty_result"))
		return
	}
	e.mu.Lock()
	if !e.generationLiveLocked(t, revision, ctx) {
		e.mu.Unlock()
		return
	}
	t.snapshot.Generation = cloneGenerationMetadata(response.Generation)
	t.snapshot.ResponsePlan = responsePlan(response.Answer)
	t.generationDone = true
	if response.Generation.Complete {
		if t.tokenCount == 0 {
			e.traceLocked(t, "llm_first_token", "")
		}
		e.traceLocked(t, "llm_completed", "")
	} else {
		e.traceLocked(t, "llm_incomplete", "incomplete_response")
	}
	e.mu.Unlock()
	var spoken speechResult
	select {
	case spoken = <-speechDone:
	case <-ctx.Done():
		return
	}
	if spoken.err != nil {
		e.stageFailure(t, "tts", spoken.err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.generationLiveLocked(t, revision, ctx) {
		return
	}
	t.producerDone = true
	if spoken.units == 0 {
		e.traceLocked(t, "tts_skipped", "")
	} else {
		e.traceLocked(t, "tts_completed", "")
		if t.snapshot.LastAudioSequence == firstSequence {
			e.failLocked(t, "tts_empty_audio")
			return
		}
	}
	e.completeIfPlayedLocked(t)
}
func responsePlan(text string) *ResponsePlan {
	return &ResponsePlan{Text: text, DialogueAct: "answer", Affect: "neutral", VoiceHint: VoiceHint{Pace: 1, Volume: 1}, Interruptible: true}
}
func (e *InteractionEngine) generationLiveLocked(t *interactionTurn, revision int, ctx context.Context) bool {
	return e.liveLocked(t) && t.snapshot.GenerationRevision == revision && ctx.Err() == nil
}
func (e *InteractionEngine) emitGenerationAudio(t *interactionTurn, revision int, ctx context.Context, chunk []byte) error {
	return e.emitSpeechUnitAudio(t, revision, ctx, "", chunk)
}
func (e *InteractionEngine) emitSpeechUnitAudio(t *interactionTurn, revision int, ctx context.Context, unitID string, chunk []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.generationLiveLocked(t, revision, ctx) {
		return context.Canceled
	}
	duration, valid := voiceWAVDuration(chunk, 60)
	if !valid {
		return errors.New("tts_invalid_audio")
	}
	if t.audioBytes+len(chunk) > 16<<20 || len(t.chunks) >= 64 {
		return errors.New("tts_audio_limit")
	}
	if len(t.chunks) == 0 {
		e.traceLocked(t, "tts_first_chunk", "")
	}
	seq := len(t.chunks) + 1
	t.chunks[seq] = &playbackChunk{duration: duration, unitID: unitID}
	for i := range t.snapshot.SpeechUnits {
		if t.snapshot.SpeechUnits[i].UnitID == unitID {
			t.snapshot.SpeechUnits[i].AudioSequences = append(t.snapshot.SpeechUnits[i].AudioSequences, seq)
		}
	}
	t.snapshot.LastAudioSequence = seq
	t.audioBytes += len(chunk)
	event := e.eventLocked(t, "audio")
	event.Sequence = seq
	event.SpeechUnitID = unitID
	event.AudioBase64 = base64.StdEncoding.EncodeToString(chunk)
	e.queueLocked(event)
	e.playbackDeadlineLocked(t)
	return nil
}

// Continue is an explicit bounded generation attempt on the same logical turn.
// A revision fence rejects callbacks and queued events from earlier attempts.
func (e *InteractionEngine) Continue(op string) (InteractionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLocked(op)
	t := e.turns[op]
	if t == nil {
		return InteractionSnapshot{}, errors.New("operation_not_found")
	}
	if e.closed {
		return InteractionSnapshot{}, errors.New("interaction_closed")
	}
	if e.active != "" || t.snapshot.State != "INCOMPLETE" {
		return InteractionSnapshot{}, errors.New("invalid_interaction_transition")
	}
	if len(e.events) > 512 {
		return InteractionSnapshot{}, errors.New("interaction_consumer_slow")
	}
	if t.snapshot.GenerationRevision >= 8 {
		return InteractionSnapshot{}, errors.New("generation_resume_limit")
	}
	prefix := ""
	if t.snapshot.ResponsePlan != nil {
		prefix = t.snapshot.ResponsePlan.Text
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	t.snapshot.GenerationRevision++
	t.snapshot.State = "THINKING"
	t.snapshot.ErrorCode = ""
	t.snapshot.Generation = nil
	t.producerDone = false
	t.generationDone = false
	t.tokenCount = 0
	e.active = op
	event := e.eventLocked(t, "state")
	snapshot := cloneInteractionSnapshot(t.snapshot)
	event.Snapshot = &snapshot
	e.queueLocked(event)
	go e.runGeneration(t, prefix)
	return cloneInteractionSnapshot(t.snapshot), nil
}
func (e *InteractionEngine) stageFailure(t *interactionTurn, stage string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.liveLocked(t) {
		return
	}
	code := stage + "_failed"
	if stage == "llm" {
		if t.snapshot.Generation == nil {
			t.snapshot.Generation = &GenerationMetadata{SchemaVersion: 2, FinishReason: "unknown", ProviderFinishReason: "unknown"}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.snapshot.Generation.FinishReason = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			t.snapshot.Generation.FinishReason = "canceled"
		}
		t.snapshot.Generation.Complete = false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = stage + "_timeout"
	} else {
		if stage == "tts" && knownSpeechError(err.Error()) {
			code = err.Error()
		}
		if stage == "llm" {
			switch err.Error() {
			case "generation_transport_eof":
				code = "llm_transport_eof"
			case "generation_timeout":
				code = "llm_timeout"
			case "generation_canceled":
				code = "llm_canceled"
			}
		}
		// Never forward or persist provider error text. Only this finite set of
		// known stable adapter codes can cross the boundary.
		for _, allowed := range []string{"asr_stream_invalid", "asr_stream_eof", "asr_canceled", "asr_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_empty_transcript", "asr_timeout", "asr_on_device_unavailable", "asr_empty_result", "tts_unavailable", "tts_invalid_audio", "tts_audio_limit", "tts_cleanup_failed", "invalid_speech_text", "llm_empty_result", "llm_transport_eof", "llm_timeout", "llm_canceled", "llm_unknown_finish", "llm_tool_calls", "invalid_audio", "invalid_voice_config"} {
			if err.Error() == allowed {
				code = allowed
				break
			}
		}
	}
	e.failLocked(t, code)
}
func (e *InteractionEngine) playbackDeadlineLocked(t *interactionTurn) {
	t.playbackGeneration++
	generation := t.playbackGeneration
	if t.playbackTimer != nil {
		t.playbackTimer.Stop()
		t.playbackTimer = nil
	}
	t.playbackDeadline = nextPlaybackDeadline(t.chunks, time.Now(), e.Timeouts.Playback)
	if t.playbackDeadline.IsZero() {
		return
	}
	t.playbackTimer = time.AfterFunc(time.Until(t.playbackDeadline), func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.liveLocked(t) && t.playbackGeneration == generation {
			e.failLocked(t, "playback_timeout")
		}
	})
}

// The earliest outstanding chunk owns the deadline．Later production and ACKs
// cannot keep resetting a stalled chunk's clock．Queued chunks get their start
// margin when the preceding chunk stops，and a started chunk gets duration +
// margin measured from its first start ACK．No wall-clock sleep is needed to
// verify these bounds in tests．
func nextPlaybackDeadline(chunks map[int]*playbackChunk, now time.Time, margin time.Duration) time.Time {
	if margin <= 0 {
		margin = time.Millisecond
	}
	if margin > 30*time.Second {
		margin = 30 * time.Second
	}
	var next *playbackChunk
	sequence := 0
	for seq, chunk := range chunks {
		if !chunk.stopped && (next == nil || seq < sequence) {
			next, sequence = chunk, seq
		}
	}
	if next == nil {
		return time.Time{}
	}
	if next.started {
		return next.startedAt.Add(next.duration + margin)
	}
	if next.waitingAt.IsZero() {
		next.waitingAt = now
	}
	return next.waitingAt.Add(margin)
}
func (e *InteractionEngine) Playback(op string, seq int, phase string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil {
		return errors.New("operation_not_found")
	}
	if interactionTerminal(t.snapshot.State) {
		return nil
	}
	c := t.chunks[seq]
	if c == nil {
		return errors.New("unknown_audio_sequence")
	}
	switch phase {
	case "started":
		if c.started {
			return nil
		}
		if t.snapshot.State != "SYNTHESIZING" && t.snapshot.State != "PLAYING" {
			return errors.New("invalid_interaction_transition")
		}
		c.started = true
		c.startedAt = time.Now()
		if t.snapshot.State == "SYNTHESIZING" {
			_ = e.transitionLocked(t, "PLAYING")
		}
		e.traceLocked(t, "audio_play_started", "")
	case "stopped":
		if c.stopped {
			return nil
		}
		if !c.started {
			return errors.New("audio_not_started")
		}
		c.stopped = true
		e.traceLocked(t, "audio_play_stopped", "")
	case "failed":
		e.failLocked(t, "playback_failed")
		return nil
	default:
		return errors.New("invalid_playback_phase")
	}
	e.refreshSpeechUnitsLocked(t)
	e.playbackDeadlineLocked(t)
	e.completeIfPlayedLocked(t)
	return nil
}
func (e *InteractionEngine) completeIfPlayedLocked(t *interactionTurn) {
	if !t.producerDone || !t.generationDone || !e.liveLocked(t) {
		return
	}
	for _, c := range t.chunks {
		if !c.stopped {
			return
		}
	}
	if t.snapshot.Generation != nil && t.snapshot.Generation.Complete {
		e.finishLocked(t, "COMPLETED", "turn_completed", "")
	} else {
		e.finishLocked(t, "INCOMPLETE", "turn_incomplete", "incomplete_response")
	}
}
func (e *InteractionEngine) failLocked(t *interactionTurn, code string) {
	if interactionTerminal(t.snapshot.State) {
		return
	}
	e.finishLocked(t, "FAILED", "turn_failed", code)
}

// Commit durable terminal metadata before exposing the terminal state. A storage
// failure therefore produces one FAILED outcome, never COMPLETED followed by FAILED.
func (e *InteractionEngine) finishLocked(t *interactionTurn, state, name, code string) {
	if state == "COMPLETED" && t.snapshot.State != "PLAYING" && !(t.snapshot.State == "THINKING" && t.generationDone) {
		state, name, code = "FAILED", "turn_failed", "invalid_completion_state"
	}
	trace := e.recordTraceLocked(t, name, code, false)
	if err := e.store.save(t.snapshot.OperationID, t.events); err != nil {
		state, name, code = "FAILED", "turn_failed", "trace_storage_failed"
		t.events = t.events[:len(t.events)-1]
		trace = e.recordTraceLocked(t, name, code, false)
	}
	e.refreshSpeechUnitsForStateLocked(t, state)
	t.snapshot.ErrorCode = code
	event := e.eventLocked(t, "trace")
	event.Trace = trace
	e.queueLocked(event)
	_ = e.transitionLocked(t, state)
	t.cancel()
	if t.asr != nil {
		t.asr.cancel()
		t.asr.pending, t.asr.final = nil, nil
		t.asr.stablePrefix = ""
	}
	if t.playbackTimer != nil {
		t.playbackTimer.Stop()
		t.playbackTimer = nil
	}
	t.playbackDeadline = time.Time{}
	if e.active == t.snapshot.OperationID {
		e.active = ""
	}
}
func (e *InteractionEngine) Cancel(op string) (InteractionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil {
		return InteractionSnapshot{}, errors.New("operation_not_found")
	}
	e.cancelTurnLocked(t)
	return cloneInteractionSnapshot(t.snapshot), nil
}
func (e *InteractionEngine) cancelTurnLocked(t *interactionTurn) {
	if !interactionTerminal(t.snapshot.State) {
		if t.snapshot.Generation == nil {
			t.snapshot.Generation = &GenerationMetadata{SchemaVersion: 2, ProviderFinishReason: "unknown"}
		}
		// A completed generation remains completed when its playback is interrupted．
		if !t.snapshot.Generation.Complete {
			t.snapshot.Generation.FinishReason = "canceled"
		}
		e.traceLocked(t, "cancel_requested", "")
		_ = e.transitionLocked(t, "CANCELING")
		t.cancel()

		e.finishLocked(t, "CANCELED", "cancel_acknowledged", "")
	}
}
func (e *InteractionEngine) Fail(op, code string) error {
	allowed := map[string]bool{"utterance_limit": true, "microphone_unavailable": true, "microphone_permission_denied": true, "microphone_failed": true, "invalid_audio": true, "playback_failed": true, "interrupted": true, "asr_failed": true, "asr_backpressure": true, "asr_protocol_error": true, "asr_timeout": true, "asr_canceled": true, "asr_unavailable": true, "asr_on_device_unavailable": true, "asr_permission_denied": true, "asr_permission_restricted": true, "asr_stream_invalid": true, "asr_stream_eof": true, "asr_empty_transcript": true, "asr_empty_result": true, "invalid_voice_config": true}
	if !allowed[code] {
		return errors.New("invalid_failure_code")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil {
		return errors.New("operation_not_found")
	}
	e.failLocked(t, code)
	return nil
}
func (e *InteractionEngine) expireLocked(op string) {
	if t := e.turns[op]; t != nil && time.Since(t.created) > maxInteractionAge {
		if !interactionTerminal(t.snapshot.State) {
			e.failLocked(t, "operation_expired")
		}
		delete(e.turns, op)
		e.store.expire(op)
	}
}
func (e *InteractionEngine) Snapshot(op string) (InteractionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLocked(op)
	t := e.turns[op]
	if t == nil {
		return InteractionSnapshot{}, errors.New("operation_not_found")
	}
	return cloneInteractionSnapshot(t.snapshot), nil
}
func (e *InteractionEngine) GetRequest(op string) (VoiceTurnRequest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLocked(op)
	t := e.turns[op]
	if t == nil {
		return VoiceTurnRequest{}, errors.New("operation_not_found")
	}
	request := t.request
	if request.Speech.Style != nil {
		style := *request.Speech.Style
		request.Speech.Style = &style
	}
	request.Chat.Tags = append([]string(nil), request.Chat.Tags...)
	request.Chat.Messages = append([]GatewayMessage(nil), request.Chat.Messages...)
	return request, nil
}
func (e *InteractionEngine) Trace(op string) ([]InteractionTraceEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLocked(op)
	if t := e.turns[op]; t != nil {
		return append([]InteractionTraceEvent(nil), t.events...), nil
	}
	return e.store.read(op)
}
func (e *InteractionEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	if e.voiceSession != nil {
		e.voiceSession.cancel()
		e.voiceSession.snapshot.State = "stopped"
	}
	for _, t := range e.turns {
		e.cancelTurnLocked(t)
	}
	close(e.done)
}

func stableVoiceIdentity(value any, provider, model, config string) (string, string, string) {
	if identified, ok := value.(voiceIdentity); ok {
		p, m, c := identified.Identity()
		if interactionIdentifier.MatchString(p) {
			provider = p
		}
		if interactionMetadataID.MatchString(m) {
			model = m
		}
		if interactionIdentifier.MatchString(c) {
			config = c
		}
	}
	return provider, model, config
}
func (e *InteractionEngine) traceLocked(t *interactionTurn, name, code string) {
	e.recordTraceLocked(t, name, code, true)
}
func (e *InteractionEngine) recordTraceLocked(t *interactionTurn, name, code string, publish bool) *InteractionTraceEvent {
	if len(t.events) >= maxInteractionEvents { // Coalesce repeated playback facts; keep every critical event kind.
		seen := map[string]bool{}
		remove := -1
		for i, ev := range t.events {
			if seen[ev.Name] && (ev.Name == "audio_play_started" || ev.Name == "audio_play_stopped") {
				remove = i
				break
			}
			seen[ev.Name] = true
		}
		if remove < 0 {
			remove = 1
		}
		t.events = append(t.events[:remove], t.events[remove+1:]...)
	}
	provider, model, config := "desktop", "interaction", "c02-v3"
	switch {
	case strings.HasPrefix(name, "asr_"):
		provider, model, config = stableVoiceIdentity(e.asr, "asr-adapter", "configured", "default")
	case strings.HasPrefix(name, "tts_"):
		provider, model, config = stableVoiceIdentity(e.tts, "tts-adapter", "configured", "default")
		if t.speech != nil {
			provider, model, config = stableVoiceIdentity(t.speech, provider, model, config)
		}
	case strings.HasPrefix(name, "llm_"):
		provider, model, config = "gateway", "configured", "default"
		if interactionIdentifier.MatchString(t.request.Chat.ProviderID) {
			provider = t.request.Chat.ProviderID
		}
		if interactionMetadataID.MatchString(t.request.Chat.ModelID) {
			model = t.request.Chat.ModelID
		}
		if interactionIdentifier.MatchString(t.request.Chat.ConfigurationID) {
			config = t.request.Chat.ConfigurationID
		}
	}
	s := t.snapshot
	trace := InteractionTraceEvent{SchemaVersion: 3, EventID: interactionID("event_"), TraceID: s.TraceID, SessionID: s.SessionID, TurnID: s.TurnID, OperationID: s.OperationID, Name: name, Source: t.source, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), MonotonicMS: time.Since(t.created).Milliseconds(), Status: s.State, ErrorCode: code, ProviderID: provider, ModelID: model, ConfigurationID: config, Generation: cloneGenerationMetadata(s.Generation), GenerationRevision: s.GenerationRevision}
	if s.InputHandoff != nil {
		timing := *s.InputHandoff
		trace.InputHandoff = &timing
	}
	if s.Interruption != nil {
		timing := *s.Interruption
		trace.Interruption = &timing
	}
	if t.asr != nil && (strings.HasPrefix(name, "asr_") || strings.HasPrefix(name, "turn_") || strings.HasPrefix(name, "cancel_")) {
		trace.ASR = cloneASRMetadata(&t.asr.metadata)
	}
	if name == "turn_completed" {
		trace.Status = "COMPLETED"
	}
	if name == "turn_failed" {
		trace.Status = "FAILED"
	}
	if name == "cancel_acknowledged" {
		trace.Status = "CANCELED"
	}
	if name == "turn_incomplete" {
		trace.Status = "INCOMPLETE"
	}
	t.events = append(t.events, trace)
	if publish {
		ev := e.eventLocked(t, "trace")
		ev.Trace = &trace
		e.queueLocked(ev)
	}
	return &trace
}
