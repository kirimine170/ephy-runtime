package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const nativeASRFrameBytes = 96 << 10

var nativeASRID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)
var nativeASRModelRevision = regexp.MustCompile(`^apple-opaque:macos-[0-9]+\.[0-9]+\.[0-9]+-build-[A-Za-z0-9._-]+:[a-z]{2,3}-[A-Z]{2}$`)

type nativeASRProcess struct {
	input       io.WriteCloser
	output      io.ReadCloser
	diagnostics io.ReadCloser
	wait        func() error
	stop        func()
}

type nativeASRStreamStart func(context.Context, string, []string) (*nativeASRProcess, error)

type nativeASRStreamLimits struct{ prepare, capture, finalize, write, callback time.Duration }

func boundedASRDuration(value, maximum time.Duration) time.Duration {
	if value <= 0 || value > maximum {
		return maximum
	}
	return value
}

func startNativeASRProcess(ctx context.Context, executable string, args []string) (*nativeASRProcess, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	diagnostics, err := command.StderrPipe()
	if err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		diagnostics.Close()
		return nil, err
	}
	return &nativeASRProcess{input: input, output: output, diagnostics: diagnostics, wait: command.Wait,
		stop: func() { _ = command.Process.Kill(); _ = input.Close(); _ = output.Close(); _ = diagnostics.Close() }}, nil
}

type nativeASRSession struct {
	mu            sync.Mutex
	request       ASRSessionRequest
	provider      *NativeVoiceASR
	process       *nativeASRProcess
	ctx           context.Context
	cancel        context.CancelFunc
	callback      func(ASRUpdate)
	writeGate     chan struct{}
	done          chan struct{}
	limits        nativeASRStreamLimits
	started       time.Time
	lastRevision  int
	lastMS        int64
	modelRevision string
	lastSequence  int
	bytes         int
	finishing     bool
	terminal      bool
	final         ASRUpdate
	err           error
	prepareTimer  *time.Timer
	captureTimer  *time.Timer
	finishTimer   *time.Timer
}

// OpenSession starts one independent Speech helper．The parent context owns the
// whole session，not just startup．PCM and hypotheses never enter a file or log．
func (p *NativeVoiceASR) OpenSession(ctx context.Context, request ASRSessionRequest, onUpdate func(ASRUpdate)) (VoiceASRSession, error) {
	for _, id := range []string{request.OperationID, request.SessionID, request.TurnID, request.SegmentID} {
		if !nativeASRID.MatchString(id) {
			return nil, errors.New("asr_stream_invalid")
		}
	}
	if request.SampleRate < 8000 || request.SampleRate > 48000 || onUpdate == nil {
		return nil, errors.New("asr_stream_invalid")
	}
	if _, err := p.Readiness(ctx); err != nil {
		return nil, err
	}
	p.streamCallbackOnce.Do(func() { p.streamCallbackGate = make(chan struct{}, 1) })
	limits := nativeASRStreamLimits{
		prepare:  boundedASRDuration(p.streamLimits.prepare, 30*time.Second),
		capture:  boundedASRDuration(p.streamLimits.capture, 60*time.Second),
		finalize: boundedASRDuration(p.streamLimits.finalize, 15*time.Second),
		write:    boundedASRDuration(p.streamLimits.write, 5*time.Second),
		callback: boundedASRDuration(p.streamLimits.callback, 2*time.Second),
	}
	// Startup，permission preparation，capture，drain，and finalization each have
	// finite bounds．Permission preparation does not consume capture duration．
	sessionCtx, cancel := context.WithTimeout(ctx, limits.prepare+limits.capture+limits.finalize+2*limits.write)
	start := p.streamStart
	if start == nil {
		start = startNativeASRProcess
	}
	process, err := start(sessionCtx, p.executable, []string{"--stream", "--locale", p.locale})
	if err != nil {
		fixed := nativeASRError(sessionCtx, err, "asr_failed")
		cancel()
		p.readiness.invalidate()
		return nil, fixed
	}
	s := &nativeASRSession{request: request, provider: p, process: process, ctx: sessionCtx, cancel: cancel,
		callback: onUpdate, writeGate: make(chan struct{}, 1), done: make(chan struct{}), limits: limits,
		started: time.Now(), modelRevision: "apple-opaque:" + p.locale}
	go s.readUpdates()
	go func() {
		select {
		case <-sessionCtx.Done():
			s.fail(nativeASRError(sessionCtx, sessionCtx.Err(), "asr_failed"))
		case <-s.done:
		}
	}()
	startFrame := map[string]any{"type": "start", "operation_id": request.OperationID, "session_id": request.SessionID,
		"turn_id": request.TurnID, "segment_id": request.SegmentID, "sample_rate": request.SampleRate}
	if err := s.send(ctx, startFrame); err != nil {
		s.fail(err)
		return nil, err
	}
	s.mu.Lock()
	if !s.terminal {
		s.prepareTimer = time.AfterFunc(limits.prepare, func() { s.fail(errors.New("asr_timeout")) })
	}
	s.mu.Unlock()
	return s, nil
}

