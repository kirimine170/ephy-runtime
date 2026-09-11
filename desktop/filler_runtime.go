package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// A deliberately separate，closed schema．No arbitrary payload can reach disk．
type FillerTrace struct {
	Kind      string  `json:"kind"`
	LatencyMS float64 `json:"latency_ms"`
}
type FillerTiming struct {
	LLMRequestMS  float64 `json:"llm_request_ms"`
	LLMFirstMS    float64 `json:"llm_first_ms"`
	TTSRequestMS  float64 `json:"tts_request_ms"`
	TTSChunkMS    float64 `json:"tts_chunk_ms"`
	AnswerReadyMS float64 `json:"answer_ready_ms"`
	LLMttftMS     float64 `json:"llm_ttft_ms"`
	TTSLatencyMS  float64 `json:"tts_latency_ms"`
}
type FillerSetup struct {
	Enabled bool           `json:"enabled"`
	Status  string         `json:"status"`
	Samples []FillerTiming `json:"samples"`
	Assets  []FillerAudio  `json:"assets"`
}
type fillerGroup struct {
	Samples []FillerTiming `json:"samples"`
	Trace   []FillerTrace  `json:"trace"`
	Updated int64          `json:"updated"`
}
type fillerStore struct {
	Groups map[string]fillerGroup `json:"groups"`
}

func validFillerTiming(s FillerTiming) bool {
	for _, v := range []float64{s.LLMRequestMS, s.LLMFirstMS, s.TTSRequestMS, s.TTSChunkMS, s.AnswerReadyMS, s.LLMttftMS, s.TTSLatencyMS} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 180000 {
			return false
		}
	}
	return s.LLMttftMS > 0 && s.TTSLatencyMS > 0 && s.LLMRequestMS <= s.LLMFirstMS && s.LLMFirstMS <= s.TTSRequestMS && s.TTSRequestMS <= s.TTSChunkMS && s.TTSChunkMS <= s.AnswerReadyMS
}
func validFillerTrace(t FillerTrace) bool {
	if math.IsNaN(t.LatencyMS) || math.IsInf(t.LatencyMS, 0) || t.LatencyMS < 0 || t.LatencyMS > 180000 {
		return false
	}
	switch t.Kind {
	case "backchannel_started", "backchannel_ended", "backchannel_stopped", "backchannel_watchdog", "backchannel_failed":
		return true
	case "filler_started", "filler_ended", "filler_disabled", "filler_expired", "filler_suppressed_fast", "filler_stopped_answer", "filler_stopped_cancel", "filler_stopped_barge_in", "filler_stopped_invalidated", "filler_failed", "filler_watchdog", "filler_gap", "filler_gap_exceeded", "filler_answer_wait":
		return true
	}
	return false
}
func fillerScope(t *interactionTurn) string {
	if t.speech == nil || !t.fillerIdentityReady {
		return ""
	}
	provider, model, voice := t.speech.Identity()
	body, _ := json.Marshal([]string{runtime.GOOS, runtime.GOARCH, runtime.Version(), provider, model, voice,
		t.request.Chat.ProviderID, t.request.Chat.ModelID, t.request.Chat.ConfigurationID, os.Getenv("EPHY_FILLER_CONDITION")})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func (e *InteractionEngine) readFillerStoreLocked() fillerStore {
	store := fillerStore{Groups: map[string]fillerGroup{}}
	if e.store == nil || e.store.dir == "" {
		return store
	}
	body, err := readFillerPrivate(filepath.Join(e.store.dir, "filler-calibration.json"), 4<<20)
	if err != nil || decodeSpeechJSON(body, &store) != nil || store.Groups == nil || len(store.Groups) > 32 {
		return fillerStore{Groups: map[string]fillerGroup{}}
	}
	for key, g := range store.Groups {
		digest, err := hex.DecodeString(key)
		valid := err == nil && len(digest) == 32 && len(g.Samples) <= 200 && len(g.Trace) <= 256 && time.Now().Unix()-g.Updated <= 7*24*3600
		for _, s := range g.Samples {
			valid = valid && validFillerTiming(s)
		}
		for _, t := range g.Trace {
			valid = valid && validFillerTrace(t)
		}
		if !valid {
			delete(store.Groups, key)
		}
	}
	return store
}
func (e *InteractionEngine) saveFillerStoreLocked(store fillerStore) error {
	if e.store == nil || e.store.dir == "" || e.store.err != nil {
		return errors.New("filler_trace_unavailable")
	}
	for len(store.Groups) > 32 {
		oldest := ""
		var age int64
		for k, g := range store.Groups {
			if oldest == "" || g.Updated < age {
				oldest, age = k, g.Updated
			}
		}
		delete(store.Groups, oldest)
	}
	body, err := json.Marshal(store)
	if err != nil {
		return errors.New("filler_trace_unavailable")
	}
	file, err := os.CreateTemp(e.store.dir, ".filler-")
	if err != nil {
		return errors.New("filler_trace_unavailable")
	}
	name := file.Name()
	defer os.Remove(name)
	_, err = file.Write(body)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("filler_trace_unavailable")
	}
	if os.Rename(name, filepath.Join(e.store.dir, "filler-calibration.json")) != nil {
		return errors.New("filler_trace_unavailable")
	}
	return nil
}

