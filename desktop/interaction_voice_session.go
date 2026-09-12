package main

import (
	"context"
	"errors"
)

// Conversation identity is stable across pause/resume．Epoch fences delayed starts．
type VoiceSessionSnapshot struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Epoch          uint64 `json:"epoch"`
	State          string `json:"state"`
}
type voiceSession struct {
	snapshot VoiceSessionSnapshot
	ctx      context.Context
	cancel   context.CancelFunc
}

func (e *InteractionEngine) validVoiceSessionLocked(r VoiceTurnRequest) bool {
	if r.VoiceSessionID == "" {
		return r.VoiceSessionEpoch == 0 && (e.voiceSession == nil || e.voiceSession.snapshot.State == "stopped" || e.voiceSession.snapshot.State == "paused")
	}
	v := e.voiceSession
	return v != nil && v.ctx.Err() == nil && v.snapshot.ID == r.VoiceSessionID && v.snapshot.Epoch == r.VoiceSessionEpoch && v.snapshot.ConversationID == r.SessionID
}
func (e *InteractionEngine) StartVoiceSession(conversationID string) (VoiceSessionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.active != "" || !interactionIdentifier.MatchString(conversationID) {
		return VoiceSessionSnapshot{}, errors.New("voice_session_unavailable")
	}
	if e.voiceSession != nil && e.voiceSession.snapshot.State != "stopped" {
		return VoiceSessionSnapshot{}, errors.New("voice_session_busy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.voiceSession = &voiceSession{snapshot: VoiceSessionSnapshot{ID: interactionID("voice_"), ConversationID: conversationID, Epoch: 1, State: "starting"}, ctx: ctx, cancel: cancel}
	return e.voiceSession.snapshot, nil
}
func (e *InteractionEngine) ChangeVoiceSession(id string, epoch uint64, action string) (VoiceSessionSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := e.voiceSession
	if e.closed || v == nil || v.snapshot.ID != id || v.snapshot.Epoch != epoch {
		return VoiceSessionSnapshot{}, errors.New("stale_voice_session")
	}
	switch action {
	case "resume":
		if v.snapshot.State != "paused" || e.active != "" {
			return VoiceSessionSnapshot{}, errors.New("voice_session_unavailable")
		}
		v.ctx, v.cancel = context.WithCancel(context.Background())
		v.snapshot.Epoch++
		v.snapshot.State = "starting"
	case "pause", "end":
		v.cancel()
		if t := e.turns[e.active]; t != nil && t.request.VoiceSessionID == id {
			e.cancelTurnLocked(t)
		}
		if action == "end" {
			v.snapshot.State = "stopped"
		} else if v.snapshot.State != "stopped" {
			v.snapshot.State = "paused"
		}
	default:
		return VoiceSessionSnapshot{}, errors.New("invalid_voice_session_action")
	}
	return v.snapshot, nil
}
func (a *App) StartVoiceSession(conversationID string) (VoiceSessionSnapshot, error) {
	return a.interactionEngine().StartVoiceSession(conversationID)
}
func (a *App) ChangeVoiceSession(id string, epoch uint64, action string) (VoiceSessionSnapshot, error) {
	return a.interactionEngine().ChangeVoiceSession(id, epoch, action)
}
