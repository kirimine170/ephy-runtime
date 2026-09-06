package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var interactionFailureTags = map[string]bool{"asr_error": true, "early_endpoint": true, "late_endpoint": true, "slow_response": true, "too_long": true, "tone_mismatch": true, "tts_pronunciation": true, "cancel_failure": true, "memory_misuse": true, "incomplete_response": true, "other": true}

type InteractionReplayRequest struct {
	SourceOperationID string `json:"source_operation_id"`
	Transcript        string `json:"transcript,omitempty"`
}

func (a *App) ReplayInteraction(request InteractionReplayRequest) (InteractionSnapshot, error) {
	snapshot, err := a.interactionEngine().Snapshot(request.SourceOperationID)
	if err != nil {
		return InteractionSnapshot{}, err
	}
	if !interactionTerminal(snapshot.State) {
		return InteractionSnapshot{}, errors.New("interaction_busy")
	}
	original, err := a.interactionEngine().GetRequest(request.SourceOperationID)
	if err != nil {
		return InteractionSnapshot{}, err
	}
	transcript := request.Transcript
	if strings.TrimSpace(transcript) == "" {
		transcript = snapshot.Transcript
	}
	if strings.TrimSpace(transcript) == "" || len(transcript) > 16000 {
		return InteractionSnapshot{}, errors.New("invalid_replay_transcript")
	}
	original.InputKind = "transcript"
	original.Chat.WebSearch = false
	original.Chat.WebPlanID = ""
	replay, err := a.StartInteraction(original)
	if err != nil {
		return replay, err
	}
	if err = a.interactionEngine().Commit(replay.OperationID, nil, transcript); err != nil {
		_ = a.interactionEngine().Fail(replay.OperationID, "invalid_audio")
		return InteractionSnapshot{}, err
	}
	return replay, nil
}

type InteractionComparisonRequest struct {
	RequestID         string  `json:"request_id"`
	SourceOperationID string  `json:"source_operation_id"`
	ModeA             string  `json:"mode_a"`
	ModeB             string  `json:"mode_b"`
	TemperatureA      float64 `json:"temperature_a"`
	TemperatureB      float64 `json:"temperature_b"`
}
type BlindInteractionComparison struct {
	ComparisonID      string `json:"comparison_id"`
	SourceOperationID string `json:"source_operation_id"`
	CandidateA        string `json:"candidate_a"`
	CandidateB        string `json:"candidate_b"`
}
type InteractionCandidateIdentity struct {
	OperationID     string              `json:"operation_id"`
	ProviderID      string              `json:"provider_id"`
	ModelID         string              `json:"model_id"`
	ConfigurationID string              `json:"configuration_id"`
	LatencyMS       int64               `json:"latency_ms"`
	Generation      *GenerationMetadata `json:"generation,omitempty"`
}
type interactionComparison struct {
	source     InteractionSnapshot
	candidates [2]InteractionCandidateIdentity
	expires    time.Time
}
type InteractionEvaluationRequest struct {
	ComparisonID string   `json:"comparison_id"`
	Choice       string   `json:"choice"`
	Correction   string   `json:"correction"`
	FailureTags  []string `json:"failure_tags"`
}

// Deliberately no transcript, prompt, history, candidate answer, source text or profile fields．
type InteractionEvaluationRecord struct {
	SchemaVersion      int                            `json:"schema_version"`
	ID                 string                         `json:"id"`
	Timestamp          string                         `json:"timestamp"`
	SourceOperationID  string                         `json:"source_operation_id"`
	TraceID            string                         `json:"trace_id"`
	SessionID          string                         `json:"session_id"`
	TurnID             string                         `json:"turn_id"`
	GenerationRevision int                            `json:"generation_revision,omitempty"`
	ComparisonID       string                         `json:"comparison_id,omitempty"`
	Candidates         []InteractionCandidateIdentity `json:"candidates,omitempty"`
	Choice             string                         `json:"choice"`
	Correction         string                         `json:"correction,omitempty"`
	FailureTags        []string                       `json:"failure_tags"`
}

