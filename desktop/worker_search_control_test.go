package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const workerSearchTestPlan = "0123456789abcdef0123456789abcdef"

type workerSearchRoundTripper func(*http.Request) (*http.Response, error)

func (f workerSearchRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func workerSearchTestPayload() GatewayChatRequest {
	return GatewayChatRequest{Model: "auto", Metadata: GatewayMetadata{WebSearch: true, WebPlanID: workerSearchTestPlan}}
}

func TestWorkerSearchClientSelectsOnlyExplicitValidChatPlan(t *testing.T) {
	ordinary := &http.Client{Timeout: 15 * time.Millisecond}
	for _, tc := range []struct {
		name, path string
		payload    any
		search     bool
	}{
		{"planned", "/v1/chat/completions", workerSearchTestPayload(), true},
		{"pointer", "/v1/chat/completions", func() *GatewayChatRequest { p := workerSearchTestPayload(); return &p }(), true},
		{"disabled", "/v1/chat/completions", GatewayChatRequest{Metadata: GatewayMetadata{WebPlanID: workerSearchTestPlan}}, false},
		{"missing", "/v1/chat/completions", GatewayChatRequest{Metadata: GatewayMetadata{WebSearch: true}}, false},
		{"invalid", "/v1/chat/completions", GatewayChatRequest{Metadata: GatewayMetadata{WebSearch: true, WebPlanID: strings.Repeat("g", 32)}}, false},
		{"short", "/v1/chat/completions", GatewayChatRequest{Metadata: GatewayMetadata{WebSearch: true, WebPlanID: "abcd"}}, false},
		{"rag", "/v1/rag/query", workerSearchTestPayload(), false},
		{"control", "/health", workerSearchTestPayload(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := workerSearchClient(ordinary, tc.path, tc.payload)
			if tc.search {
				if selected == ordinary || selected.Timeout != 0 {
					t.Fatal("planned search retained a total generation timeout")
				}
			} else if selected != ordinary || selected.Timeout != ordinary.Timeout {
				t.Fatal("ordinary request timeout changed")
			}
			if ordinary.Timeout != 15*time.Millisecond {
				t.Fatal("shared client mutated")
			}
		})
	}
}

func TestWorkerSearchHTTPJSONAndSSEOutliveOrdinaryClientAllowance(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(80 * time.Millisecond):
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"answer\":\"ready\"}\n\n")
				} else {
					fmt.Fprint(w, `{"answer":"ready"}`)
				}
			}))
			defer server.Close()
			ordinary := &http.Client{Timeout: 15 * time.Millisecond}
			read := func(client *http.Client) (string, error) {
				response, err := client.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader("{}"))
				if err != nil {
					return "", err
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				return string(body), err
			}
			if _, err := read(ordinary); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("ordinary client lost its elapsed deadline", err)
			}
			body, err := read(workerSearchClient(ordinary, "/v1/chat/completions", workerSearchTestPayload()))
			if err != nil || !strings.Contains(body, "ready") {
				t.Fatal("planned search was cut off by the ordinary allowance", err)
			}
		})
	}
}

func TestWorkerSearchProductTransportsUsePayloadPolicy(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, search := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/search=%t", stream, search), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						fmt.Fprint(w, "data: ready\n\n")
					} else {
						fmt.Fprint(w, `{"answer":"ready"}`)
					}
				}))
				defer server.Close()
				app := NewApp()
				app.baseURL = server.URL
				app.httpClient.Timeout = 15 * time.Millisecond
				var observed atomic.Int32
				app.httpClient.Transport = workerSearchRoundTripper(func(r *http.Request) (*http.Response, error) {
					deadline, hasDeadline := r.Context().Deadline()
					if search && hasDeadline || !search && (!hasDeadline || time.Until(deadline) < 5*time.Minute) {
						t.Error("product transport selected the wrong deadline policy")
					}
					observed.Add(1)
					return http.DefaultTransport.RoundTrip(r)
				})
				payload := workerSearchTestPayload()
				payload.Metadata.WebSearch = search
				var err error
				if stream {
					err = app.streamGatewayResponseContext(context.Background(), "/v1/chat/completions", payload, func(_, _ string) error { return nil })
				} else {
					var out map[string]any
					err = app.postJSONContext(context.Background(), "/v1/chat/completions", payload, &out)
				}
				if err != nil || observed.Load() != 1 || app.httpClient.Timeout != 15*time.Millisecond {
					t.Fatal("product request failed or mutated shared timeout", err)
				}
			})
		}
	}
}

