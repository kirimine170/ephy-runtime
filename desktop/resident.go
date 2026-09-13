package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) residentSessionID(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return fmt.Sprintf("%s_%x", a.residentBootID, digest[:8])
}
func residentEnabled() bool { return os.Getenv("EPHY_RESIDENT") == "1" }
func residentNamespace() string {
	n := os.Getenv("EPHY_RESIDENT_NAMESPACE")
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(n) {
		return "resident"
	}
	return n
}
func residentApplicationTitle() string {
	if residentEnabled() {
		return "Ephy Resident"
	}
	return ephyRuntimeTitle
}
func residentApplicationID() string {
	if residentEnabled() {
		return ephyRuntimeSingleInstanceID + "." + residentNamespace()
	}
	return ephyRuntimeSingleInstanceID
}
func residentGatewayURL() string {
	if residentEnabled() {
		if raw := strings.TrimRight(os.Getenv("EPHY_GATEWAY_URL"), "/"); raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
				return raw
			}
		}
		return "http://127.0.0.1:18900"
	}
	return "http://127.0.0.1:8000"
}
func residentInteractionRoot(root string) string {
	if residentEnabled() {
		if state := os.Getenv("EPHY_RESIDENT_STATE_ROOT"); filepath.IsAbs(state) {
			return filepath.Join(state, "interaction")
		}
		return filepath.Join(root, "local-data", residentNamespace(), "interaction")
	}
	return filepath.Join(root, "data", "runtime", "interaction")
}
func (a *App) GetResidentConfig() map[string]any {
	return map[string]any{"enabled": residentEnabled(), "namespace": residentNamespace(), "initial_mode": "paused", "close_behavior": "quit", "voice_profile_id": os.Getenv("EPHY_RESIDENT_VOICE_PROFILE_ID")}
}
func (a *App) ExitResident() {
	if residentEnabled() {
		if ctx := a.currentContext(); ctx != nil {
			runtime.Quit(ctx)
		}
	}
}

type ResidentFeedbackTarget struct {
	PlayedText      string   `json:"played_text,omitempty"`
	ModelID         string   `json:"-"`
	VoiceID         string   `json:"-"`
	PromptID        string   `json:"-"`
	ConfigurationID string   `json:"-"`
	TurnID          string   `json:"turn_id"`
	RunID           string   `json:"run_id"`
	GenerationID    string   `json:"generation_id"`
	SpeechUnitIDs   []string `json:"speech_unit_ids"`
	DeliveryStatus  string   `json:"delivery_status"`
	MemoryIDs       []string `json:"memory_ids"`
}

func residentTarget(s InteractionSnapshot) ResidentFeedbackTarget {
	target := ResidentFeedbackTarget{TurnID: s.TurnID, RunID: s.OperationID, GenerationID: fmt.Sprintf("%s:%d", s.OperationID, s.GenerationRevision), SpeechUnitIDs: []string{}, MemoryIDs: []string{}, DeliveryStatus: "unplayed"}
	target.ModelID, target.VoiceID, target.PromptID, target.ConfigurationID = s.ModelID, s.VoiceID, s.PromptID, s.ConfigurationID
	target.MemoryIDs = append([]string{}, s.MemoryIDs...)
	if len(s.SpeechUnits) == 0 {
		target.DeliveryStatus = "unknown"
	}
	allPlayed := len(s.SpeechUnits) > 0 && s.Generation != nil && s.Generation.Complete
	prefixComplete := true
	for _, unit := range s.SpeechUnits {
		target.SpeechUnitIDs = append(target.SpeechUnitIDs, unit.UnitID)
		if unit.PlaybackStarted {
			target.DeliveryStatus = "partial"
		}
		if prefixComplete && unit.State == "completed" && unit.SynthesisComplete {
			target.PlayedText += unit.Text
		} else {
			prefixComplete = false
		}
		if unit.State != "completed" || !unit.SynthesisComplete {
			allPlayed = false
		}
	}
	if allPlayed {
		target.DeliveryStatus = "played"
	}
	if len([]rune(target.PlayedText)) > 2000 {
		target.PlayedText = string([]rune(target.PlayedText)[:2000])
	}
	return target
}

