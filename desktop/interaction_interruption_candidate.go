package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A candidate recognizes a short input without changing the responding turn．
// Its hypotheses are transient and never become chat messages or trace payloads．
// Confirmation still uses Interrupt followed by the ordinary，verified input path．
type InterruptionCandidateSnapshot struct {
	CandidateID string            `json:"candidate_id"`
	Request     ASRSessionRequest `json:"request"`
	Update      *ASRUpdate        `json:"update,omitempty"`
	Activity    *ASRUpdate        `json:"activity,omitempty"`
	ErrorCode   string            `json:"error_code,omitempty"`
}

// Fixed interruption measurements only．No transcript，audio or speaker
// identity crosses the telemetry boundary．
type InterruptionTelemetry struct {
	Kind       string `json:"kind"`
	DurationMS int    `json:"duration_ms"`
	Outcome    string `json:"outcome,omitempty"`
}

type interactionInterruptionCandidate struct {
	inputMu         sync.Mutex
	id              string
	revision        int
	request         ASRSessionRequest
	ctx             context.Context
	cancel          context.CancelFunc
	session         VoiceASRSession
	sequence, bytes int
	latest          *ASRUpdate
	activity        *ASRUpdate
	lastUpdate      *ASRUpdate
	errorCode       string
	telemetry       map[string]bool
	closed          bool
}

func candidateResponding(state string) bool {
	return state == "TRANSCRIBING" || state == "THINKING" || state == "SYNTHESIZING" || state == "PLAYING"
}

func (e *InteractionEngine) candidateLiveLocked(t *interactionTurn, c *interactionInterruptionCandidate) bool {
	return e.liveLocked(t) && t.interruptionCandidate == c && !c.closed &&
		t.snapshot.GenerationRevision == c.revision && e.validVoiceSessionLocked(t.request)
}

func (e *InteractionEngine) candidateTraceLocked(t *interactionTurn, reason string) {
	// Repeated background activity cannot exhaust the critical turn event budget．
	if t.interruptionTraceCount < 16 && len(t.events) < maxInteractionEvents-40 {
		t.interruptionTraceCount++
		e.traceLocked(t, "interruption_candidate_"+reason, "")
	}
}

func (e *InteractionEngine) candidateTelemetryTraceLocked(t *interactionTurn, event InterruptionTelemetry) {
	if t.interruptionTraceCount >= 16 || len(t.events) >= maxInteractionEvents-40 {
		return
	}
	t.interruptionTraceCount++
	trace := e.recordTraceLocked(t, "interruption_"+event.Kind, "", false)
	copyForTrace := event
	trace.InterruptionTelemetry = &copyForTrace
	copyForStore := event
	t.events[len(t.events)-1].InterruptionTelemetry = &copyForStore
	published := e.eventLocked(t, "trace")
	published.Trace = trace
	e.queueLocked(published)
}

func candidateSnapshot(c *interactionInterruptionCandidate) InterruptionCandidateSnapshot {
	s := InterruptionCandidateSnapshot{CandidateID: c.id, Request: c.request, ErrorCode: c.errorCode}
	if c.latest != nil {
		u := *c.latest
		s.Update = &u
	}
	if c.activity != nil {
		u := *c.activity
		u.Activity = cloneASRActivity(u.Activity)
		s.Activity = &u
	}
	return s
}

