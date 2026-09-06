package main

// GenerationMetadata contains terminal facts only. Content and provider errors
// never belong in this versioned DTO or durable trace.
type GenerationMetadata struct {
	SchemaVersion            int    `json:"schema_version"`
	FinishReason             string `json:"finish_reason"`
	ProviderFinishReason     string `json:"provider_finish_reason"`
	OutputBudget             int    `json:"output_budget"`
	TotalTokenBudget         int    `json:"total_token_budget"`
	MaxSegments              int    `json:"max_segments"`
	SoftTargetPercent        int    `json:"soft_target_percent"`
	CompletionTokens         int    `json:"completion_tokens"`
	CompletionTokensObserved bool   `json:"completion_tokens_observed"`
	ReasoningTokens          *int   `json:"reasoning_tokens"`
	ReasoningTokenSource     string `json:"reasoning_token_source"`
	SegmentCount             int    `json:"segment_count"`
	ContinuationCount        int    `json:"continuation_count"`
	FirstRawDeltaAt          string `json:"first_raw_delta_at,omitempty"`
	FirstVisibleContentAt    string `json:"first_visible_content_at,omitempty"`
	TerminalSSEAt            string `json:"terminal_sse_at,omitempty"`
	TerminalSSE              bool   `json:"terminal_sse"`
	DoneReceived             bool   `json:"done_received"`
	Complete                 bool   `json:"complete"`
}

type GenerationLimits struct {
	SegmentTokens     int `json:"segment_tokens"`
	MaxSegments       int `json:"max_segments"`
	MaxTotalTokens    int `json:"max_total_tokens"`
	SoftTargetPercent int `json:"soft_target_percent"`
}

type GenerationProgress struct {
	CommittedText string
	SpeechUnits   []string
	Metadata      GenerationMetadata
}

func normalizedFinishReason(reason string) string {
	switch reason {
	case "stop", "length", "tool_calls", "timeout", "transport_eof", "canceled":
		return reason
	default:
		return "unknown"
	}
}

func cloneGenerationMetadata(g *GenerationMetadata) *GenerationMetadata {
	if g == nil {
		return nil
	}
	copy := *g
	if g.ReasoningTokens != nil {
		n := *g.ReasoningTokens
		copy.ReasoningTokens = &n
	}
	return &copy
}