// Capture the old response before a replacement turn or cancellation invalidates it．
// Recognized controls never replace this pointer with their own empty output．
func (a *App) captureResidentTarget(sessionID string) {
	e := a.interactionEngine()
	e.mu.Lock()
	var latest *interactionTurn
	for _, turn := range e.turns {
		if turn.snapshot.SessionID == sessionID && (turn.snapshot.Transcript != "" || turn.snapshot.ResponsePlan != nil || len(turn.snapshot.SpeechUnits) > 0) && (latest == nil || turn.created.After(latest.created)) {
			latest = turn
		}
	}
	var target ResidentFeedbackTarget
	if latest != nil {
		target = residentTarget(cloneInteractionSnapshot(latest.snapshot))
	}
	e.mu.Unlock()
	if latest != nil {
		a.residentMu.Lock()
		a.residentTargets[sessionID] = target
		a.residentMu.Unlock()
	}
}
func (a *App) residentHTTP(method, path string, payload any) (map[string]any, error) {
	if !residentEnabled() {
		return nil, errors.New("resident_disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var out map[string]any
	if method == http.MethodPost {
		err := a.postJSONContext(ctx, path, payload, &out)
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("resident_gateway_status_%d", response.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&out)
	return out, err
}
func (a *App) rememberResidentState(sessionID string, result map[string]any) {
	state := result
	if value, ok := result["state"].(map[string]any); ok {
		state = value
	}
	a.residentMu.Lock()
	a.residentSessions[sessionID] = state
	a.residentMu.Unlock()
}
func (a *App) invalidateResidentCandidates(sessionID string) {
	a.residentMu.Lock()
	a.residentEpochs[sessionID]++
	active := []string{}
	for id, candidate := range a.residentCandidates {
		if candidate.sessionID == sessionID {
			delete(a.residentCandidates, id)
		}
	}
	for id, candidate := range a.residentActiveCandidates {
		if candidate.sessionID == sessionID {
			active = append(active, id)
			delete(a.residentActiveCandidates, id)
		}
	}
	a.residentMu.Unlock()
	if len(active) > 0 {
		e := a.interactionEngine()
		for _, id := range active {
			_, _ = e.Cancel(id)
		}
	}
}
func (a *App) ConfigureResidentSession(sessionID string, ownerSelected, storageConsent bool, mode string) (map[string]any, error) {
	if !interactionIdentifier.MatchString(sessionID) {
		return nil, errors.New("invalid_session")
	}
	if mode != "paused" && mode != "reactive" && mode != "observe" && mode != "companion" {
		return nil, errors.New("invalid_resident_mode")
	}
	a.invalidateResidentCandidates(sessionID)
	a.residentMu.Lock()
	a.residentModes[sessionID] = mode
	a.residentMu.Unlock()
	result, err := a.residentHTTP(http.MethodPost, "/v1/resident/sessions", map[string]any{"session_id": a.residentSessionID(sessionID), "owner_selected": ownerSelected, "storage_consent": storageConsent})
	if err == nil {
		a.rememberResidentState(sessionID, result)
	}
	return result, err
}
func (a *App) GetResidentState(sessionID string) (map[string]any, error) {
	if !interactionIdentifier.MatchString(sessionID) {
		return nil, errors.New("invalid_session")
	}
	result, err := a.residentHTTP(http.MethodGet, "/v1/resident/sessions/"+url.PathEscape(a.residentSessionID(sessionID)), nil)
	if err == nil {
		a.rememberResidentState(sessionID, result)
	}
	return result, err
}
func (a *App) SubmitResidentFeedback(request map[string]any) (map[string]any, error) {
	sessionID, _ := request["session_id"].(string)
	if !interactionIdentifier.MatchString(sessionID) {
		return nil, errors.New("invalid_session")
	}
	a.invalidateResidentCandidates(sessionID)
	wire := make(map[string]any, len(request))
	for key, value := range request {
		wire[key] = value
	}
	wire["session_id"] = a.residentSessionID(sessionID)
	result, err := a.residentHTTP(http.MethodPost, "/v1/resident/feedback", wire)
	if err == nil {
		a.rememberResidentState(sessionID, result)
	}
	return result, err
}
func (a *App) UndoResidentChange(changeID string, request map[string]any) (map[string]any, error) {
	if !interactionIdentifier.MatchString(changeID) {
		return nil, errors.New("invalid_change")
	}
	sessionID, _ := request["session_id"].(string)
	wire := make(map[string]any, len(request))
	for key, value := range request {
		wire[key] = value
	}
	wire["session_id"] = a.residentSessionID(sessionID)
	result, err := a.residentHTTP(http.MethodPost, "/v1/resident/changes/"+url.PathEscape(changeID)+"/undo", wire)
	if err == nil {
		a.rememberResidentState(sessionID, result)
	}
	return result, err
}

// This bounded lexical gate runs before the ordinary model request．The backend
// alone validates the typed change，speaker，scope and effective revision．Quoted
// text/tool output never comes through this direct-input gate as an instruction．
var residentControl = regexp.MustCompile(`^(止めて|とめて|停止して|ストップ|待って|今は話しかけないで|しばらく話しかけないで|((今後も|これからも|いつも)[，、\s]*)?(もっと短くして|短くして|説明が長い|返答が長い|呼びかけを減らして|名前を呼ぶ回数を減らして|名前を呼ばないで)|今の言い方はよかった|今の言い方は良かった|よかった|良かった|嫌だった|今のは嫌だった|今の声が嫌だった|それは人前で言わないで|違う[，、,]?それは.{1,300}|さっきの変更を戻して|最後の変更を戻して)[．。.!！?？\s]*$`)

func residentDirectedText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"Ephy，", "Ephy、", "Ephy,", "Ephy ", "ephy ", "エフィ，", "エフィ、", "エフィ ", "エフィー，", "エフィー、"} {
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true
		}
	}
	return text, false
}
func residentInputControl(text string) bool {
	if strings.ContainsAny(text, "「」『』\"`“”\n") {
		return false
	}
	return residentControl.MatchString(text)
}
func (a *App) emitResident(result map[string]any) {
	if ctx := a.currentContext(); ctx != nil {
		runtime.EventsEmit(ctx, "resident-event", result)
	}
}
func (a *App) interceptResidentInput(snapshot InteractionSnapshot, request VoiceTurnRequest, text string) bool {
	if !residentEnabled() {
		return false
	}
	a.residentMu.Lock()
	mode := a.residentModes[snapshot.SessionID]
	state := a.residentSessions[snapshot.SessionID]
	target := a.residentTargets[snapshot.SessionID]
	a.residentMu.Unlock()
	a.invalidateResidentCandidates(snapshot.SessionID)
	clean, directed := residentDirectedText(text)
	if (mode == "observe" || mode == "companion") && !directed && request.InputKind != "text" {
		a.emitResident(map[string]any{"session_id": snapshot.SessionID, "status": "observed", "observation": text, "mode": mode, "operation_id": snapshot.OperationID})
		return true
	}
	if !residentInputControl(clean) || request.InputKind == "transcript" {
		a.residentMu.Lock()
		pending := a.residentPending[snapshot.SessionID]
		a.residentMu.Unlock()
		if pending != nil {
			select {
			case <-pending:
			case <-time.After(4 * time.Second):
			}
		}
		return false
	}
	source := "voice"
	if request.InputKind == "text" {
		source = "ui"
	}
	speaker := "unknown"
	if state["owner_selected"] == true {
		speaker = "owner"
	}
	feedback := map[string]any{"session_id": snapshot.SessionID, "dedupe_id": snapshot.OperationID + ":feedback", "expected_revision": state["revision"], "text": clean, "input_source": source, "speaker": speaker, "mode": mode, "voice_id": request.Speech.VoiceProfileID, "configuration_id": request.Chat.ConfigurationID, "model_id": request.Chat.ModelID}
	if mode == "reactive" || mode == "paused" || mode == "" {
		feedback["mode"] = "assistant"
	}
	if target.RunID != "" {
		feedback["target"] = target
		feedback["model_id"], feedback["voice_id"], feedback["prompt_id"], feedback["configuration_id"] = target.ModelID, target.VoiceID, target.PromptID, target.ConfigurationID
	}
	// Cancellation and the next microphone turn are independent of persistence．
	select {
	case a.residentRequests <- struct{}{}:
		a.residentMu.Lock()
		previous := a.residentPending[snapshot.SessionID]
		done := make(chan struct{})
		a.residentPending[snapshot.SessionID] = done
		a.residentMu.Unlock()
		go func() {
			defer close(done)
			if previous != nil {
				select {
				case <-previous:
				case <-time.After(4 * time.Second):
				}
			}
			defer func() { <-a.residentRequests }()
			result, err := a.SubmitResidentFeedback(feedback)
			if err != nil {
				a.emitResident(map[string]any{"session_id": snapshot.SessionID, "status": "failed", "error": "feedback_not_saved"})
			} else {
				result["session_id"] = snapshot.SessionID
				a.emitResident(result)
			}
		}()
	default:
		a.emitResident(map[string]any{"session_id": snapshot.SessionID, "status": "failed", "error": "feedback_queue_full"})
	}
	return true
}

