package main

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ASRMetadata contains measurements only．No hypothesis crosses the trace boundary．
// Latencies use the Runtime session clock，not the adapter's clock origin．
type ASRMetadata struct {
	Provider        string              `json:"provider,omitempty"`
	ModelRevision   string              `json:"model_revision,omitempty"`
	RevisionCount   int                 `json:"revision_count"`
	CharacterCount  int                 `json:"character_count"`
	FirstAudioMS    *int64              `json:"first_audio_ms,omitempty"`
	FirstPartialMS  *int64              `json:"first_partial_ms,omitempty"`
	FirstStableMS   *int64              `json:"first_stable_ms,omitempty"`
	FinalMS         *int64              `json:"final_ms,omitempty"`
	FinalizationMS  *int64              `json:"finalization_ms,omitempty"`
	InputSampleRate int                 `json:"input_sample_rate,omitempty"`
	InputSamples    int                 `json:"input_samples,omitempty"`
	ClippedSamples  int                 `json:"clipped_samples,omitempty"`
	VADAudioMS      int                 `json:"vad_audio_ms,omitempty"`
	VADSpeechMS     int                 `json:"vad_speech_ms,omitempty"`
	VADLastSpeechMS int                 `json:"vad_last_speech_ms,omitempty"`
	Diagnostic      *ASRDiagnostic      `json:"diagnostic,omitempty"`
	Capture         *ASRCaptureMetadata `json:"capture,omitempty"`
	EndpointReason  string              `json:"endpoint_reason,omitempty"`
}

// Actual browser-reported settings only．Device names and IDs are excluded．
type ASRCaptureMetadata struct {
	ContextSampleRate int   `json:"context_sample_rate"`
	TrackSampleRate   int   `json:"track_sample_rate,omitempty"`
	ChannelCount      int   `json:"channel_count,omitempty"`
	EchoCancellation  *bool `json:"echo_cancellation,omitempty"`
	NoiseSuppression  *bool `json:"noise_suppression,omitempty"`
	AutoGainControl   *bool `json:"auto_gain_control,omitempty"`
}

