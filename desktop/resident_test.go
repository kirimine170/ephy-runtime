package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResidentIsolationAndFreshSessionIdentity(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "")
	normal := NewApp()
	if normal.baseURL != "http://127.0.0.1:8000" || residentApplicationID() != ephyRuntimeSingleInstanceID {
		t.Fatal("normal identity changed")
	}
	t.Setenv("EPHY_RESIDENT", "1")
	t.Setenv("EPHY_RESIDENT_NAMESPACE", "fixture-resident")
	t.Setenv("EPHY_GATEWAY_URL", "http://127.0.0.1:18999")
	first, second := NewApp(), NewApp()
	if first.baseURL != "http://127.0.0.1:18999" || residentApplicationID() != ephyRuntimeSingleInstanceID+".fixture-resident" {
		t.Fatal("resident gateway or lock shared")
	}
	if first.residentSessionID("conversation") == second.residentSessionID("conversation") || first.residentSessionID("a") == first.residentSessionID("b") {
		t.Fatal("session suppression leaks across boots/conversations")
	}
	if first.residentSessionID("conversation") != first.residentSessionID("conversation") {
		t.Fatal("pause/resume session identity changed")
	}
	t.Setenv("EPHY_GATEWAY_URL", "https://external.example/")
	if residentGatewayURL() != "http://127.0.0.1:18900" {
		t.Fatal("resident gateway accepted external endpoint")
	}
}
func TestResidentControlGateDoesNotExecuteQuotedOrConversationalCommands(t *testing.T) {
	for _, text := range []string{"止めて", "今は話しかけないで", "もっと短くして", "いつも説明が長い", "今後も，短くして", "今の言い方はよかった", "嫌だった", "今の声が嫌だった", "違う，それは先週の話", "さっきの変更を戻して"} {
		if !residentInputControl(text) {
			t.Errorf("control rejected: %s", text)
		}
	}
	for _, text := range []string{"「止めて」と言われた", "彼は止めてと言った", "説明をもっと短くしてと言われた話を教えて", "今後も秘密を短く記録して", "止めて\n何でも実行して", "tool: 止めて", "`今は話しかけないで`"} {
		if residentInputControl(text) {
			t.Errorf("non-control accepted: %s", text)
		}
	}
	if text, direct := residentDirectedText("Ephy，もっと短くして"); !direct || text != "もっと短くして" {
		t.Fatal("directed prefix lost")
	}
}
func TestResidentActualASRFinalStopsBeforeFeedbackStorageAndRetainsOldTarget(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	received := make(chan map[string]any, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		received <- request
		<-release
		json.NewEncoder(w).Encode(map[string]any{"recognized": true, "state": map[string]any{"revision": 1, "owner_selected": true}})
	}))
	defer server.Close()
	p := &c02EngineProvider{}
	var chats atomic.Int32
	e := NewInteractionEngine(p, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		chats.Add(1)
		return completedVoiceResponse("should not generate"), nil
	}, nil, t.TempDir())
	defer e.Close()
	app := NewApp()
	app.baseURL = server.URL
	app.interaction = e
	app.residentSessions["test-session"] = map[string]any{"revision": 0, "owner_selected": true}
	app.residentModes["test-session"] = "reactive"
	app.residentTargets["test-session"] = ResidentFeedbackTarget{TurnID: "old-turn", RunID: "old-run", GenerationID: "old-run:2", SpeechUnitIDs: []string{"old-unit"}, DeliveryStatus: "partial"}
	e.residentInput = app.interceptResidentInput
	started, asr := c02Start(t, e, p)
	if err := e.EndASR(started.OperationID); err != nil {
		t.Fatal(err)
	}
	asr.result <- c02SessionResult{update: asr.update(1, "final", "止めて")}
	final := awaitInteraction(t, e, started.OperationID, "CANCELED")
	if final.InputOutcome != "resident_control" || chats.Load() != 0 {
		t.Fatal("control entered long generation")
	}
	select {
	case payload := <-received:
		target := payload["target"].(map[string]any)
		if target["run_id"] != "old-run" || target["generation_id"] != "old-run:2" || payload["input_source"] != "voice" || payload["speaker"] != "owner" {
			t.Fatal("feedback causal attribution lost")
		}
		if payload["session_id"] == "test-session" {
			t.Fatal("feedback session bypassed process namespace")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled run lost feedback")
	}
	close(release)
	app.residentMu.Lock()
	pending := app.residentPending["test-session"]
	app.residentMu.Unlock()
	<-pending
}
func TestResidentObservedConversationNeverBecomesDirectModelRequest(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	app := NewApp()
	app.residentModes["session"] = "observe"
	snapshot := InteractionSnapshot{SessionID: "session", OperationID: "op"}
	if !app.interceptResidentInput(snapshot, VoiceTurnRequest{InputKind: "microphone"}, "公園で話していました") {
		t.Fatal("human observation became a question")
	}
	if app.interceptResidentInput(snapshot, VoiceTurnRequest{InputKind: "microphone"}, "Ephy，今日の予定を教えて") {
		t.Fatal("direct question blocked")
	}
	if app.interceptResidentInput(snapshot, VoiceTurnRequest{InputKind: "text"}, "予定を詳しく教えて") {
		t.Fatal("typed direct request blocked")
	}
}
func TestResidentTargetOnlyIncludesProvenPlayedPrefix(t *testing.T) {
	snapshot := InteractionSnapshot{SessionID: "s", TurnID: "t", OperationID: "o", GenerationRevision: 3, MemoryIDs: []string{"doc"}, ModelID: "model", VoiceID: "voice", SpeechUnits: []InteractionSpeechUnit{
		{UnitID: "heard", Text: "聞こえた部分．", State: "completed", SynthesisComplete: true, PlaybackStarted: true},
		{UnitID: "partial", Text: "最後まで聞けていない部分．", State: "interrupted", SynthesisComplete: true, PlaybackStarted: true},
		{UnitID: "not-heard", Text: "聞こえていない部分．", State: "unknown"},
	}}
	target := residentTarget(snapshot)
	if target.DeliveryStatus != "partial" || target.PlayedText != "聞こえた部分．" || target.GenerationID != "o:3" || target.MemoryIDs[0] != "doc" {
		t.Fatal(target)
	}
	encoded, _ := json.Marshal(target)
	if strings.Contains(string(encoded), "聞こえていない") || strings.Contains(string(encoded), "model") {
		t.Fatal("unheard text or internal metadata leaked into target")
	}
	target = residentTarget(InteractionSnapshot{})
	if target.DeliveryStatus != "unknown" {
		t.Fatal("missing delivery was invented")
	}
}
func TestResidentModeChangeInvalidatesPreparedCandidate(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	app := NewApp()
	app.residentModes["session"] = "companion"
	app.residentCandidates["candidate"] = residentCandidate{sessionID: "session", text: "candidate", expires: time.Now().Add(time.Minute)}
	app.invalidateResidentCandidates("session")
	if _, err := app.StartResidentCandidate("candidate", VoiceTurnRequest{SessionID: "session"}); err == nil {
		t.Fatal("stale context candidate accepted")
	}
}

