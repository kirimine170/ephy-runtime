package main

import "context"

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
	OperationID   string `json:"operation_id"`
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	SegmentID     string `json:"segment_id"`
	Revision      int    `json:"revision"`
	Phase         string `json:"phase"`
	Transcript    string `json:"transcript,omitempty"`
	StablePrefix  string `json:"stable_prefix,omitempty"`
	Provider      string `json:"provider"`
	ModelRevision string `json:"model_revision"`
	MonotonicMS   int64  `json:"monotonic_ms"`
	ErrorCode     string `json:"error_code,omitempty"`
}

type VoiceASRSession interface {
	Append(context.Context, int, []byte) error
	Finish(context.Context) (ASRUpdate, error)
	Cancel()
}

type VoiceStreamingASR interface {
	OpenSession(context.Context, ASRSessionRequest, func(ASRUpdate)) (VoiceASRSession, error)
}

const maxASRPCMChunkBytes = 64 << 10
const maxASRPCMBytes = 8 << 20
const maxASRRevisions = 2048
