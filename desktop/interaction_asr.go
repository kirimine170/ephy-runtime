package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ASRMetadata contains measurements only．No hypothesis crosses the trace boundary．
// Latencies use the Runtime session clock，not the adapter's clock origin．
type ASRMetadata struct {
	Provider       string `json:"provider,omitempty"`
	ModelRevision  string `json:"model_revision,omitempty"`
	RevisionCount  int    `json:"revision_count"`
	CharacterCount int    `json:"character_count"`
	FirstAudioMS   *int64 `json:"first_audio_ms,omitempty"`
	FirstPartialMS *int64 `json:"first_partial_ms,omitempty"`
	FirstStableMS  *int64 `json:"first_stable_ms,omitempty"`
	FinalMS        *int64 `json:"final_ms,omitempty"`
	FinalizationMS *int64 `json:"finalization_ms,omitempty"`
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
	completed                     bool
	metadata                      ASRMetadata
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
	ctx, cancel := context.WithTimeout(t.ctx, 125*time.Second)
	a := &interactionASRSession{ctx: ctx, cancel: cancel, started: time.Now(), lastMonotonicMS: -1,
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
		s, err := provider.OpenSession(ctx, a.request, func(update ASRUpdate) { e.receiveASR(t, a, update) })
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
	if a.metadata.FirstAudioMS == nil {
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
	a.ended = time.Now()
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
	if a.final == nil || final != *a.final || final.Phase != "final" {
		e.failLocked(t, "asr_stream_invalid")
		e.mu.Unlock()
		return
	}
	ms := time.Since(a.ended).Milliseconds()
	a.metadata.FinalizationMS = &ms
	a.completed = true
	a.cancel()
	text := final.Transcript
	e.mu.Unlock()
	e.acceptTranscript(t, text)
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
		case "asr_stream_invalid", "asr_stream_eof", "asr_timeout", "asr_canceled", "asr_unavailable", "asr_on_device_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_empty_result", "asr_empty_transcript", "invalid_audio", "invalid_voice_config":
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
	if update.Revision > maxASRRevisions || update.MonotonicMS < a.lastMonotonicMS || update.MonotonicMS < 0 || !interactionIdentifier.MatchString(update.Provider) || !interactionMetadataID.MatchString(update.ModelRevision) || !utf8.ValidString(update.Transcript) || !utf8.ValidString(update.StablePrefix) || len(update.Transcript) > 16<<10 || !strings.HasPrefix(update.Transcript, update.StablePrefix) {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	if a.metadata.Provider != "" && (a.metadata.Provider != update.Provider || a.metadata.ModelRevision != update.ModelRevision) {
		e.failLocked(t, "asr_stream_invalid")
		return
	}
	switch update.Phase {
	case "partial", "stable", "final":
		if update.ErrorCode != "" || (update.Phase == "final" && (strings.TrimSpace(update.Transcript) == "" || update.StablePrefix != update.Transcript)) || (update.Phase == "stable" && update.StablePrefix == "") || !strings.HasPrefix(update.StablePrefix, a.stablePrefix) {
			e.failLocked(t, "asr_stream_invalid")
			return
		}
	case "failure", "timeout", "canceled":
		if update.Transcript != "" || update.StablePrefix != "" {
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
	if update.Phase == "final" {
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
	if t == nil || t.asr == nil || t.asr.pending == nil || !e.liveLocked(t) {
		e.mu.Unlock()
		return
	}
	update := *t.asr.pending
	t.asr.pending = nil
	event := e.eventLocked(t, "asr_update")
	event.ASR = &update
	e.mu.Unlock()
	if e.emit != nil {
		e.emit(event)
	}
}
