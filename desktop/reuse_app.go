package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed experimental/reuse.html
var reusePage string

type reuseCandidate struct {
	Text     string `json:"text"`
	Evidence []struct {
		DocID string `json:"doc_id"`
		SHA   string `json:"sha256"`
		Quote string `json:"quote"`
	} `json:"evidence"`
	Reason string `json:"reason"`
}
type reusePending struct {
	Candidate reuseCandidate
	Revision  uint64
	Expires   time.Time
}
type reuseObservation struct {
	SpeakerID string `json:"speaker_id"`
	AtMS      int    `json:"at_ms"`
	Text      string `json:"text"`
}
type reuseExperiment struct {
	mu                    sync.Mutex
	engine                *InteractionEngine
	profile, token, topic string
	modelEngine           string
	revision              uint64
	speaking              bool
	suppressed            map[string]bool
	pending               *reusePending
	cooldown              time.Time
	cancel                context.CancelFunc
	active                string
	events                []InteractionEvent
	status                string
	candidate             reuseCandidate
	latest                InteractionSnapshot
	replays               map[string][]reuseObservation
	observations          []reuseObservation
	feedback              []map[string]string
}

func (s *reuseExperiment) invalidateLocked() {
	s.revision++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.pending = nil
	s.events = nil
	s.observations = nil
	if s.active != "" {
		s.latest, _ = s.engine.Cancel(s.active)
	}
	s.active = ""
}
func (s *reuseExperiment) speakLocked() {
	p := s.pending
	if p == nil {
		return
	}
	if p.Revision != s.revision || time.Now().After(p.Expires) || s.suppressed[s.topic] {
		s.pending = nil
		s.status = "discard"
		return
	}
	if s.speaking || time.Now().Before(s.cooldown) {
		s.status = "wait"
		return
	}
	snapshot, err := s.engine.StartPreparedCandidate(VoiceTurnRequest{SessionID: "reuse-synthetic", Speech: SpeechOptions{VoiceProfileID: s.profile}}, p.Candidate.Text)
	s.pending = nil
	if err != nil {
		s.status = "speech_unavailable"
		return
	}
	s.active = snapshot.OperationID
	s.status = "speak"
	s.cooldown = time.Now().Add(8 * time.Second)
}
func (s *reuseExperiment) observe(caseID string) {
	s.mu.Lock()
	s.invalidateLocked()
	s.topic = caseID
	if s.suppressed[caseID] {
		s.status = "suppressed"
		s.mu.Unlock()
		return
	}
	revision := s.revision
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.cancel = cancel
	s.status = "checking_memory"
	s.candidate = reuseCandidate{}
	s.mu.Unlock()
	defer cancel()
	// Replay explicit synthetic speakers in timestamp order．Only the final
	// observation triggers a memory check；these are not user questions．
	previous := 0
	for _, observation := range s.replays[caseID] {
		timer := time.NewTimer(time.Duration(observation.AtMS-previous) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.mu.Lock()
		if revision != s.revision {
			s.mu.Unlock()
			return
		}
		s.observations = append(s.observations, observation)
		s.mu.Unlock()
		previous = observation.AtMS
	}
	body, _ := json.Marshal(map[string]any{"case": caseID, "engine": s.modelEngine, "operation_id": interactionID("obs_"), "generation_revision": revision})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:18868/candidate", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+os.Getenv("EPHY_TTS_BEARER_TOKEN"))
	request.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}
	response, err := client.Do(request)
	var output []byte
	if err == nil {
		output, err = io.ReadAll(io.LimitReader(response.Body, 32<<10+1))
		response.Body.Close()
		if response.StatusCode != 200 {
			err = errors.New("candidate_failed")
		}
	}
	transport.CloseIdleConnections()
	var result struct {
		Candidate reuseCandidate `json:"candidate"`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision != s.revision {
		return
	}
	if ctx.Err() != nil {
		s.status = "discard"
		return
	}
	if err != nil || len(output) > 32<<10 || json.Unmarshal(output, &result) != nil {
		s.status = "candidate_unavailable"
		return
	}
	if result.Candidate.Text == "" || len(result.Candidate.Evidence) == 0 {
		s.status = "insufficient_evidence"
		return
	}
	s.candidate = result.Candidate
	s.pending = &reusePending{result.Candidate, revision, time.Now().Add(15 * time.Second)}
	s.speakLocked()
}
func (s *reuseExperiment) handler(w http.ResponseWriter, r *http.Request) {
	const origin = "http://127.0.0.1:18880"
	if r.Host != "127.0.0.1:18880" {
		http.Error(w, "invalid_host", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, strings.ReplaceAll(reusePage, "REUSE_TOKEN", s.token))
		return
	}
	if r.Header.Get("X-Ephy-Reuse") != s.token || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
		http.Error(w, "forbidden", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/events" && r.Method == "GET" {
		s.mu.Lock()
		s.speakLocked()
		events := s.events
		s.events = nil
		delivery, _ := recordedAssistant(s.latest, s.latest.State)
		result := map[string]any{"status": s.status, "candidate": s.candidate, "events": events, "delivery": delivery, "snapshot": s.latest, "profile": s.profile, "revision": s.revision, "observations": s.observations, "feedback": s.feedback}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var value struct {
		Case      string `json:"case"`
		Action    string `json:"action"`
		Operation string `json:"operation"`
		Sequence  int    `json:"sequence"`
		Phase     string `json:"phase"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		http.Error(w, "invalid_request", 400)
		return
	}
	switch r.URL.Path {
	case "/observe":
		switch value.Case {
		case "simple", "additional_search", "missing", "corrected", "injection":
		default:
			http.Error(w, "invalid_fixture", 400)
			return
		}
		go s.observe(value.Case)
	case "/control":
		s.mu.Lock()
		if value.Action == "natural" || value.Action == "intrusive" || value.Action == "not_now" {
			if len(s.feedback) == 32 {
				s.feedback = s.feedback[1:]
			}
			s.feedback = append(s.feedback, map[string]string{"topic": s.topic, "action": value.Action, "revision": fmt.Sprint(s.revision)})
		}
		switch value.Action {
		case "speaking":
			s.speaking = true
		case "quiet":
			s.speaking = false
			s.speakLocked()
		case "backchannel": // Ephy decision：do not interrupt the current output．
		case "intrusive", "not_now":
			s.suppressed[s.topic] = true
			s.invalidateLocked()
			s.status = "suppressed"
		case "natural":
			s.status = "feedback_recorded"
		case "cancel", "participants", "topic":
			s.invalidateLocked()
			s.status = "discard"
		default:
			s.mu.Unlock()
			http.Error(w, "invalid_control", 400)
			return
		}
		s.mu.Unlock()
	case "/ack":
		var err error
		if value.Phase == "interrupted" {
			err = s.engine.ObserveCandidateStop(value.Operation, value.Sequence)
		} else {
			err = s.engine.Playback(value.Operation, value.Sequence, value.Phase)
		}
		if err != nil {
			http.Error(w, "stale_ack", 409)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, "{}")
}

func runReuseExperiment() error {
	root := os.Getenv("EPHY_RUNTIME_ROOT")
	state := os.Getenv("EPHY_REUSE_STATE")
	if !filepath.IsAbs(root) || !filepath.IsAbs(state) {
		return errors.New("explicit_reuse_paths_required")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	profile := os.Getenv("EPHY_REUSE_PROFILE")
	if profile != "voice-irodori-anime-exp" && profile != "voice-irodori-audiocpp-exp" {
		return errors.New("explicit_reuse_profile_required")
	}
	engineName := os.Getenv("EPHY_REUSE_ENGINE")
	if engineName != "fixed" && engineName != "pydantic" {
		return errors.New("invalid_reuse_engine")
	}
	s := &reuseExperiment{modelEngine: engineName, profile: profile, token: hex.EncodeToString(token), suppressed: map[string]bool{}, revision: 1, status: "ready"}
	fixture, err := os.ReadFile(filepath.Join(root, "scripts", "reuse_core", "fixtures.json"))
	var replays struct {
		Synthetic bool `json:"synthetic"`
		Cases     []struct {
			ID           string             `json:"id"`
			Observations []reuseObservation `json:"observations"`
		} `json:"cases"`
	}
	if err != nil || json.Unmarshal(fixture, &replays) != nil || !replays.Synthetic {
		return errors.New("synthetic_replay_required")
	}
	s.replays = map[string][]reuseObservation{}
	for _, scenario := range replays.Cases {
		previous := 0
		for _, event := range scenario.Observations {
			if event.AtMS <= previous || event.AtMS > 5000 || (event.SpeakerID != "fixture-hana" && event.SpeakerID != "fixture-yuki") {
				return errors.New("invalid_replay")
			}
			previous = event.AtMS
		}
		s.replays[scenario.ID] = scenario.Observations
	}
	tts := NewVoiceTTSRegistry()
	ctxProfiles, cancelProfiles := context.WithTimeout(context.Background(), 5*time.Second)
	catalog := tts.Profiles(ctxProfiles)
	cancelProfiles()
	if catalog.ErrorCode != "" {
		return errors.New("reuse_speech_unavailable")
	}
	s.engine = NewInteractionEngine(NewNativeVoiceASR(root), tts, func(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
		return nil, errors.New("candidate_only")
	}, func(event InteractionEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if event.OperationID != s.active {
			return
		}
		if len(s.events) < 64 {
			s.events = append(s.events, event)
		} else {
			go s.engine.Cancel(event.OperationID)
		}
		if event.Snapshot != nil {
			s.latest = *event.Snapshot
			if s.latest.State == "COMPLETED" {
				s.status = "completed"
			}
			if s.latest.State == "FAILED" {
				s.status = "speech_unavailable"
			}
		}
	}, filepath.Join(state, "trace"))
	defer s.engine.Close()
	server := &http.Server{Addr: "127.0.0.1:18880", Handler: http.HandlerFunc(s.handler), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); s.mu.Lock(); s.invalidateLocked(); s.mu.Unlock(); _ = server.Close() }()
	fmt.Println("Ephy reuse experiment: http://127.0.0.1:18880")
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