func TestWorkerSearchExplicitCancellationReachesPlanOnce(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, cancelStatus := range []int{http.StatusOK, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("stream=%t/cancel_status=%d", stream, cancelStatus), func(t *testing.T) {
				entered := make(chan struct{})
				var cancelCount atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/web-search/cancel" {
						var payload map[string]string
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["plan_id"] != workerSearchTestPlan || r.Method != http.MethodPost {
							t.Error("cancel lost the original plan")
						}
						cancelCount.Add(1)
						w.WriteHeader(cancelStatus)
						fmt.Fprint(w, `{}`)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, ": waiting\n\n")
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-r.Context().Done()
				}))
				defer server.Close()
				app := NewApp()
				app.baseURL = server.URL
				app.httpClient.Transport = workerSearchRoundTripper(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/v1/web-search/cancel" {
						deadline, ok := r.Context().Deadline()
						if !ok || r.Context().Err() != nil || time.Until(deadline) > 3*time.Second || time.Until(deadline) < time.Second {
							t.Error("cancel did not use a fresh bounded control context")
						}
					}
					return http.DefaultTransport.RoundTrip(r)
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan error, 1)
				go func() {
					if stream {
						result <- app.streamGatewayResponseContext(ctx, "/v1/chat/completions", workerSearchTestPayload(), func(_, _ string) error { return nil })
					} else {
						var out map[string]any
						result <- app.postJSONContext(ctx, "/v1/chat/completions", workerSearchTestPayload(), &out)
					}
				}()
				interactionGenerationReceive(t, entered)
				cancel()
				err := interactionGenerationReceive(t, result)
				if !errors.Is(err, context.Canceled) || cancelCount.Load() != 1 {
					t.Fatal("explicit cancellation was replaced or not sent exactly once", err, cancelCount.Load())
				}
			})
		}
	}
}

func TestWorkerSearchTransportFailuresAndInvalidPlansDoNotCancel(t *testing.T) {
	for _, kind := range []string{"success", "network", "eof", "deadline", "missing", "malformed", "disabled", "already_canceled"} {
		t.Run(kind, func(t *testing.T) {
			var cancels atomic.Int32
			entered := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/web-search/cancel" {
					cancels.Add(1)
					fmt.Fprint(w, `{}`)
					return
				}
				entered <- struct{}{}
				switch kind {
				case "network":
					connection, _, _ := w.(http.Hijacker).Hijack()
					connection.Close()
				case "eof":
					fmt.Fprint(w, "data: incomplete")
				case "deadline", "missing", "malformed", "disabled":
					<-r.Context().Done()
				default:
					fmt.Fprint(w, "data: ready\n\n")
				}
			}))
			defer server.Close()
			app := NewApp()
			app.baseURL = server.URL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer deadlineCancel()
			}
			payload := workerSearchTestPayload()
			switch kind {
			case "missing":
				payload.Metadata.WebPlanID = ""
			case "malformed":
				payload.Metadata.WebPlanID = strings.Repeat("z", 32)
			case "disabled":
				payload.Metadata.WebSearch = false
			case "already_canceled":
				cancel()
			}
			result := make(chan error, 1)
			go func() {
				result <- app.streamGatewayResponseContext(ctx, "/v1/chat/completions", payload, func(_, _ string) error { return nil })
			}()
			if kind == "missing" || kind == "malformed" || kind == "disabled" {
				interactionGenerationReceive(t, entered)
				cancel()
			}
			err := interactionGenerationReceive(t, result)
			if kind == "success" && err != nil || kind != "success" && err == nil || cancels.Load() != 0 {
				t.Fatal("transport outcome changed or a resumable failure cancelled the plan", err, cancels.Load())
			}
		})
	}
}

