package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nativeStreamRequest() ASRSessionRequest {
	return ASRSessionRequest{OperationID: "operation-1", SessionID: "session-1", TurnID: "turn-1", SegmentID: "segment-1", SampleRate: 16000}
}

func nativeStreamUpdate(request ASRSessionRequest, revision int, phase, text string) ASRUpdate {
	stable := ""
	if phase == "final" {
		stable = text
	}
	return ASRUpdate{OperationID: request.OperationID, SessionID: request.SessionID, TurnID: request.TurnID, SegmentID: request.SegmentID,
		Revision: revision, Phase: phase, Transcript: text, StablePrefix: stable, Provider: "macos-speech",
		ModelRevision: "apple-opaque:macos-26.0.0-build-25A123:ja-JP", MonotonicMS: int64(revision)}
}

type nativeStreamFixture struct {
	input      *io.PipeReader
	output     *io.PipeWriter
	diagnostic *io.PipeWriter
	process    *nativeASRProcess
	exit       chan error
	once       sync.Once
	stops      atomic.Int64
}

func newNativeStreamFixture() *nativeStreamFixture {
	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	diagnosticR, diagnosticW := io.Pipe()
	f := &nativeStreamFixture{input: inputR, output: outputW, diagnostic: diagnosticW, exit: make(chan error, 1)}
	f.process = &nativeASRProcess{input: inputW, output: outputR, diagnostics: diagnosticR,
		wait: func() error { return <-f.exit }, stop: func() {
			f.stops.Add(1)
			inputW.Close()
			outputR.Close()
			diagnosticR.Close()
			f.finish(errors.New("private process stopped"))
		}}
	return f
}

func (f *nativeStreamFixture) finish(err error) {
	f.once.Do(func() { f.input.Close(); f.output.Close(); f.diagnostic.Close(); f.exit <- err })
}

func (f *nativeStreamFixture) emit(update ASRUpdate) {
	data, _ := json.Marshal(update)
	_, _ = f.output.Write(append(data, '\n'))
}

func nativeSessionTestProvider(t *testing.T, fixture *nativeStreamFixture) (*NativeVoiceASR, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	checks, starts := &atomic.Int64{}, &atomic.Int64{}
	p := asrTestProvider(t, func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
		checks.Add(1)
		return []byte("ready"), nil, nil
	})
	p.streamStart = func(ctx context.Context, _ string, args []string) (*nativeASRProcess, error) {
		starts.Add(1)
		if strings.Join(args, " ") != "--stream --locale ja-JP" {
			t.Errorf("unexpected streaming args: %v", args)
		}
		return fixture.process, nil
	}
	t.Cleanup(func() { fixture.process.stop() })
	return p, checks, starts
}

func (f *nativeStreamFixture) receive(onFrame func(map[string]json.RawMessage)) {
	scanner := bufio.NewScanner(f.input)
	scanner.Buffer(make([]byte, 4096), nativeASRFrameBytes)
	for scanner.Scan() {
		var frame map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &frame) == nil {
			onFrame(frame)
		}
	}
}

func frameKind(frame map[string]json.RawMessage) string {
	var kind string
	_ = json.Unmarshal(frame["type"], &kind)
	return kind
}

