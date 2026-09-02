package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newKarteConversationTestApp(t *testing.T, handler http.Handler) (*App, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	app := NewApp()
	app.baseURL = server.URL
	t.Cleanup(server.Close)
	return app, server
}

func karteConversationRequestFixture() KarteConversationRequest {
	return KarteConversationRequest{
		ConversationID: "conversation-001",
		OccurredAt:     "2026-09-01T10:30:00+09:00",
		Messages: []KarteConversationMessage{
			{Role: "user", Content: "方針を決めたい"},
			{Role: "assistant", Content: "project優先で保存します．"},
		},
		Project:            "ephy",
		Sensitivity:        "internal",
		Tags:               []string{},
		Resolution:         "create",
		ReviewedPlanSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func TestPlanKarteConversationProxiesValidatedConversation(t *testing.T) {
	app, _ := newKarteConversationTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/karte/conversations/plan" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request KarteConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ConversationID != "conversation-001" || len(request.Messages) != 2 || request.Project != "ephy" {
			t.Fatalf("unexpected payload: %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidate_id":"ephy-chat-001","recommendation":"create","publishable":true,"needs_project":false,"reasons":[],"summary_title":"方針","summary_markdown":"# 方針","similar_documents":[],"proposal":{"operation":"create"}}`))
	}))

	response, err := app.PlanKarteConversation(karteConversationRequestFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !response.Publishable || response.CandidateID != "ephy-chat-001" || response.Proposal["operation"] != "create" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestPublishAndStatusKarteConversation(t *testing.T) {
	app, _ := newKarteConversationTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/karte/conversations/publish":
			var request KarteConversationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ReviewedPlanSHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Fatalf("reviewed plan digest was not forwarded: %#v", request)
			}
			_, _ = w.Write([]byte(`{"candidate_id":"ephy-chat-001","state":"pending","path":"/safe/pending/ephy-chat-001.json","plan":{"candidate_id":"ephy-chat-001","recommendation":"create","publishable":true,"needs_project":false,"reasons":[],"summary_title":"方針","summary_markdown":"# 方針","similar_documents":[],"proposal":{"operation":"create"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/karte/proposals/ephy-chat-001":
			_, _ = w.Write([]byte(`{"candidate_id":"ephy-chat-001","state":"pending","receipt":null}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	published, err := app.PublishKarteConversation(karteConversationRequestFixture())
	if err != nil {
		t.Fatal(err)
	}
	status, err := app.GetKarteProposalStatus(published.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != "pending" || status.State != "pending" {
		t.Fatalf("unexpected publish/status: %#v %#v", published, status)
	}
}

func TestGetKarteProposalStatusRejectsBlankCandidateID(t *testing.T) {
	app := NewApp()
	if _, err := app.GetKarteProposalStatus("  "); err == nil {
		t.Fatal("expected blank candidate_id to be rejected")
	}
}
