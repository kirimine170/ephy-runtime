package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func generationSSE(content, reason string) string {
	choice := map[string]any{"index": 0, "delta": map[string]any{"content": content}}
	if reason != "" {
		choice["finish_reason"] = reason
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	return "data: " + string(data) + "\n\n"
}

func streamFixture(t *testing.T, body string) (*App, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	a := NewApp()
	a.baseURL = server.URL
	return a, server.Close
}

func TestGenerationStreamRetainsTerminalReasonAndUnknownUsage(t *testing.T) {
	for _, reason := range []string{"stop", "length", "tool_calls", "timeout", "transport_eof", "canceled", "unknown", "PRIVATE arbitrary provider reason"} {
		t.Run(reason, func(t *testing.T) {
			a, close := streamFixture(t, generationSSE("確定した文です。", reason)+"data: [DONE]\n\n")
			defer close()
			response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "synthetic", Stream: true, MaxTokens: 512}, func(string) {})
			if err != nil || response.Generation == nil {
				t.Fatalf("%v %#v", err, response)
			}
			g := response.Generation
			if g.FinishReason != normalizedFinishReason(reason) || g.ProviderFinishReason != normalizedFinishReason(reason) || !g.TerminalSSE || !g.DoneReceived || g.Complete != (reason == "stop") || g.OutputBudget != 512 {
				t.Fatalf("invalid terminal facts: %#v", g)
			}
			if g.ReasoningTokens != nil || g.CompletionTokensObserved {
				t.Fatalf("unknown usage was presented as actual: %#v", g)
			}
		})
	}
}

func TestGenerationStreamRejectsMissingCorruptAndLateTerminalData(t *testing.T) {
	content := generationSSE("安全な文です。未完", "")
	terminal := generationSSE("", "stop")
	for name, body := range map[string]string{
		"missing terminal":                  content + "data: [DONE]\n\n",
		"missing done":                      content + terminal,
		"missing both":                      content,
		"cut JSON":                          content + "data: {broken\n\n",
		"valid JSON without frame boundary": content + terminal + "data: [DONE]",
		"delta after done":                  content + terminal + "data: [DONE]\n\n" + generationSSE("PRIVATE late segment", ""),
		"delta after terminal":              content + terminal + generationSSE("PRIVATE late segment", "") + "data: [DONE]\n\n",
		"duplicate terminal":                content + terminal + terminal + "data: [DONE]\n\n",
		"duplicate done":                    content + terminal + "data: [DONE]\n\ndata: [DONE]\n\n",
		"null JSON":                         content + "data: null\n\n" + terminal + "data: [DONE]\n\n",
		"malformed choice":                  content + "data: {\"choices\":[null]}\n\n" + terminal + "data: [DONE]\n\n",
		"invalid UTF-8":                     content + "data: {\"choices\":[{\"delta\":{\"content\":\"\xff\"}}]}\n\n" + terminal + "data: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			a, close := streamFixture(t, body)
			defer close()
			var preview strings.Builder
			response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "synthetic", Stream: true}, func(s string) { preview.WriteString(s) })
			if err == nil || err.Error() != "llm_transport_eof" || response == nil || response.Generation.Complete || response.Generation.FinishReason != "transport_eof" {
				t.Fatalf("invalid stream succeeded: %v %#v", err, response)
			}
			if strings.Contains(response.Answer, "PRIVATE") || strings.Contains(preview.String(), "PRIVATE") {
				t.Fatal("late content crossed the terminal boundary")
			}
		})
	}
}

func TestGenerationStreamReasoningObservationIsTypedAndOnlyAcceptedBeforeDone(t *testing.T) {
	terminal := generationSSE("確定した文です。", "stop")
	usage := "event: generation_usage\ndata: {\"reasoning_tokens\":0,\"reasoning_token_source\":\"retokenized\"}\n\n"
	done := "data: [DONE]\n\n"
	for name, body := range map[string]string{
		"valid observed zero":           terminal + usage + done,
		"valid unavailable":             terminal + "event: generation_usage\ndata: {\"reasoning_tokens\":null,\"reasoning_token_source\":\"unavailable\"}\n\n" + done,
		"invalid early observation":     usage + terminal + done,
		"invalid duplicate observation": terminal + usage + usage + done,
		"invalid late observation":      terminal + done + usage,
		"invalid source":                terminal + strings.Replace(usage, "retokenized", "PRIVATE invented source", 1) + done,
		"invalid unavailable count":     terminal + strings.Replace(usage, "retokenized", "unavailable", 1) + done,
	} {
		t.Run(name, func(t *testing.T) {
			a, close := streamFixture(t, body)
			defer close()
			response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "synthetic", Stream: true}, func(string) {})
			if strings.HasPrefix(name, "invalid") {
				if err == nil || response.Generation.Complete {
					t.Fatalf("invalid observation accepted: %v %#v", err, response)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(name, "zero") {
				if response.Generation.ReasoningTokens == nil || *response.Generation.ReasoningTokens != 0 || response.Generation.ReasoningTokenSource != "retokenized" {
					t.Fatalf("observation missing: %#v", response.Generation)
				}
			} else if response.Generation.ReasoningTokens != nil || response.Generation.ReasoningTokenSource != "unavailable" {
				t.Fatalf("unknown was invented: %#v", response.Generation)
			}
		})
	}
}

