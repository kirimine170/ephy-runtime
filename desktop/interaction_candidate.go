package main

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// StartPreparedCandidate is an internal experimental entry after Runtime has
// checked evidence，participants，revision and expiry．It does not invent a user
// question or emit an ASR final．The existing assembler，TTS and delivery own output．
func (e *InteractionEngine) StartPreparedCandidate(request VoiceTurnRequest, text string) (InteractionSnapshot, error) {
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 180 {
		return InteractionSnapshot{}, errors.New("invalid_candidate")
	}
	request.InputKind = "transcript"
	snapshot, err := e.start(request, text)
	if err != nil {
		return snapshot, err
	}
	e.mu.Lock()
	t := e.turns[snapshot.OperationID]
	if e.recorder != nil {
		key, recordErr := e.recorder.BeginCandidate(t.snapshot.OperationID, t.snapshot.SessionID)
		if recordErr != nil {
			e.failLocked(t, "recording_storage_failed")
			e.mu.Unlock()
			return snapshot, recordErr
		}
		t.recordingKey = key
	}
	_ = e.transitionLocked(t, "TRANSCRIBING")
	_ = e.transitionLocked(t, "THINKING")
	snapshot = cloneInteractionSnapshot(t.snapshot)
	e.mu.Unlock()
	go e.runGeneration(t, "")
	return snapshot, nil
}

// ObserveCandidateStop applies a playback observation to this exact experimental
// operation before the usual cancellation path．No completed interval is invented．
func (e *InteractionEngine) ObserveCandidateStop(op string, sequence int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil || t.approvedCandidate == "" || t.chunks[sequence] == nil {
		return errors.New("stale_candidate_audio")
	}
	if interactionTerminal(t.snapshot.State) {
		return nil
	}
	c := t.chunks[sequence]
	if !c.stopped {
		c.started = true
		c.interrupted = true
	}
	e.cancelTurnLocked(t)
	return nil
}
