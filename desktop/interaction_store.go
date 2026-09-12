package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This allowlisted schema deliberately has no payload, prompt, transcript,
// response, private context, audio bytes, or free-form provider error field.
type InteractionTraceEvent struct {
	InputHandoff       *InteractionInputHandoffTiming `json:"input_handoff,omitempty"`
	Interruption       *InteractionInterruptionTiming `json:"interruption,omitempty"`
	SchemaVersion      int                            `json:"schema_version"`
	EventID            string                         `json:"event_id"`
	TraceID            string                         `json:"trace_id"`
	SessionID          string                         `json:"session_id"`
	TurnID             string                         `json:"turn_id"`
	OperationID        string                         `json:"operation_id"`
	Name               string                         `json:"name"`
	Source             string                         `json:"source"`
	Timestamp          string                         `json:"timestamp"`
	MonotonicMS        int64                          `json:"monotonic_ms"`
	Status             string                         `json:"status"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	ProviderID         string                         `json:"provider_id"`
	ModelID            string                         `json:"model_id"`
	ConfigurationID    string                         `json:"configuration_id"`
	Generation         *GenerationMetadata            `json:"generation,omitempty"`
	GenerationRevision int                            `json:"generation_revision,omitempty"`
	ASR                *ASRMetadata                   `json:"asr,omitempty"`
}
type TraceValidation struct {
	Valid       bool             `json:"valid"`
	Missing     []string         `json:"missing"`
	LatenciesMS map[string]int64 `json:"latencies_ms"`
}

func ValidateTrace(events []InteractionTraceEvent) TraceValidation {
	validation := TraceValidation{Valid: true, Missing: []string{}, LatenciesMS: map[string]int64{}}
	first := map[string]int64{}
	last := map[string]int64{}
	for _, event := range events {
		if event.Name == "turn_completed" && event.SchemaVersion >= 2 && (event.Generation == nil || !event.Generation.Complete || !event.Generation.TerminalSSE || !event.Generation.DoneReceived || event.Generation.FinishReason != "stop") {
			validation.Missing = append(validation.Missing, "generation_complete")
		}
		if _, ok := first[event.Name]; !ok {
			first[event.Name] = event.MonotonicMS
		}
		last[event.Name] = event.MonotonicMS
		if event.InputHandoff != nil {
			validation.LatenciesMS["input_handoff_asr_ready"] = int64(event.InputHandoff.ASRReadyMS)
			validation.LatenciesMS["input_handoff_drained"] = int64(event.InputHandoff.DrainedMS)
		}
		if event.Interruption != nil {
			validation.LatenciesMS["local_stop"] = int64(event.Interruption.LocalStopMS)
		}
	}
	required := []string{"user_speech_start"}
	if _, listening := first["listening_started"]; listening {
		if _, speech := first["user_speech_start"]; !speech {
			if _, complete := first["turn_completed"]; !complete {
				required = []string{"listening_started"}
			}
		}
	}
	if _, completed := first["turn_completed"]; completed {
		required = append(required, "user_speech_end", "endpoint_commit", "asr_started", "asr_final", "llm_requested", "llm_first_token", "llm_completed")
		if _, skipped := first["tts_skipped"]; !skipped {
			required = append(required, "tts_requested", "tts_first_chunk", "tts_completed", "audio_play_started", "audio_play_stopped")
		}
	} else if _, canceled := first["cancel_requested"]; canceled {
		required = append(required, "cancel_acknowledged")
	} else if _, failed := first["turn_failed"]; !failed {
		if _, incomplete := first["turn_incomplete"]; !incomplete {
			required = append(required, "terminal_event")
		}
	}
	for _, name := range required {
		if _, ok := first[name]; !ok {
			validation.Missing = append(validation.Missing, name)
		}
	}
	for label, pair := range map[string][2]string{"speech_first_partial": {"user_speech_start", "asr_first_partial"}, "speech_to_endpoint": {"user_speech_start", "endpoint_commit"}, "asr": {"asr_started", "asr_final"}, "asr_first_partial": {"asr_started", "asr_first_partial"}, "asr_first_stable": {"asr_started", "asr_first_stable"}, "asr_finalization": {"endpoint_commit", "asr_final"}, "llm_ttft": {"llm_requested", "llm_first_token"}, "llm": {"llm_requested", "llm_completed"}, "tts_ttfc": {"tts_requested", "tts_first_chunk"}, "tts": {"tts_requested", "tts_completed"}, "first_audio": {"endpoint_commit", "audio_play_started"}, "turn": {"user_speech_start", "turn_completed"}} {
		start, okStart := first[pair[0]]
		end, okEnd := first[pair[1]]
		if okStart && okEnd {
			validation.LatenciesMS[label] = end - start
		}
	}
	if start, ok := first["audio_play_started"]; ok {
		if end, ok := last["audio_play_stopped"]; ok {
			validation.LatenciesMS["playback"] = end - start
		}
	}
	if start, ok := first["cancel_requested"]; ok {
		if end, ok := first["cancel_acknowledged"]; ok {
			validation.LatenciesMS["cancel"] = end - start
		}
	}
	validation.Valid = len(validation.Missing) == 0
	return validation
}

type interactionTraceStore struct {
	dir string
	err error
}

func newInteractionTraceStore(dir string) *interactionTraceStore {
	s := &interactionTraceStore{dir: dir}
	if dir != "" {
		s.err = os.MkdirAll(dir, 0700)
		if s.err == nil {
			info, err := os.Lstat(dir)
			if err != nil {
				s.err = err
			} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				s.err = errors.New("invalid_trace_directory")
			}
		}
		if s.err == nil {
			s.err = s.prune()
		}
	}
	return s
}
func (s *interactionTraceStore) save(op string, events []InteractionTraceEvent) error {
	if s.dir == "" {
		return nil
	}
	if s.err != nil {
		return s.err
	}
	if !interactionIdentifier.MatchString(op) || len(events) > maxInteractionEvents {
		return errors.New("invalid_trace")
	}
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.dir, ".trace-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(s.dir, op+".json")); err != nil {
		return err
	}
	return s.prune()
}
func (s *interactionTraceStore) expire(op string) {
	if s.dir == "" || !interactionIdentifier.MatchString(op) {
		return
	}
	path := filepath.Join(s.dir, op+".json")
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(path)
	}
}
func (s *interactionTraceStore) read(op string) ([]InteractionTraceEvent, error) {
	if s.dir == "" || !interactionIdentifier.MatchString(op) {
		return nil, errors.New("operation_not_found")
	}
	path := filepath.Join(s.dir, op+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 || time.Since(info.ModTime()) > maxInteractionAge {
		return nil, errors.New("operation_not_found")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("trace_read_failed")
	}
	var events []InteractionTraceEvent
	if json.Unmarshal(data, &events) != nil || len(events) > maxInteractionEvents {
		return nil, errors.New("invalid_trace")
	}
	return events, nil
}
func (s *interactionTraceStore) prune() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type traceFile struct {
		name string
		at   time.Time
	}
	var files []traceFile
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "operation_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if time.Since(info.ModTime()) > maxInteractionAge {
			if err = os.Remove(filepath.Join(s.dir, entry.Name())); err != nil {
				return err
			}
			continue
		}
		files = append(files, traceFile{entry.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for len(files) > maxInteractionTurns {
		if err = os.Remove(filepath.Join(s.dir, files[0].name)); err != nil {
			return err
		}
		files = files[1:]
	}
	return nil
}