func TestNativeASRSessionEmitsIncrementalBeforeFinishWithOnePreparedProcess(t *testing.T) {
	f := newNativeStreamFixture()
	p, checks, starts := nativeSessionTestProvider(t, f)
	request := nativeStreamRequest()
	var audioFrames atomic.Int64
	go f.receive(func(frame map[string]json.RawMessage) {
		switch frameKind(frame) {
		case "start":
			for key, want := range map[string]string{"operation_id": request.OperationID, "session_id": request.SessionID, "turn_id": request.TurnID, "segment_id": request.SegmentID} {
				var value string
				_ = json.Unmarshal(frame[key], &value)
				if value != want {
					t.Errorf("missing correlated %s", key)
				}
			}
		case "audio":
			var encoded string
			_ = json.Unmarshal(frame["pcm_base64"], &encoded)
			pcm, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(pcm) != 320 {
				t.Error("PCM frame changed")
			}
			revision := int(audioFrames.Add(1))
			f.emit(nativeStreamUpdate(request, revision, "partial", strings.Repeat("春", revision)))
		case "finish":
			f.emit(nativeStreamUpdate(request, 3, "final", "春夏秋冬。"))
			f.finish(nil)
		}
	})
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates := make(chan ASRUpdate, 8)
	session, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) { updates <- update })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	for sequence := 1; sequence <= 2; sequence++ {
		if err := session.Append(context.Background(), sequence, make([]byte, 320)); err != nil {
			t.Fatal(err)
		}
		select {
		case update := <-updates:
			if update.Phase != "partial" || update.Revision != sequence || update.StablePrefix != "" {
				t.Fatalf("bad incremental update: %#v", update)
			}
		case <-time.After(time.Second):
			t.Fatal("partial was held until endpoint")
		}
	}
	final, err := session.Finish(context.Background())
	if err != nil || final.Phase != "final" || final.Transcript != "春夏秋冬。" || final.StablePrefix != final.Transcript {
		t.Fatalf("missing final: %#v %v", final, err)
	}
	if second, err := session.Finish(context.Background()); err != nil || second != final {
		t.Fatal("Finish is not idempotent")
	}
	if err := session.Append(context.Background(), 3, []byte{0, 0}); err != nil {
		t.Fatal("confirmed early final rejected further append")
	}
	if checks.Load() != 1 || starts.Load() != 1 || audioFrames.Load() != 2 {
		t.Fatalf("preparation/process duplication: checks=%d starts=%d frames=%d", checks.Load(), starts.Load(), audioFrames.Load())
	}
}

