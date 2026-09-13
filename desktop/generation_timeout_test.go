package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReasoningStreamOutlivesControlTimeoutAndStillCancels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"回答です．\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	app := NewApp()
	app.baseURL = server.URL
	app.httpClient.Timeout = 5 * time.Millisecond
	request := ChatRequest{Mode: "work", Prompt: "合成テスト", Stream: true}
	response, err := app.chatWithContext(context.Background(), request, nil)
	if err != nil || response == nil || !response.Generation.Complete {
		t.Fatal("control timeout cut off a live reasoning request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err = app.chatWithContext(ctx, request, nil); err == nil {
		t.Fatal("generation ignored the caller's cancellation deadline")
	}
	if app.httpClient.Timeout != 5*time.Millisecond || app.clientForPath("/health").Timeout != 5*time.Millisecond {
		t.Fatal("generation changed the control request timeout")
	}
}

func TestReasoningGenerationHasFiniteAllowanceBeyondNinetySeconds(t *testing.T) {
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, _ ChatRequest, _ func(string)) (*ChatResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 5*time.Minute || time.Until(deadline) > 15*time.Minute {
			t.Error("reasoning needs a finite allowance beyond the former 90-second cutoff")
		}
		return completedVoiceResponse("合成テストの回答です．"), nil
	})
	defaults := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, nil, nil, "")
	t.Cleanup(defaults.Close)
	h.engine.Timeouts.LLM = defaults.Timeouts.LLM
	s := h.start(t, "reasoning-timeout-fixture", GenerationLimits{})
	awaitInteraction(t, h.engine, s.OperationID, "COMPLETED")
}
