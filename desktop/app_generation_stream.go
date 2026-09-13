package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
	"unicode/utf8"
)

// Only fixed，allowlisted failure codes cross the bridge or enter a trace．
type generationStreamError struct {
	code   string
	reason string
}

func (e *generationStreamError) Error() string { return e.code }

func incompleteTransport() error {
	return &generationStreamError{code: "llm_transport_eof", reason: "transport_eof"}
}

func parseGenerationStreamError(data string) error {
	var payload struct {
		Code         string `json:"code"`
		FinishReason string `json:"finish_reason"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return incompleteTransport()
	}
	switch payload.Code {
	case "llm_timeout":
		return &generationStreamError{code: "llm_timeout", reason: "timeout"}
	case "llm_transport_eof":
		return incompleteTransport()
	case "canceled", "llm_canceled":
		return &generationStreamError{code: "llm_canceled", reason: "canceled"}
	default:
		return &generationStreamError{code: "backend_unavailable", reason: "unknown"}
	}
}

func safeGenerationError(ctx context.Context, err error) *generationStreamError {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return &generationStreamError{code: "llm_canceled", reason: "canceled"}
	}
	var networkError net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		return &generationStreamError{code: "llm_timeout", reason: "timeout"}
	}
	var fixed *generationStreamError
	if errors.As(err, &fixed) {
		return fixed
	}
	return &generationStreamError{code: "llm_transport_eof", reason: "transport_eof"}
}

func generationCount(value any) (int, bool) {
	f, ok := value.(float64)
	if !ok || f < 0 || f > 1_000_000_000 || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

func applyGenerationUsage(generation *GenerationMetadata, raw map[string]any) error {
	value, exists := raw["usage"]
	if !exists || value == nil {
		return nil
	}
	usage, ok := value.(map[string]any)
	if !ok {
		return incompleteTransport()
	}
	if value, present := usage["completion_tokens"]; present {
		count, valid := generationCount(value)
		if !valid {
			return incompleteTransport()
		}
		generation.CompletionTokens = count
		generation.CompletionTokensObserved = true
	}
	if value, present := usage["completion_tokens_details"]; present && value != nil {
		details, valid := value.(map[string]any)
		if !valid {
			return incompleteTransport()
		}
		if value, present := details["reasoning_tokens"]; present && value != nil {
			count, valid := generationCount(value)
			if !valid {
				return incompleteTransport()
			}
			generation.ReasoningTokens = &count
			generation.ReasoningTokenSource = "provider"
		}
	}
	return nil
}

func (a *App) consumeGenerationStream(ctx context.Context, payload GatewayChatRequest, requestID string, onToken func(string)) (*ChatResponse, error) {
	recording, _ := ctx.Value(recordingStreamContextKey{}).(*recordingStream)
	emit := func(event ChatStreamEvent) {
		if ctx.Err() != nil {
			return
		}
		if onToken != nil {
			if event.Kind == "delta" && event.Channel == "answer" {
				onToken(event.Delta)
			}
			return
		}
		a.emitChatStreamEvent(event)
	}
	generation := &GenerationMetadata{SchemaVersion: 2, FinishReason: "unknown", ProviderFinishReason: "unknown", SegmentCount: 1, ReasoningTokenSource: "unavailable"}
	if payload.MaxTokens != nil {
		generation.OutputBudget = *payload.MaxTokens
	}
	var answer, thinking strings.Builder
	response := &ChatResponse{Generation: generation, Sources: []SearchItem{}}
	reasoningUsageSeen := false
	err := a.streamGatewayResponseContext(ctx, "/v1/chat/completions", payload, func(eventType, data string) error {
		// Once DONE arrives，no subsequent event may mutate this operation．
		if generation.DoneReceived {
			return incompleteTransport()
		}
		if !utf8.ValidString(data) {
			return incompleteTransport()
		}
		if data == "[DONE]" {
			generation.DoneReceived = true
			if !generation.TerminalSSE {
				return incompleteTransport()
			}
			return nil
		}
		switch eventType {
		case "generation_usage":
			if !generation.TerminalSSE || reasoningUsageSeen {
				return incompleteTransport()
			}
			reasoningUsageSeen = true
			var usage struct {
				ReasoningTokens *int   `json:"reasoning_tokens"`
				Source          string `json:"reasoning_token_source"`
			}
			if json.Unmarshal([]byte(data), &usage) != nil {
				return incompleteTransport()
			}
			if usage.Source != "retokenized" && usage.Source != "unavailable" {
				return incompleteTransport()
			}
			if usage.Source == "retokenized" && (usage.ReasoningTokens == nil || *usage.ReasoningTokens < 0 || *usage.ReasoningTokens > 200000) {
				return incompleteTransport()
			}
			if usage.Source == "unavailable" && usage.ReasoningTokens != nil {
				return incompleteTransport()
			}
			if generation.ReasoningTokenSource != "provider" {
				generation.ReasoningTokens, generation.ReasoningTokenSource = usage.ReasoningTokens, usage.Source
			}
			return nil
		case "error":
			return parseGenerationStreamError(data)
		case "route":
			var route struct {
				Provider        string `json:"provider"`
				Model           string `json:"model"`
				ConfigurationID string `json:"configuration_id"`
			}
			if generation.TerminalSSE || json.Unmarshal([]byte(data), &route) != nil {
				return incompleteTransport()
			}
			reportInteractionModel(ctx, route.Provider, route.Model, route.ConfigurationID)
			return nil
		case "web_search_status":
			var status WebSearchStatus
			if generation.TerminalSSE || json.Unmarshal([]byte(data), &status) != nil {
				return incompleteTransport()
			}
			response.WebSearchStatus = &status
			emit(ChatStreamEvent{RequestID: requestID, Kind: eventType, WebSearchStatus: &status})
			return nil
		case "karte_context_status":
			var status KarteContextStatus
			if generation.TerminalSSE || json.Unmarshal([]byte(data), &status) != nil {
				return incompleteTransport()
			}
			response.KarteContextStatus = &status
			emit(ChatStreamEvent{RequestID: requestID, Kind: eventType, KarteContextStatus: &status})
			return nil
		case "sources":
			var sourcePayload struct {
				Sources []SearchItem `json:"sources"`
			}
			if generation.TerminalSSE || json.Unmarshal([]byte(data), &sourcePayload) != nil {
				return incompleteTransport()
			}
			response.Sources = sourcePayload.Sources
			emit(ChatStreamEvent{RequestID: requestID, Kind: eventType, Sources: response.Sources})
			return nil
		}
		var raw map[string]any
		if json.Unmarshal([]byte(data), &raw) != nil || raw == nil {
			return incompleteTransport()
		}
		choices, ok := raw["choices"].([]any)
		if !ok || len(choices) > 1 {
			return incompleteTransport()
		}
		if generation.TerminalSSE && len(choices) > 0 {
			return incompleteTransport()
		}
		if err := applyGenerationUsage(generation, raw); err != nil {
			return err
		}
		if len(choices) == 0 {
			return nil
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			return incompleteTransport()
		}
		if index, exists := choice["index"]; exists && index != float64(0) {
			return incompleteTransport()
		}
		chunk, err := parseStreamChunk(eventType, data)
		if err != nil {
			return incompleteTransport()
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if (chunk.Thinking != "" || chunk.Answer != "") && generation.FirstRawDeltaAt == "" {
			generation.FirstRawDeltaAt = now
		}
		if chunk.Thinking != "" {
			if thinking.Len()+len(chunk.Thinking) > 2*1024*1024 {
				return incompleteTransport()
			}
			thinking.WriteString(chunk.Thinking)
			emit(ChatStreamEvent{RequestID: requestID, Kind: "delta", Channel: "thinking", Delta: chunk.Thinking})
		}
		if chunk.Answer != "" {
			if answer.Len()+len(chunk.Answer) > 2*1024*1024 {
				return incompleteTransport()
			}
			if generation.FirstVisibleContentAt == "" {
				generation.FirstVisibleContentAt = now
			}
			answer.WriteString(chunk.Answer)
			if err := recording.progress(answer.String(), false, false); err != nil {
				return err
			}
			emit(ChatStreamEvent{RequestID: requestID, Kind: "delta", Channel: "answer", Delta: chunk.Answer})
		}
		if value, exists := choice["finish_reason"]; exists && value != nil {
			reason, valid := value.(string)
			if !valid || reason == "" {
				return incompleteTransport()
			}
			generation.ProviderFinishReason = normalizedFinishReason(reason)
			generation.FinishReason = generation.ProviderFinishReason
			generation.TerminalSSE = true
			generation.TerminalSSEAt = now
		}
		return ctx.Err()
	})
	response.Answer, response.Thinking = answer.String(), thinking.String()
	if err == nil && (!generation.TerminalSSE || !generation.DoneReceived) {
		err = incompleteTransport()
	}
	if err != nil {
		fixed := safeGenerationError(ctx, err)
		generation.FinishReason = fixed.reason
		response.FinishReason = fixed.reason
		emit(ChatStreamEvent{RequestID: requestID, Kind: "error", Error: fixed.code})
		return response, fixed
	}
	generation.Complete = generation.FinishReason == "stop"
	response.FinishReason = generation.FinishReason
	if err := recording.progress(response.Answer, true, generation.Complete); err != nil {
		emit(ChatStreamEvent{RequestID: requestID, Kind: "error", Error: err.Error()})
		return response, err
	}
	// Raw is deliberately metadata-only for a streamed request．
	response.Raw = map[string]any{"stream": true, "generation": cloneGenerationMetadata(generation)}
	emit(ChatStreamEvent{RequestID: requestID, Kind: "done", Thinking: response.Thinking,
		Answer: response.Answer, Sources: response.Sources, FinishReason: response.FinishReason})
	return response, nil
}