func nativeASRError(ctx context.Context, err error, fallback string) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return errors.New("asr_canceled")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("asr_timeout")
	}
	if err != nil {
		switch err.Error() {
		case "asr_stream_invalid", "asr_stream_eof", "asr_timeout", "asr_canceled", "asr_failed", "invalid_audio", "invalid_voice_config", "asr_permission_denied", "asr_permission_restricted", "asr_on_device_unavailable", "asr_unavailable", "asr_empty_transcript":
			return errors.New(err.Error())
		}
	}
	return errors.New(fallback)
}

func (s *nativeASRSession) send(ctx context.Context, frame any) error {
	data, err := json.Marshal(frame)
	if err != nil || len(data)+1 > nativeASRFrameBytes {
		return errors.New("asr_stream_invalid")
	}
	data = append(data, '\n')
	writeCtx, cancel := context.WithTimeout(ctx, s.limits.write)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := io.Copy(s.process.input, bytes.NewReader(data)); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			return nativeASRError(s.ctx, err, "asr_stream_eof")
		}
		return nil
	case <-writeCtx.Done():
		err := nativeASRError(writeCtx, writeCtx.Err(), "asr_timeout")
		// Preserve the timeout before closing stdout can race with the reader．
		s.fail(err)
		return err
	case <-s.ctx.Done():
		err := nativeASRError(s.ctx, s.ctx.Err(), "asr_canceled")
		s.fail(err)
		return err
	}
}

func (s *nativeASRSession) acquireWrite(ctx context.Context) (bool, error) {
	select {
	case s.writeGate <- struct{}{}:
		return true, nil
	case <-ctx.Done():
		return false, nativeASRError(ctx, ctx.Err(), "asr_canceled")
	case <-s.done:
		return false, nil
	}
}

func (s *nativeASRSession) releaseWrite() {
	select {
	case <-s.writeGate:
	default:
	}
}

func (s *nativeASRSession) Append(ctx context.Context, sequence int, pcm []byte) error {
	if ctx.Err() != nil {
		s.fail(nativeASRError(ctx, ctx.Err(), "asr_canceled"))
		return nativeASRError(ctx, ctx.Err(), "asr_canceled")
	}
	acquired, err := s.acquireWrite(ctx)
	if err != nil {
		s.fail(err)
		return err
	}
	if acquired {
		defer s.releaseWrite()
	}
	s.mu.Lock()
	if s.terminal {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if s.final.Phase == "final" {
		// The helper has already exited successfully while its final callback is
		// being delivered．No more PCM belongs to this recognition session．
		s.mu.Unlock()
		return nil
	}
	if s.finishing || sequence != s.lastSequence+1 || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxASRPCMChunkBytes ||
		s.bytes+len(pcm) > min(maxASRPCMBytes, s.request.SampleRate*2*60) {
		s.mu.Unlock()
		err := errors.New("asr_stream_invalid")
		s.fail(err)
		return err
	}
	s.lastSequence, s.bytes = sequence, s.bytes+len(pcm)
	s.mu.Unlock()
	err = s.send(ctx, map[string]any{"type": "audio", "sequence": sequence, "pcm_base64": base64.StdEncoding.EncodeToString(pcm)})
	if err == nil {
		s.mu.Lock()
		if !s.terminal && !s.finishing && s.captureTimer == nil {
			if s.prepareTimer != nil {
				s.prepareTimer.Stop()
			}
			// Permission/UI preparation before the first PCM consumes no capture
			// time．Keep a finite drain margin for the final frontend PCM frames．
			// The independent whole-session deadline still bounds an idle session．
			s.captureTimer = time.AfterFunc(s.limits.capture+s.limits.write, func() { _, _ = s.Finish(context.Background()) })
		}
		s.mu.Unlock()
	}
	if err != nil {
		// Natural final may close stdin while the last in-flight write finishes．
		select {
		case <-s.done:
			s.mu.Lock()
			finalErr := s.err
			s.mu.Unlock()
			return finalErr
		case <-time.After(100 * time.Millisecond):
		}
		s.fail(err)
	}
	return err
}

func (s *nativeASRSession) Finish(ctx context.Context) (ASRUpdate, error) {
	acquired, err := s.acquireWrite(ctx)
	if err != nil {
		s.fail(err)
		return ASRUpdate{}, err
	}
	s.mu.Lock()
	terminal, finishing := s.terminal, s.finishing
	if !terminal && !finishing {
		s.finishing = true
		if s.prepareTimer != nil {
			s.prepareTimer.Stop()
		}
		if s.captureTimer != nil {
			s.captureTimer.Stop()
		}
		s.finishTimer = time.AfterFunc(s.limits.finalize, func() { s.fail(errors.New("asr_timeout")) })
	}
	s.mu.Unlock()
	if !terminal && !finishing {
		if err := s.send(ctx, map[string]any{"type": "finish"}); err != nil {
			select {
			case <-s.done:
			default:
				s.fail(err)
			}
		}
	}
	if acquired {
		s.releaseWrite()
	}
	select {
	case <-s.done:
		s.mu.Lock()
		final, err := s.final, s.err
		s.mu.Unlock()
		return final, err
	case <-ctx.Done():
		err := nativeASRError(ctx, ctx.Err(), "asr_canceled")
		s.fail(err)
		return ASRUpdate{}, err
	}
}

func (s *nativeASRSession) Cancel() { s.fail(errors.New("asr_canceled")) }

func (s *nativeASRSession) call(update ASRUpdate) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.limits.callback)
	defer cancel()
	select {
	case s.provider.streamCallbackGate <- struct{}{}:
	case <-ctx.Done():
		return nativeASRError(ctx, ctx.Err(), "asr_timeout")
	}
	result := make(chan bool, 1)
	go func() {
		ok := false
		defer func() { _ = recover(); <-s.provider.streamCallbackGate; result <- ok }()
		s.mu.Lock()
		terminal := s.terminal
		s.mu.Unlock()
		if terminal {
			ok = true
			return
		}
		s.callback(update)
		ok = true
	}()
	select {
	case ok := <-result:
		if !ok {
			return errors.New("asr_failed")
		}
		return nil
	case <-ctx.Done():
		return nativeASRError(ctx, ctx.Err(), "asr_timeout")
	}
}