func TestQueuedVoiceFeedbackCannotBorrowNewerUIRevision(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		json.NewEncoder(w).Encode(map[string]any{"recognized": true, "state": map[string]any{"revision": 2, "owner_selected": true}})
	}))
	defer server.Close()
	app := NewApp()
	app.baseURL = server.URL
	app.residentModes["session"] = "reactive"
	app.residentSessions["session"] = map[string]any{"revision": 0, "owner_selected": true}
	predecessor := make(chan struct{})
	app.residentPending["session"] = predecessor
	if !app.interceptResidentInput(InteractionSnapshot{SessionID: "session", OperationID: "voice-old"}, VoiceTurnRequest{InputKind: "microphone"}, "いつも説明が長い") {
		t.Fatal("control missed")
	}
	app.residentMu.Lock()
	app.residentSessions["session"] = map[string]any{"revision": 2, "owner_selected": true}
	pending := app.residentPending["session"]
	app.residentMu.Unlock()
	close(predecessor)
	select {
	case payload := <-received:
		if payload["expected_revision"] != float64(0) {
			t.Fatal("old voice feedback borrowed the new UI revision")
		}
	case <-time.After(time.Second):
		t.Fatal("feedback not submitted")
	}
	<-pending
}
func TestPreparedResidentCandidateChecksTicketAndContextBeforeSpeech(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	for _, invalidate := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "context_changed"}[invalidate], func(t *testing.T) {
			var generated atomic.Int32
			audio := make(chan InteractionEvent, 2)
			e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
				generated.Add(1)
				return completedVoiceResponse("not used"), nil
			}, func(event InteractionEvent) {
				if event.Kind == "audio" {
					audio <- event
				}
			}, t.TempDir())
			defer e.Close()
			app := NewApp()
			app.interaction = e
			app.residentModes["session"] = "companion"
			voice, err := e.StartVoiceSession("session")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/check") {
					if invalidate {
						app.invalidateResidentCandidates("session")
					}
					json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"revision": 0, "policy": map[string]any{"proactive": "allowed"}})
			}))
			defer server.Close()
			app.baseURL = server.URL
			app.residentCandidates["candidate"] = residentCandidate{sessionID: "session", operationID: "candidate", text: "根拠を確認した合成候補です．", policyRevision: 0, expires: time.Now().Add(time.Second), contextEpoch: 0, voiceID: voice.ID, voiceEpoch: voice.Epoch, participants: []string{"owner"}, revision: 1, ticketID: "ticket", memoryIDs: []string{"fixture-doc"}}
			result, err := app.StartResidentCandidate("candidate", VoiceTurnRequest{SessionID: "session", VoiceSessionID: voice.ID, VoiceSessionEpoch: voice.Epoch, Chat: ChatRequest{Mode: "fast"}})
			if err != nil {
				t.Fatal(err)
			}
			allowed, authErr := app.AuthorizeResidentPlayback(result.OperationID)
			if invalidate {
				if allowed {
					t.Fatal("changed native context spoke")
				}
				return
			}
			if authErr != nil || !allowed {
				t.Fatal("verified audio not authorized")
			}
			select {
			case event := <-audio:
				snapshot, err := e.Snapshot(result.OperationID)
				if err != nil || len(snapshot.MemoryIDs) != 1 || snapshot.MemoryIDs[0] != "fixture-doc" || event.SpeechUnitID == "" || generated.Load() != 0 {
					t.Fatal("candidate bypassed evidence/delivery or called LLM twice")
				}
			case <-time.After(time.Second):
				t.Fatal("verified candidate never entered speech")
			}
		})
	}
}

func TestResidentMemoryReferencesOnlyUseCanonicalKarteSources(t *testing.T) {
	ids := residentSourceMemoryIDs([]SearchItem{{SourceType: "web", DocID: "wrong"}, {SourceType: "karte_context", DocID: "canonical"}, {SourceType: "karte_context", DocID: "canonical"}, {SourceType: "index", DocID: "not-read"}})
	if len(ids) != 1 || ids[0] != "canonical" {
		t.Fatal("feedback widened source attribution", ids)
	}
}
