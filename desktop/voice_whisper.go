package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type WhisperASRConfig struct {
	Helper       string `json:"helper"`
	HelperSHA256 string `json:"helper_sha256"`
	Model        string `json:"model"`
	ModelID      string `json:"model_id"`
	ModelSHA256  string `json:"model_sha256"`
	VAD          string `json:"vad"`
	VADSHA256    string `json:"vad_sha256"`
	Threads      int    `json:"threads"`
	StepMS       int    `json:"step_ms"`
	Language     string `json:"language"`
	Backend      string `json:"backend"`
}

type WhisperVoiceASR struct {
	mu                                       sync.Mutex
	config                                   WhisperASRConfig
	configErr                                error
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	process                                  *nativeASRProcess
	start                                    nativeASRStreamStart
	writeGate                                chan struct{}
	active                                   *whisperASRSession
	epoch                                    uint64
	loading, ready, closed                   bool
	failure                                  string
	lastStart                                time.Time
	startupTimeout, ioTimeout, cancelTimeout time.Duration
}

func NewConfiguredVoiceASR(root string) VoiceASR {
	if strings.TrimSpace(os.Getenv("EPHY_ASR_PROVIDER")) != "whisper-cpp" {
		if value := strings.TrimSpace(os.Getenv("EPHY_ASR_PROVIDER")); value != "" && value != "macos-speech" {
			return newWhisperVoiceASR(WhisperASRConfig{}, errors.New("invalid_voice_config"))
		}
		return NewNativeVoiceASR(root)
	}
	path := strings.TrimSpace(os.Getenv("EPHY_ASR_CONFIG"))
	if path == "" {
		path = filepath.Join(root, "configs", "asr.local.json")
	}
	var config WhisperASRConfig
	data, err := os.ReadFile(path)
	if err == nil && len(data) <= 16384 {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&config)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("invalid_voice_config")
			}
		}
	} else {
		err = errors.New("invalid_voice_config")
	}
	return newWhisperVoiceASR(config, err)
}

