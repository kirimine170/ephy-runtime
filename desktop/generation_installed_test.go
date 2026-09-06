package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in acceptance uses existing services and the installed fallback voice.
// Generated content remains in memory; only temporary playback WAVs exist, and
// they are removed on completion or cancellation. No microphone is recorded.
type installedGenerationTransport struct {
	mu     sync.Mutex
	seed   int
	cache  bool
	bodies []*installedGenerationCapture
}
type installedGenerationCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (c *installedGenerationCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.Write(p)
}
func (c *installedGenerationCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.String()
}

type installedGenerationBody struct {
	io.Reader
	io.Closer
}

func (p *installedGenerationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body.Close()
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	p.mu.Lock()
	body["seed"], body["cache_prompt"] = p.seed, p.cache
	p.mu.Unlock()
	data, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	response, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	buffer := &installedGenerationCapture{}
	p.mu.Lock()
	p.bodies = append(p.bodies, buffer)
	p.mu.Unlock()
	response.Body = installedGenerationBody{Reader: io.TeeReader(response.Body, buffer), Closer: response.Body}
	return response, nil
}

func TestGenerationInstalledSeasonsAndPlayback(t *testing.T) {
	if os.Getenv("EPHY_C01_INSTALLED") != "1" || runtime.GOOS != "darwin" {
		t.Skip("opt in to existing model and real macOS playback")
	}
	app := NewApp()
	transport := &installedGenerationTransport{}
	app.httpClient = &http.Client{Transport: transport, Timeout: 180 * time.Second}
	tts := NewNativeVoiceTTS()
	private := t.TempDir()
	tts.tempRoot = private
	events := make(chan InteractionEvent, 1024)
	engine := NewInteractionEngine(testVoiceASR{}, tts, app.chatWithContext, func(event InteractionEvent) { events <- event }, "")
	defer engine.Close()
	for _, scenario := range []struct {
		name          string
		seed          int
		cache, cancel bool
	}{
		{"prompt_cache_cold", 42, false, false}, {"prompt_cache_warm", 42, true, false},
		{"repeat_warm", 43, true, false}, {"cancel_during_output", 44, true, true},
		{"after_cancel_new_session", 45, true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			transport.mu.Lock()
			transport.seed, transport.cache = scenario.seed, scenario.cache
			transport.bodies = nil
			transport.mu.Unlock()
			s, err := engine.Start(VoiceTurnRequest{SessionID: "c01-installed-" + scenario.name, InputKind: "transcript", Chat: ChatRequest{Mode: "fast", Stream: true, Temperature: .2, MaxTokens: 512, SourceScope: "all"}})
			if err != nil {
				t.Fatal("cannot start acceptance operation")
			}
			if err = engine.Commit(s.OperationID, nil, "日本の四季についてもうちょっと長めに説明して"); err != nil {
				t.Fatal("cannot commit acceptance input")
			}
			// Three bounded generated segments can take several minutes to speak.
			// Provider stage deadlines are unchanged; this includes real playback.
			deadline := time.NewTimer(600 * time.Second)
			defer deadline.Stop()
			playback := make(chan InteractionEvent, 64)
			playerDone := make(chan error, 1)
			engine.mu.Lock()
			operationContext := engine.turns[s.OperationID].ctx
			engine.mu.Unlock()
			go func() {
				for event := range playback {
					if operationContext.Err() != nil {
						continue
					}
					data, err := base64.StdEncoding.DecodeString(event.AudioBase64)
					if err != nil {
						playerDone <- err
						return
					}
					file, err := os.CreateTemp(private, "playback-*.wav")
					if err != nil {
						playerDone <- err
						return
					}
					name := file.Name()
					_, writeErr := file.Write(data)
					closeErr := file.Close()
					if writeErr != nil || closeErr != nil {
						os.Remove(name)
						playerDone <- io.ErrShortWrite
						return
					}
					cmd := exec.CommandContext(operationContext, "/usr/bin/afplay", name)
					cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
					err = cmd.Start()
					if err == nil {
						_ = engine.Playback(s.OperationID, event.Sequence, "started")
						if scenario.cancel {
							time.Sleep(150 * time.Millisecond)
							_, _ = engine.Cancel(s.OperationID)
						}
						err = cmd.Wait()
					}
					removeErr := os.Remove(name)
					if removeErr != nil {
						playerDone <- removeErr
						return
					}
					if err != nil && operationContext.Err() == nil {
						_ = engine.Fail(s.OperationID, "playback_failed")
						playerDone <- err
						return
					}
					_ = engine.Playback(s.OperationID, event.Sequence, "stopped")
				}
				playerDone <- nil
			}()
			var terminal InteractionSnapshot
			chunks := 0
			for terminal.State == "" {
				select {
				case <-deadline.C:
					timedOut, _ := engine.Snapshot(s.OperationID)
					metadata, _ := json.Marshal(map[string]any{"scenario": scenario.name, "state": timedOut.State, "generation": timedOut.Generation, "audio_chunks": chunks})
					t.Log("acceptance deadline metadata: " + string(metadata))
					_, _ = engine.Cancel(s.OperationID)
					close(playback)
					<-playerDone
					t.Fatal("acceptance timeout")
				case event := <-events:
					if event.OperationID != s.OperationID {
						continue
					}
					if event.Kind == "audio" {
						chunks++
						playback <- event
					}
					if event.Kind == "trace" && event.Trace != nil && (event.Trace.Name == "llm_completed" || event.Trace.Name == "llm_incomplete") {
						metadata, _ := json.Marshal(map[string]any{"scenario": scenario.name, "milestone": event.Trace.Name, "generation": event.Trace.Generation, "monotonic_ms": event.Trace.MonotonicMS})
						t.Log(string(metadata))
					}
					if event.Kind == "state" && event.Snapshot != nil && interactionTerminal(event.Snapshot.State) {
						terminal = *event.Snapshot
					}
				}
			}
			close(playback)
			if err := <-playerDone; err != nil {
				t.Fatal("OS playback failed")
			}
			if scenario.cancel {
				if terminal.State != "CANCELED" {
					t.Fatal("canceled operation completed")
				}
			} else {
				if terminal.State != "COMPLETED" || terminal.Generation == nil || !terminal.Generation.Complete {
					t.Fatalf("generation terminal state=%s code=%s", terminal.State, terminal.ErrorCode)
				}
				for _, season := range []string{"春", "夏", "秋", "冬"} {
					if !strings.Contains(terminal.ResponsePlan.Text, season) {
						t.Fatal("completed response omitted a season")
					}
				}
				boundaries, balanced := generationBoundaries(terminal.ResponsePlan.Text, true)
				if !balanced || len(boundaries) == 0 {
					t.Fatal("completed response has no safe boundary")
				}
			}
			// All producer stages have observed operation cancellation before this check.
			var remaining []string
			for retry := 0; retry < 50; retry++ {
				remaining = nil
				_ = filepath.WalkDir(private, func(path string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						remaining = append(remaining, path)
					}
					return nil
				})
				if len(remaining) == 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(remaining) != 0 {
				t.Fatal("temporary playback or synthesis audio remains")
			}
			trace, _ := engine.Trace(s.OperationID)
			validation := ValidateTrace(trace)
			text := ""
			if terminal.ResponsePlan != nil {
				text = terminal.ResponsePlan.Text
			}
			sum := sha256.Sum256([]byte(text))
			var usages []map[string]any
			transport.mu.Lock()
			for _, buffer := range transport.bodies {
				for _, line := range strings.Split(buffer.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var payload map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload) == nil {
						if usage, ok := payload["usage"].(map[string]any); ok {
							safe := map[string]any{}
							for _, key := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
								if value, ok := usage[key].(float64); ok {
									safe[key] = value
								}
							}
							if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
								if value, ok := details["cached_tokens"].(float64); ok {
									safe["cached_tokens"] = value
								}
							}
							usages = append(usages, safe)
						}
					}
				}
			}
			transport.mu.Unlock()
			record := map[string]any{"scenario": scenario.name, "state": terminal.State, "generation": terminal.Generation, "latencies_ms": validation.LatenciesMS, "audio_chunks": chunks, "visible_characters": len([]rune(text)), "visible_sha256": hex.EncodeToString(sum[:]), "usage": usages, "temporary_wavs_remaining": 0, "playback": "real afplay process start; acoustic onset not measured", "microphone_recorded": false, "model_load_cold": false}
			encoded, _ := json.Marshal(record)
			t.Log(string(encoded))
		})
	}
}