func (a *App) initComparisonsLocked() {
	if a.interactionComparisons == nil {
		a.interactionComparisons = map[string]*interactionComparison{}
	}
	if a.comparisonCancels == nil {
		a.comparisonCancels = map[string]context.CancelFunc{}
	}
	if a.comparisonCanceled == nil {
		a.comparisonCanceled = map[string]time.Time{}
	}
	for id, pair := range a.interactionComparisons {
		if time.Now().After(pair.expires) {
			delete(a.interactionComparisons, id)
		}
	}
	for id, at := range a.comparisonCanceled {
		if time.Since(at) > time.Hour {
			delete(a.comparisonCanceled, id)
		}
	}
	for len(a.interactionComparisons) > 16 {
		oldest := ""
		var at time.Time
		for id, p := range a.interactionComparisons {
			if oldest == "" || p.expires.Before(at) {
				oldest, at = id, p.expires
			}
		}
		delete(a.interactionComparisons, oldest)
	}
}
func (a *App) CancelInteractionComparison(requestID string) error {
	if !interactionIdentifier.MatchString(requestID) {
		return errors.New("invalid_comparison_id")
	}
	a.interactionEvalMu.Lock()
	defer a.interactionEvalMu.Unlock()
	a.initComparisonsLocked()
	if cancel := a.comparisonCancels[requestID]; cancel != nil {
		cancel()
	}
	// Covers cancel arriving before the generation bridge call starts．
	if len(a.comparisonCanceled) >= 32 {
		for id := range a.comparisonCanceled {
			delete(a.comparisonCanceled, id)
			break
		}
	}
	a.comparisonCanceled[requestID] = time.Now()
	return nil
}
func (a *App) GenerateInteractionComparison(request InteractionComparisonRequest) (BlindInteractionComparison, error) {
	empty := BlindInteractionComparison{}
	modes := map[string]bool{"auto": true, "fast": true, "work": true, "code": true, "rag": true}
	if !interactionIdentifier.MatchString(request.RequestID) || !modes[request.ModeA] || !modes[request.ModeB] || math.IsNaN(request.TemperatureA) || math.IsInf(request.TemperatureA, 0) || math.IsNaN(request.TemperatureB) || math.IsInf(request.TemperatureB, 0) || request.TemperatureA < 0 || request.TemperatureA > 2 || request.TemperatureB < 0 || request.TemperatureB > 2 || (request.ModeA == request.ModeB && request.TemperatureA == request.TemperatureB) {
		return empty, errors.New("invalid_comparison_configuration")
	}
	a.interactionEvalMu.Lock()
	closed := a.comparisonClosed
	a.interactionEvalMu.Unlock()
	if closed {
		return empty, errors.New("comparison_closed")
	}
	snapshot, err := a.interactionEngine().Snapshot(request.SourceOperationID)
	if err != nil || !interactionTerminal(snapshot.State) || snapshot.Transcript == "" {
		return empty, errors.New("replay_source_unavailable")
	}
	original, err := a.interactionEngine().GetRequest(request.SourceOperationID)
	if err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a.interactionEvalMu.Lock()
	a.initComparisonsLocked()
	if a.comparisonClosed {
		a.interactionEvalMu.Unlock()
		return empty, errors.New("comparison_closed")
	}
	if len(a.comparisonCancels) != 0 {
		a.interactionEvalMu.Unlock()
		return empty, errors.New("comparison_busy")
	}
	if _, exists := a.comparisonCanceled[request.RequestID]; exists {
		a.interactionEvalMu.Unlock()
		return empty, errors.New("comparison_canceled")
	}
	if _, exists := a.interactionComparisons[request.RequestID]; exists {
		a.interactionEvalMu.Unlock()
		return empty, errors.New("comparison_id_used")
	}
	a.comparisonCancels[request.RequestID] = cancel
	a.interactionEvalMu.Unlock()
	defer func() {
		a.interactionEvalMu.Lock()
		delete(a.comparisonCancels, request.RequestID)
		a.interactionEvalMu.Unlock()
	}()
	pair := &interactionComparison{source: snapshot, expires: time.Now().Add(time.Hour)}
	// Only identifiers from source snapshot are retained in the comparison cache．
	pair.source.Transcript = ""
	pair.source.ResponsePlan = nil
	var answers [2]string
	for i, mode := range []string{request.ModeA, request.ModeB} {
		req := original.Chat
		req.Mode = mode
		req.Prompt = snapshot.Transcript
		req.Stream = true
		req.SessionMode = "voice"
		req.Temperature = []float64{request.TemperatureA, request.TemperatureB}[i]
		req.temperatureExplicit = true
		req.RequestID = interactionID("operation_")
		// Existing explicit web approval is turn-specific，so replay comparisons stay local．
		req.WebSearch = false
		req.WebPlanID = ""
		if req.MaxTokens <= 0 || req.MaxTokens > 512 {
			req.MaxTokens = 512
		}
		config, _ := json.Marshal(struct {
			Mode        string
			Temperature float64
			MaxTokens   int
		}{mode, req.Temperature, req.MaxTokens})
		digest := sha256.Sum256(config)
		identity := InteractionCandidateIdentity{OperationID: req.RequestID, ProviderID: "gateway-router", ModelID: "route:" + mode, ConfigurationID: hex.EncodeToString(digest[:8])}
		candidateCtx := context.WithValue(ctx, interactionModelKey{}, func(provider, model, configuration string) {
			if interactionIdentifier.MatchString(provider) && interactionMetadataID.MatchString(model) && interactionIdentifier.MatchString(configuration) {
				identity.ProviderID, identity.ModelID = provider, model
				resolved := sha256.Sum256([]byte(configuration + ":" + hex.EncodeToString(digest[:8])))
				identity.ConfigurationID = hex.EncodeToString(resolved[:16])
			}
		})
		start := time.Now()
		response, err := assembleGeneration(candidateCtx, req, original.GenerationLimits, "", a.chatWithContext, func(GenerationProgress) {})
		if err != nil || ctx.Err() != nil {
			return empty, errors.New("comparison_failed_or_canceled")
		}
		if response == nil || response.Generation == nil || !response.Generation.Complete || strings.TrimSpace(response.Answer) == "" || len(response.Answer) > 64000 {
			return empty, errors.New("comparison_invalid_response")
		}
		identity.LatencyMS = time.Since(start).Milliseconds()
		identity.Generation = cloneGenerationMetadata(response.Generation)
		pair.candidates[i] = identity
		answers[i] = response.Answer
	}
	var shuffle [1]byte
	if _, err := rand.Read(shuffle[:]); err != nil {
		return empty, err
	}
	if shuffle[0]&1 == 1 {
		pair.candidates[0], pair.candidates[1] = pair.candidates[1], pair.candidates[0]
		answers[0], answers[1] = answers[1], answers[0]
	}
	a.interactionEvalMu.Lock()
	defer a.interactionEvalMu.Unlock()
	if ctx.Err() != nil {
		return empty, errors.New("comparison_canceled")
	}
	a.interactionComparisons[request.RequestID] = pair
	a.initComparisonsLocked()
	return BlindInteractionComparison{request.RequestID, request.SourceOperationID, answers[0], answers[1]}, nil
}
func validateInteractionVote(request InteractionEvaluationRequest) error {
	if request.Choice != "A" && request.Choice != "B" && request.Choice != "tie" && request.Choice != "neither" {
		return errors.New("invalid_preference_choice")
	}
	if len(request.Correction) > 16000 || len(request.FailureTags) > len(interactionFailureTags) {
		return errors.New("evaluation_too_large")
	}
	seen := map[string]bool{}
	for _, tag := range request.FailureTags {
		if !interactionFailureTags[tag] || seen[tag] {
			return errors.New("invalid_failure_tag")
		}
		seen[tag] = true
	}
	return nil
}
func (a *App) SaveInteractionEvaluation(request InteractionEvaluationRequest) (InteractionEvaluationRecord, error) {
	if err := validateInteractionVote(request); err != nil {
		return InteractionEvaluationRecord{}, err
	}
	a.interactionEvalMu.Lock()
	defer a.interactionEvalMu.Unlock()
	a.initComparisonsLocked()
	pair := a.interactionComparisons[request.ComparisonID]
	if pair == nil {
		return InteractionEvaluationRecord{}, errors.New("comparison_expired")
	}
	s := pair.source
	current, err := a.interactionEngine().Snapshot(s.OperationID)
	if err != nil || current.GenerationRevision != s.GenerationRevision {
		return InteractionEvaluationRecord{}, errors.New("comparison_source_changed")
	}
	record := InteractionEvaluationRecord{SchemaVersion: 2, ID: interactionID("evaluation_"), Timestamp: time.Now().UTC().Format(time.RFC3339Nano), SourceOperationID: s.OperationID, TraceID: s.TraceID, SessionID: s.SessionID, TurnID: s.TurnID, GenerationRevision: s.GenerationRevision, ComparisonID: request.ComparisonID, Candidates: append([]InteractionCandidateIdentity(nil), pair.candidates[:]...), Choice: request.Choice, Correction: request.Correction, FailureTags: append([]string(nil), request.FailureTags...)}
	if err := a.storeInteractionEvaluationLocked(record); err != nil {
		return InteractionEvaluationRecord{}, err
	}
	return record, nil
}
func (a *App) RecordInteractionFeedback(operationID string) (InteractionEvaluationRecord, error) {
	snapshot, err := a.interactionEngine().Snapshot(operationID)
	if err != nil {
		return InteractionEvaluationRecord{}, err
	}
	a.interactionEvalMu.Lock()
	defer a.interactionEvalMu.Unlock()
	tag := "other"
	if snapshot.State == "INCOMPLETE" {
		tag = "incomplete_response"
	}
	record := InteractionEvaluationRecord{SchemaVersion: 2, ID: interactionID("evaluation_"), Timestamp: time.Now().UTC().Format(time.RFC3339Nano), SourceOperationID: operationID, TraceID: snapshot.TraceID, SessionID: snapshot.SessionID, TurnID: snapshot.TurnID, GenerationRevision: snapshot.GenerationRevision, Choice: "flagged", FailureTags: []string{tag}}
	if err := a.storeInteractionEvaluationLocked(record); err != nil {
		return InteractionEvaluationRecord{}, err
	}
	return record, nil
}
func (a *App) interactionLocalDirectory(parts ...string) (string, error) {
	root := a.workspaceRoot
	if root == "" {
		root = detectWorkspaceRoot()
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("evaluation_storage_unavailable")
	}
	dir := filepath.Join(append([]string{root}, parts...)...)
	check := func() error {
		for p := dir; p != filepath.Dir(p); p = filepath.Dir(p) {
			info, err := os.Lstat(p)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return errors.New("evaluation_storage_unavailable")
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("evaluation_storage_unavailable")
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("evaluation_storage_unavailable")
	}
	if err := check(); err != nil {
		return "", err
	}
	return dir, nil
}
func (a *App) interactionEvaluationPath() (string, error) {
	dir, err := a.interactionLocalDirectory("data", "runtime", "interaction-evaluations")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "records.json"), nil
}
func (a *App) loadInteractionEvaluationsLocked() ([]InteractionEvaluationRecord, error) {
	path, err := a.interactionEvaluationPath()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return []InteractionEvaluationRecord{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("evaluation_storage_unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []InteractionEvaluationRecord
	if json.Unmarshal(data, &records) != nil || len(records) > 256 {
		return nil, errors.New("evaluation_storage_invalid")
	}
	return records, nil
}
func (a *App) storeInteractionEvaluationLocked(record InteractionEvaluationRecord) error {
	records, err := a.loadInteractionEvaluationsLocked()
	if err != nil {
		return err
	}
	// One current vote per comparison; repeat clicks and corrections replace that vote．
	kept := records[:0]
	for _, old := range records {
		if record.ComparisonID != "" && old.ComparisonID == record.ComparisonID {
			continue
		}
		if record.Choice == "flagged" && old.Choice == "flagged" && old.SourceOperationID == record.SourceOperationID {
			continue
		}
		kept = append(kept, old)
	}
	records = append(kept, record)
	if len(records) > 256 {
		records = records[len(records)-256:]
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	path, err := a.interactionEvaluationPath()
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".evaluation-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func (a *App) ExportInteractionEvaluations(format string) (*ExportResultResponse, error) {
	if format != "json" && format != "jsonl" {
		return nil, errors.New("invalid_export_format")
	}
	a.interactionEvalMu.Lock()
	defer a.interactionEvalMu.Unlock()
	records, err := a.loadInteractionEvaluationsLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Timestamp < records[j].Timestamp })
	var data []byte
	if format == "json" {
		data, err = json.MarshalIndent(records, "", "  ")
	} else {
		for _, record := range records {
			line, e := json.Marshal(record)
			if e != nil {
				return nil, e
			}
			data = append(data, append(line, '\n')...)
		}
	}
	if err != nil {
		return nil, err
	}
	dir, err := a.interactionLocalDirectory("data", "exports")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, interactionID("interaction-evaluations-")+"."+format)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return &ExportResultResponse{Path: path}, nil
}
