package main

import (
	"context"
	"errors"
	"math"
)

// A SpeechUnit can contain several WAVs．Only a successful producer close and
// natural endings of every WAV prove that the whole unit finished playing．
// Text is transient snapshot data，never included in the metadata trace store．
type InteractionSpeechUnit struct {
	UnitID             string `json:"unit_id"`
	Text               string `json:"text"`
	GenerationRevision int    `json:"generation_revision"`
	State              string `json:"state"`
	AudioSequences     []int  `json:"audio_sequences"`
	SynthesisComplete  bool   `json:"synthesis_complete"`
	PlaybackStarted    bool   `json:"playback_started"`
}
type InteractionPlaybackObservation struct {
	Sequence int    `json:"sequence"`
	State    string `json:"state"`
}
type InteractionInterruptionTiming struct {
	LocalStopMS float64 `json:"local_stop_ms"`
}
type InteractionInputHandoffTiming struct {
	ASRReadyMS      int `json:"asr_ready_ms"`
	DrainedMS       int `json:"drained_ms"`
	BufferedAudioMS int `json:"buffered_audio_ms"`
}
type InteractionInterruption struct {
	LocalStopMS        float64                          `json:"local_stop_ms"`
	VoiceSessionID     string                           `json:"voice_session_id"`
	VoiceSessionEpoch  uint64                           `json:"voice_session_epoch"`
	SessionID          string                           `json:"session_id"`
	TurnID             string                           `json:"turn_id"`
	OperationID        string                           `json:"operation_id"`
	GenerationRevision int                              `json:"generation_revision"`
	Playback           []InteractionPlaybackObservation `json:"playback"`
}

func (e *InteractionEngine) registerSpeechUnitLocked(t *interactionTurn, revision int, text string) string {
	id := interactionID("unit_")
	t.snapshot.SpeechUnits = append(t.snapshot.SpeechUnits, InteractionSpeechUnit{UnitID: id, Text: text, GenerationRevision: revision, State: "unknown", AudioSequences: []int{}})
	return id
}
func (e *InteractionEngine) sealSpeechUnit(t *interactionTurn, revision int, ctx context.Context, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.generationLiveLocked(t, revision, ctx) {
		return
	}
	for i := range t.snapshot.SpeechUnits {
		if t.snapshot.SpeechUnits[i].UnitID == id {
			t.snapshot.SpeechUnits[i].SynthesisComplete = true
		}
	}
	e.refreshSpeechUnitsLocked(t)
}
func (e *InteractionEngine) refreshSpeechUnitsLocked(t *interactionTurn) {
	e.refreshSpeechUnitsForStateLocked(t, t.snapshot.State)
}
func (e *InteractionEngine) refreshSpeechUnitsForStateLocked(t *interactionTurn, state string) {
	for i := range t.snapshot.SpeechUnits {
		u := &t.snapshot.SpeechUnits[i]
		started, interrupted, complete := false, false, u.SynthesisComplete && len(u.AudioSequences) > 0
		for _, seq := range u.AudioSequences {
			c := t.chunks[seq]
			started = started || c.started
			interrupted = interrupted || c.interrupted
			complete = complete && c.stopped && !c.interrupted
		}
		u.State = "unknown"
		u.PlaybackStarted = started
		switch {
		case complete:
			u.State = "completed"
		case interrupted:
			u.State = "interrupted"
		case started && !interactionTerminal(state) && state != "CANCELING":
			u.State = "started"
		}
	}
}

// Local mute/stop happens before this call．Neither cancellation of providers
// nor their cleanup is awaited．The voice session context remains the owner of
// the next input；only this exact old operation and revision can be canceled．
func (e *InteractionEngine) Interrupt(r InteractionInterruption) (InteractionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[r.OperationID]
	if t == nil || r.SessionID != t.snapshot.SessionID || r.TurnID != t.snapshot.TurnID || r.GenerationRevision != t.snapshot.GenerationRevision || r.VoiceSessionID != t.request.VoiceSessionID || r.VoiceSessionEpoch != t.request.VoiceSessionEpoch || !e.validVoiceSessionLocked(t.request) {
		return InteractionSnapshot{}, errors.New("stale_interruption")
	}
	if len(r.Playback) > 64 || math.IsNaN(r.LocalStopMS) || math.IsInf(r.LocalStopMS, 0) || r.LocalStopMS < 0 || r.LocalStopMS > 5000 {
		return InteractionSnapshot{}, errors.New("invalid_playback_observation")
	}
	seen := map[int]bool{}
	for _, o := range r.Playback {
		if t.chunks[o.Sequence] == nil || seen[o.Sequence] || (o.State != "completed" && o.State != "interrupted") {
			return InteractionSnapshot{}, errors.New("invalid_playback_observation")
		}
		seen[o.Sequence] = true
	}
	if interactionTerminal(t.snapshot.State) {
		return cloneInteractionSnapshot(t.snapshot), nil
	}
	for _, o := range r.Playback {
		c := t.chunks[o.Sequence]
		c.started = true
		if !c.stopped {
			c.stopped = o.State == "completed"
			c.interrupted = o.State == "interrupted"
		}
	}
	t.snapshot.Interruption = &InteractionInterruptionTiming{LocalStopMS: r.LocalStopMS}
	e.traceLocked(t, "user_barge_in", "")
	e.cancelTurnLocked(t)
	return cloneInteractionSnapshot(t.snapshot), nil
}

func (a *App) InterruptInteraction(request InteractionInterruption) (InteractionSnapshot, error) {
	return a.interactionEngine().Interrupt(request)
}

func (a *App) RecordInteractionInputHandoff(operationID string, revision int, timing InteractionInputHandoffTiming) error {
	return a.interactionEngine().RecordInputHandoff(operationID, revision, timing)
}
func (e *InteractionEngine) RecordInputHandoff(op string, revision int, timing InteractionInputHandoffTiming) error {
	if timing.ASRReadyMS < 0 || timing.DrainedMS < timing.ASRReadyMS || timing.DrainedMS > 5000 || timing.BufferedAudioMS < 0 || timing.BufferedAudioMS > 2000 {
		return errors.New("invalid_input_handoff_timing")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[op]
	if t == nil || !e.liveLocked(t) || t.snapshot.GenerationRevision != revision || t.request.VoiceSessionID == "" || !e.validVoiceSessionLocked(t.request) || t.asr == nil {
		return errors.New("stale_input_handoff")
	}
	if t.snapshot.InputHandoff != nil {
		return nil
	}
	t.snapshot.InputHandoff = &timing
	e.traceLocked(t, "input_handoff_ready", "")
	return nil
}
