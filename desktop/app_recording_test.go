package main

import (
	"context"
	"desktop/recording"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newAppRecordingStore(t *testing.T) (*recording.Store, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "spool")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	settings := recording.Settings{Configured: true, Enabled: true, DataRoot: filepath.Join(base, "karte"), Project: "synthetic", Timezone: "Asia/Tokyo", ConversationID: uuid.NewString(), ScopeID: uuid.NewString(), ProducerID: uuid.NewString(), PolicyID: uuid.NewString(), Epoch: 1}
	b, _ := json.Marshal(settings)
	if err = os.WriteFile(filepath.Join(home, "settings.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := recording.New(recording.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, home
}

func awaitRecordedPrefix(t *testing.T, home, prefix string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(home, "turns", "*.json"))
		for _, file := range files {
			b, _ := os.ReadFile(file)
			var tr struct {
				Draft *struct {
					Text string `json:"text"`
				} `json:"draft"`
			}
			if json.Unmarshal(b, &tr) == nil && tr.Draft != nil && tr.Draft.Text == prefix {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("streamed committed prefix was not durable before terminal")
}

func blockRecordingTurnWrite(t *testing.T, home string) func() {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(home, "turns", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("missing accepted user", err)
	}
	file := files[0]
	if err = os.Rename(file, file+".preserved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Remove(file); _ = os.Rename(file+".preserved", file) }
}

func TestRecordingTextStreamsPreservePreOffAndPreRestartPrefix(t *testing.T) {
	for _, rag := range []bool{false, true} {
		for _, boundary := range []string{"off", "restart"} {
			t.Run(fmt.Sprintf("rag=%t/%s", rag, boundary), func(t *testing.T) {
				s, home := newAppRecordingStore(t)
				release := make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private reasoning\",\"content\":\"確定した接頭辞です．\"},\"finish_reason\":null}]}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OFF以降の本文です．\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				defer unblock()
				app := NewApp()
				app.workspaceRoot = t.TempDir()
				app.baseURL = server.URL
				app.recorder = s
				app.recordingInit = true
				conv := s.Snapshot().Settings.ConversationID
				done := make(chan error, 1)
				go func() {
					if rag {
						_, err := app.RunRagQueryAction(QueryRequest{SessionID: conv, RequestID: "stream", Query: "synthetic user", Answer: true, Stream: true})
						done <- err
					} else {
						_, err := app.RunChatAction(ChatRequest{SessionID: conv, RequestID: "stream", Prompt: "synthetic user", Mode: "fast", Stream: true})
						done <- err
					}
				}()
				awaitRecordedPrefix(t, home, "確定した接頭辞です．")
				if boundary == "off" {
					if _, err := s.Configure(context.Background(), recording.ConfigureRequest{Enabled: false}); err != nil {
						t.Fatal(err)
					}
				} else {
					s.Close()
					restarted, err := recording.New(recording.Options{Home: home})
					if err != nil {
						t.Fatal(err)
					}
					defer restarted.Close()
				}
				unblock()
				if err := <-done; boundary == "off" && err != nil {
					t.Fatal(err)
				}
				events := readSpoolEvents(t, home)
				if len(events) != 2 || events[1].Text != "確定した接頭辞です．" || events[1].Assistant.Display != "confirmed_prefix" {
					t.Fatalf("lost pre-boundary text: %+v", events)
				}
				if boundary == "restart" && (events[1].Assistant.Generation != "failed" || events[1].Assistant.Playback != "not_started") {
					t.Fatal("invented terminal after restart")
				}
			})
		}
	}
}

func TestRecordingAssistantWriteFailureIsReturnedForTextAndVoice(t *testing.T) {
	for _, rag := range []bool{false, true} {
		t.Run(fmt.Sprintf("text/rag=%t", rag), func(t *testing.T) {
			s, home := newAppRecordingStore(t)
			var restore func()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				restore = blockRecordingTurnWrite(t, home)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"保存失敗を検証します．\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			app := NewApp()
			app.workspaceRoot = t.TempDir()
			app.baseURL = server.URL
			app.recorder = s
			app.recordingInit = true
			var err error
			if rag {
				_, err = app.RunRagQueryAction(QueryRequest{SessionID: s.Snapshot().Settings.ConversationID, RequestID: "failed", Query: "synthetic user", Answer: true, Stream: true})
			} else {
				_, err = app.RunChatAction(ChatRequest{SessionID: s.Snapshot().Settings.ConversationID, RequestID: "failed", Prompt: "synthetic user", Mode: "fast", Stream: true})
			}
			if restore != nil {
				restore()
			}
			if err == nil || err.Error() != "recording_storage_failed" || s.Snapshot().Code != "recording_storage_failed" {
				t.Fatal("recording failure reported success", err)
			}
			if events := readSpoolEvents(t, home); len(events) != 1 || events[0].Type != "user_final" {
				t.Fatal("accepted user was lost")
			}
		})
	}
	t.Run("voice", func(t *testing.T) {
		s, home := newAppRecordingStore(t)
		restore := make(chan func(), 1)
		h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, req ChatRequest, onToken func(string)) (*ChatResponse, error) {
			restore <- blockRecordingTurnWrite(t, home)
			onToken("保存失敗を検証します．")
			return completedVoiceResponse("保存失敗を検証します．"), nil
		})
		h.engine.recorder = s
		started := h.start(t, s.Snapshot().Settings.ConversationID, GenerationLimits{})
		result := awaitInteraction(t, h.engine, started.OperationID, "FAILED")
		(<-restore)()
		if result.ErrorCode != "recording_storage_failed" {
			t.Fatal(result.ErrorCode)
		}
		b, _ := os.ReadFile(filepath.Join(h.store, started.OperationID+".json"))
		if strings.Contains(string(b), "turn_completed") || strings.Contains(string(b), "保存失敗") {
			t.Fatal("false completion or body in trace")
		}
	})
}

func TestRecordingTextTerminalDoesNotHideDiskOrTransportFailure(t *testing.T) {
	for _, kind := range []string{"chat_disk", "rag_disk", "rag_truncated"} {
		t.Run(kind, func(t *testing.T) {
			s, home := newAppRecordingStore(t)
			restore := make(chan func(), 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "rag_truncated" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"確定した接頭辞です．\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
					return
				}
				restore <- blockRecordingTurnWrite(t, home)
				if kind == "chat_disk" {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"synthetic complete"},"finish_reason":"stop"}]}`)
				} else {
					fmt.Fprint(w, `{"answer":"synthetic complete","finish_reason":"stop","sources":[]}`)
				}
			}))
			defer server.Close()
			app := NewApp()
			app.workspaceRoot = t.TempDir()
			app.baseURL = server.URL
			app.recorder = s
			app.recordingInit = true
			var err error
			if kind == "chat_disk" {
				_, err = app.RunChatAction(ChatRequest{SessionID: s.Snapshot().Settings.ConversationID, RequestID: "terminal", Prompt: "synthetic user", Mode: "fast"})
			} else {
				_, err = app.RunRagQueryAction(QueryRequest{SessionID: s.Snapshot().Settings.ConversationID, RequestID: "terminal", Query: "synthetic user", Answer: true, Stream: kind == "rag_truncated"})
			}
			if kind == "rag_truncated" {
				events := readSpoolEvents(t, home)
				if err == nil || len(events) != 2 || events[1].Text != "確定した接頭辞です．" || events[1].Assistant.Generation != "failed" {
					t.Fatal("truncated response became complete", err)
				}
			} else {
				(<-restore)()
				if err == nil || err.Error() != "recording_storage_failed" {
					t.Fatal("terminal write failed silently", err)
				}
			}
		})
	}
}
func readSpoolEvents(t *testing.T, home string) []recording.Event {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(home, "turns", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events []recording.Event
	for _, file := range files {
		b, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var tr struct {
			Items []struct {
				Event *recording.Event `json:"event"`
			} `json:"items"`
		}
		if e = json.Unmarshal(b, &tr); e != nil {
			t.Fatal(e)
		}
		for _, item := range tr.Items {
			if item.Event != nil {
				events = append(events, *item.Event)
			}
		}
	}
	return events
}
func TestRecordingVoiceFinalIsDurableBeforeLLMAndTraceHasNoBody(t *testing.T) {
	s, home := newAppRecordingStore(t)
	h := newInteractionGenerationHarness(t, testVoiceTTS{}, func(ctx context.Context, req ChatRequest, onToken func(string)) (*ChatResponse, error) {
		events := readSpoolEvents(t, home)
		if len(events) != 1 || events[0].Type != "user_final" || events[0].Text != req.Prompt {
			t.Error("LLM started before final was durable")
		}
		onToken("確認済みの回答です．")
		r := completedVoiceResponse("確認済みの回答です．")
		r.Thinking = "internal reasoning must not be recorded"
		return r, nil
	})
	h.engine.recorder = s
	started := h.start(t, s.Snapshot().Settings.ConversationID, GenerationLimits{})
	awaitInteraction(t, h.engine, started.OperationID, "COMPLETED")
	events := readSpoolEvents(t, home)
	if len(events) != 2 || events[1].Assistant == nil || events[1].Assistant.Generation != "completed" || events[1].Assistant.Playback != "completed" {
		t.Fatalf("wrong result: %+v", events)
	}
	if strings.Contains(events[1].Text, "reasoning") || strings.Contains(events[1].Text, "history-marker") {
		t.Fatal("unconfirmed source entered recording")
	}
	b, err := os.ReadFile(filepath.Join(h.store, started.OperationID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-prompt-marker", "確認済み", "reasoning must"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatal("body leaked into trace")
		}
	}
}
func TestRecordingTextAndRagUseSameConversationAndNoExecutionBody(t *testing.T) {
	s, home := newAppRecordingStore(t)
	conv := s.Snapshot().Settings.ConversationID
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(readSpoolEvents(t, home)) == 0 {
			t.Error("request preceded preservation")
		}
		if r.URL.Path == "/v1/rag/query" {
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			if payload["session_id"] != nil || payload["continuation_of"] != nil || payload["request_id"] != nil {
				t.Error("local identity leaked into incompatible query wire")
			}
			fmt.Fprint(w, `{"answer":"synthetic RAG answer","finish_reason":"stop","sources":[]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"synthetic text answer","reasoning_content":"private internal reasoning"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	app := NewApp()
	app.workspaceRoot = t.TempDir()
	app.baseURL = server.URL
	app.recorder = s
	app.recordingInit = true
	if _, e := app.RunChatAction(ChatRequest{SessionID: conv, RequestID: "text1", Prompt: "synthetic text user", Mode: "fast"}); e != nil {
		t.Fatal(e)
	}
	if _, e := app.RunRagQueryAction(QueryRequest{SessionID: conv, RequestID: "rag1", Query: "synthetic rag user", Answer: true}); e != nil {
		t.Fatal(e)
	}
	events := readSpoolEvents(t, home)
	if len(events) != 4 {
		t.Fatal("conversation path was not recorded")
	}
	for _, e := range events {
		if e.ConversationID != conv || strings.Contains(e.Text, "private internal") {
			t.Fatal("identity or reasoning contamination")
		}
	}
	b, e := os.ReadFile(app.executionHistoryFilePath())
	if e != nil {
		t.Fatal(e)
	}
	for _, forbidden := range []string{"synthetic text user", "synthetic text answer", "synthetic rag user", "synthetic RAG answer", "private internal"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatal("conversation duplicated in execution history")
		}
	}
}

func TestRecordingInterruptionKeepsCommittedPrefixAndPlaybackObservation(t *testing.T) {
	s, home := newAppRecordingStore(t)
	ready := make(chan struct{})
	startedAudio := make(chan struct{}, 1)
	engine := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{stream: func(ctx context.Context, text string, emit func([]byte) error) error {
		if e := emit(testVoiceWAV()); e != nil {
			return e
		}
		<-ctx.Done()
		return ctx.Err()
	}}, func(ctx context.Context, r ChatRequest, onToken func(string)) (*ChatResponse, error) {
		onToken("確定した接頭辞です．")
		close(ready)
		<-ctx.Done()
		return nil, ctx.Err()
	}, func(InteractionEvent) {}, t.TempDir())
	engine.recorder = s
	engine.emit = func(event InteractionEvent) {
		if event.Kind == "audio" {
			_ = engine.Playback(event.OperationID, event.Sequence, "started")
			startedAudio <- struct{}{}
		}
	}
	defer engine.Close()
	start, e := engine.Start(VoiceTurnRequest{SessionID: s.Snapshot().Settings.ConversationID, InputKind: "transcript", Chat: ChatRequest{Mode: "fast", MaxTokens: 128}})
	if e != nil {
		t.Fatal(e)
	}
	if e = engine.Commit(start.OperationID, nil, "割込み前の発言"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("generation did not commit")
	}
	select {
	case <-startedAudio:
	case <-time.After(2 * time.Second):
		t.Fatal("playback did not start")
	}
	if _, e = engine.Cancel(start.OperationID); e != nil {
		t.Fatal(e)
	}
	awaitInteraction(t, engine, start.OperationID, "CANCELED")
	events := readSpoolEvents(t, home)
	if len(events) != 2 {
		t.Fatal("missing interrupted result")
	}
	result := events[1]
	if result.Text != "確定した接頭辞です．" || result.Assistant.Generation != "canceled" || result.Assistant.Display != "confirmed_prefix" || result.Assistant.Playback == "completed" || result.Assistant.SpeechUnits[0].State == "completed" {
		t.Fatalf("invented completion: %+v", result)
	}
}