func (e *InteractionEngine) BeginInterruptionCandidate(op, candidateID string, revision, sampleRate int) (InterruptionCandidateSnapshot, error) {
	if !interactionIdentifier.MatchString(candidateID) || sampleRate < 8000 || sampleRate > 48000 {
		return InterruptionCandidateSnapshot{}, errors.New("invalid_audio")
	}
	provider, ok := e.asr.(VoiceStreamingASR)
	if !ok {
		return InterruptionCandidateSnapshot{}, errors.New("asr_unavailable")
	}
	e.mu.Lock()
	t := e.turns[op]
	if t == nil || !e.liveLocked(t) || !candidateResponding(t.snapshot.State) ||
		t.request.InputKind != "microphone" || !e.validVoiceSessionLocked(t.request) || t.snapshot.GenerationRevision != revision ||
		(t.interruptionCandidate != nil && (t.interruptionCandidate.id == candidateID || (!t.interruptionCandidate.closed && t.interruptionCandidate.ctx.Err() == nil))) {
		e.mu.Unlock()
		return InterruptionCandidateSnapshot{}, errors.New("invalid_interaction_transition")
	}
	ctx, cancel := context.WithTimeout(t.ctx, 3*time.Second)
	c := &interactionInterruptionCandidate{id: candidateID, revision: revision, ctx: ctx, cancel: cancel, telemetry: map[string]bool{},
		request: ASRSessionRequest{OperationID: op, SessionID: t.snapshot.SessionID, TurnID: t.snapshot.TurnID,
			SegmentID: interactionID("segment_"), SampleRate: sampleRate}}
	t.interruptionCandidate = c
	e.candidateTraceLocked(t, "started")
	e.mu.Unlock()

	type opened struct {
		session VoiceASRSession
		err     error
	}
	ready := make(chan opened)
	go func() {
		// Share the existing gate．Even a slow previous final or cancellation cannot
		// create two native recognizers or hold the engine mutex during startup．
		select {
		case e.asrSlot <- struct{}{}:
		case <-ctx.Done():
			return
		}
		open := provider.OpenSession
		if candidateProvider, ok := provider.(VoiceInterruptionASR); ok {
			open = candidateProvider.OpenInterruptionSession
		}
		s, err := open(ctx, c.request, func(u ASRUpdate) { e.receiveInterruptionCandidate(t, c, u) })
		if s != nil {
			s = &exclusiveASRSession{VoiceASRSession: s, release: func() { <-e.asrSlot }}
		} else {
			<-e.asrSlot
		}
		select {
		case ready <- opened{s, err}:
		case <-ctx.Done():
			if s != nil {
				s.Cancel()
			}
		}
	}()
	var result opened
	select {
	case result = <-ready:
	case <-ctx.Done():
		result.err = ctx.Err()
	}
	if result.err == nil && result.session == nil {
		result.err = errors.New("asr_unavailable")
	}
	if result.err != nil {
		cancel()
		if result.session != nil {
			go result.session.Cancel()
		}
		e.closeInterruptionCandidate(t, c, "unavailable")
		return InterruptionCandidateSnapshot{}, errors.New(asrStreamError(result.err))
	}
	e.mu.Lock()
	if !e.candidateLiveLocked(t, c) || ctx.Err() != nil {
		e.mu.Unlock()
		cancel()
		go result.session.Cancel()
		e.closeInterruptionCandidate(t, c, "unavailable")
		return InterruptionCandidateSnapshot{}, errors.New("asr_canceled")
	}
	c.session = result.session
	snapshot := candidateSnapshot(c)
	e.mu.Unlock()
	go func() {
		<-ctx.Done()
		result.session.Cancel()
		e.closeInterruptionCandidate(t, c, "expired")
	}()
	return snapshot, nil
}

func (e *InteractionEngine) closeInterruptionCandidate(t *interactionTurn, c *interactionInterruptionCandidate, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c.cancel()
	if c.closed {
		return
	}
	c.closed = true
	c.latest = nil
	c.activity, c.lastUpdate = nil, nil
	if e.liveLocked(t) && t.interruptionCandidate == c {
		e.candidateTraceLocked(t, reason)
	}
}

func (e *InteractionEngine) CancelInterruptionCandidate(op, id, reason string) error {
	switch reason {
	case "confirmed", "noise", "non_target", "acknowledgement", "expired", "unavailable", "detached", "completed":
	default:
		return errors.New("invalid_interruption_candidate")
	}
	e.mu.Lock()
	t := e.turns[op]
	if t == nil || t.interruptionCandidate == nil || t.interruptionCandidate.id != id {
		e.mu.Unlock()
		return nil // An old cleanup must never cancel a replacement candidate．
	}
	c := t.interruptionCandidate
	e.mu.Unlock()
	e.closeInterruptionCandidate(t, c, reason)
	return nil
}

