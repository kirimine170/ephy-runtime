package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func evaluationTestApp(t *testing.T) (*App, InteractionSnapshot) {
	t.Helper()
	a := NewApp()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.workspaceRoot = root
	var e *InteractionEngine
	e = NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, func(event InteractionEvent) {
		if event.Kind == "audio" {
			e.Playback(event.OperationID, event.Sequence, "started")
			e.Playback(event.OperationID, event.Sequence, "stopped")
		}
	}, "")
	a.interaction = e
	t.Cleanup(e.Close)
	s, err := a.StartInteraction(VoiceTurnRequest{SessionID: "evaluation-session", Chat: ChatRequest{Mode: "auto", Prompt: "private prompt", Messages: []GatewayMessage{{Role: "user", Content: "private prior user"}, {Role: "assistant", Content: "private prior answer"}}, WebSearch: true, WebPlanID: "old-approval", SourceScope: "private-scope"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Commit(s.OperationID, nil, "private source transcript"); err != nil {
		t.Fatal(err)
	}
	return a, awaitInteraction(t, e, s.OperationID, "COMPLETED")
}
func evaluationRequest(source InteractionSnapshot, id string) InteractionComparisonRequest {
	return InteractionComparisonRequest{RequestID: id, SourceOperationID: source.OperationID, ModeA: "fast", ModeB: "work", TemperatureA: 0.2, TemperatureB: 0.8}
}
func evaluationServer(t *testing.T, a *App) <-chan GatewayChatRequest {
	t.Helper()
	requests := make(chan GatewayChatRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body GatewayChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: route\ndata: {\"provider\":\"local-provider\",\"model\":\"local/%s\",\"configuration_id\":\"resolved-config\"}\n\n", body.Metadata.Mode)
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "private candidate answer " + body.Metadata.Mode}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	t.Cleanup(server.Close)
	a.baseURL = server.URL
	return requests
}

func TestInteractionReplayPreservesHistoryAndSessionWithFreshIDs(t *testing.T) {
	a, source := evaluationTestApp(t)
	replay, err := a.ReplayInteraction(InteractionReplayRequest{SourceOperationID: source.OperationID, Transcript: "explicit replay transcript"})
	if err != nil {
		t.Fatal(err)
	}
	done := awaitInteraction(t, a.interaction, replay.OperationID, "COMPLETED")
	if replay.SessionID != source.SessionID || replay.OperationID == source.OperationID || replay.TraceID == source.TraceID || replay.TurnID == source.TurnID {
		t.Fatal(source, replay)
	}
	original, _ := a.interaction.GetRequest(source.OperationID)
	current, _ := a.interaction.GetRequest(replay.OperationID)
	if !reflect.DeepEqual(original.Chat.Messages, current.Chat.Messages) || current.Chat.Prompt != "explicit replay transcript" || done.Transcript != "explicit replay transcript" || current.InputKind != "transcript" {
		t.Fatal(original, current, done)
	}
	if current.Chat.WebSearch || current.Chat.WebPlanID != "" {
		t.Fatal("reused previous turn web approval")
	}
	fallback, err := a.ReplayInteraction(InteractionReplayRequest{SourceOperationID: source.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	awaitInteraction(t, a.interaction, fallback.OperationID, "COMPLETED")
	fallbackRequest, _ := a.interaction.GetRequest(fallback.OperationID)
	if fallbackRequest.Chat.Prompt != source.Transcript {
		t.Fatal(fallbackRequest)
	}
	if _, err := a.ReplayInteraction(InteractionReplayRequest{SourceOperationID: source.OperationID, Transcript: strings.Repeat("x", 16001)}); err == nil {
		t.Fatal("oversized replay accepted")
	}
}

func TestBlindInteractionComparisonUsesEqualHistoryDistinctConfigurationsAndPrivateRecords(t *testing.T) {
	a, source := evaluationTestApp(t)
	requests := evaluationServer(t, a)
	blind, err := a.GenerateInteractionComparison(evaluationRequest(source, "comparison-private"))
	if err != nil {
		t.Fatal(err)
	}
	first, second := <-requests, <-requests
	if !reflect.DeepEqual(first.Messages, second.Messages) || len(first.Messages) != 3 || first.Messages[2].Content != source.Transcript {
		t.Fatal(first, second)
	}
	if first.Metadata.SessionID != source.SessionID || second.Metadata.SessionID != source.SessionID || first.Metadata.SessionMode != "voice" || second.Metadata.SessionMode != "voice" {
		t.Fatal("session changed")
	}
	if first.Metadata.Mode == second.Metadata.Mode || first.Temperature == nil || second.Temperature == nil || *first.Temperature == *second.Temperature {
		t.Fatal("candidate configuration did not differ")
	}
	if first.Metadata.WebSearch || second.Metadata.WebSearch || first.Metadata.WebPlanID != "" || second.Metadata.WebPlanID != "" {
		t.Fatal("comparison reused web approval")
	}
	answers := map[string]bool{blind.CandidateA: true, blind.CandidateB: true}
	if !answers["private candidate answer fast"] || !answers["private candidate answer work"] {
		t.Fatal(blind)
	}
	encodedBlind, _ := json.Marshal(blind)
	for _, hidden := range []string{"model_id", "provider_id", "configuration_id"} {
		if strings.Contains(string(encodedBlind), hidden) {
			t.Fatal("identity exposed before vote", string(encodedBlind))
		}
	}
	a.interactionEvalMu.Lock()
	pair := a.interactionComparisons[blind.ComparisonID]
	if pair.source.Transcript != "" || pair.source.ResponsePlan != nil {
		t.Error("raw source retained in comparison cache")
	}
	a.interactionEvalMu.Unlock()
	vote := InteractionEvaluationRequest{ComparisonID: blind.ComparisonID, Choice: "A", Correction: "user-authored correction", FailureTags: []string{"too_long", "tone_mismatch"}}
	record, err := a.SaveInteractionEvaluation(vote)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Candidates) != 2 || record.Candidates[0].OperationID == record.Candidates[1].OperationID || record.Candidates[0].OperationID == source.OperationID || record.Candidates[0].ConfigurationID == record.Candidates[1].ConfigurationID {
		t.Fatal(record)
	}
	for _, candidate := range record.Candidates {
		if candidate.ProviderID != "local-provider" || !strings.HasPrefix(candidate.ModelID, "local/") || !interactionIdentifier.MatchString(candidate.ConfigurationID) {
			t.Fatal(candidate)
		}
	}
	originalIdentity := record.Candidates[0]
	record.Candidates[0].ModelID = "mutated"
	vote.FailureTags[0] = "other"
	if record.FailureTags[0] != "too_long" {
		t.Fatal("returned tags alias caller request")
	}
	updated, err := a.SaveInteractionEvaluation(InteractionEvaluationRequest{ComparisonID: blind.ComparisonID, Choice: "B", Correction: "user-authored correction", FailureTags: []string{"too_long"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Candidates[0] != originalIdentity {
		t.Fatal("returned record mutated private pair")
	}
	a.interactionEvalMu.Lock()
	records, err := a.loadInteractionEvaluationsLocked()
	a.interactionEvalMu.Unlock()
	if err != nil || len(records) != 1 || records[0].Choice != "B" || records[0].FailureTags[0] != "too_long" {
		t.Fatal(records, err)
	}
	path, err := a.interactionEvaluationPath()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{path}
	for _, format := range []string{"json", "jsonl"} {
		result, err := a.ExportInteractionEvaluations(format)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, result.Path)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{"private source transcript", "private prompt", "private prior user", "private prior answer", "private candidate answer", "private-scope", "UklGR", "\"audio\"", "\"prompt\"", "\"messages\"", "\"answer\"", "\"context\""} {
			if strings.Contains(string(data), raw) {
				t.Fatalf("source content persisted in %s: %s", path, raw)
			}
		}
		if !strings.Contains(string(data), "user-authored correction") {
			t.Fatal("explicit correction was lost")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("evaluation file permissions", info.Mode())
		}
	}
	if _, err := a.GenerateInteractionComparison(evaluationRequest(source, blind.ComparisonID)); err == nil {
		t.Fatal("comparison ID reused")
	}
}

func TestInteractionComparisonCacheKeepsSixteenthVoteAndEvictsOnlyOnInsert(t *testing.T) {
	a, source := evaluationTestApp(t)
	a.interactionEvalMu.Lock()
	a.initComparisonsLocked()
	for i := 0; i < 16; i++ {
		a.interactionComparisons[fmt.Sprintf("pair-%02d", i)] = &interactionComparison{source: source, expires: time.Now().Add(time.Hour + time.Duration(i)*time.Second)}
	}
	a.interactionEvalMu.Unlock()
	if _, err := a.SaveInteractionEvaluation(InteractionEvaluationRequest{ComparisonID: "pair-00", Choice: "tie"}); err != nil {
		t.Fatal("saving at capacity evicted existing vote", err)
	}
	a.interactionEvalMu.Lock()
	if len(a.interactionComparisons) != 16 {
		t.Error("save changed capacity")
	}
	a.interactionComparisons["pair-new"] = &interactionComparison{source: source, expires: time.Now().Add(2 * time.Hour)}
	a.initComparisonsLocked()
	if len(a.interactionComparisons) != 16 || a.interactionComparisons["pair-00"] != nil {
		t.Error("wrong insertion eviction")
	}
	a.interactionComparisons["pair-01"].expires = time.Now().Add(-time.Second)
	a.interactionEvalMu.Unlock()
	if _, err := a.SaveInteractionEvaluation(InteractionEvaluationRequest{ComparisonID: "pair-01", Choice: "A"}); err == nil {
		t.Fatal("expired pair accepted")
	}
}

func TestInteractionEvaluationRetentionAndFeedbackDeduplication(t *testing.T) {
	a, source := evaluationTestApp(t)
	if _, err := a.RecordInteractionFeedback(source.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecordInteractionFeedback(source.OperationID); err != nil {
		t.Fatal(err)
	}
	a.interactionEvalMu.Lock()
	records, err := a.loadInteractionEvaluationsLocked()
	if err != nil || len(records) != 1 {
		a.interactionEvalMu.Unlock()
		t.Fatal(records, err)
	}
	for i := 0; i < 270; i++ {
		record := InteractionEvaluationRecord{SchemaVersion: 1, ID: fmt.Sprintf("evaluation-%03d", i), Timestamp: time.Now().UTC().Format(time.RFC3339Nano), SourceOperationID: source.OperationID, ComparisonID: fmt.Sprintf("comparison-%03d", i), Choice: "tie", FailureTags: []string{}}
		if err := a.storeInteractionEvaluationLocked(record); err != nil {
			a.interactionEvalMu.Unlock()
			t.Fatal(err)
		}
	}
	records, err = a.loadInteractionEvaluationsLocked()
	a.interactionEvalMu.Unlock()
	if err != nil || len(records) != 256 || records[0].ID != "evaluation-014" || records[255].ID != "evaluation-269" {
		t.Fatal(len(records), err)
	}
}

func TestInteractionComparisonRejectsInvalidConfigurationAndVotes(t *testing.T) {
	a, source := evaluationTestApp(t)
	valid := evaluationRequest(source, "invalid-config")
	for _, mutate := range []func(*InteractionComparisonRequest){func(r *InteractionComparisonRequest) { r.TemperatureA = math.NaN() }, func(r *InteractionComparisonRequest) { r.TemperatureB = math.Inf(1) }, func(r *InteractionComparisonRequest) { r.TemperatureA = -0.1 }, func(r *InteractionComparisonRequest) { r.TemperatureB = 2.1 }, func(r *InteractionComparisonRequest) { r.ModeA = "unknown" }, func(r *InteractionComparisonRequest) { r.ModeB = r.ModeA; r.TemperatureB = r.TemperatureA }, func(r *InteractionComparisonRequest) { r.RequestID = "../escape" }} {
		request := valid
		mutate(&request)
		if _, err := a.GenerateInteractionComparison(request); err == nil || err.Error() != "invalid_comparison_configuration" {
			t.Fatal(request, err)
		}
	}
	for _, vote := range []InteractionEvaluationRequest{{Choice: "invalid"}, {Choice: "A", FailureTags: []string{"invented"}}, {Choice: "B", FailureTags: []string{"too_long", "too_long"}}, {Choice: "tie", Correction: strings.Repeat("x", 16001)}} {
		if err := validateInteractionVote(vote); err == nil {
			t.Fatal("invalid vote accepted", vote)
		}
	}
	if _, err := a.ExportInteractionEvaluations("csv"); err == nil {
		t.Fatal("unknown export format accepted")
	}
}

func TestInteractionComparisonCancellationAndShutdownCancelHTTP(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint("shutdown=", shutdown), func(t *testing.T) {
			a, source := evaluationTestApp(t)
			entered := make(chan struct{}, 1)
			closed := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"private partial\"}}]}\n\n")
				w.(http.Flusher).Flush()
				entered <- struct{}{}
				select {
				case <-r.Context().Done():
					closed <- struct{}{}
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			a.baseURL = server.URL
			done := make(chan error, 1)
			request := evaluationRequest(source, "cancel-http")
			go func() { _, err := a.GenerateInteractionComparison(request); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("comparison HTTP not started")
			}
			if _, err := a.GenerateInteractionComparison(evaluationRequest(source, "concurrent")); err == nil || err.Error() != "comparison_busy" {
				t.Fatal("concurrent comparison accepted", err)
			}
			if shutdown {
				a.shutdown(context.Background())
			} else {
				if err := a.CancelInteractionComparison(request.RequestID); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled comparison returned result")
				}
			case <-time.After(time.Second):
				t.Fatal("comparison cancel did not return")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("cancellation left HTTP open")
			}
			a.interactionEvalMu.Lock()
			if len(a.comparisonCancels) != 0 || a.interactionComparisons[request.RequestID] != nil {
				t.Error("canceled comparison retained")
			}
			a.interactionEvalMu.Unlock()
			if shutdown {
				if _, err := a.GenerateInteractionComparison(evaluationRequest(source, "after-close")); err == nil || err.Error() != "comparison_closed" {
					t.Fatal("comparison started after shutdown", err)
				}
			}
		})
	}
	a, source := evaluationTestApp(t)
	request := evaluationRequest(source, "pre-canceled")
	a.CancelInteractionComparison(request.RequestID)
	if _, err := a.GenerateInteractionComparison(request); err == nil || err.Error() != "comparison_canceled" {
		t.Fatal("pre-cancel lost", err)
	}
}

func TestInteractionEvaluationRejectsStorageAndExportSymlinks(t *testing.T) {
	for _, kind := range []string{"records", "storage-directory", "export-directory", "export-ancestor"} {
		t.Run(kind, func(t *testing.T) {
			a, source := evaluationTestApp(t)
			if _, err := a.RecordInteractionFeedback(source.OperationID); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			switch kind {
			case "records":
				path, _ := a.interactionEvaluationPath()
				os.Remove(path)
				os.Symlink(filepath.Join(outside, "records.json"), path)
			case "storage-directory":
				dir := filepath.Join(a.workspaceRoot, "data", "runtime", "interaction-evaluations")
				os.Rename(dir, dir+"-saved")
				os.Symlink(outside, dir)
			case "export-directory":
				os.Symlink(outside, filepath.Join(a.workspaceRoot, "data", "exports"))
			case "export-ancestor":
				data := filepath.Join(a.workspaceRoot, "data")
				os.Rename(data, filepath.Join(outside, "moved-data"))
				os.Symlink(filepath.Join(outside, "moved-data"), data)
			}
			if _, err := a.ExportInteractionEvaluations("json"); err == nil {
				t.Fatal("symlink export accepted")
			}
		})
	}
}

func TestInteractionComparisonSendsExplicitZeroTemperatureWithoutChangingLegacyDefault(t *testing.T) {
	a, source := evaluationTestApp(t)
	requests := evaluationServer(t, a)
	request := evaluationRequest(source, "explicit-zero")
	request.ModeB = request.ModeA
	request.TemperatureA = 0
	blind, err := a.GenerateInteractionComparison(request)
	if err != nil {
		t.Fatal(err)
	}
	first, second := <-requests, <-requests
	if first.Temperature == nil || *first.Temperature != 0 || second.Temperature == nil || *second.Temperature != request.TemperatureB {
		t.Fatal("explicit candidate temperature lost", first.Temperature, second.Temperature)
	}
	record, err := a.SaveInteractionEvaluation(InteractionEvaluationRequest{ComparisonID: blind.ComparisonID, Choice: "tie"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Candidates[0].ConfigurationID == record.Candidates[1].ConfigurationID {
		t.Fatal("different temperature configurations share an ID")
	}
	if _, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "legacy default", Stream: true}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if legacy := <-requests; legacy.Temperature != nil {
		t.Fatal("legacy zero became an explicit temperature", legacy.Temperature)
	}
}
