package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newPreferenceTestApp(t *testing.T, handler http.Handler) (*App, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	app := newTestAppWithWorkspace(t)
	app.baseURL = server.URL
	app.httpClient = server.Client()
	app.preferenceHTTPClient = server.Client()
	return app, server
}

func TestPreferenceAPIFlowAndSessionResume(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/eval/preferences/sessions", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`{"sessions":[{"session_id":"session-1","reviewed":1,"remaining":2}]}`))
			return
		}
		var payload PreferenceSessionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ModelRole != "fast" || payload.PairCount != 3 || payload.ComparisonMode != "prompt_v2_v3" {
			t.Fatalf("unexpected session request: %#v", payload)
		}
		_, _ = writer.Write([]byte(`{"session_id":"session-1"}`))
	})
	mux.HandleFunc("/v1/eval/preferences/sessions/session-1/generate", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"generated":[{"pair_id":"pair-1"}]}`))
	})
	mux.HandleFunc("/v1/eval/preferences/sessions/session-1/next", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"pair":{"pair_id":"pair-1","response_left":"left","response_right":"right"}}`))
	})
	mux.HandleFunc("/v1/eval/preferences/pairs/pair-1/vote", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"vote_id":"vote-1"}`))
	})

	app, server := newPreferenceTestApp(t, mux)
	defer server.Close()

	created, err := app.CreatePreferenceSession(PreferenceSessionRequest{
		DatasetPath:    "configs/eval.preference.v3.yaml",
		ModelRole:      "fast",
		PairCount:      3,
		Prefetch:       2,
		ComparisonMode: "prompt_v2_v3",
		GenerationParameters: PreferenceGenerationParameters{
			Temperature: 0.8,
			TopP:        0.95,
			MaxTokens:   256,
		},
	})
	if err != nil || created["session_id"] != "session-1" {
		t.Fatalf("failed to create preference session: %#v %v", created, err)
	}
	if _, err := app.GeneratePreferencePairs("session-1", PreferenceGenerateRequest{Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.NextPreferencePair("session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.VotePreferencePair("pair-1", PreferenceVoteRequest{Selection: "left"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := app.ListPreferenceSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed["sessions"].([]any)) != 1 {
		t.Fatalf("unexpected resumable sessions: %#v", resumed)
	}

	history, err := app.readExecutionHistory()
	if err != nil || len(history) < 3 || history[0].Kind != "preference" {
		t.Fatalf("preference execution history was not recorded: %#v %v", history, err)
	}
}

func TestPreferenceExportPassesOnlyServerControlledSessionAndReportsPathError(t *testing.T) {
	var receivedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/eval/preferences/sessions/session-1/export", func(writer http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.Path
		var payload PreferenceExportRequest
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if strings.Contains(payload.Output, "..") {
			http.Error(writer, "outside data root", http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{"records":1}`))
	})
	app, server := newPreferenceTestApp(t, mux)
	defer server.Close()

	if _, err := app.ExportPreferenceSession("session-1", PreferenceExportRequest{
		Format: "dpo", Output: "../outside.jsonl",
	}); err == nil {
		t.Fatal("expected server-side export path rejection")
	}
	if receivedPath != "/v1/eval/preferences/sessions/session-1/export" {
		t.Fatalf("unexpected export endpoint: %s", receivedPath)
	}
}

func TestPreferenceGatewayUnavailableReturnsError(t *testing.T) {
	app := newTestAppWithWorkspace(t)
	app.baseURL = "http://127.0.0.1:1"
	app.httpClient = &http.Client{}

	if _, err := app.ListPreferenceSessions(); err == nil {
		t.Fatal("expected unavailable gateway error")
	}
}

func TestPreferenceGenerationUsesLongRunningClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/eval/preferences/sessions/session-1/generate", func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"generated":[{"pair_id":"pair-1"}]}`))
	})
	app, server := newPreferenceTestApp(t, mux)
	defer server.Close()
	app.httpClient = &http.Client{Timeout: time.Millisecond}

	if _, err := app.GeneratePreferencePairs("session-1", PreferenceGenerateRequest{Limit: 1}); err != nil {
		t.Fatalf("preference generation used the standard client timeout: %v", err)
	}
	if NewApp().preferenceHTTPClient.Timeout != 20*time.Minute {
		t.Fatalf("unexpected preference generation timeout: %s", NewApp().preferenceHTTPClient.Timeout)
	}
}