func (s *nativeASRSession) validate(update ASRUpdate) error {
	if update.OperationID != s.request.OperationID || update.SessionID != s.request.SessionID || update.TurnID != s.request.TurnID || update.SegmentID != s.request.SegmentID ||
		update.Provider != "macos-speech" || len(update.ModelRevision) > 128 || !nativeASRModelRevision.MatchString(update.ModelRevision) || !strings.HasSuffix(update.ModelRevision, ":"+s.provider.locale) ||
		update.Revision <= s.lastRevision || update.Revision > maxASRRevisions || update.MonotonicMS < s.lastMS || update.MonotonicMS < 0 || update.MonotonicMS > 120_000 ||
		!utf8.ValidString(update.Transcript) || !utf8.ValidString(update.StablePrefix) || len(update.Transcript) > 16<<10 || len(update.StablePrefix) > 16<<10 {
		return errors.New("asr_stream_invalid")
	}
	if s.lastRevision > 0 && update.ModelRevision != s.modelRevision {
		return errors.New("asr_stream_invalid")
	}
	switch update.Phase {
	case "partial":
		if update.StablePrefix != "" || update.ErrorCode != "" || update.Revision >= maxASRRevisions {
			return errors.New("asr_stream_invalid")
		}
	case "stable":
		if update.StablePrefix == "" || !strings.HasPrefix(update.Transcript, update.StablePrefix) || update.ErrorCode != "" || update.Revision >= maxASRRevisions {
			return errors.New("asr_stream_invalid")
		}
	case "final":
		if strings.TrimSpace(update.Transcript) == "" || update.StablePrefix != update.Transcript || update.ErrorCode != "" {
			return errors.New("asr_stream_invalid")
		}
	case "failure", "timeout", "canceled":
		if update.Transcript != "" || update.StablePrefix != "" || update.ErrorCode == "" || nativeASRError(context.Background(), errors.New(update.ErrorCode), "asr_failed").Error() != update.ErrorCode {
			return errors.New("asr_stream_invalid")
		}
		if (update.Phase == "timeout") != (update.ErrorCode == "asr_timeout") || (update.Phase == "canceled") != (update.ErrorCode == "asr_canceled") {
			return errors.New("asr_stream_invalid")
		}
	default:
		return errors.New("asr_stream_invalid")
	}
	return nil
}