func (a *App) RetractResidentFeedback(eventID string, request map[string]any) (map[string]any, error) {
	if !interactionIdentifier.MatchString(eventID) {
		return nil, errors.New("invalid_feedback_event")
	}
	sessionID, _ := request["session_id"].(string)
	if !interactionIdentifier.MatchString(sessionID) {
		return nil, errors.New("invalid_session")
	}
	a.invalidateResidentCandidates(sessionID)
	wire := make(map[string]any, len(request))
	for key, value := range request {
		wire[key] = value
	}
	wire["session_id"] = a.residentSessionID(sessionID)
	result, err := a.residentHTTP("POST", "/v1/resident/feedback/"+url.PathEscape(eventID)+"/retract", wire)
	if err == nil {
		a.rememberResidentState(sessionID, result)
	}
	return result, err
}

// Only canonical Karte context documents are candidates for a narrower sharing
// policy．Web results，indexes and arbitrary tool references do not confer it．
func residentSourceMemoryIDs(sources []SearchItem) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		if source.SourceType == "karte_context" && source.DocID != "" && len(source.DocID) <= 200 && !seen[source.DocID] {
			ids = append(ids, source.DocID)
			seen[source.DocID] = true
			if len(ids) == 32 {
				break
			}
		}
	}
	return ids
}