func newWhisperVoiceASR(config WhisperASRConfig, err error) *WhisperVoiceASR {
	if config.Language == "" {
		config.Language = "ja"
	}
	if config.Backend == "" {
		config.Backend = "metal"
	}
	if config.Threads == 0 {
		config.Threads = 4
	}
	if config.StepMS == 0 {
		config.StepMS = 500
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &WhisperVoiceASR{config: config, configErr: err, ctx: ctx, cancel: cancel,
		writeGate: make(chan struct{}, 1), startupTimeout: 60 * time.Second, ioTimeout: 2 * time.Second, cancelTimeout: 2 * time.Second}
	if err == nil && (!nativeASRID.MatchString(config.ModelID) || len(config.ModelID) > 48 || config.Threads < 1 || config.Threads > 16 || config.StepMS < 100 || config.StepMS > 1500 || config.Language != "ja" || config.Backend != "metal") {
		p.configErr = errors.New("invalid_voice_config")
	}
	return p
}

func (p *WhisperVoiceASR) modelRevision() string {
	return p.config.ModelID + ":sha256:" + p.config.ModelSHA256[:min(16, len(p.config.ModelSHA256))]
}
func (p *WhisperVoiceASR) Identity() (string, string, string) {
	return "whisper-cpp", p.modelRevision(), "local-ja"
}
func (p *WhisperVoiceASR) Capabilities() ASRCapabilities {
	return ASRCapabilities{Partial: true, Activity: true, NoSpeech: true}
}
func (p *WhisperVoiceASR) Ready(ctx context.Context) error {
	r, err := p.Readiness(ctx)
	if err == nil && !r.CanStart {
		return errors.New("asr_loading")
	}
	return err
}

func (p *WhisperVoiceASR) Readiness(ctx context.Context) (VoiceReadiness, error) {
	if ctx.Err() != nil {
		return VoiceReadiness{}, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return blockedVoiceReadiness("asr_unavailable"), errors.New("asr_unavailable")
	}
	if p.configErr != nil {
		return blockedVoiceReadiness("invalid_voice_config"), errors.New("invalid_voice_config")
	}
	if !p.ready && !p.loading && (p.failure == "" || (p.failure == "asr_worker_exited" && time.Since(p.lastStart) > time.Second)) {
		p.loading = true
		p.failure = ""
		p.epoch++
		p.lastStart = time.Now()
		go p.initialize(p.epoch)
	}
	result := VoiceReadiness{State: "loading", Provider: "whisper-cpp", Model: p.config.ModelID,
		Capabilities: p.Capabilities()}
	if p.ready {
		result.State = "ready"
		result.CanStart = true
	}
	if p.failure != "" {
		result.State = "unavailable"
		result.ErrorCode = p.failure
		return result, errors.New(p.failure)
	}
	return result, nil
}

func verifyWhisperAsset(ctx context.Context, path, digest string, executable bool) error {
	if !filepath.IsAbs(path) || len(digest) != 64 {
		return errors.New("invalid_voice_config")
	}
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != 32 {
		return errors.New("invalid_voice_config")
	}
	f, err := os.Open(path)
	if err != nil {
		return errors.New("asr_model_missing")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4<<30 || (executable && info.Mode().Perm()&0111 == 0) {
		return errors.New("invalid_voice_config")
	}
	h := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := f.Read(buffer)
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("asr_model_missing")
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("asr_model_mismatch")
	}
	return nil
}

func (p *WhisperVoiceASR) initialize(epoch uint64) {
	ctx, cancel := context.WithTimeout(p.ctx, p.startupTimeout)
	defer cancel()
	for i, asset := range [][2]string{{p.config.Helper, p.config.HelperSHA256}, {p.config.Model, p.config.ModelSHA256}, {p.config.VAD, p.config.VADSHA256}} {
		if err := verifyWhisperAsset(ctx, asset[0], asset[1], i == 0); err != nil {
			p.breakWorker(epoch, asrStreamError(err))
			return
		}
	}
	start := p.start
	if start == nil {
		start = startNativeASRProcess
	}
	process, err := start(p.ctx, p.config.Helper, []string{"--model", p.config.Model, "--vad", p.config.VAD, "--model-revision", p.modelRevision(), "--threads", strconv.Itoa(p.config.Threads), "--step-ms", strconv.Itoa(p.config.StepMS)})
	if err != nil {
		p.breakWorker(epoch, "asr_worker_exited")
		return
	}
	p.mu.Lock()
	if p.closed || p.epoch != epoch {
		p.mu.Unlock()
		process.stop()
		_ = process.wait()
		return
	}
	p.process = process
	p.mu.Unlock()
	go func() {
		if process.diagnostics != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(process.diagnostics, 64<<10))
			_ = process.diagnostics.Close()
		}
	}()
	go p.readWorker(epoch, process)
	// The process is lifetime-owned by p，not by a single readiness request．
	for {
		p.mu.Lock()
		ready, stale, failed := p.ready, p.epoch != epoch || p.closed, p.failure != ""
		p.mu.Unlock()
		if ready || stale || failed {
			return
		}
		select {
		case <-ctx.Done():
			p.breakWorker(epoch, "asr_model_load_failed")
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (p *WhisperVoiceASR) breakWorker(epoch uint64, code string) {
	p.breakWorkerFor(epoch, code, nil)
}

func (p *WhisperVoiceASR) breakWorkerFor(epoch uint64, code string, only *whisperASRSession) {
	p.mu.Lock()
	if p.epoch != epoch || (only != nil && p.active != only) {
		p.mu.Unlock()
		return
	}
	process, s := p.process, p.active
	p.process = nil
	p.active = nil
	p.ready = false
	p.loading = false
	if p.failure == "" {
		p.failure = code
	}
	p.mu.Unlock()
	if s != nil {
		s.complete(ASRUpdate{}, errors.New(code))
	}
	if process != nil {
		process.stop()
	}
}

func (p *WhisperVoiceASR) Close() {
	p.mu.Lock()
	p.closed = true
	epoch := p.epoch
	p.mu.Unlock()
	p.cancel()
	p.breakWorker(epoch, "asr_canceled")
}

func (p *WhisperVoiceASR) send(ctx context.Context, s *whisperASRSession, frame map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, p.ioTimeout)
	defer cancel()
	select {
	case p.writeGate <- struct{}{}:
		defer func() { <-p.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p.mu.Lock()
	process, valid := p.process, p.active == s && p.epoch == s.epoch && p.ready
	p.mu.Unlock()
	if !valid || process == nil {
		return errors.New("asr_canceled")
	}
	frame["protocol"] = 1
	for key, value := range map[string]string{"operation_id": s.request.OperationID, "session_id": s.request.SessionID, "turn_id": s.request.TurnID, "segment_id": s.request.SegmentID} {
		frame[key] = value
	}
	data, err := json.Marshal(frame)
	if err != nil || len(data)+1 > nativeASRFrameBytes {
		return errors.New("asr_stream_invalid")
	}
	data = append(data, '\n')
	written := make(chan error, 1)
	go func() { _, err := process.input.Write(data); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.breakWorkerFor(s.epoch, "asr_worker_exited", s)
			return errors.New("asr_worker_exited")
		}
		return nil
	case <-ctx.Done():
		p.breakWorkerFor(s.epoch, "asr_worker_exited", s)
		return errors.New("asr_timeout")
	}
}

type whisperFrame struct {
	ASRUpdate
	Type         string           `json:"type"`
	Protocol     int              `json:"protocol"`
	SampleRate   int              `json:"sample_rate"`
	Capabilities *ASRCapabilities `json:"capabilities,omitempty"`
	StepMS       int              `json:"step_ms,omitempty"`
}

func (p *WhisperVoiceASR) readWorker(epoch uint64, process *nativeASRProcess) {
	defer func() { _ = process.output.Close(); _ = process.wait(); p.breakWorker(epoch, "asr_worker_exited") }()
	scanner := bufio.NewScanner(process.output)
	scanner.Buffer(make([]byte, 4096), nativeASRFrameBytes)
	for scanner.Scan() {
		var frame whisperFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil || !utf8.Valid(scanner.Bytes()) || frame.Protocol != 1 {
			p.breakWorker(epoch, "asr_stream_invalid")
			return
		}
		p.mu.Lock()
		if p.closed || p.epoch != epoch {
			p.mu.Unlock()
			return
		}
		s := p.active
		if frame.Type == "ready" {
			if p.ready || !p.loading || frame.Provider != "whisper-cpp" || frame.ModelRevision != p.modelRevision() || frame.SampleRate != 16000 || frame.Capabilities == nil || *frame.Capabilities != p.Capabilities() || frame.StepMS != p.config.StepMS {
				p.mu.Unlock()
				p.breakWorker(epoch, "asr_stream_invalid")
				return
			}
			p.ready = true
			p.loading = false
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()
		if frame.Type == "fatal" {
			p.breakWorker(epoch, asrStreamError(errors.New(frame.ErrorCode)))
			return
		}
		if s == nil || frame.OperationID != s.request.OperationID || frame.SessionID != s.request.SessionID || frame.TurnID != s.request.TurnID || frame.SegmentID != s.request.SegmentID {
			p.breakWorker(epoch, "asr_stream_invalid")
			return
		}
		if err := s.receive(frame); err != nil {
			p.breakWorker(epoch, asrStreamError(err))
			return
		}
	}
	p.breakWorker(epoch, "asr_worker_exited")
}

type whisperASRSession struct {
	mu                                       sync.Mutex
	inputMu                                  sync.Mutex
	cancelOnce                               sync.Once
	p                                        *WhisperVoiceASR
	epoch                                    uint64
	request                                  ASRSessionRequest
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	onUpdate                                 func(ASRUpdate)
	started, done                            chan struct{}
	startedOK, terminal, canceled, finishing bool
	sequence, bytes, revision                int
	monotonic                                int64
	pending                                  *ASRUpdate
	final                                    ASRUpdate
	err                                      error
}

func (p *WhisperVoiceASR) OpenSession(ctx context.Context, r ASRSessionRequest, onUpdate func(ASRUpdate)) (VoiceASRSession, error) {
	return p.open(ctx, r, onUpdate, true, p.config.StepMS)
}
func (p *WhisperVoiceASR) OpenInterruptionSession(ctx context.Context, r ASRSessionRequest, onUpdate func(ASRUpdate)) (VoiceASRSession, error) {
	return p.open(ctx, r, onUpdate, true, min(p.config.StepMS, 200))
}

func (p *WhisperVoiceASR) open(ctx context.Context, r ASRSessionRequest, onUpdate func(ASRUpdate), partial bool, stepMS int) (VoiceASRSession, error) {
	for _, id := range []string{r.OperationID, r.SessionID, r.TurnID, r.SegmentID} {
		if !nativeASRID.MatchString(id) {
			return nil, errors.New("asr_stream_invalid")
		}
	}
	if r.SampleRate < 8000 || r.SampleRate > 48000 || onUpdate == nil {
		return nil, errors.New("invalid_audio")
	}
	if err := p.Ready(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	p.mu.Lock()
	if p.active != nil || !p.ready || p.closed {
		p.mu.Unlock()
		cancel()
		return nil, errors.New("asr_busy")
	}
	s := &whisperASRSession{p: p, epoch: p.epoch, request: r, ctx: ctx, cancel: cancel, onUpdate: onUpdate, started: make(chan struct{}), done: make(chan struct{}), monotonic: -1}
	p.active = s
	p.mu.Unlock()
	if err := p.send(ctx, s, map[string]any{"type": "start", "sample_rate": r.SampleRate, "partial": partial, "step_ms": stepMS}); err != nil {
		p.breakWorkerFor(s.epoch, "asr_worker_exited", s)
		cancel()
		return nil, err
	}
	select {
	case <-s.started:
	case <-s.done:
		cancel()
		return nil, s.err
	case <-ctx.Done():
		s.Cancel()
		return nil, ctx.Err()
	case <-time.After(p.ioTimeout):
		p.breakWorker(s.epoch, "asr_timeout")
		cancel()
		return nil, errors.New("asr_timeout")
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Cancel()
		case <-s.done:
		}
	}()
	return s, nil
}

func (s *whisperASRSession) callback(u ASRUpdate) error {
	result := make(chan bool, 1)
	go func() { ok := false; defer func() { _ = recover(); result <- ok }(); s.onUpdate(u); ok = true }()
	select {
	case ok := <-result:
		if !ok {
			return errors.New("asr_failed")
		}
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-time.After(2 * time.Second):
		return errors.New("asr_timeout")
	}
}

func (s *whisperASRSession) receive(frame whisperFrame) error {
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	if frame.Type == "started" {
		if s.startedOK {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
		s.startedOK = true
		close(s.started)
		s.mu.Unlock()
		return nil
	}
	if !s.startedOK {
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	if frame.Type == "done" {
		if s.pending == nil || s.pending.Phase != frame.Phase {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
		u := *s.pending
		canceled := s.canceled
		s.mu.Unlock()
		var err error
		if u.Phase == "failure" || u.Phase == "timeout" || u.Phase == "canceled" {
			err = errors.New(asrStreamError(errors.New(u.ErrorCode)))
		}
		if canceled {
			err = errors.New("asr_canceled")
		} else if u.Phase == "failure" || u.Phase == "timeout" {
			// A decoder failure can arrive while recording．Notify the engine now
			// so it releases capture without waiting for a later Append or Finish．
			_ = s.callback(u)
		} else if err == nil {
			if callbackErr := s.callback(u); callbackErr != nil {
				err = callbackErr
			}
		}
		s.p.mu.Lock()
		if s.p.active == s {
			s.p.active = nil
		}
		s.p.mu.Unlock()
		s.complete(u, err)
		return nil
	}
	if frame.Type != "update" || s.pending != nil {
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	u := frame.ASRUpdate
	if u.Diagnostic != nil {
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	if u.Provider != "whisper-cpp" || u.ModelRevision != s.p.modelRevision() || u.Revision <= s.revision || u.Revision > maxASRRevisions || u.MonotonicMS < s.monotonic || u.MonotonicMS < 0 || u.MonotonicMS > 125000 || !utf8.ValidString(u.Transcript) || len(u.Transcript) > 16<<10 {
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	terminal := false
	switch u.Phase {
	case "activity":
		if !validASRActivity(u.Activity) || u.Transcript != "" || u.StablePrefix != "" || u.ErrorCode != "" {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
	case "partial":
		if u.Activity != nil || u.StablePrefix != "" || u.ErrorCode != "" {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
	case "final":
		if !s.finishing || u.Activity != nil || strings.TrimSpace(u.Transcript) == "" || u.StablePrefix != u.Transcript || u.ErrorCode != "" {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
		terminal = true
	case "no_speech":
		if !s.finishing || u.Activity != nil || u.Transcript != "" || u.StablePrefix != "" || u.ErrorCode != "" {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
		terminal = true
	case "failure", "timeout", "canceled":
		validCode := (u.Phase == "failure" && (u.ErrorCode == "asr_failed" || u.ErrorCode == "asr_empty_result" || u.ErrorCode == "invalid_audio")) ||
			(u.Phase == "timeout" && u.ErrorCode == "asr_timeout") || (u.Phase == "canceled" && u.ErrorCode == "asr_canceled" && s.canceled)
		if u.Activity != nil || u.Transcript != "" || u.StablePrefix != "" || !validCode {
			s.mu.Unlock()
			return errors.New("asr_stream_invalid")
		}
		terminal = true
	default:
		s.mu.Unlock()
		return errors.New("asr_stream_invalid")
	}
	s.revision = u.Revision
	s.monotonic = u.MonotonicMS
	if terminal {
		s.pending = &u
		s.mu.Unlock()
		return nil
	}
	canceled := s.canceled
	s.mu.Unlock()
	if !canceled {
		return s.callback(u)
	}
	return nil
}

func (s *whisperASRSession) complete(u ASRUpdate, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return
	}
	s.final = u
	s.err = err
	s.terminal = true
	close(s.done)
	s.cancel()
}

func (s *whisperASRSession) Append(ctx context.Context, sequence int, pcm []byte) error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.mu.Lock()
	if s.terminal || s.canceled || s.finishing {
		s.mu.Unlock()
		return errors.New("asr_canceled")
	}
	if sequence != s.sequence+1 || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxASRPCMChunkBytes || s.bytes+len(pcm) > s.request.SampleRate*2*60 {
		s.mu.Unlock()
		return errors.New("invalid_audio")
	}
	s.sequence = sequence
	s.bytes += len(pcm)
	s.mu.Unlock()
	return s.p.send(ctx, s, map[string]any{"type": "audio", "sequence": sequence, "pcm_base64": base64.StdEncoding.EncodeToString(pcm)})
}

func (s *whisperASRSession) Finish(ctx context.Context) (ASRUpdate, error) {
	s.inputMu.Lock()
	s.mu.Lock()
	if !s.terminal && s.pending == nil && !s.finishing && !s.canceled {
		if s.bytes == 0 {
			s.mu.Unlock()
			s.inputMu.Unlock()
			return ASRUpdate{}, errors.New("invalid_audio")
		}
		s.finishing = true
		s.mu.Unlock()
		err := s.p.send(ctx, s, map[string]any{"type": "finish"})
		s.inputMu.Unlock()
		if err != nil {
			return ASRUpdate{}, err
		}
	} else {
		s.mu.Unlock()
		s.inputMu.Unlock()
	}
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.final, s.err
	case <-ctx.Done():
		s.Cancel()
		return ASRUpdate{}, ctx.Err()
	}
}

func (s *whisperASRSession) Cancel() {
	s.cancelOnce.Do(func() {
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			return
		}
		s.canceled = true
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), s.p.ioTimeout+s.p.cancelTimeout)
		defer cancel()
		s.inputMu.Lock()
		err := s.p.send(ctx, s, map[string]any{"type": "cancel"})
		s.inputMu.Unlock()
		if err == nil {
			select {
			case <-s.done:
				return
			case <-ctx.Done():
			}
		}
		s.p.breakWorkerFor(s.epoch, "asr_worker_exited", s)
	})
}

func (p *WhisperVoiceASR) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if !validVoiceWAV(audio, 60) {
		return "", errors.New("invalid_audio")
	}
	rate, pcm := voicePCMFromWAV(audio)
	id := interactionID("file_")
	s, err := p.open(ctx, ASRSessionRequest{OperationID: id, SessionID: id, TurnID: id, SegmentID: id, SampleRate: rate}, func(ASRUpdate) {}, false, p.config.StepMS)
	if err != nil {
		return "", err
	}
	defer s.Cancel()
	sequence := 0
	for len(pcm) > 0 {
		n := min(len(pcm), maxASRPCMChunkBytes)
		sequence++
		if err = s.Append(ctx, sequence, pcm[:n]); err != nil {
			return "", err
		}
		pcm = pcm[n:]
	}
	u, err := s.Finish(ctx)
	if err != nil {
		return "", err
	}
	if u.Phase == "no_speech" {
		return "", errors.New("asr_empty_result")
	}
	return u.Transcript, nil
}
