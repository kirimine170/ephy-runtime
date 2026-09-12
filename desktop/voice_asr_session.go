package main

import (
	"context"
	"math"
)

type ASRCapabilities struct {
	Partial  bool `json:"partial"`
	Activity bool `json:"activity"`
	NoSpeech bool `json:"no_speech"`
}

type voiceASRCapabilityProvider interface{ Capabilities() ASRCapabilities }

func asrCapabilities(provider VoiceASR) ASRCapabilities {
	if p, ok := provider.(voiceASRCapabilityProvider); ok {
		return p.Capabilities()
	}
	_, streaming := provider.(VoiceStreamingASR)
	return ASRCapabilities{Partial: streaming}
}

// The provider's audio sample clock，not inference arrival time．No audio or text．
type ASRAudioActivity struct {
	AudioMS      int     `json:"audio_ms"`
	LastSpeechMS int     `json:"last_speech_ms"`
	SpeechMS     int     `json:"speech_ms"`
	Probability  float64 `json:"probability"`
	Speaking     bool    `json:"speaking"`
	HasSpeech    bool    `json:"has_speech"`
}

func validASRActivity(a *ASRAudioActivity) bool {
	return a != nil && a.AudioMS >= 0 && a.AudioMS <= 60000 && a.LastSpeechMS >= 0 && a.LastSpeechMS <= a.AudioMS &&
		a.SpeechMS >= 0 && a.SpeechMS <= a.AudioMS+32 && !math.IsNaN(a.Probability) && !math.IsInf(a.Probability, 0) && a.Probability >= 0 && a.Probability <= 1
}

// Streaming ASR is an adapter session，independent of Conversation and UI．
// Audio is transient mono signed PCM16 little endian at the declared rate．
// Only a final update can become a conversation message．Stable remains display-only．
type ASRSessionRequest struct {
	OperationID string `json:"operation_id"`
	SessionID   string `json:"session_id"`
	TurnID      string `json:"turn_id"`
	SegmentID   string `json:"segment_id"`
	SampleRate  int    `json:"sample_rate"`
}

type ASRUpdate struct {
	OperationID   string            `json:"operation_id"`
	SessionID     string            `json:"session_id"`
	TurnID        string            `json:"turn_id"`
	SegmentID     string            `json:"segment_id"`
	Revision      int               `json:"revision"`
	Phase         string            `json:"phase"`
	Transcript    string            `json:"transcript,omitempty"`
	StablePrefix  string            `json:"stable_prefix,omitempty"`
	Provider      string            `json:"provider"`
	ModelRevision string            `json:"model_revision"`
	MonotonicMS   int64             `json:"monotonic_ms"`
	ErrorCode     string            `json:"error_code,omitempty"`
	Activity      *ASRAudioActivity `json:"activity,omitempty"`
	Diagnostic    *ASRDiagnostic    `json:"diagnostic,omitempty"`
}

// Fixed categories and numeric codes only．NSError descriptions never cross IPC．
type ASRDiagnostic struct {
	Domain string `json:"domain"`
	Code   int64  `json:"code"`
}

func validASRDiagnostic(d *ASRDiagnostic) bool {
	if d == nil {
		return true
	}
	if d.Code < -2147483648 || d.Code > 2147483647 {
		return false
	}
	switch d.Domain {
	case "speech_assistant", "speech_recognition", "url", "cocoa", "osstatus", "other":
		return true
	}
	return false
}

type VoiceASRSession interface {
	Append(context.Context, int, []byte) error
	Finish(context.Context) (ASRUpdate, error)
	Cancel()
}

type VoiceStreamingASR interface {
	OpenSession(context.Context, ASRSessionRequest, func(ASRUpdate)) (VoiceASRSession, error)
}

// A short speculative input may request a faster partial cadence without
// reducing the ordinary utterance model or changing canonical final handling．
type VoiceInterruptionASR interface {
	OpenInterruptionSession(context.Context, ASRSessionRequest, func(ASRUpdate)) (VoiceASRSession, error)
}

const maxASRPCMChunkBytes = 64 << 10
const maxASRPCMBytes = 8 << 20
const maxASRRevisions = 2048