func cloneASRCapture(m *ASRCaptureMetadata) *ASRCaptureMetadata {
	if m == nil {
		return nil
	}
	c := *m
	copyBool := func(b *bool) *bool {
		if b == nil {
			return nil
		}
		v := *b
		return &v
	}
	c.EchoCancellation = copyBool(m.EchoCancellation)
	c.NoiseSuppression = copyBool(m.NoiseSuppression)
	c.AutoGainControl = copyBool(m.AutoGainControl)
	return &c
}
func (e *InteractionEngine) RecordASRCapture(op string, m ASRCaptureMetadata) error {
	if m.ContextSampleRate < 8000 || m.ContextSampleRate > 192000 || (m.TrackSampleRate != 0 && (m.TrackSampleRate < 8000 || m.TrackSampleRate > 192000)) || m.ChannelCount < 0 || m.ChannelCount > 32 {
		return errors.New("invalid_audio")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil || !e.liveLocked(t) || t.asr == nil || t.asr.metadata.Capture != nil {
		return errors.New("asr_canceled")
	}
	t.asr.metadata.Capture = cloneASRCapture(&m)
	return nil
}

func cloneASRMetadata(v *ASRMetadata) *ASRMetadata {
	if v == nil {
		return nil
	}
	c := *v
	copyMS := func(p *int64) *int64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	c.FirstAudioMS, c.FirstPartialMS, c.FirstStableMS = copyMS(v.FirstAudioMS), copyMS(v.FirstPartialMS), copyMS(v.FirstStableMS)
	c.FinalMS, c.FinalizationMS = copyMS(v.FinalMS), copyMS(v.FinalizationMS)
	c.Capture = cloneASRCapture(v.Capture)
	if v.Diagnostic != nil {
		d := *v.Diagnostic
		c.Diagnostic = &d
	}
	return &c
}

type interactionASRSession struct {
	inputMu                       sync.Mutex
	request                       ASRSessionRequest
	session                       VoiceASRSession
	ctx                           context.Context
	cancel                        context.CancelFunc
	started, ended                time.Time
	sequence, bytes, lastRevision int
	lastMonotonicMS               int64
	stablePrefix                  string
	final                         *ASRUpdate
	pending                       *ASRUpdate
	pendingActivity               *ASRUpdate
	activityMode, speechObserved  bool
	completed                     bool
	metadata                      ASRMetadata
}

// A canceled ASR must release its provider session before another one opens．
// Input remains in the finite frontend handoff queue while this gate is held．
// This gate never waits for cancellation of LLM or TTS providers．
type exclusiveASRSession struct {
	VoiceASRSession
	once    sync.Once
	release func()
}

func (s *exclusiveASRSession) Cancel() {
	s.once.Do(func() { defer s.release(); s.VoiceASRSession.Cancel() })
}

func (e *InteractionEngine) BeginASR(op string, sampleRate int) (ASRSessionRequest, error) {
	if sampleRate < 8000 || sampleRate > 48000 {
		return ASRSessionRequest{}, errors.New("invalid_audio")
	}
	provider, ok := e.asr.(VoiceStreamingASR)
	if !ok {
		return ASRSessionRequest{}, errors.New("asr_unavailable")
	}
	e.mu.Lock()
	t := e.turns[op]
	if t == nil || !e.liveLocked(t) || t.snapshot.State != "RECORDING" || t.source != "microphone" || t.asr != nil {
		e.mu.Unlock()
		return ASRSessionRequest{}, errors.New("invalid_interaction_transition")
	}
	// Setup／permission waiting，60 s PCM，draining and finalization each remain
	// bounded．The outer guard leaves room for those phases without shortening
	// valid input merely because microphone permission took time．
	parent := t.ctx
	if t.request.VoiceSessionID != "" {
		if !e.validVoiceSessionLocked(t.request) {
			e.mu.Unlock()
			return ASRSessionRequest{}, errors.New("asr_canceled")
		}
		parent = e.voiceSession.ctx
	}
	ctx, cancel := context.WithTimeout(parent, 125*time.Second)
	a := &interactionASRSession{ctx: ctx, cancel: cancel, started: time.Now(), lastMonotonicMS: -1, activityMode: asrCapabilities(e.asr).Activity,
		request: ASRSessionRequest{OperationID: op, SessionID: t.snapshot.SessionID, TurnID: t.snapshot.TurnID, SegmentID: interactionID("segment_"), SampleRate: sampleRate}}
	t.asr = a
	e.traceLocked(t, "asr_started", "")
	e.mu.Unlock()
	// An adapter's startup cannot hold a bridge call indefinitely．A late session
	// is canceled without attaching it to a newer operation．
	type opened struct {
		session VoiceASRSession
		err     error
	}
	ready := make(chan opened)
	go func() {
		select {
		case e.asrSlot <- struct{}{}:
		case <-ctx.Done():
			return
		}
		s, err := provider.OpenSession(ctx, a.request, func(update ASRUpdate) { e.receiveASR(t, a, update) })
		if s != nil {
			s = &exclusiveASRSession{VoiceASRSession: s, release: func() { <-e.asrSlot }}
		} else {
			<-e.asrSlot
		}
		// Unbuffered handoff makes ownership explicit even if startup and cancel
		// finish simultaneously．Exactly one side disposes of the session．
		select {
		case ready <- opened{s, err}:
		case <-ctx.Done():
			if s != nil {
				s.Cancel()
			}
		}
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var result opened
	select {
	case result = <-ready:
	case <-ctx.Done():
		result.err = ctx.Err()
	case <-timer.C:
		result.err = context.DeadlineExceeded
	}
	if result.err == nil && result.session == nil {
		result.err = errors.New("asr_failed")
	}
	if result.err != nil {
		cancel()
		e.stageFailure(t, "asr", result.err)
		if result.session != nil {
			go result.session.Cancel()
		}
		return ASRSessionRequest{}, errors.New(asrStreamError(result.err))
	}
	e.mu.Lock()
	if !e.liveLocked(t) || a.ctx.Err() != nil {
		e.mu.Unlock()
		go result.session.Cancel()
		return ASRSessionRequest{}, errors.New("asr_canceled")
	}
	a.session = result.session
	e.mu.Unlock()
	go func() {
		<-ctx.Done()
		e.mu.Lock()
		if !a.completed && e.liveLocked(t) {
			e.failLocked(t, asrStreamError(ctx.Err()))
		}
		e.mu.Unlock()
		result.session.Cancel()
	}()
	return a.request, nil
}

func (e *InteractionEngine) asrForInput(op string) (*interactionTurn, *interactionASRSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil || t.asr == nil || t.asr.session == nil || !e.liveLocked(t) {
		return nil, nil, errors.New("asr_canceled")
	}
	return t, t.asr, nil
}

func (e *InteractionEngine) AppendASRAudio(op string, sequence int, pcm []byte) error {
	t, a, err := e.asrForInput(op)
	if err != nil {
		return err
	}
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	e.mu.Lock()
	if !e.liveLocked(t) || t.snapshot.State != "RECORDING" || !a.ended.IsZero() {
		e.mu.Unlock()
		return errors.New("asr_canceled")
	}
	if sequence != a.sequence+1 || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxASRPCMChunkBytes || a.bytes+len(pcm) > maxASRPCMBytes || a.bytes+len(pcm) > a.request.SampleRate*2*60 {
		e.failLocked(t, "invalid_audio")
		e.mu.Unlock()
		return errors.New("invalid_audio")
	}
	a.sequence, a.bytes = sequence, a.bytes+len(pcm)
	a.metadata.InputSampleRate = a.request.SampleRate
	a.metadata.InputSamples += len(pcm) / 2
	for offset := 0; offset < len(pcm); offset += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
		if sample >= 32760 || sample <= -32760 {
			a.metadata.ClippedSamples++
		}
	}
	if a.metadata.FirstAudioMS == nil {
		if t.request.VoiceSessionID != "" && !a.activityMode {
			e.traceLocked(t, "user_speech_start", "")
		}
		ms := time.Since(a.started).Milliseconds()
		a.metadata.FirstAudioMS = &ms
	}
	alreadyFinal := a.final != nil
	e.mu.Unlock()
	if alreadyFinal {
		return nil
	}
	_, err = runInteractionStage(a.ctx, 5*time.Second, func(ctx context.Context) (bool, error) {
		err := a.session.Append(ctx, sequence, pcm)
		return err == nil, err
	})
	if err != nil {
		e.stageFailure(t, "asr", err)
		return errors.New(asrStreamError(err))
	}
	return nil
}

func (e *InteractionEngine) EndASR(op string) error {
	return e.EndASRWithReason(op, "manual")
}
func (e *InteractionEngine) EndASRWithReason(op, reason string) error {
	switch reason {
	case "manual", "silence", "provider_final", "idle_refresh", "duration_limit":
	default:
		return errors.New("invalid_audio")
	}
	t, a, err := e.asrForInput(op)
	if err != nil {
		return err
	}
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	e.mu.Lock()
	if !e.liveLocked(t) || t.snapshot.State != "RECORDING" || !a.ended.IsZero() {
		e.mu.Unlock()
		return errors.New("invalid_interaction_transition")
	}
	if a.bytes == 0 {
		e.failLocked(t, "invalid_audio")
		e.mu.Unlock()
		return errors.New("invalid_audio")
	}
	if t.request.VoiceSessionID != "" && e.validVoiceSessionLocked(t.request) {
		e.voiceSession.snapshot.State = "responding"
	}
	a.ended = time.Now()
	a.metadata.EndpointReason = reason
	e.traceLocked(t, "user_speech_end", "")
	e.traceLocked(t, "endpoint_commit", "")
	_ = e.transitionLocked(t, "TRANSCRIBING")
	e.mu.Unlock()
	go e.finishASR(t, a)
	return nil
}

func (e *InteractionEngine) finishASR(t *interactionTurn, a *interactionASRSession) {
	timeout := e.Timeouts.ASR
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	final, err := runInteractionStage(a.ctx, timeout, a.session.Finish)
	if err != nil {
		e.stageFailure(t, "asr", err)
		return
	}
	// Some adapters return the final only from Finish．Others also publish it for
	// live display．Revisions are accepted once，and these two finals must agree．
	e.receiveASR(t, a, final)
	e.mu.Lock()
	if !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	if a.final == nil || !sameASRFinal(final, *a.final) || (final.Phase != "final" && final.Phase != "no_speech") {
		e.failLocked(t, "asr_stream_invalid")
		e.mu.Unlock()
		return
	}
	ms := time.Since(a.ended).Milliseconds()
	a.metadata.FinalizationMS = &ms
	a.completed = true
	a.cancel()
	if final.Phase == "no_speech" {
		t.snapshot.InputOutcome = "no_speech"
		if e.validVoiceSessionLocked(t.request) && e.voiceSession != nil {
			e.voiceSession.snapshot.State = "listening"
		}
		_ = e.transitionLocked(t, "CANCELING")
		e.finishLocked(t, "CANCELED", "asr_no_speech", "")
		e.mu.Unlock()
		return
	}
	text := final.Transcript
	e.mu.Unlock()
	e.acceptTranscript(t, text)
}

func sameASRFinal(a, b ASRUpdate) bool {
	if (a.Diagnostic == nil) != (b.Diagnostic == nil) {
		return false
	}
	if a.Diagnostic != nil && *a.Diagnostic != *b.Diagnostic {
		return false
	}
	a.Diagnostic, b.Diagnostic = nil, nil
	return a == b
}

func asrStreamError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "asr_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "asr_timeout"
	}
	if err != nil {
		switch err.Error() {
		case "asr_stream_invalid", "asr_stream_eof", "asr_timeout", "asr_canceled", "asr_unavailable", "asr_on_device_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_empty_result", "asr_empty_transcript", "invalid_audio", "invalid_voice_config", "asr_model_missing", "asr_model_mismatch", "asr_model_load_failed", "asr_loading", "asr_worker_exited", "asr_busy":
			return err.Error()
		}
	}
	return "asr_failed"
}

