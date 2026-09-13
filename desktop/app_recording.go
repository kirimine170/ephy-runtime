package main

import (
	"context"
	"desktop/recording"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func (a *App) recordingStore() *recording.Store {
	a.recordingMu.Lock()
	defer a.recordingMu.Unlock()
	if a.recorder != nil || a.recordingInit {
		return a.recorder
	}
	a.recordingInit = true
	root := os.Getenv("KARTE_DATA_DIR")
	if root == "" {
		root = filepath.Join(a.workspaceRoot, "data", "runtime", "karte-data")
	}
	control := os.Getenv("EPHY_KARTE_CONTROL")
	if control == "" {
		if executable, e := os.Executable(); e == nil {
			control = filepath.Join(filepath.Dir(executable), "..", "Helpers", "karte-ephy-control")
		}
	}
	s, e := recording.New(recording.Options{Home: os.Getenv("EPHY_RECORDING_HOME"), DataRoot: root, ControlPath: control, KarteConfigRoot: os.Getenv("EPHY_KARTE_CONFIG_ROOT"), Prepare: func(ctx context.Context, root string) error {
		var response struct {
			SafetyVersion int    `json:"safety_version"`
			Safe          bool   `json:"safe"`
			DataRoot      string `json:"data_root"`
		}
		if err := a.postJSONContext(ctx, "/v1/karte/records/prepare", map[string]any{}, &response); err != nil {
			return err
		}
		physical, e := filepath.EvalSymlinks(response.DataRoot)
		if e != nil || physical != root || response.SafetyVersion != 1 || !response.Safe {
			return errors.New("recording_access_guard_unavailable")
		}
		return nil
	}})
	if e != nil {
		a.recordingError = "recording_storage_unavailable"
		return nil
	}
	a.recorder = s
	s.Start()
	return s
}
func (a *App) GetRecordingStatus() recording.Status {
	if s := a.recordingStore(); s != nil {
		return s.Snapshot()
	}
	return recording.Status{State: "save_failed", Code: "recording_storage_unavailable", Records: []recording.RecordStatus{}}
}
func (a *App) ConfigureRecording(request recording.ConfigureRequest) (recording.Status, error) {
	s := a.recordingStore()
	if s == nil {
		return a.GetRecordingStatus(), errors.New("recording_storage_unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.Configure(ctx, request)
}
func (a *App) RetryRecording() (recording.Status, error) {
	s := a.recordingStore()
	if s == nil {
		return a.GetRecordingStatus(), errors.New("recording_storage_unavailable")
	}
	err := s.Retry()
	return s.Snapshot(), err
}
func (a *App) SetRecordingConversation(id string) (string, error) {
	s := a.recordingStore()
	if s == nil {
		return "", errors.New("recording_storage_unavailable")
	}
	return s.SetConversation(id)
}
func (a *App) ReadRecordedConversation(target recording.Target) (recording.ReadResult, error) {
	s := a.recordingStore()
	if s == nil {
		return recording.ReadResult{}, errors.New("recording_storage_unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.Read(ctx, target)
}

func recordedAssistant(snapshot InteractionSnapshot, state string) (recording.Assistant, bool) {
	complete := snapshot.Generation != nil && snapshot.Generation.Complete
	a := recording.Assistant{Generation: "failed", Display: "none", Playback: "unknown", SpeechUnits: []recording.SpeechUnit{}}
	if complete {
		a.Generation = "completed"
	} else if state == "CANCELED" || state == "CANCELING" {
		a.Generation = "canceled"
	}
	started, interrupted, allComplete := false, false, len(snapshot.SpeechUnits) > 0
	for _, u := range snapshot.SpeechUnits {
		a.SpeechUnits = append(a.SpeechUnits, recording.SpeechUnit{UnitID: u.UnitID, State: u.State})
		started = started || u.PlaybackStarted
		interrupted = interrupted || u.State == "interrupted"
		allComplete = allComplete && u.State == "completed"
	}
	switch {
	case allComplete:
		a.Playback = "completed"
	case interrupted:
		a.Playback = "interrupted"
	case snapshot.ErrorCode == "playback_failed":
		a.Playback = "failed"
	case !started && snapshot.LastAudioSequence == 0:
		a.Playback = "not_started"
	}
	return a, complete
}
func (e *InteractionEngine) checkpointRecordingLocked(t *interactionTurn) error {
	if e.recorder == nil || t.recordingKey == "" {
		return nil
	}
	text := ""
	if t.snapshot.ResponsePlan != nil {
		text = t.snapshot.ResponsePlan.Text
	}
	a, complete := recordedAssistant(t.snapshot, t.snapshot.State)
	return e.recorder.Checkpoint(t.recordingKey, text, a, complete)
}
func (e *InteractionEngine) finishRecordingLocked(t *interactionTurn, state string) error {
	if e.recorder == nil || t.recordingKey == "" {
		return nil
	}
	if err := e.checkpointRecordingLocked(t); err != nil {
		return err
	}
	a, complete := recordedAssistant(t.snapshot, state)
	return e.recorder.Finish(t.recordingKey, a, complete)
}

type recordingStreamContextKey struct{}
type recordingStream struct {
	store     *recording.Store
	key       string
	committed string
	failure   error
}

func (r *recordingStream) progress(text string, terminal, complete bool) error {
	state := recording.Assistant{Generation: "failed", Playback: "not_started", SpeechUnits: []recording.SpeechUnit{}}
	if complete {
		state.Generation = "completed"
	}
	return r.persist(text, state, terminal, complete)
}
func (r *recordingStream) persist(text string, state recording.Assistant, terminal, complete bool) error {
	if r == nil || r.key == "" {
		return nil
	}
	if r.failure != nil {
		return r.failure
	}
	prefix := text
	if !complete {
		prefix = r.committed
		boundaries, _ := generationBoundaries(text, false)
		if len(boundaries) > 0 && boundaries[len(boundaries)-1] > len(prefix) {
			prefix = text[:boundaries[len(boundaries)-1]]
		}
	}
	if prefix != r.committed || terminal {
		if err := r.store.Checkpoint(r.key, prefix, state, complete); err != nil {
			r.failure = &generationStreamError{code: "recording_storage_failed", reason: "unknown"}
			return r.failure
		}
		r.committed = prefix
	}
	if terminal {
		if err := r.store.Finish(r.key, state, complete); err != nil {
			r.failure = &generationStreamError{code: "recording_storage_failed", reason: "unknown"}
			return r.failure
		}
	}
	return nil
}

func (a *App) recordedTextChat(request ChatRequest) (*ChatResponse, error) {
	s := a.recordingStore()
	key := ""
	if s != nil {
		var err error
		requestID := request.RequestID
		if requestID == "" {
			requestID = interactionID("chat_")
		}
		if request.ContinuationOf != "" {
			key, err = s.BeginContinuation(request.ContinuationOf, requestID, request.SessionID, 0)
		} else {
			key, err = s.Begin(requestID, request.SessionID, request.Prompt, "text", nil)
		}
		if err != nil {
			return nil, err
		}
	}
	progress := &recordingStream{store: s, key: key}
	request.recording = progress
	response, err := a.Chat(request)
	if s != nil && key != "" {
		state := recording.Assistant{Generation: "failed", Playback: "not_started", SpeechUnits: []recording.SpeechUnit{}}
		text := ""
		complete := false
		if response != nil {
			text = response.Answer
			complete = err == nil && response.FinishReason == "stop"
			if response.Generation != nil {
				complete = err == nil && response.Generation.Complete
			}
			if complete {
				state.Generation = "completed"
			}
		}
		if errors.Is(err, context.Canceled) {
			state.Generation = "canceled"
		}
		if recordingErr := progress.persist(text, state, true, complete); recordingErr != nil {
			return response, recordingErr
		}
	}
	return response, err
}

func (a *App) recordedQuery(request QueryRequest) (*QueryResponse, error) {
	s := a.recordingStore()
	key := ""
	if s != nil {
		var err error
		if request.ContinuationOf != "" {
			key, err = s.BeginContinuation(request.ContinuationOf, request.RequestID, request.SessionID, 0)
		} else {
			key, err = s.Begin(request.RequestID, request.SessionID, request.Query, "text", nil)
		}
		if err != nil {
			return nil, err
		}
	}
	progress := &recordingStream{store: s, key: key}
	request.recording = progress
	response, err := a.Query(request)
	if s != nil && key != "" {
		state := recording.Assistant{Generation: "failed", Playback: "not_started", SpeechUnits: []recording.SpeechUnit{}}
		text := ""
		complete := false
		if response != nil {
			text = response.Answer
			complete = err == nil && response.FinishReason == "stop"
		}
		if complete {
			state.Generation = "completed"
		}
		if recordingErr := progress.persist(text, state, true, complete); recordingErr != nil {
			return response, recordingErr
		}
	}
	return response, err
}
