package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const workerSearchCancelTimeout = 3 * time.Second

// A plan identifier selects a transport policy，not authorization．The Gateway
// remains responsible for approval and only cancels a submitted Worker session．
func workerSearchPlanID(enabled bool, planID string) string {
	if !enabled || len(planID) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(planID); err != nil {
		return ""
	}
	return planID
}

func workerSearchPayloadPlan(path string, payload any) string {
	if path != "/v1/chat/completions" {
		return ""
	}
	switch request := payload.(type) {
	case GatewayChatRequest:
		return workerSearchPlanID(request.Metadata.WebSearch, request.Metadata.WebPlanID)
	case *GatewayChatRequest:
		if request != nil {
			return workerSearchPlanID(request.Metadata.WebSearch, request.Metadata.WebPlanID)
		}
	}
	return ""
}

func workerSearchClient(client *http.Client, path string, payload any) *http.Client {
	if workerSearchPayloadPlan(path, payload) == "" {
		return client
	}
	copy := *client
	copy.Timeout = 0
	return &copy
}

func (a *App) clientForPayload(path string, payload any) *http.Client {
	return workerSearchClient(a.clientForPath(path), path, payload)
}

// Called once by a started chat transport．Only intentional caller cancellation
// cancels the remote search；transport failures retain the resumable plan．
func (a *App) cancelWorkerSearchOnCallerCancel(ctx context.Context, path string, payload any, result error) {
	planID := workerSearchPayloadPlan(path, payload)
	if planID == "" || !errors.Is(result, context.Canceled) || ctx.Err() != context.Canceled {
		return
	}
	control, cancel := context.WithTimeout(context.Background(), workerSearchCancelTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"plan_id": planID})
	request, err := http.NewRequestWithContext(control, http.MethodPost, a.baseURL+"/v1/web-search/cancel", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	client := *a.httpClient
	client.Timeout = workerSearchCancelTimeout
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
	}
}

func runGenerationStage[T any](ctx context.Context, timeout time.Duration, request ChatRequest, work func(context.Context) (T, error)) (T, error) {
	if request.SourceScope != "recorded_conversation" && workerSearchPlanID(request.WebSearch, request.WebPlanID) != "" {
		return runInteractionContext(ctx, work)
	}
	return runInteractionStage(ctx, timeout, work)
}
