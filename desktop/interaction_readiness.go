package main

import (
	"context"
	"errors"
	"time"
)

// VoiceReadiness describes availability without provider diagnostics or paths．
// permission_required permits explicit recording but does not claim authorization．
type VoiceReadiness struct {
	State     string `json:"state"`
	CanStart  bool   `json:"can_start"`
	ErrorCode string `json:"error_code,omitempty"`
}

type voiceReadinessProvider interface {
	Readiness(context.Context) (VoiceReadiness, error)
}

func blockedVoiceReadiness(code string) VoiceReadiness {
	switch code {
	case "asr_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_on_device_unavailable", "asr_timeout", "asr_canceled", "invalid_voice_config":
	default:
		code = "asr_unavailable"
	}
	return VoiceReadiness{State: "unavailable", ErrorCode: code}
}

func readInteractionASRReadiness(ctx context.Context, provider VoiceASR, timeout time.Duration) VoiceReadiness {
	if provider == nil {
		return blockedVoiceReadiness("asr_unavailable")
	}
	readiness, err := runInteractionStage(ctx, timeout, func(probeCtx context.Context) (VoiceReadiness, error) {
		if detailed, ok := provider.(voiceReadinessProvider); ok {
			return detailed.Readiness(probeCtx)
		}
		if err := provider.Ready(probeCtx); err != nil {
			return VoiceReadiness{}, err
		}
		return VoiceReadiness{State: "ready", CanStart: true}, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return blockedVoiceReadiness("asr_timeout")
		}
		if errors.Is(err, context.Canceled) {
			return blockedVoiceReadiness("asr_canceled")
		}
		return blockedVoiceReadiness(err.Error())
	}
	if readiness.CanStart && readiness.ErrorCode == "" && (readiness.State == "ready" || readiness.State == "permission_required") {
		return readiness
	}
	return blockedVoiceReadiness(readiness.ErrorCode)
}