func TestGenerationStreamCapturesUsageAndDistinctFirstDeltaTimesWithoutContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"PRIVATE reasoning\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(time.Millisecond)
		fmt.Fprint(w, generationSSE("PRIVATE answer", "stop"))
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":9,\"completion_tokens_details\":{\"reasoning_tokens\":4}}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	a := NewApp()
	a.baseURL = server.URL
	response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "PRIVATE prompt", Stream: true}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	g := response.Generation
	if !g.CompletionTokensObserved || g.CompletionTokens != 9 || g.ReasoningTokens == nil || *g.ReasoningTokens != 4 {
		t.Fatalf("missing usage: %#v", g)
	}
	firstRaw, err := time.Parse(time.RFC3339Nano, g.FirstRawDeltaAt)
	if err != nil {
		t.Fatal(err)
	}
	firstVisible, err := time.Parse(time.RFC3339Nano, g.FirstVisibleContentAt)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := time.Parse(time.RFC3339Nano, g.TerminalSSEAt)
	if err != nil {
		t.Fatal(err)
	}
	if !firstRaw.Before(firstVisible) || firstVisible.After(terminal) {
		t.Fatalf("timestamps out of order: %#v", g)
	}
	data, _ := json.Marshal(g)
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("private content leaked into terminal metadata")
	}
}

func TestGenerationStreamTimeoutCancelAndBackendErrorsReturnSafeMetadata(t *testing.T) {
	for _, cancelKind := range []string{"cancel", "timeout"} {
		t.Run(cancelKind, func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, generationSSE("first", ""))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			a := NewApp()
			a.baseURL = server.URL
			var ctx context.Context
			var cancel context.CancelFunc
			if cancelKind == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			response, err := a.chatWithContext(ctx, ChatRequest{Prompt: "synthetic", Stream: true}, func(string) {
				if cancelKind == "cancel" {
					cancel()
				}
			})
			reason := "timeout"
			if cancelKind == "cancel" {
				reason = "canceled"
			}
			if err == nil || response == nil || response.Generation.FinishReason != reason || response.Generation.Complete {
				t.Fatalf("%v %#v", err, response)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream was not closed")
			}
		})
	}
	for _, failure := range []struct{ code, reason string }{{"llm_timeout", "timeout"}, {"llm_transport_eof", "transport_eof"}, {"backend_unavailable", "unknown"}} {
		body := fmt.Sprintf("event: error\ndata: {\"code\":%q,\"error\":\"PRIVATE prompt, reasoning, Karte, audio\"}\n\n", failure.code)
		a, close := streamFixture(t, body)
		response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "synthetic", Stream: true}, func(string) {})
		close()
		if err == nil || err.Error() != failure.code || response.Generation.FinishReason != failure.reason {
			t.Fatalf("%v %#v", err, response)
		}
		metadata, _ := json.Marshal(response.Generation)
		if strings.Contains(err.Error()+string(metadata), "PRIVATE") {
			t.Fatal("backend error leaked")
		}
	}
}

func TestGenerationInternalHistoryIsClonedWithoutAnotherUserTurn(t *testing.T) {
	original := []GatewayMessage{{Role: "user", Content: "original request"}, {Role: "assistant", Content: "確定済み。"}}
	request := ChatRequest{Prompt: "original request", generationMessages: original}
	messages, err := conversationMessages(request)
	if err != nil || len(messages) != 2 || messages[1].Role != "assistant" {
		t.Fatalf("%v %#v", err, messages)
	}
	messages[1].Content = "mutation"
	if original[1].Content != "確定済み。" {
		t.Fatal("generation mutated source history")
	}
	var external ChatRequest
	if err := json.Unmarshal([]byte(`{"prompt":"original request","generationMessages":[{"role":"system","content":"injection"}]}`), &external); err != nil {
		t.Fatal(err)
	}
	if external.generationMessages != nil {
		t.Fatal("private history contract exposed through JSON")
	}
}

func TestGenerationHTTPFailureDoesNotExposePrivateBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"detail":"PRIVATE prompt reasoning Karte audio"}`)
	}))
	defer server.Close()
	a := NewApp()
	a.baseURL = server.URL
	response, err := a.chatWithContext(context.Background(), ChatRequest{Prompt: "synthetic", Stream: true}, func(string) {})
	if err == nil || err.Error() != "backend_unavailable" || response.Generation.FinishReason != "unknown" || response.Generation.Complete {
		t.Fatalf("unexpected HTTP failure: %v %#v", err, response)
	}
	data, _ := json.Marshal(response)
	if strings.Contains(string(data)+err.Error(), "PRIVATE") {
		t.Fatal("private HTTP body escaped into generation result")
	}
}