func (e *InteractionEngine) RecordInterruptionTelemetry(op string, revision int, candidateID string, event InterruptionTelemetry) error {
	valid := false
	switch event.Kind {
	case "duck_started":
		valid = event.DurationMS == 0 && event.Outcome == ""
	case "duck_ended":
		valid = event.DurationMS >= 0 && event.DurationMS <= 3000 && (event.Outcome == "confirmed" || event.Outcome == "rejected")
	case "false_duck":
		valid = event.DurationMS >= 0 && event.DurationMS <= 3000 && event.Outcome == "rejected"
	}
	if !valid || !interactionIdentifier.MatchString(candidateID) {
		return errors.New("invalid_interruption_telemetry")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil || t.snapshot.GenerationRevision != revision || t.interruptionCandidate == nil || t.interruptionCandidate.id != candidateID {
		return errors.New("stale_interruption_telemetry")
	}
	c := t.interruptionCandidate
	if c.telemetry[event.Kind] || (event.Kind != "duck_started" && !c.telemetry["duck_started"]) ||
		(event.Kind == "false_duck" && !c.telemetry["duck_ended"]) {
		return errors.New("invalid_interruption_telemetry")
	}
	c.telemetry[event.Kind] = true
	e.candidateTelemetryTraceLocked(t, event)
	if interactionTerminal(t.snapshot.State) {
		if err := e.store.save(t.snapshot.OperationID, t.events); err != nil {
			return errors.New("trace_storage_unavailable")
		}
	}
	return nil
}

func (e *InteractionEngine) AppendInterruptionCandidate(op, id string, sequence int, pcm []byte) (InterruptionCandidateSnapshot, error) {
	e.mu.Lock()
	t := e.turns[op]
	if t == nil || t.interruptionCandidate == nil || t.interruptionCandidate.id != id {
		e.mu.Unlock()
		return InterruptionCandidateSnapshot{}, errors.New("asr_canceled")
	}
	c := t.interruptionCandidate
	e.mu.Unlock()
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
	e.mu.Lock()
	if !e.candidateLiveLocked(t, c) || c.session == nil || c.ctx.Err() != nil {
		e.mu.Unlock()
		return InterruptionCandidateSnapshot{}, errors.New("asr_canceled")
	}
	seconds := 2
	if asrCapabilities(e.asr).Activity {
		seconds = 3
	}
	if sequence != c.sequence+1 || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxASRPCMChunkBytes || c.bytes+len(pcm) > c.request.SampleRate*2*seconds {
		e.mu.Unlock()
		e.closeInterruptionCandidate(t, c, "unavailable")
		return InterruptionCandidateSnapshot{}, errors.New("invalid_audio")
	}
	c.sequence, c.bytes = sequence, c.bytes+len(pcm)
	final := c.latest != nil && c.latest.Phase == "final"
	e.mu.Unlock()
	if !final {
		if err := c.session.Append(c.ctx, sequence, pcm); err != nil {
			e.closeInterruptionCandidate(t, c, "unavailable")
			return InterruptionCandidateSnapshot{}, errors.New(asrStreamError(err))
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.candidateLiveLocked(t, c) || c.ctx.Err() != nil {
		return InterruptionCandidateSnapshot{}, errors.New("asr_canceled")
	}
	return candidateSnapshot(c), nil
}

func (e *InteractionEngine) receiveInterruptionCandidate(t *interactionTurn, c *interactionInterruptionCandidate, u ASRUpdate) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.candidateLiveLocked(t, c) || c.ctx.Err() != nil {
		return
	}
	r := c.request
	if u.OperationID != r.OperationID || u.SessionID != r.SessionID || u.TurnID != r.TurnID || u.SegmentID != r.SegmentID {
		return
	}
	if c.lastUpdate != nil && (u.Revision <= c.lastUpdate.Revision || (c.latest != nil && c.latest.Phase == "final")) {
		return
	}
	valid := u.Revision > 0 && u.Revision <= maxASRRevisions && u.MonotonicMS >= 0 && validASRDiagnostic(u.Diagnostic) &&
		interactionIdentifier.MatchString(u.Provider) && interactionMetadataID.MatchString(u.ModelRevision) &&
		utf8.ValidString(u.Transcript) && utf8.ValidString(u.StablePrefix) && len(u.Transcript) <= 16<<10 &&
		strings.HasPrefix(u.Transcript, u.StablePrefix)
	if c.lastUpdate != nil {
		valid = valid && u.MonotonicMS >= c.lastUpdate.MonotonicMS && u.Provider == c.lastUpdate.Provider &&
			u.ModelRevision == c.lastUpdate.ModelRevision
	}
	switch u.Phase {
	case "activity":
		valid = valid && asrCapabilities(e.asr).Activity && validASRActivity(u.Activity) && u.ErrorCode == "" && u.Transcript == "" && u.StablePrefix == ""
		if valid && c.activity != nil {
			valid = u.Activity.AudioMS >= c.activity.Activity.AudioMS && u.Activity.LastSpeechMS >= c.activity.Activity.LastSpeechMS
		}
	case "partial", "stable", "final":
		valid = valid && u.Activity == nil && u.ErrorCode == "" && (u.Phase != "stable" || u.StablePrefix != "") &&
			(u.Phase != "final" || (strings.TrimSpace(u.Transcript) != "" && u.StablePrefix == u.Transcript))
		if c.latest != nil {
			valid = valid && strings.HasPrefix(u.StablePrefix, c.latest.StablePrefix)
		}
	default:
		valid = false
	}
	if !valid {
		c.errorCode = "asr_failed"
		c.cancel() // Candidate failure never fails or cancels the responding turn．
		return
	}
	if u.Activity != nil {
		u.Activity = cloneASRActivity(u.Activity)
	}
	c.lastUpdate = &u
	if u.Phase == "activity" {
		c.activity = &u
	} else {
		c.latest = &u
	}
}
