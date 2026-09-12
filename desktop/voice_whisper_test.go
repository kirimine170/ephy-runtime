package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func whisperTestProvider(t *testing.T) (*WhisperVoiceASR, *nativeStreamFixture) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset")
	data := []byte("explicit test fixture")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	p := newWhisperVoiceASR(WhisperASRConfig{Helper: path, HelperSHA256: digest, Model: path, ModelID: "test-model", ModelSHA256: digest, VAD: path, VADSHA256: digest}, nil)
	p.ioTimeout = 200 * time.Millisecond
	p.cancelTimeout = 200 * time.Millisecond
	f := newNativeStreamFixture()
	p.start = func(context.Context, string, []string) (*nativeASRProcess, error) { return f.process, nil }
	t.Cleanup(p.Close)
	return p, f
}
func whisperEmit(f *nativeStreamFixture, frame whisperFrame) {
	frame.Protocol = 1
	data, _ := json.Marshal(frame)
	_, _ = f.output.Write(append(data, '\n'))
}
func whisperReady(t *testing.T, p *WhisperVoiceASR, f *nativeStreamFixture) {
	t.Helper()
	r, err := p.Readiness(context.Background())
	if err != nil || r.State != "loading" || r.CanStart {
		t.Fatal("did not report loading", r, err)
	}
	caps := p.Capabilities()
	go whisperEmit(f, whisperFrame{Type: "ready", SampleRate: 16000, Capabilities: &caps, StepMS: p.config.StepMS, ASRUpdate: ASRUpdate{Provider: "whisper-cpp", ModelRevision: p.modelRevision()}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r, err = p.Readiness(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if r.CanStart {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("warmup never became ready")
}
func whisperUpdate(p *WhisperVoiceASR, r ASRSessionRequest, rev int, phase, text string) ASRUpdate {
	u := nativeStreamUpdate(r, rev, phase, text)
	u.Provider = "whisper-cpp"
	u.ModelRevision = p.modelRevision()
	return u
}

func TestWhisperFinalWaitsForSessionACKAndWorkerRemainsResident(t *testing.T) {
	p, f := whisperTestProvider(t)
	whisperReady(t, p, f)
	ack := make(chan struct{})
	offered := make(chan struct{}, 1)
	go f.receive(func(frame map[string]json.RawMessage) {
		var r ASRSessionRequest
		data, _ := json.Marshal(frame)
		_ = json.Unmarshal(data, &r)
		u := whisperUpdate(p, r, 1, "final", "試験の確定文")
		switch frameKind(frame) {
		case "start":
			whisperEmit(f, whisperFrame{Type: "started", ASRUpdate: u})
		case "finish":
			whisperEmit(f, whisperFrame{Type: "update", ASRUpdate: u})
			offered <- struct{}{}
			<-ack
			whisperEmit(f, whisperFrame{Type: "done", ASRUpdate: u})
		case "cancel":
			u.Phase = "canceled"
			u.Transcript = ""
			u.StablePrefix = ""
			u.ErrorCode = "asr_canceled"
			whisperEmit(f, whisperFrame{Type: "update", ASRUpdate: u})
			whisperEmit(f, whisperFrame{Type: "done", ASRUpdate: u})
		}
	})
	var finals atomic.Int32
	s, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(u ASRUpdate) {
		if u.Phase == "final" {
			finals.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(context.Background(), 1, make([]byte, 320)); err != nil {
		t.Fatal(err)
	}
	done := make(chan ASRUpdate, 1)
	go func() {
		u, e := s.Finish(context.Background())
		if e != nil {
			t.Error(e)
		}
		done <- u
	}()
	<-offered
	if finals.Load() != 0 {
		t.Fatal("unacknowledged final escaped")
	}
	select {
	case <-done:
		t.Fatal("Finish returned before ACK")
	default:
	}
	if _, err = p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {}); err == nil {
		t.Fatal("concurrent session accepted")
	}
	close(ack)
	select {
	case u := <-done:
		if u.Phase != "final" {
			t.Fatal(u)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK not released")
	}
	s.Cancel()
	if finals.Load() != 1 || f.stops.Load() != 0 {
		t.Fatal("final duplication or resident process stopped")
	}
	next := nativeStreamRequest()
	next.SegmentID = "next-segment"
	replacement, err := p.OpenSession(context.Background(), next, func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel()
	if f.stops.Load() != 0 {
		t.Fatal("old cancellation stopped next session")
	}
	replacement.Cancel()
	if f.stops.Load() != 0 {
		t.Fatal("cooperative cancellation restarted model")
	}
}

func TestWhisperRepeatedNoSpeechAndCancelReuseWorker(t *testing.T) {
	p, f := whisperTestProvider(t)
	whisperReady(t, p, f)
	go f.receive(func(frame map[string]json.RawMessage) {
		var r ASRSessionRequest
		data, _ := json.Marshal(frame)
		_ = json.Unmarshal(data, &r)
		u := whisperUpdate(p, r, 1, "no_speech", "")
		switch frameKind(frame) {
		case "start":
			whisperEmit(f, whisperFrame{Type: "started", ASRUpdate: u})
		case "finish", "cancel":
			if frameKind(frame) == "cancel" {
				u.Phase = "canceled"
				u.ErrorCode = "asr_canceled"
			}
			whisperEmit(f, whisperFrame{Type: "update", ASRUpdate: u})
			whisperEmit(f, whisperFrame{Type: "done", ASRUpdate: u})
		}
	})
	for i := 0; i < 30; i++ {
		r := nativeStreamRequest()
		r.SegmentID = interactionID("segment_")
		s, err := p.OpenSession(context.Background(), r, func(u ASRUpdate) {
			if u.Transcript != "" {
				t.Error("no-speech invented text")
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Append(context.Background(), 2, []byte{0, 0}); err == nil {
			t.Fatal("sequence gap accepted")
		}
		if err = s.Append(context.Background(), 1, []byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			u, err := s.Finish(context.Background())
			if err != nil || u.Phase != "no_speech" {
				t.Fatal(u, err)
			}
		}
		s.Cancel()
	}
	if f.stops.Load() != 0 {
		t.Fatal("session completion killed resident worker")
	}
}

func TestWhisperProtocolRejectsUncorrelatedAndInvalidFrames(t *testing.T) {
	for _, mode := range []string{"identity", "model", "revision", "activity", "early-final", "missing-ack"} {
		t.Run(mode, func(t *testing.T) {
			p, f := whisperTestProvider(t)
			whisperReady(t, p, f)
			go f.receive(func(frame map[string]json.RawMessage) {
				u := whisperUpdate(p, nativeStreamRequest(), 1, "partial", "候補")
				if frameKind(frame) == "start" {
					whisperEmit(f, whisperFrame{Type: "started", ASRUpdate: u})
					return
				}
				if frameKind(frame) != "audio" {
					return
				}
				switch mode {
				case "identity":
					u.SegmentID = "foreign"
				case "model":
					u.ModelRevision = "foreign"
				case "revision":
					u.Revision = 2049
				case "activity":
					u.Phase = "activity"
					u.Transcript = ""
					u.Activity = &ASRAudioActivity{AudioMS: 60001}
				case "early-final":
					u.Phase = "final"
					u.StablePrefix = u.Transcript
				case "missing-ack":
					whisperEmit(f, whisperFrame{Type: "done", ASRUpdate: u})
					return
				}
				whisperEmit(f, whisperFrame{Type: "update", ASRUpdate: u})
			})
			s, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Append(context.Background(), 1, []byte{0, 0})
			select {
			case <-s.(*whisperASRSession).done:
			case <-time.After(time.Second):
				t.Fatal("invalid frame not terminated")
			}
			if _, err = s.Finish(context.Background()); err == nil {
				t.Fatal("invalid protocol returned final")
			}
		})
	}
}

func TestWhisperUnresponsiveCancelIsBoundedAndRestartsExplicitly(t *testing.T) {
	p, f := whisperTestProvider(t)
	whisperReady(t, p, f)
	go f.receive(func(frame map[string]json.RawMessage) {
		if frameKind(frame) == "start" {
			whisperEmit(f, whisperFrame{Type: "started", ASRUpdate: whisperUpdate(p, nativeStreamRequest(), 1, "", "")})
		}
	})
	s, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(ASRUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	s.Cancel()
	if time.Since(begin) > time.Second || f.stops.Load() == 0 {
		t.Fatal("cancel did not bound stuck worker")
	}
	p.mu.Lock()
	p.lastStart = time.Now().Add(-2 * time.Second)
	p.mu.Unlock()
	replacement := newNativeStreamFixture()
	p.start = func(context.Context, string, []string) (*nativeASRProcess, error) { return replacement.process, nil }
	r, err := p.Readiness(context.Background())
	if err != nil || r.State != "loading" {
		t.Fatal("worker crash cannot be retried", r, err)
	}
}

func TestWhisperAssetsAndProviderConfigurationFailClosed(t *testing.T) {
	p, _ := whisperTestProvider(t)
	for _, mutate := range []func(*WhisperASRConfig){func(c *WhisperASRConfig) { c.ModelSHA256 = strings.Repeat("0", 64) }, func(c *WhisperASRConfig) { c.Model = "/missing-test-model" }} {
		c := p.config
		mutate(&c)
		q := newWhisperVoiceASR(c, nil)
		defer q.Close()
		_, _ = q.Readiness(context.Background())
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			r, err := q.Readiness(context.Background())
			if err != nil {
				if !strings.HasPrefix(r.ErrorCode, "asr_model_") {
					t.Fatal(r)
				}
				break
			}
			if r.CanStart {
				t.Fatal("unverified asset became ready")
			}
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyWhisperAsset(ctx, p.config.Model, p.config.ModelSHA256, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	t.Setenv("EPHY_ASR_PROVIDER", "unknown")
	if err := NewConfiguredVoiceASR(t.TempDir()).Ready(context.Background()); err == nil {
		t.Fatal("invalid provider fell back silently")
	}
}

func TestWhisperDecoderFailureNotifiesRecordingBeforeFinish(t *testing.T) {
	p, f := whisperTestProvider(t)
	whisperReady(t, p, f)
	go f.receive(func(frame map[string]json.RawMessage) {
		u := whisperUpdate(p, nativeStreamRequest(), 1, "failure", "")
		u.ErrorCode = "asr_empty_result"
		switch frameKind(frame) {
		case "start":
			whisperEmit(f, whisperFrame{Type: "started", ASRUpdate: u})
		case "audio":
			whisperEmit(f, whisperFrame{Type: "update", ASRUpdate: u})
			whisperEmit(f, whisperFrame{Type: "done", ASRUpdate: u})
		}
	})
	events := make(chan ASRUpdate, 1)
	s, err := p.OpenSession(context.Background(), nativeStreamRequest(), func(u ASRUpdate) { events <- u })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(context.Background(), 1, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-events:
		if u.ErrorCode != "asr_empty_result" || u.Phase != "failure" {
			t.Fatal(u)
		}
	case <-time.After(time.Second):
		t.Fatal("failure hidden until endpoint")
	}
	if _, err = s.Finish(context.Background()); err == nil || err.Error() != "asr_empty_result" {
		t.Fatal(err)
	}
}
