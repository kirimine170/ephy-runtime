package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Messages is bounded prior history．Prompt is appended exactly once for both inputs．
func conversationMessages(request ChatRequest) ([]GatewayMessage, error) {
	if request.generationMessages != nil {
		if len(request.generationMessages) == 0 || len(request.generationMessages) > 32 {
			return nil, fmt.Errorf("invalid generation bounds")
		}
		messages := make([]GatewayMessage, 0, len(request.generationMessages))
		size := 0
		for _, message := range request.generationMessages {
			if message.Role != "user" && message.Role != "assistant" {
				return nil, fmt.Errorf("invalid generation history role")
			}
			size += len(message.Content)
			if size > 64000 || len(message.Content) > 16000 {
				return nil, fmt.Errorf("generation history too large")
			}
			messages = append(messages, message)
		}
		return messages, nil
	}
	if len(request.Messages) > 30 || len(request.Prompt) > 16000 || strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("invalid conversation bounds")
	}
	messages := make([]GatewayMessage, 0, len(request.Messages)+1)
	size := len(request.Prompt)
	for _, message := range request.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return nil, fmt.Errorf("invalid history role")
		}
		size += len(message.Content)
		if size > 64000 || len(message.Content) > 16000 {
			return nil, fmt.Errorf("conversation history too large")
		}
		messages = append(messages, message)
	}
	return append(messages, GatewayMessage{Role: "user", Content: request.Prompt}), nil
}

func (a *App) postJSONContext(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return fmt.Errorf("gateway returned %s", response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(out)
}

func (a *App) interactionEngine() *InteractionEngine {
	a.interactionMu.Lock()
	defer a.interactionMu.Unlock()
	if a.interaction == nil {
		root := a.workspaceRoot
		if root == "" {
			root = detectWorkspaceRoot()
		}
		a.interaction = NewInteractionEngine(NewNativeVoiceASR(root), NewNativeVoiceTTS(), a.chatWithContext, func(event InteractionEvent) {
			if ctx := a.currentContext(); ctx != nil {
				runtime.EventsEmit(ctx, "interaction-event", event)
			}
		}, filepath.Join(root, "data", "runtime", "interaction"))
	}
	return a.interaction
}

func (a *App) StartInteraction(request VoiceTurnRequest) (InteractionSnapshot, error) {
	request.Chat.SessionID = request.SessionID
	request.Chat.SessionMode = "voice"
	request.Chat.Stream = true
	if request.Chat.Mode == "" {
		request.Chat.Mode = "auto"
	}
	request.Chat.ModelID = "route:" + request.Chat.Mode
	request.Chat.ProviderID = "gateway-router"
	config, _ := json.Marshal(struct {
		Mode        string
		Temperature float64
		MaxTokens   int
		Scope       string
	}{request.Chat.Mode, request.Chat.Temperature, request.Chat.MaxTokens, request.Chat.SourceScope})
	digest := sha256.Sum256(config)
	request.Chat.ConfigurationID = hex.EncodeToString(digest[:8])
	return a.interactionEngine().Start(request)
}

// Readiness creates no interaction turn and requests no microphone access．
func (a *App) GetInteractionASRReadiness() VoiceReadiness {
	ctx := a.currentContext()
	if ctx == nil {
		ctx = context.Background()
	}
	engine := a.interactionEngine()
	engine.mu.Lock()
	provider, closed := engine.asr, engine.closed
	engine.mu.Unlock()
	if closed {
		return blockedVoiceReadiness("asr_unavailable")
	}
	return readInteractionASRReadiness(ctx, provider, 5*time.Second)
}

func (a *App) CommitInteraction(operationID string, audioBase64 string) error {
	if len(audioBase64) > 12*1024*1024 {
		return a.interactionEngine().Fail(operationID, "invalid_audio")
	}
	audio, err := base64.StdEncoding.DecodeString(audioBase64)
	if err != nil {
		return a.interactionEngine().Fail(operationID, "invalid_audio")
	}
	return a.interactionEngine().Commit(operationID, audio, "")
}

func (a *App) BeginInteractionASR(operationID string, sampleRate int) (ASRSessionRequest, error) {
	return a.interactionEngine().BeginASR(operationID, sampleRate)
}

// The microphone sends bounded PCM frames directly to its operation session．
// No raw audio is added to a request，trace，evaluation or persistent file．
func (a *App) AppendInteractionAudio(operationID string, sequence int, pcmBase64 string) error {
	if len(pcmBase64) > (maxASRPCMChunkBytes+2)/3*4 {
		return a.interactionEngine().Fail(operationID, "invalid_audio")
	}
	pcm, err := base64.StdEncoding.DecodeString(pcmBase64)
	if err != nil {
		return a.interactionEngine().Fail(operationID, "invalid_audio")
	}
	return a.interactionEngine().AppendASRAudio(operationID, sequence, pcm)
}

func (a *App) EndInteractionASR(operationID string) error {
	return a.interactionEngine().EndASR(operationID)
}
func (a *App) CancelInteraction(operationID string) (InteractionSnapshot, error) {
	return a.interactionEngine().Cancel(operationID)
}
func (a *App) ContinueInteraction(operationID string) (InteractionSnapshot, error) {
	return a.interactionEngine().Continue(operationID)
}
func (a *App) GetInteraction(operationID string) (InteractionSnapshot, error) {
	return a.interactionEngine().Snapshot(operationID)
}
func (a *App) InteractionPlayback(operationID string, sequence int, phase string) error {
	return a.interactionEngine().Playback(operationID, sequence, phase)
}
func (a *App) FailInteraction(operationID string, code string) error {
	switch code {
	case "microphone_unavailable", "microphone_permission_denied", "microphone_failed", "invalid_audio", "playback_failed", "interrupted", "asr_failed", "asr_backpressure", "asr_protocol_error", "asr_timeout", "asr_canceled", "asr_unavailable", "asr_on_device_unavailable", "asr_permission_denied", "asr_permission_restricted", "asr_stream_invalid", "asr_stream_eof", "asr_empty_transcript", "asr_empty_result", "invalid_voice_config":
	default:
		code = "microphone_failed"
	}
	return a.interactionEngine().Fail(operationID, code)
}
func (a *App) GetInteractionTrace(operationID string) ([]InteractionTraceEvent, error) {
	return a.interactionEngine().Trace(operationID)
}
func (a *App) ValidateInteractionTrace(operationID string) (TraceValidation, error) {
	events, err := a.interactionEngine().Trace(operationID)
	if err != nil {
		return TraceValidation{}, err
	}
	return ValidateTrace(events), nil
}