func (a *App) GetInteractionFiller(operationID string, revision int) FillerSetup {
	result := FillerSetup{Status: "disabled", Samples: []FillerTiming{}, Assets: []FillerAudio{}}
	e := a.interactionEngine()
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[operationID]
	if t == nil || !e.liveLocked(t) || revision != 1 || t.snapshot.GenerationRevision != revision || t.fillerSetupServed || !t.fillerIdentityReady {
		return result
	}
	t.fillerSetupServed = true
	// Explicit condition distinguishes this host／cold-warm regime／load setup．
	if os.Getenv("EPHY_FILLER_CONDITION") == "" || (os.Getenv("EPHY_FILLER_BUNDLE") == "" && os.Getenv("EPHY_FILLER_SHADOW") != "1") {
		return result
	}
	t.fillerScope = fillerScope(t)
	if t.fillerScope == "" {
		return result
	}
	group := e.readFillerStoreLocked().Groups[t.fillerScope]
	result.Samples = append([]FillerTiming{}, group.Samples...)
	result.Status = "calibrating"
	assets, err := loadFillerAssets(os.Getenv("EPHY_FILLER_BUNDLE"), t.speech)
	if err != nil {
		result.Status = "assets_unavailable"
		return result
	}
	if len(group.Samples) < 30 {
		return result
	}
	// Repeated overrun／output failures disable this condition until recalibration．
	failures := 0
	for _, event := range group.Trace {
		if event.Kind == "filler_gap_exceeded" || event.Kind == "filler_failed" || event.Kind == "filler_watchdog" || event.Kind == "backchannel_failed" || event.Kind == "backchannel_watchdog" {
			failures++
		}
	}
	if failures >= 3 {
		result.Status = "quality_disabled"
		return result
	}
	if t.snapshot.LastAudioSequence > 0 {
		result.Status = "answer_ready"
		return result
	}
	result.Enabled = true
	result.Status = "ready"
	result.Assets = assets
	return result
}

// Called after first body decode．Native durations are recomputed from Go's
// trace，rather than trusting the browser to invent a faster LLM／TTS value．
func (a *App) RecordInteractionFillerTiming(operationID string, revision int, sample FillerTiming) error {
	e := a.interactionEngine()
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[operationID]
	if t == nil || !e.liveLocked(t) || revision != 1 || t.snapshot.GenerationRevision != revision || t.fillerSampleSaved || t.fillerScope == "" || t.fillerScope != fillerScope(t) || len(t.chunks) == 0 {
		return nil
	}
	values := ValidateTrace(t.events).LatenciesMS
	llm, okLLM := values["llm_ttft"]
	tts, okTTS := values["tts_ttfc"]
	if !okLLM || !okTTS {
		return nil
	}
	sample.LLMttftMS = float64(llm)
	sample.TTSLatencyMS = float64(tts)
	if !validFillerTiming(sample) {
		return errors.New("invalid_filler_timing")
	}
	store := e.readFillerStoreLocked()
	group := store.Groups[t.fillerScope]
	group.Samples = append(group.Samples, sample)
	if len(group.Samples) > 200 {
		group.Samples = group.Samples[len(group.Samples)-200:]
	}
	group.Updated = time.Now().Unix()
	store.Groups[t.fillerScope] = group
	if err := e.saveFillerStoreLocked(store); err != nil {
		return err
	}
	t.fillerSampleSaved = true
	return nil
}
func (a *App) RecordInteractionFillerTrace(operationID string, revision int, event FillerTrace) error {
	if !validFillerTrace(event) {
		return errors.New("invalid_filler_trace")
	}
	e := a.interactionEngine()
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.turns[operationID]
	// Terminal cancel may arrive before its local stop telemetry．Still bounded
	// by the original revision，scope and a per-turn event budget．
	if t == nil || revision != 1 || t.snapshot.GenerationRevision != revision || t.fillerScope == "" || t.fillerTraceCount >= 12 {
		return nil
	}
	store := e.readFillerStoreLocked()
	group := store.Groups[t.fillerScope]
	group.Trace = append(group.Trace, event)
	if len(group.Trace) > 256 {
		group.Trace = group.Trace[len(group.Trace)-256:]
	}
	group.Updated = time.Now().Unix()
	store.Groups[t.fillerScope] = group
	t.fillerTraceCount++
	return e.saveFillerStoreLocked(store)
}