func (s *nativeASRSession) readUpdates() {
	// Drain stderr without retaining arbitrary diagnostics or blocking the helper．
	var diagnosticMu sync.Mutex
	var diagnostic []byte
	diagnosticDone := make(chan struct{})
	go func() {
		defer close(diagnosticDone)
		buffer := make([]byte, 512)
		for {
			n, err := s.process.diagnostics.Read(buffer)
			if n > 0 {
				diagnosticMu.Lock()
				keep := min(n, 512-len(diagnostic))
				diagnostic = append(diagnostic, buffer[:keep]...)
				diagnosticMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	scanner := bufio.NewScanner(s.process.output)
	scanner.Buffer(make([]byte, 4096), nativeASRFrameBytes)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			return index + 1, bytes.TrimSuffix(data[:index], []byte{'\r'}), nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil
	})
	var final *ASRUpdate
	var failure error
	for scanner.Scan() {
		data := scanner.Bytes()
		if !utf8.Valid(data) {
			failure = errors.New("asr_stream_invalid")
			break
		}
		var update ASRUpdate
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		var trailing any
		if decoder.Decode(&update) != nil || decoder.Decode(&trailing) != io.EOF {
			failure = errors.New("asr_stream_invalid")
			break
		}
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			break
		}
		err := s.validate(update)
		if final != nil {
			err = errors.New("asr_stream_invalid")
		}
		if err == nil {
			s.lastRevision, s.lastMS, s.modelRevision = update.Revision, update.MonotonicMS, update.ModelRevision
		}
		s.mu.Unlock()
		if err != nil {
			failure = err
			break
		}
		if update.Phase == "final" {
			copy := update
			final = &copy
			continue
		}
		if update.ErrorCode != "" {
			failure = errors.New(update.ErrorCode)
			break
		}
		if err := s.call(update); err != nil {
			failure = err
			break
		}
	}
	if scanner.Err() != nil && failure == nil {
		failure = errors.New("asr_stream_eof")
	}
	if failure != nil {
		s.process.stop()
	}
	waitErr := s.process.wait()
	select {
	case <-diagnosticDone:
	case <-time.After(time.Second):
		_ = s.process.diagnostics.Close()
	}
	if failure == nil && waitErr != nil {
		diagnosticMu.Lock()
		code := strings.TrimSpace(string(diagnostic))
		diagnosticMu.Unlock()
		failure = nativeASRError(s.ctx, errors.New(code), "asr_failed")
	}
	if failure == nil && final == nil {
		failure = nativeASRError(s.ctx, nil, "asr_stream_eof")
	}
	if failure != nil {
		s.fail(failure)
		return
	}
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return
	}
	// A final callback may start Finish immediately．Mark verified end-of-input
	// before that callback so Finish never writes to an already exited helper．
	// Finish still waits for callback acceptance before returning this final．
	s.final, s.finishing = *final, true
	s.mu.Unlock()
	if err := s.call(*final); err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	if !s.terminal {
		s.terminal, s.final = true, *final
		if s.prepareTimer != nil {
			s.prepareTimer.Stop()
		}
		if s.captureTimer != nil {
			s.captureTimer.Stop()
		}
		if s.finishTimer != nil {
			s.finishTimer.Stop()
		}
		s.provider.readiness.invalidate() // Permission may have changed during this session．
		close(s.done)
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *nativeASRSession) fail(err error) {
	err = nativeASRError(s.ctx, err, "asr_failed")
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return
	}
	s.terminal, s.err, s.final = true, err, ASRUpdate{}
	if s.prepareTimer != nil {
		s.prepareTimer.Stop()
	}
	if s.captureTimer != nil {
		s.captureTimer.Stop()
	}
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	phase := "failure"
	if err.Error() == "asr_timeout" {
		phase = "timeout"
	}
	if err.Error() == "asr_canceled" {
		phase = "canceled"
	}
	update := ASRUpdate{OperationID: s.request.OperationID, SessionID: s.request.SessionID, TurnID: s.request.TurnID, SegmentID: s.request.SegmentID,
		Revision: min(s.lastRevision+1, maxASRRevisions), Phase: phase, Provider: "macos-speech", ModelRevision: s.modelRevision,
		MonotonicMS: max(s.lastMS, time.Since(s.started).Milliseconds()), ErrorCode: err.Error()}
	s.provider.readiness.invalidate()
	close(s.done)
	s.mu.Unlock()
	s.process.stop()
	// Failure notification is bounded independently of the now-canceled process．
	go func() {
		select {
		case s.provider.streamCallbackGate <- struct{}{}:
		default:
			return
		}
		defer func() { _ = recover(); <-s.provider.streamCallbackGate }()
		s.callback(update)
	}()
	s.cancel()
}
