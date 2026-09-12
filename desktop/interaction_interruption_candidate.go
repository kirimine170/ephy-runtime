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
	ErrorCode   string            `json:"error_code,omitempty"`
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
	errorCode       string
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

func candidateSnapshot(c *interactionInterruptionCandidate) InterruptionCandidateSnapshot {
	s := InterruptionCandidateSnapshot{CandidateID: c.id, Request: c.request, ErrorCode: c.errorCode}
	if c.latest != nil {
		u := *c.latest
		s.Update = &u
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
		t.request.VoiceSessionID == "" || !e.validVoiceSessionLocked(t.request) || t.snapshot.GenerationRevision != revision ||
		(t.interruptionCandidate != nil && (t.interruptionCandidate.id == candidateID || (!t.interruptionCandidate.closed && t.interruptionCandidate.ctx.Err() == nil))) {
		e.mu.Unlock()
		return InterruptionCandidateSnapshot{}, errors.New("invalid_interaction_transition")
	}
	ctx, cancel := context.WithTimeout(t.ctx, 3*time.Second)
	c := &interactionInterruptionCandidate{id: candidateID, revision: revision, ctx: ctx, cancel: cancel,
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
		s, err := provider.OpenSession(ctx, c.request, func(u ASRUpdate) { e.receiveInterruptionCandidate(t, c, u) })
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
	if e.liveLocked(t) && t.interruptionCandidate == c {
		e.candidateTraceLocked(t, reason)
	}
}

func (e *InteractionEngine) CancelInterruptionCandidate(op, id, reason string) error {
	switch reason {
	case "confirmed", "noise", "acknowledgement", "expired", "unavailable", "detached", "completed":
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
	if sequence != c.sequence+1 || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxASRPCMChunkBytes || c.bytes+len(pcm) > c.request.SampleRate*2*2 {
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
	if c.latest != nil && (u.Revision <= c.latest.Revision || c.latest.Phase == "final") {
		return
	}
	valid := u.Revision > 0 && u.Revision <= maxASRRevisions && u.MonotonicMS >= 0 &&
		interactionIdentifier.MatchString(u.Provider) && interactionMetadataID.MatchString(u.ModelRevision) &&
		utf8.ValidString(u.Transcript) && utf8.ValidString(u.StablePrefix) && len(u.Transcript) <= 16<<10 &&
		strings.HasPrefix(u.Transcript, u.StablePrefix)
	if c.latest != nil {
		valid = valid && u.MonotonicMS >= c.latest.MonotonicMS && u.Provider == c.latest.Provider &&
			u.ModelRevision == c.latest.ModelRevision && strings.HasPrefix(u.StablePrefix, c.latest.StablePrefix)
	}
	switch u.Phase {
	case "partial", "stable", "final":
		valid = valid && u.ErrorCode == "" && (u.Phase != "stable" || u.StablePrefix != "") &&
			(u.Phase != "final" || (strings.TrimSpace(u.Transcript) != "" && u.StablePrefix == u.Transcript))
	default:
		valid = false
	}
	if !valid {
		c.errorCode = "asr_failed"
		c.cancel() // Candidate failure never fails or cancels the responding turn．
		return
	}
	c.latest = &u
}