func TestWorkerSearchCoincidentalCancellationDoesNotCancelIndependentFailure(t *testing.T) {
	app := NewApp()
	var calls atomic.Int32
	app.httpClient.Transport = workerSearchRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("synthetic disconnected transport")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.cancelWorkerSearchOnCallerCancel(ctx, "/v1/chat/completions", workerSearchTestPayload(), io.ErrUnexpectedEOF)
	app.cancelWorkerSearchOnCallerCancel(ctx, "/v1/chat/completions", workerSearchTestPayload(), context.DeadlineExceeded)
	if calls.Load() != 0 {
		t.Fatal("unrelated failure was mistaken for intentional cancellation")
	}
}

func TestWorkerSearchInteractionOutlivesOrdinaryStageAndKeepsCancellation(t *testing.T) {
	for _, search := range []bool{false, true} {
		t.Run(fmt.Sprint("search=", search), func(t *testing.T) {
			entered := make(chan bool, 1)
			release, unblock := interactionGenerationRelease(t)
			h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, _ ChatRequest, _ func(string)) (*ChatResponse, error) {
				_, deadline := ctx.Deadline()
				entered <- deadline
				<-release // Deliberately ignore cancellation to exercise the harness．
				return completedVoiceResponse("調査の回答です．"), nil
			})
			h.engine.Timeouts.LLM = 25 * time.Millisecond
			snapshot, err := h.engine.Start(VoiceTurnRequest{SessionID: "web-stage", InputKind: "text", Chat: ChatRequest{Mode: "fast", Prompt: "公開情報の質問", WebSearch: search, WebPlanID: workerSearchTestPlan}})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.engine.Commit(snapshot.OperationID, nil, "公開情報の質問"); err != nil {
				t.Fatal(err)
			}
			if deadline := interactionGenerationReceive(t, entered); deadline == search {
				t.Fatal("interaction did not select the planned search stage policy")
			}
			if !search {
				failed := awaitInteraction(t, h.engine, snapshot.OperationID, "FAILED")
				if failed.ErrorCode != "llm_timeout" {
					t.Fatal("ordinary interaction lost its finite timeout", failed.ErrorCode)
				}
			} else {
				time.Sleep(80 * time.Millisecond)
				active, err := h.engine.Snapshot(snapshot.OperationID)
				if err != nil || active.State != "THINKING" {
					t.Fatal("search interaction stopped at the ordinary LLM deadline", active.State, err)
				}
				if _, err := h.engine.Cancel(snapshot.OperationID); err != nil {
					t.Fatal(err)
				}
				awaitInteraction(t, h.engine, snapshot.OperationID, "CANCELED")
			}
			unblock()
		})
	}
}

func TestWorkerSearchContextOnlyStageReturnsWhenProviderIgnoresCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release, unblock := interactionGenerationRelease(t)
	result := make(chan error, 1)
	go func() {
		_, err := runGenerationStage(ctx, time.Millisecond, ChatRequest{WebSearch: true, WebPlanID: workerSearchTestPlan}, func(ctx context.Context) (bool, error) {
			close(entered)
			<-release
			return true, nil
		})
		result <- err
	}()
	interactionGenerationReceive(t, entered)
	cancel()
	if err := interactionGenerationReceive(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal("context-only stage was held by a provider ignoring cancellation", err)
	}
	unblock()
}

func TestWorkerSearchMissingPlanAndLocalRecallKeepOrdinaryStageDeadline(t *testing.T) {
	for _, request := range []ChatRequest{
		{WebSearch: true},
		{WebSearch: true, WebPlanID: strings.Repeat("z", 32)},
		{WebPlanID: workerSearchTestPlan},
		{WebSearch: true, WebPlanID: workerSearchTestPlan, SourceScope: "recorded_conversation"},
	} {
		_, err := runGenerationStage(context.Background(), 10*time.Millisecond, request, func(ctx context.Context) (bool, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("ordinary or local stage lost its finite deadline")
			}
			<-ctx.Done()
			return false, ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("ordinary or local generation did not time out", err)
		}
	}
}