func TestNativeASRSessionAcceptsNaturalFinalBeforeEndpoint(t *testing.T) {
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	request := nativeStreamRequest()
	go f.receive(func(frame map[string]json.RawMessage) {
		if frameKind(frame) == "audio" {
			f.emit(nativeStreamUpdate(request, 1, "final", "自然な終了。"))
			f.finish(nil)
		}
	})
	finalSeen := make(chan struct{})
	session, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) {
		if update.Phase == "final" {
			close(finalSeen)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	if err := session.Append(context.Background(), 1, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finalSeen:
	case <-time.After(time.Second):
		t.Fatal("natural final not delivered while recording")
	}
	if err := session.Append(context.Background(), 2, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	if final, err := session.Finish(context.Background()); err != nil || final.Transcript != "自然な終了。" {
		t.Fatalf("%#v %v", final, err)
	}
}

func TestNativeASRSessionValidatesCorrelationsRevisionPhaseMetadataAndUTF8(t *testing.T) {
	request := nativeStreamRequest()
	s := &nativeASRSession{request: request, provider: &NativeVoiceASR{locale: "ja-JP"}, lastRevision: 1, lastMS: 1, modelRevision: "apple-opaque:macos-26.0.0-build-25A123:ja-JP"}
	for name, mutate := range map[string]func(*ASRUpdate){
		"operation": func(u *ASRUpdate) { u.OperationID = "other" }, "session": func(u *ASRUpdate) { u.SessionID = "other" },
		"turn": func(u *ASRUpdate) { u.TurnID = "other" }, "segment": func(u *ASRUpdate) { u.SegmentID = "other" },
		"revision": func(u *ASRUpdate) { u.Revision = 1 }, "revision max": func(u *ASRUpdate) { u.Revision = maxASRRevisions + 1 },
		"monotonic": func(u *ASRUpdate) { u.MonotonicMS = 0 }, "provider": func(u *ASRUpdate) { u.Provider = "private provider" },
		"model": func(u *ASRUpdate) { u.ModelRevision = "private model metadata" }, "locale": func(u *ASRUpdate) { u.ModelRevision = "apple-opaque:macos-26.0.0-build-25A123:en-US" },
		"model bound": func(u *ASRUpdate) {
			u.ModelRevision = "apple-opaque:macos-26.0.0-build-" + strings.Repeat("A", 128) + ":ja-JP"
		},
		"phase": func(u *ASRUpdate) { u.Phase = "guessed" }, "UTF8": func(u *ASRUpdate) { u.Transcript = "\xff" },
		"text bound": func(u *ASRUpdate) { u.Transcript = strings.Repeat("あ", 6000) }, "unstable prefix": func(u *ASRUpdate) { u.StablePrefix = u.Transcript },
		"error body":      func(u *ASRUpdate) { u.ErrorCode = "PRIVATE diagnostic" },
		"timeout reason":  func(u *ASRUpdate) { u.Phase, u.Transcript, u.ErrorCode = "timeout", "", "asr_failed" },
		"canceled reason": func(u *ASRUpdate) { u.Phase, u.Transcript, u.ErrorCode = "failure", "", "asr_canceled" },
	} {
		t.Run(name, func(t *testing.T) {
			update := nativeStreamUpdate(request, 2, "partial", "春")
			mutate(&update)
			if err := s.validate(update); err == nil || err.Error() != "asr_stream_invalid" {
				t.Fatalf("invalid update accepted: %v", err)
			}
		})
	}
	stable := nativeStreamUpdate(request, 2, "stable", "確定部分と続き")
	stable.StablePrefix = "確定部分"
	if err := s.validate(stable); err != nil {
		t.Fatal("generic stable contract rejected")
	}
}

func TestNativeASRSessionRejectsMalformedMissingAndFailedFinal(t *testing.T) {
	request := nativeStreamRequest()
	for _, mode := range []string{"missing", "malformed", "UTF8", "truncated final", "failed after final", "late after final", "oversized frame", "private failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeStreamFixture()
			p, _, _ := nativeSessionTestProvider(t, f)
			var finalCalls atomic.Int64
			go f.receive(func(frame map[string]json.RawMessage) {
				if frameKind(frame) != "finish" {
					return
				}
				var failure error
				switch mode {
				case "malformed":
					_, _ = f.output.Write([]byte("{private malformed}\n"))
				case "UTF8":
					_, _ = f.output.Write([]byte("\xff\n"))
				case "truncated final":
					data, _ := json.Marshal(nativeStreamUpdate(request, 1, "final", "完全な文。"))
					_, _ = f.output.Write(data)
				case "failed after final":
					f.emit(nativeStreamUpdate(request, 1, "final", "完全な文。"))
					failure = errors.New("private failure")
				case "late after final":
					f.emit(nativeStreamUpdate(request, 1, "final", "完全な文。"))
					f.emit(nativeStreamUpdate(request, 2, "partial", "private late"))
				case "oversized frame":
					_, _ = f.output.Write([]byte(strings.Repeat("x", nativeASRFrameBytes+1)))
				case "private failure":
					_, _ = f.diagnostic.Write([]byte("PRIVATE transcript/audio diagnostic"))
					failure = errors.New("private process failure")
				}
				f.finish(failure)
			})
			session, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) {
				if update.Phase == "final" {
					finalCalls.Add(1)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel()
			final, err := session.Finish(context.Background())
			if err == nil || strings.Contains(strings.ToLower(err.Error()), "private") || final.Transcript != "" || finalCalls.Load() != 0 {
				t.Fatalf("failed final published: %#v %v calls=%d", final, err, finalCalls.Load())
			}
		})
	}
}

func TestNativeASRSessionChunkSequenceAndDurationBounds(t *testing.T) {
	for _, sequence := range []int{-1, 0, 2} {
		t.Run("sequence "+strconv.Itoa(sequence), func(t *testing.T) {
			f := newNativeStreamFixture()
			p, _, _ := nativeSessionTestProvider(t, f)
			go f.receive(func(map[string]json.RawMessage) {})
			session, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel()
			if err := session.Append(context.Background(), sequence, []byte{0, 0}); err == nil || err.Error() != "asr_stream_invalid" {
				t.Fatal("out-of-order audio was accepted")
			}
		})
	}
	for name, pcm := range map[string][]byte{"empty": nil, "odd": {0}, "large": make([]byte, maxASRPCMChunkBytes+2)} {
		t.Run(name, func(t *testing.T) {
			f := newNativeStreamFixture()
			p, _, _ := nativeSessionTestProvider(t, f)
			go f.receive(func(map[string]json.RawMessage) {})
			session, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel()
			if err := session.Append(context.Background(), 1, pcm); err == nil {
				t.Fatal("invalid PCM accepted")
			}
		})
	}
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	go f.receive(func(map[string]json.RawMessage) {})
	request := nativeStreamRequest()
	request.SampleRate = 8000
	session, err := p.OpenSession(context.Background(), request, func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	pcm := make([]byte, 8000*2)
	for index := 1; index <= 60; index++ {
		if err := session.Append(context.Background(), index, pcm); err != nil {
			t.Fatalf("valid duration rejected at %d: %v", index, err)
		}
	}
	if err := session.Append(context.Background(), 61, []byte{0, 0}); err == nil {
		t.Fatal("capture exceeded 60 seconds")
	}
}

func TestNativeASRSessionFinalCallbackCanEndpointImmediately(t *testing.T) {
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	request := nativeStreamRequest()
	go f.receive(func(frame map[string]json.RawMessage) {
		if frameKind(frame) == "audio" {
			f.emit(nativeStreamUpdate(request, 1, "final", "確定した回答。"))
			f.finish(nil)
		}
	})
	var session VoiceASRSession
	var err error
	finishResult := make(chan error, 1)
	session, err = p.OpenSession(context.Background(), request, func(update ASRUpdate) {
		if update.Phase != "final" {
			return
		}
		// Simulate the engine scheduling endpoint from the natural final callback．
		if err := session.Append(context.Background(), 2, []byte{0, 0}); err != nil {
			finishResult <- err
			return
		}
		started := make(chan struct{})
		go func() {
			close(started)
			final, err := session.Finish(context.Background())
			if err == nil && final.Phase != "final" {
				err = errors.New("missing verified final")
			}
			finishResult <- err
		}()
		<-started
		// Give the concurrent endpoint enough time to reach its input check．
		time.Sleep(20 * time.Millisecond)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	if err := session.Append(context.Background(), 1, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finishResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("natural endpoint blocked")
	}
}

func TestNativeASRSessionStartValidationAndFailureClassification(t *testing.T) {
	for _, mutation := range []func(*ASRSessionRequest){
		func(r *ASRSessionRequest) { r.OperationID = "" },
		func(r *ASRSessionRequest) { r.SessionID = "PRIVATE session" },
		func(r *ASRSessionRequest) { r.TurnID = strings.Repeat("x", 129) },
		func(r *ASRSessionRequest) { r.SegmentID = "../private" },
		func(r *ASRSessionRequest) { r.SampleRate = 7999 },
		func(r *ASRSessionRequest) { r.SampleRate = 48001 },
	} {
		p := &NativeVoiceASR{}
		request := nativeStreamRequest()
		mutation(&request)
		if _, err := p.OpenSession(context.Background(), request, func(ASRUpdate) {}); err == nil || err.Error() != "asr_stream_invalid" {
			t.Fatal("invalid start reached readiness/process")
		}
	}
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	p.streamStart = func(context.Context, string, []string) (*nativeASRProcess, error) {
		return nil, errors.New("PRIVATE executable failure")
	}
	if _, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {}); err == nil || err.Error() != "asr_failed" {
		t.Fatalf("launch failure mistaken for cancellation: %v", err)
	}
}

func TestNativeASRSessionBlockedInputIsBounded(t *testing.T) {
	for _, duringStart := range []bool{true, false} {
		f := newNativeStreamFixture()
		p, _, _ := nativeSessionTestProvider(t, f)
		p.streamLimits.write = 25 * time.Millisecond
		if !duringStart {
			go func() { scanner := bufio.NewScanner(f.input); _ = scanner.Scan() }()
		}
		started := time.Now()
		session, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
		if !duringStart {
			if err != nil {
				t.Fatal(err)
			}
			err = session.Append(context.Background(), 1, make([]byte, 320))
			session.Cancel()
		}
		if err == nil || err.Error() != "asr_timeout" || time.Since(started) > time.Second {
			t.Fatalf("blocked write not bounded: %v", err)
		}
	}
}

func TestNativeASRSessionTerminalInvalidatesReadiness(t *testing.T) {
	f := newNativeStreamFixture()
	p, checks, _ := nativeSessionTestProvider(t, f)
	go f.receive(func(map[string]json.RawMessage) {})
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 1 {
		t.Fatal("startup repeated readiness process")
	}
	session.Cancel()
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 2 {
		t.Fatal("terminal session retained stale permission readiness")
	}
}

func TestNativeASRSessionCaptureTimerStartsWithFirstPCMAndRemainsBounded(t *testing.T) {
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	p.streamLimits.capture = 25 * time.Millisecond
	p.streamLimits.prepare = 90 * time.Millisecond
	p.streamLimits.write = 20 * time.Millisecond
	p.streamLimits.finalize = 400 * time.Millisecond
	request := nativeStreamRequest()
	finished := make(chan time.Time, 1)
	go f.receive(func(frame map[string]json.RawMessage) {
		if frameKind(frame) == "finish" {
			finished <- time.Now()
			f.emit(nativeStreamUpdate(request, 1, "final", "確定。"))
			f.finish(nil)
		}
	})
	session, err := p.OpenSession(context.Background(), request, func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	// Simulate microphone permission/UI preparation longer than capture+drain．
	select {
	case <-finished:
		t.Fatal("idle preparation consumed capture time")
	case <-time.After(75 * time.Millisecond):
	}
	firstPCM := time.Now()
	if err := session.Append(context.Background(), 1, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	select {
	case endpoint := <-finished:
		if elapsed := endpoint.Sub(firstPCM); elapsed < 40*time.Millisecond || elapsed > 250*time.Millisecond {
			t.Fatalf("capture endpoint did not retain bounded drain margin: %v", elapsed)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("automatic endpoint is unbounded")
	}
	if final, err := session.Finish(context.Background()); err != nil || final.Phase != "final" {
		t.Fatalf("automatic finalization failed: %v", err)
	}
}

func TestNativeASRSessionWithoutPCMHasFiniteWholeSessionDeadline(t *testing.T) {
	f := newNativeStreamFixture()
	p, _, _ := nativeSessionTestProvider(t, f)
	p.streamLimits = nativeASRStreamLimits{prepare: 30 * time.Millisecond, capture: 25 * time.Millisecond, write: 20 * time.Millisecond, finalize: 30 * time.Millisecond}
	go f.receive(func(map[string]json.RawMessage) {})
	session, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	select {
	case <-session.(*nativeASRSession).done:
	case <-time.After(time.Second):
		t.Fatal("idle session deadline is unbounded")
	}
	if final, err := session.Finish(context.Background()); err == nil || err.Error() != "asr_timeout" || final.Transcript != "" {
		t.Fatalf("idle session did not time out safely: %v", err)
	}
}

func TestNativeASRSessionCancelTimeoutAndCallbackFailureAreBounded(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "callback panic", "callback blocked"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeStreamFixture()
			p, _, _ := nativeSessionTestProvider(t, f)
			p.streamLimits.finalize = 30 * time.Millisecond
			p.streamLimits.callback = 30 * time.Millisecond
			request := nativeStreamRequest()
			callbackEntered := make(chan struct{})
			release := make(chan struct{})
			allowCallbackFailure := make(chan struct{})
			go f.receive(func(frame map[string]json.RawMessage) {
				if frameKind(frame) == "audio" && strings.HasPrefix(mode, "callback") {
					f.emit(nativeStreamUpdate(request, 1, "partial", "春"))
				}
			})
			session, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) {
				if update.Phase != "partial" {
					return
				}
				close(callbackEntered)
				<-allowCallbackFailure
				if mode == "callback panic" {
					panic("PRIVATE callback diagnostic")
				}
				if mode == "callback blocked" {
					<-release
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel()
			start := time.Now()
			if mode == "cancel" {
				session.Cancel()
			}
			if strings.HasPrefix(mode, "callback") {
				if err := session.Append(context.Background(), 1, make([]byte, 320)); err != nil {
					t.Fatal(err)
				}
				<-callbackEntered
				close(allowCallbackFailure)
			}
			_, err = session.Finish(context.Background())
			if mode == "callback blocked" {
				close(release)
			}
			if err == nil || strings.Contains(err.Error(), "PRIVATE") || time.Since(start) > time.Second {
				t.Fatalf("unbounded/private failure: %v", err)
			}
			if f.stops.Load() == 0 {
				t.Fatal("helper process was not stopped")
			}
		})
	}
}

func TestNativeASRSessionCallbackFailureDoesNotBlockNextSession(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "panic", "failure blocked", "final blocked"} {
		t.Run(mode, func(t *testing.T) {
			old := newNativeStreamFixture()
			p, _, _ := nativeSessionTestProvider(t, old)
			p.streamLimits.callback = 50 * time.Millisecond
			entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(release)
			request := nativeStreamRequest()
			go old.receive(func(frame map[string]json.RawMessage) {
				if frameKind(frame) != "audio" {
					return
				}
				update := nativeStreamUpdate(request, 1, "partial", "old synthetic")
				if mode == "failure blocked" {
					update.Phase, update.Transcript, update.ErrorCode = "failure", "", "asr_failed"
				}
				if mode == "final blocked" {
					update = nativeStreamUpdate(request, 1, "final", "old synthetic")
				}
				old.emit(update)
				if mode == "final blocked" {
					old.finish(nil)
				}
			})
			var calls atomic.Int32
			session, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) {
				if mode == "panic" && update.Phase == "failure" {
					return
				}
				if calls.Add(1) != 1 {
					return
				}
				close(entered)
				defer close(exited)
				if mode == "panic" {
					panic("PRIVATE synthetic callback")
				}
				<-release
			})
			if err != nil {
				t.Fatal(err)
			}
			// Append may legitimately observe an already sealed callback failure．
			appendErr := session.Append(context.Background(), 1, []byte{0, 0})
			if appendErr != nil && appendErr.Error() != "asr_failed" {
				t.Fatal(appendErr)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("old callback not entered")
			}
			if mode == "cancel" {
				session.Cancel()
			}
			select {
			case <-session.(*nativeASRSession).done:
			case <-time.After(time.Second):
				t.Fatal("old session not bounded")
			}
			_, err = session.Finish(context.Background())
			want := "asr_timeout"
			if mode == "cancel" {
				want = "asr_canceled"
			}
			if mode == "panic" || mode == "failure blocked" {
				want = "asr_failed"
			}
			if err == nil || err.Error() != want {
				t.Fatalf("terminal reason: %v，want %s", err, want)
			}
			next := newNativeStreamFixture()
			defer next.process.stop()
			p.streamStart = func(context.Context, string, []string) (*nativeASRProcess, error) { return next.process, nil }
			request.OperationID, request.TurnID, request.SegmentID = "operation-2", "turn-2", "segment-2"
			go next.receive(func(frame map[string]json.RawMessage) {
				if frameKind(frame) == "finish" {
					next.emit(nativeStreamUpdate(request, 1, "final", "new synthetic"))
					next.finish(nil)
				}
			})
			var finalCalls atomic.Int32
			healthy, err := p.OpenSession(context.Background(), request, func(update ASRUpdate) {
				if update.Phase == "final" {
					finalCalls.Add(1)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer healthy.Cancel()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			final, err := healthy.Finish(ctx)
			if err != nil || final.Transcript != "new synthetic" || finalCalls.Load() != 1 {
				t.Fatalf("old callback blocked next session: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatal("callbacks overlapped within the old session")
			}
			if mode != "panic" {
				select {
				case <-exited:
					t.Fatal("old callback unexpectedly released")
				default:
				}
			}
		})
	}
}

func TestAppleDiagnosticIsFixedAndFinalRemainsSemanticallyEqual(t *testing.T) {
	r := nativeStreamRequest()
	s := &nativeASRSession{request: r, provider: &NativeVoiceASR{locale: "ja-JP"}, lastMS: -1}
	u := nativeStreamUpdate(r, 1, "final", "確定結果")
	u.Diagnostic = &ASRDiagnostic{Domain: "speech_assistant", Code: 1101}
	if err := s.validate(u); err != nil {
		t.Fatal(err)
	}
	b := u
	d := *u.Diagnostic
	b.Diagnostic = &d
	if !sameASRFinal(u, b) {
		t.Fatal("diagnostic pointer identity invalidated the same final")
	}
	b.Diagnostic.Code = 1
	if sameASRFinal(u, b) {
		t.Fatal("changed diagnostic ignored")
	}
	for _, diagnostic := range []*ASRDiagnostic{{Domain: "private path", Code: 1101}, {Domain: "other", Code: 1 << 40}} {
		u.Diagnostic = diagnostic
		if err := s.validate(u); err == nil {
			t.Fatal("unbounded diagnostic accepted")
		}
	}
}
