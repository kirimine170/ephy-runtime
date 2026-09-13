package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type residentCandidate struct {
	sessionID      string
	operationID    string
	text           string
	policyRevision float64
	expires        time.Time
	contextEpoch   uint64
	voiceID        string
	voiceEpoch     uint64
	participants   any
	revision       any
	ticketID       any
	memoryIDs      []string
	authorized     bool
}

// A transient candidate is retained only inside the current desktop process．
// IDs and text submitted later by the UI cannot substitute for this result．
func (a *App) CreateResidentCandidate(request map[string]any) (map[string]any, error) {
	sessionID, _ := request["session_id"].(string)
	if !residentEnabled() || !interactionIdentifier.MatchString(sessionID) {
		return nil, errors.New("invalid_resident_session")
	}
	a.residentMu.Lock()
	mode := a.residentModes[sessionID]
	epoch := a.residentEpochs[sessionID]
	a.residentMu.Unlock()
	e := a.interactionEngine()
	e.mu.Lock()
	var voiceID string
	var voiceEpoch uint64
	if e.voiceSession != nil {
		voiceID = e.voiceSession.snapshot.ID
		voiceEpoch = e.voiceSession.snapshot.Epoch
	}
	e.mu.Unlock()
	if mode != "observe" && mode != "companion" {
		return nil, errors.New("resident_observation_disabled")
	}
	wire := make(map[string]any, len(request))
	for key, value := range request {
		wire[key] = value
	}
	wire["session_id"] = a.residentSessionID(sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	var result map[string]any
	if err := a.postJSONContext(ctx, "/v1/resident/candidate", wire, &result); err != nil {
		return nil, err
	}
	result["session_id"] = sessionID
	if result["status"] == "candidate" {
		candidate, _ := result["candidate"].(map[string]any)
		text, _ := candidate["text"].(string)
		revision, _ := result["policy_revision"].(float64)
		operationID, _ := request["operation_id"].(string)
		memoryIDs := []string{}
		if evidence, ok := candidate["evidence"].([]any); ok {
			for _, item := range evidence {
				if ref, ok := item.(map[string]any); ok {
					if id, ok := ref["doc_id"].(string); ok {
						memoryIDs = append(memoryIDs, id)
					}
				}
			}
		}
		a.residentMu.Lock()
		if a.residentEpochs[sessionID] != epoch {
			a.residentMu.Unlock()
			return map[string]any{"status": "discarded"}, nil
		}
		if len(a.residentCandidates) >= 8 {
			clear(a.residentCandidates)
		}
		a.residentCandidates[operationID] = residentCandidate{sessionID: sessionID, operationID: operationID, text: text, policyRevision: revision, expires: time.Now().Add(15 * time.Second), contextEpoch: epoch, voiceID: voiceID, voiceEpoch: voiceEpoch, participants: request["participants"], revision: request["revision"], ticketID: result["ticket_id"], memoryIDs: memoryIDs}
		a.residentMu.Unlock()
	}
	return result, nil
}
func (a *App) CancelResidentCandidate(sessionID, operationID string) (map[string]any, error) {
	a.residentMu.Lock()
	delete(a.residentCandidates, operationID)
	a.residentMu.Unlock()
	return a.residentHTTP("POST", "/v1/resident/candidate/cancel", map[string]any{"session_id": a.residentSessionID(sessionID), "operation_id": operationID})
}
func (a *App) StartResidentCandidate(operationID string, request VoiceTurnRequest) (InteractionSnapshot, error) {
	a.residentMu.Lock()
	candidate, ok := a.residentCandidates[operationID]
	delete(a.residentCandidates, operationID)
	mode := a.residentModes[request.SessionID]
	epoch := a.residentEpochs[request.SessionID]
	a.residentMu.Unlock()
	if !ok || !residentEnabled() || mode != "companion" || epoch != candidate.contextEpoch || request.VoiceSessionID != candidate.voiceID || request.VoiceSessionEpoch != candidate.voiceEpoch || candidate.sessionID != request.SessionID || time.Now().After(candidate.expires) {
		return InteractionSnapshot{}, errors.New("stale_resident_candidate")
	}
	state, err := a.GetResidentState(request.SessionID)
	if err != nil {
		return InteractionSnapshot{}, err
	}
	policy, _ := state["policy"].(map[string]any)
	if state["revision"] != candidate.policyRevision || policy["proactive"] == "suppressed" || time.Now().After(candidate.expires) {
		return InteractionSnapshot{}, errors.New("stale_resident_policy")
	}
	a.residentMu.Lock()
	valid := a.residentModes[request.SessionID] == "companion" && a.residentEpochs[request.SessionID] == candidate.contextEpoch
	a.residentMu.Unlock()
	if !valid || time.Now().After(candidate.expires) {
		return InteractionSnapshot{}, errors.New("stale_resident_candidate")
	}
	request.CandidateMemoryIDs = candidate.memoryIDs
	request.Chat.ResidentSessionID = a.residentSessionID(request.SessionID)
	request.Chat.ModelID = "resident-candidate"
	request.Chat.ConfigurationID = fmt.Sprintf("resident-policy-%v", candidate.policyRevision)
	snapshot, err := a.interactionEngine().StartPreparedCandidate(request, candidate.text)
	if err != nil {
		return snapshot, err
	}
	a.residentMu.Lock()
	a.residentActiveCandidates[snapshot.OperationID] = candidate
	a.residentMu.Unlock()
	time.AfterFunc(time.Until(candidate.expires), func() {
		a.residentMu.Lock()
		active, exists := a.residentActiveCandidates[snapshot.OperationID]
		if exists && !active.authorized {
			delete(a.residentActiveCandidates, snapshot.OperationID)
		}
		a.residentMu.Unlock()
		if exists && !active.authorized {
			_, _ = a.interactionEngine().Cancel(snapshot.OperationID)
		}
	})
	return snapshot, nil
}

// First audio is still inaudible when this gate runs．The same one-use ticket is
// checked after synthesis，so expiry and sharing changes cannot leak queued audio．
func (a *App) AuthorizeResidentPlayback(operationID string) (bool, error) {
	a.residentMu.Lock()
	candidate, ok := a.residentActiveCandidates[operationID]
	epoch := a.residentEpochs[candidate.sessionID]
	mode := a.residentModes[candidate.sessionID]
	a.residentMu.Unlock()
	if !ok {
		return false, nil
	}
	if epoch != candidate.contextEpoch || mode != "companion" || (!candidate.authorized && time.Now().After(candidate.expires)) {
		return false, nil
	}
	if !candidate.authorized {
		check, err := a.residentHTTP("POST", "/v1/resident/candidate/check", map[string]any{"session_id": a.residentSessionID(candidate.sessionID), "operation_id": candidate.operationID, "revision": candidate.revision, "participants": candidate.participants, "ticket_id": candidate.ticketID})
		if err != nil || check["status"] != "ready" {
			_, _ = a.interactionEngine().Cancel(operationID)
			return false, err
		}
	}
	a.residentMu.Lock()
	current, exists := a.residentActiveCandidates[operationID]
	valid := exists && a.residentEpochs[candidate.sessionID] == candidate.contextEpoch && a.residentModes[candidate.sessionID] == "companion" && (candidate.authorized || time.Now().Before(candidate.expires))
	if valid {
		current.authorized = true
		a.residentActiveCandidates[operationID] = current
	}
	a.residentMu.Unlock()
	if !valid {
		_, _ = a.interactionEngine().Cancel(operationID)
	}
	return valid, nil
}
