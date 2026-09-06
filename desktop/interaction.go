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
	SessionID string      `json:"session_id"`
	InputKind string      `json:"input_kind,omitempty"`
	Chat      ChatRequest `json:"chat"`
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
	TraceID      string        `json:"trace_id"`
	SessionID    string        `json:"session_id"`
	TurnID       string        `json:"turn_id"`
	OperationID  string        `json:"operation_id"`
	State        string        `json:"state"`
	Transcript   string        `json:"transcript,omitempty"`
	ResponsePlan *ResponsePlan `json:"response_plan,omitempty"`
	ErrorCode    string        `json:"error_code,omitempty"`
}
type InteractionEvent struct {
	Kind        string                 `json:"kind"`
	TraceID     string                 `json:"trace_id"`
	SessionID   string                 `json:"session_id"`
	TurnID      string                 `json:"turn_id"`
	OperationID string                 `json:"operation_id"`
	Snapshot    *InteractionSnapshot   `json:"snapshot,omitempty"`
	Text        string                 `json:"text,omitempty"`
	AudioBase64 string                 `json:"audio_base64,omitempty"`
	Sequence    int                    `json:"sequence,omitempty"`
	Trace       *InteractionTraceEvent `json:"trace,omitempty"`
}
type InteractionTimeouts struct{ ASR, LLM, TTS, Playback time.Duration }
type playbackChunk struct{ started, stopped bool }
type interactionTurn struct {
	snapshot               InteractionSnapshot
	request                VoiceTurnRequest
	ctx                    context.Context
	cancel                 context.CancelFunc
	created                time.Time
	events                 []InteractionTraceEvent
	chunks                 map[int]*playbackChunk
	producerDone           bool
	playbackTimer          *time.Timer
	playbackGeneration     uint64
	tokenCount, audioBytes int
	source                 string
}
type InteractionEngine struct {
	mu     sync.Mutex
	asr    VoiceASR
	tts    VoiceTTS
	chat   func(context.Context, ChatRequest, func(string)) (*ChatResponse, error)
	emit   func(InteractionEvent)
	turns  map[string]*interactionTurn
	active string
	closed bool
	events chan InteractionEvent
	done   chan struct{}
	store  *interactionTraceStore
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
	return state == "COMPLETED" || state == "CANCELED" || state == "FAILED"
}
func (e *InteractionEngine) dispatch() {
	for {
		select {
		case <-e.done:
			return
		case event := <-e.events:
			e.mu.Lock()
			turn := e.turns[event.OperationID]
			live := turn != nil && !interactionTerminal(turn.snapshot.State)
			e.mu.Unlock()
			if (event.Kind == "audio" || event.Kind == "token" || event.Kind == "transcript") && !live {
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
	return InteractionEvent{Kind: kind, TraceID: s.TraceID, SessionID: s.SessionID, TurnID: s.TurnID, OperationID: s.OperationID}
}

// A turn emits at most 128 traces, 128 token previews, 64 audio chunks and 16
// state/text events. Start reserves 512 queue slots, so producers never block
// under the state mutex, including when the UI calls Playback synchronously.
func (e *InteractionEngine) queueLocked(event InteractionEvent) { e.events <- event }
func cloneInteractionSnapshot(s InteractionSnapshot) InteractionSnapshot {
	if s.ResponsePlan != nil {
		p := *s.ResponsePlan
		s.ResponsePlan = &p
	}
	return s
}
func (e *InteractionEngine) transitionLocked(t *interactionTurn, state string) error {
	old := t.snapshot.State
	allowed := map[string]string{"IDLE": "RECORDING", "RECORDING": "TRANSCRIBING", "TRANSCRIBING": "THINKING", "THINKING": "SYNTHESIZING", "SYNTHESIZING": "PLAYING", "PLAYING": "COMPLETED", "CANCELING": "CANCELED"}
	if interactionTerminal(old) || (allowed[old] != state && state != "CANCELING" && state != "FAILED") {
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
	t := &interactionTurn{snapshot: InteractionSnapshot{TraceID: interactionID("trace_"), SessionID: request.SessionID, TurnID: interactionID("turn_"), OperationID: op, State: "IDLE"}, request: request, ctx: ctx, cancel: cancel, created: time.Now(), chunks: map[int]*playbackChunk{}, source: request.InputKind}
	e.turns[op] = t
	e.active = op
	e.traceLocked(t, "user_speech_start", "")
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
	if t.snapshot.State != "RECORDING" {
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
	e.traceLocked(t, "llm_requested", "")
	req := t.request.Chat
	requestConfigurationID := req.ConfigurationID
	e.mu.Unlock()
	response, err := runInteractionStage(t.ctx, e.Timeouts.LLM, func(ctx context.Context) (*ChatResponse, error) {
		ctx = context.WithValue(ctx, interactionModelKey{}, func(provider, model, configuration string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if !e.liveLocked(t) || ctx.Err() != nil {
				return
			}
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
		})
		return e.chat(ctx, req, func(token string) {
			if token == "" {
				return
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if !e.liveLocked(t) || t.snapshot.State != "THINKING" || ctx.Err() != nil {
				return
			}
			if t.tokenCount == 0 {
				e.traceLocked(t, "llm_first_token", "")
			}
			t.tokenCount++
			if t.tokenCount <= 128 {
				ev := e.eventLocked(t, "token")
				if len(token) > 4096 {
					token = token[:4096]
				}
				ev.Text = token
				e.queueLocked(ev)
			}
		})
	})
	if err != nil {
		e.stageFailure(t, "llm", err)
		return
	}
	if response == nil || strings.TrimSpace(response.Answer) == "" || len(response.Answer) > 128<<10 {
		e.stageFailure(t, "llm", errors.New("llm_empty_result"))
		return
	}
	e.mu.Lock()
	if !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	if t.tokenCount == 0 {
		e.traceLocked(t, "llm_first_token", "")
	}
	e.traceLocked(t, "llm_completed", "")
	t.snapshot.ResponsePlan = &ResponsePlan{Text: response.Answer, DialogueAct: "answer", Affect: "neutral", VoiceHint: VoiceHint{Pace: 1, Volume: 1}, Interruptible: true}
	_ = e.transitionLocked(t, "SYNTHESIZING")
	e.traceLocked(t, "tts_requested", "")
	e.mu.Unlock()
	_, err = runInteractionStage(t.ctx, e.Timeouts.TTS, func(ctx context.Context) (bool, error) {
		if err := e.tts.Ready(ctx); err != nil {
			return false, err
		}
		err := e.tts.Stream(ctx, response.Answer, func(chunk []byte) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if !e.liveLocked(t) || ctx.Err() != nil {
				return context.Canceled
			}
			if len(chunk) < 12 || string(chunk[:4]) != "RIFF" || string(chunk[8:12]) != "WAVE" {
				return errors.New("tts_invalid_audio")
			}
			if len(chunk) > 2<<20 || t.audioBytes+len(chunk) > 16<<20 || len(t.chunks) >= 64 {
				return errors.New("tts_audio_limit")
			}
			if len(t.chunks) == 0 {
				e.traceLocked(t, "tts_first_chunk", "")
			}
			seq := len(t.chunks) + 1
			t.chunks[seq] = &playbackChunk{}
			t.audioBytes += len(chunk)
			ev := e.eventLocked(t, "audio")
			ev.Sequence = seq
			ev.AudioBase64 = base64.StdEncoding.EncodeToString(chunk)
			e.queueLocked(ev)
			e.playbackDeadlineLocked(t)
			return nil
		})
		return err == nil, err
	})
	if err != nil {
		e.stageFailure(t, "tts", err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.liveLocked(t) {
		return
	}
	t.producerDone = true
	e.traceLocked(t, "tts_completed", "")
	if len(t.chunks) == 0 {
		e.failLocked(t, "tts_empty_audio")
		return
	}
	e.completeIfPlayedLocked(t)
}
func (e *InteractionEngine) stageFailure(t *interactionTurn, stage string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.liveLocked(t) {
		return
	}
	code := stage + "_failed"
	if errors.Is(err, context.DeadlineExceeded) {
		code = stage + "_timeout"
	} else {
		// Never forward or persist provider error text. Only this finite set of
		// known stable adapter codes can cross the boundary.
		for _, allowed := range []string{"asr_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_empty_transcript", "asr_timeout", "asr_on_device_unavailable", "asr_empty_result", "tts_unavailable", "tts_invalid_audio", "tts_audio_limit", "tts_cleanup_failed", "invalid_speech_text", "llm_empty_result", "invalid_audio", "invalid_voice_config"} {
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
	pending := false
	for _, c := range t.chunks {
		if !c.stopped {
			pending = true
			break
		}
	}
	if t.playbackTimer != nil {
		t.playbackTimer.Stop()
		t.playbackTimer = nil
	}
	if !pending {
		return
	}
	timeout := e.Timeouts.Playback
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	t.playbackTimer = time.AfterFunc(timeout, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.liveLocked(t) && t.playbackGeneration == generation {
			e.failLocked(t, "playback_timeout")
		}
	})
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
	e.playbackDeadlineLocked(t)
	e.completeIfPlayedLocked(t)
	return nil
}
func (e *InteractionEngine) completeIfPlayedLocked(t *interactionTurn) {
	if !t.producerDone || !e.liveLocked(t) {
		return
	}
	for _, c := range t.chunks {
		if !c.stopped {
			return
		}
	}
	if t.snapshot.State != "PLAYING" {
		return
	}
	e.finishLocked(t, "COMPLETED", "turn_completed", "")
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
	trace := e.recordTraceLocked(t, name, code, false)
	if err := e.store.save(t.snapshot.OperationID, t.events); err != nil {
		state, name, code = "FAILED", "turn_failed", "trace_storage_failed"
		t.events = t.events[:len(t.events)-1]
		trace = e.recordTraceLocked(t, name, code, false)
	}
	t.snapshot.ErrorCode = code
	event := e.eventLocked(t, "trace")
	event.Trace = trace
	e.queueLocked(event)
	_ = e.transitionLocked(t, state)
	t.cancel()
	if t.playbackTimer != nil {
		t.playbackTimer.Stop()
		t.playbackTimer = nil
	}
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
	if !interactionTerminal(t.snapshot.State) {
		e.traceLocked(t, "cancel_requested", "")
		_ = e.transitionLocked(t, "CANCELING")
		t.cancel()
		for _, chunk := range t.chunks {
			if chunk.started && !chunk.stopped {
				chunk.stopped = true
				e.traceLocked(t, "audio_play_stopped", "interrupted")
			}
		}
		e.finishLocked(t, "CANCELED", "cancel_acknowledged", "")
	}
	return cloneInteractionSnapshot(t.snapshot), nil
}
func (e *InteractionEngine) Fail(op, code string) error {
	allowed := map[string]bool{"microphone_unavailable": true, "microphone_permission_denied": true, "microphone_failed": true, "invalid_audio": true, "playback_failed": true, "interrupted": true}
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
	for _, t := range e.turns {
		if !interactionTerminal(t.snapshot.State) {
			e.traceLocked(t, "cancel_requested", "")
			_ = e.transitionLocked(t, "CANCELING")
			t.cancel()
			for _, chunk := range t.chunks {
				if chunk.started && !chunk.stopped {
					chunk.stopped = true
					e.traceLocked(t, "audio_play_stopped", "interrupted")
				}
			}
			e.finishLocked(t, "CANCELED", "cancel_acknowledged", "")
		}
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
	provider, model, config := "desktop", "interaction", "c0-v1"
	switch {
	case strings.HasPrefix(name, "asr_"):
		provider, model, config = stableVoiceIdentity(e.asr, "native-asr", "macOS", "on-device")
	case strings.HasPrefix(name, "tts_"):
		provider, model, config = stableVoiceIdentity(e.tts, "native-tts", "Kyoko", "default")
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
	trace := InteractionTraceEvent{SchemaVersion: 1, EventID: interactionID("event_"), TraceID: s.TraceID, SessionID: s.SessionID, TurnID: s.TurnID, OperationID: s.OperationID, Name: name, Source: t.source, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), MonotonicMS: time.Since(t.created).Milliseconds(), Status: s.State, ErrorCode: code, ProviderID: provider, ModelID: model, ConfigurationID: config}
	if name == "turn_completed" {
		trace.Status = "COMPLETED"
	}
	if name == "turn_failed" {
		trace.Status = "FAILED"
	}
	if name == "cancel_acknowledged" {
		trace.Status = "CANCELED"
	}
	t.events = append(t.events, trace)
	if publish {
		ev := e.eventLocked(t, "trace")
		ev.Trace = &trace
		e.queueLocked(ev)
	}
	return &trace
}
