package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testDetailedVoiceASR struct {
	testVoiceASR
	readiness func(context.Context) (VoiceReadiness, error)
}

func (p testDetailedVoiceASR) Readiness(ctx context.Context) (VoiceReadiness, error) {
	return p.readiness(ctx)
}

func TestInteractionASRReadinessDistinguishesPermissionWithoutCreatingTurn(t *testing.T) {
	for _, state := range []string{"ready", "permission_required"} {
		t.Run(state, func(t *testing.T) {
			var checks, transcriptions atomic.Int32
			provider := testDetailedVoiceASR{
				testVoiceASR: testVoiceASR{
					ready: func(context.Context) error { return errors.New("legacy readiness must not override detailed state") },
					transcribe: func(context.Context, []byte) (string, error) {
						transcriptions.Add(1)
						return "private transcript", nil
					},
				},
				readiness: func(ctx context.Context) (VoiceReadiness, error) {
					checks.Add(1)
					if _, ok := ctx.Deadline(); !ok {
						return VoiceReadiness{}, errors.New("missing readiness deadline")
					}
					return VoiceReadiness{State: state, CanStart: true}, nil
				},
			}
			engine := NewInteractionEngine(provider, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
			defer engine.Close()
			app := &App{interaction: engine}
			actual := app.GetInteractionASRReadiness()
			if actual != (VoiceReadiness{State: state, CanStart: true}) || checks.Load() != 1 || transcriptions.Load() != 0 {
				t.Fatal(actual, checks.Load(), transcriptions.Load())
			}
			engine.mu.Lock()
			turns, active := len(engine.turns), engine.active
			engine.mu.Unlock()
			if turns != 0 || active != "" {
				t.Fatal("readiness created an interaction turn")
			}
		})
	}
}

func TestInteractionASRReadinessSupportsLegacyAdapter(t *testing.T) {
	for _, code := range []string{"", "asr_permission_denied", "asr_unavailable"} {
		provider := testVoiceASR{ready: func(context.Context) error {
			if code == "" {
				return nil
			}
			return errors.New(code)
		}}
		actual := readInteractionASRReadiness(context.Background(), provider, time.Second)
		if code == "" {
			if !actual.CanStart || actual.State != "ready" {
				t.Fatal(actual)
			}
		} else if actual.CanStart || actual.ErrorCode != code {
			t.Fatal(actual)
		}
	}
}

func TestInteractionASRReadinessRejectsMalformedProviderStateAndDiagnostics(t *testing.T) {
	for _, probe := range []struct {
		value VoiceReadiness
		err   error
		code  string
	}{
		{VoiceReadiness{State: "unavailable", ErrorCode: "asr_permission_restricted"}, nil, "asr_permission_restricted"},
		{VoiceReadiness{State: "unavailable", ErrorCode: "asr_on_device_unavailable"}, nil, "asr_on_device_unavailable"},
		{VoiceReadiness{State: "ready", CanStart: true, ErrorCode: "private path /users/private"}, nil, "asr_unavailable"},
		{VoiceReadiness{State: "private provider detail", CanStart: true}, nil, "asr_unavailable"},
		{VoiceReadiness{State: "permission_required", CanStart: false}, nil, "asr_unavailable"},
		{VoiceReadiness{}, errors.New("private provider diagnostic"), "asr_unavailable"},
		{VoiceReadiness{}, context.DeadlineExceeded, "asr_timeout"},
		{VoiceReadiness{}, context.Canceled, "asr_canceled"},
	} {
		provider := testDetailedVoiceASR{readiness: func(context.Context) (VoiceReadiness, error) {
			return probe.value, probe.err
		}}
		actual := readInteractionASRReadiness(context.Background(), provider, time.Second)
		if actual.State != "unavailable" || actual.CanStart || actual.ErrorCode != probe.code {
			t.Fatal(actual)
		}
		data, _ := json.Marshal(actual)
		if strings.Contains(string(data), "private") {
			t.Fatal("provider diagnostic escaped readiness DTO")
		}
	}
}

func TestInteractionASRReadinessBoundsIgnoringProviderAndRejectsLateReady(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	provider := testDetailedVoiceASR{readiness: func(context.Context) (VoiceReadiness, error) {
		<-release
		return VoiceReadiness{State: "ready", CanStart: true}, nil
	}}
	actual := readInteractionASRReadiness(context.Background(), provider, 10*time.Millisecond)
	if actual.CanStart || actual.ErrorCode != "asr_timeout" {
		t.Fatal(actual)
	}
}

func TestInteractionASRReadinessInheritsAppCancellationAndClosedEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, t.TempDir())
	app := &App{interaction: engine, ctx: ctx}
	if actual := app.GetInteractionASRReadiness(); actual.CanStart || actual.ErrorCode != "asr_canceled" {
		t.Fatal(actual)
	}
	engine.Close()
	if actual := app.GetInteractionASRReadiness(); actual.CanStart || actual.ErrorCode != "asr_unavailable" {
		t.Fatal(actual)
	}
	if actual := readInteractionASRReadiness(context.Background(), nil, time.Second); actual.CanStart || actual.ErrorCode != "asr_unavailable" {
		t.Fatal(actual)
	}
}