func (e *InteractionEngine) receiveASR(t *interactionTurn, a *interactionASRSession, update ASRUpdate) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.liveLocked(t) || t.asr != a || a.completed || (t.snapshot.State != "RECORDING" && t.snapshot.State != "TRANSCRIBING") {
		return
	}
	r := a.request
	// Late callbacks from another identity，or older revisions，are discarded．
	if update.OperationID != r.OperationID || update.SessionID != r.SessionID || update.TurnID != r.TurnID || update.SegmentID != r.SegmentID || update.Revision <= a.lastRevision {
		return
	}
	if a.final != nil {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	if !validASRDiagnostic(update.Diagnostic) || (update.Diagnostic != nil && (update.Provider != "macos-speech" || (update.Phase != "failure" && update.Phase != "final"))) {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	if update.Revision > maxASRRevisions || update.MonotonicMS < a.lastMonotonicMS || update.MonotonicMS < 0 || !interactionIdentifier.MatchString(update.Provider) || !interactionMetadataID.MatchString(update.ModelRevision) || !utf8.ValidString(update.Transcript) || !utf8.ValidString(update.StablePrefix) || len(update.Transcript) > 16<<10 || !strings.HasPrefix(update.Transcript, update.StablePrefix) {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	if a.metadata.Provider != "" && (a.metadata.Provider != update.Provider || a.metadata.ModelRevision != update.ModelRevision) {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	switch update.Phase {
	case "activity":
		if !a.activityMode || !validASRActivity(update.Activity) || update.Activity.AudioMS < a.metadata.VADAudioMS || update.Activity.SpeechMS < a.metadata.VADSpeechMS || update.Activity.LastSpeechMS < a.metadata.VADLastSpeechMS ||
			update.Activity.AudioMS > a.metadata.InputSamples*1000/a.request.SampleRate+32 || (a.speechObserved && !update.Activity.HasSpeech) || update.Transcript != "" || update.StablePrefix != "" || update.ErrorCode != "" {
			e.failLocked(t, "asr_stream_invalid")
			return
		}
	case "no_speech":
		if !asrCapabilities(e.asr).NoSpeech || a.ended.IsZero() || update.Transcript != "" || update.StablePrefix != "" || update.ErrorCode != "" || update.Activity != nil {
			e.failLocked(t, "asr_stream_invalid")
			return
		}
	case "partial", "stable", "final":
		if update.Activity != nil || update.ErrorCode != "" || (update.Phase == "final" && (strings.TrimSpace(update.Transcript) == "" || update.StablePrefix != update.Transcript)) || (update.Phase == "stable" && update.StablePrefix == "") || !strings.HasPrefix(update.StablePrefix, a.stablePrefix) {
			e.failLocked(t, "asr_stream_invalid")
			return
		}
	case "failure", "timeout", "canceled":
		if update.Transcript != "" || update.StablePrefix != "" || update.Activity != nil {
			e.failLocked(t, "asr_stream_invalid")
			return
		}
	default:
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	a.lastRevision, a.lastMonotonicMS = update.Revision, update.MonotonicMS
	a.metadata.Provider, a.metadata.ModelRevision = update.Provider, update.ModelRevision
	a.metadata.RevisionCount++
	if update.Diagnostic != nil {
		d := *update.Diagnostic
		update.Diagnostic = &d
		a.metadata.Diagnostic = &d
	}
	if update.Phase == "activity" {
		activity := *update.Activity
		update.Activity = &activity
		a.metadata.VADAudioMS = activity.AudioMS
		a.metadata.VADSpeechMS = activity.SpeechMS
		a.metadata.VADLastSpeechMS = activity.LastSpeechMS
		if activity.HasSpeech && !a.speechObserved {
			a.speechObserved = true
			if t.request.VoiceSessionID != "" {
				e.traceLocked(t, "user_speech_start", "")
			}
		}
		a.pendingActivity = &update
		select {
		case e.asrWake <- struct{}{}:
		default:
		}
		return
	}
	a.metadata.CharacterCount = utf8.RuneCountInString(update.Transcript)
	a.stablePrefix = update.StablePrefix
	ms := time.Since(a.started).Milliseconds()
	if update.Phase == "partial" && strings.TrimSpace(update.Transcript) != "" && a.metadata.FirstPartialMS == nil {
		a.metadata.FirstPartialMS = &ms
		e.traceLocked(t, "asr_first_partial", "")
	}
	if (update.StablePrefix != "" || update.Phase == "final") && a.metadata.FirstStableMS == nil {
		a.metadata.FirstStableMS = &ms
		e.traceLocked(t, "asr_first_stable", "")
	}
	if update.Phase == "final" || update.Phase == "no_speech" {
		a.final = &update
		a.metadata.FinalMS = &ms
	}
	if update.Phase == "failure" || update.Phase == "timeout" || update.Phase == "canceled" {
		code := asrStreamError(errors.New(update.ErrorCode))
		if update.Phase == "timeout" {
			code = "asr_timeout"
		}
		if update.Phase == "canceled" {
			code = "asr_canceled"
		}
		e.failLocked(t, code)
		return
	}
	// One replaceable slot per live operation keeps 2048 revisions from filling
	// the critical event queue．Final is sealed and can never be overwritten．
	a.pending = &update
	select {
	case e.asrWake <- struct{}{}:
	default:
	}
}

func (e *InteractionEngine) dispatchASR() {
	e.mu.Lock()
	t := e.turns[e.active]
	if t == nil || t.asr == nil || (t.asr.pending == nil && t.asr.pendingActivity == nil) || !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	var update ASRUpdate
	if t.asr.pendingActivity != nil && (t.asr.pending == nil || t.asr.pendingActivity.Revision < t.asr.pending.Revision) {
		update = *t.asr.pendingActivity
		t.asr.pendingActivity = nil
	} else {
		update = *t.asr.pending
		t.asr.pending = nil
	}
	if t.asr.pending != nil || t.asr.pendingActivity != nil {
		select {
		case e.asrWake <- struct{}{}:
		default:
		}
	}
	event := e.eventLocked(t, "asr_update")
	event.ASR = &update
	e.mu.Unlock()
	if e.emit != nil {
		e.emit(event)
	}
}
