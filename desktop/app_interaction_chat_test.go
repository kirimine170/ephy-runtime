package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTextAndVoiceShareBoundedHistoryAndGateway(t *testing.T) {
	received := make(chan GatewayChatRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body GatewayChatRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		received <- body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	app := NewApp()
	app.baseURL = server.URL
	history := []GatewayMessage{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier answer"}}
	for _, mode := range []string{"default", "voice"} {
		response, err := app.chatWithContext(context.Background(), ChatRequest{Prompt: "current", Messages: history, SessionID: "same-session", SessionMode: mode, Stream: true}, func(s string) {})
		if err != nil || response.Answer != "answer" {
			t.Fatalf("%v %#v", err, response)
		}
		got := <-received
		if len(got.Messages) != 3 || got.Messages[0].Content != "earlier" || got.Messages[2].Content != "current" || got.Metadata.SessionID != "same-session" || got.Metadata.SessionMode != mode {
			t.Fatalf("history/session not shared: %#v", got)
		}
	}
	if len(history) != 2 {
		t.Fatal("mutated caller history")
	}
}

func TestSharedChatCancelClosesBackendAndDropsLateTokens(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	app := NewApp()
	app.baseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	_, err := app.chatWithContext(ctx, ChatRequest{Prompt: "synthetic", Stream: true}, func(s string) { count++; cancel() })
	if err == nil || count != 1 {
		t.Fatalf("cancel was ignored: %v tokens=%d", err, count)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("HTTP stream remained open")
	}
}

func TestHistoryRejectsSystemInjectionAndBounds(t *testing.T) {
	for _, req := range []ChatRequest{{Prompt: "x", Messages: []GatewayMessage{{Role: "system", Content: "untrusted"}}}, {Prompt: "x", Messages: make([]GatewayMessage, 31)}} {
		if _, err := conversationMessages(req); err == nil {
			t.Fatal("invalid history accepted")
		}
	}
	messages, err := conversationMessages(ChatRequest{Prompt: "legacy"})
	if err != nil || len(messages) != 1 || messages[0].Content != "legacy" {
		t.Fatal("legacy text chat broken")
	}
}
