package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"desktop/recording"
	"desktop/recording/recallselect"
)

type recordedRecaller interface {
	Recall(context.Context, string, int) ([]recording.RecallSource, error)
}

// recordedRecallChat is a fixed-answer first connection from ordinary chat to
// Karte v2. Its explicit scope never enters the v1 Personal Context path or a
// model. A later model integration must preserve both authorization checks.
func (a *App) recordedRecallChat(ctx context.Context, request ChatRequest, onToken func(string)) (*ChatResponse, error) {
	store := a.recordingStore()
	if store == nil {
		return nil, errors.New("recording_storage_unavailable")
	}
	return fixedRecordedRecall(ctx, store, request, onToken)
}

func fixedRecordedRecall(ctx context.Context, store recordedRecaller, request ChatRequest, onToken func(string)) (*ChatResponse, error) {
	term := recallselect.SearchTerm(request.Prompt)
	if term == "" {
		return nil, errors.New("invalid_recall_query")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	before, err := store.Recall(ctx, term, 5)
	if err != nil {
		return nil, err
	}
	// Re-authorize immediately before answer text is emitted or returned.
	current, err := store.Recall(ctx, term, 5)
	if err != nil || !reflect.DeepEqual(before, current) {
		return nil, errors.New("stale_recall")
	}
	sources := make([]SearchItem, 0, len(current))
	lines := make([]string, 0, len(current))
	for index, source := range current {
		id := fmt.Sprintf("R%d", index+1)
		lines = append(lines, source.Text+" ["+id+"]")
		sources = append(sources, SearchItem{
			ChunkID: fmt.Sprintf("karte-v2:%s:%d:%s", source.Target.DocID, source.Target.Revision, source.Event.EventID),
			DocID:   source.Target.DocID, SourceType: "karte_record_v2", SourceID: id,
			TrustLevel: "local_untrusted", Title: "Recorded conversation",
			ChunkText: source.Text, Snippet: source.Text,
			KarteRecordV2: &KarteRecordV2Citation{
				Target: source.Target, Event: source.Event,
				ConversationID: source.ConversationID, TurnID: source.TurnID,
			},
		})
	}
	answer := "No authorized saved conversation matched."
	if len(lines) > 0 {
		answer = strings.Join(lines, "\n")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if onToken != nil {
		// Match the gateway adapter's interaction side channel. The engine
		// publishes sources separately from its committed answer/speech, so
		// citations must arrive first, after the same output authorization.
		if observer, ok := ctx.Value(interactionChatEventKey{}).(func(ChatStreamEvent)); ok {
			observer(ChatStreamEvent{RequestID: request.RequestID, Kind: "sources", Sources: sources})
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		onToken(answer)
	}
	// This in-process adapter owns a single, fully materialized answer. Its
	// synthetic stop and end-of-stream are both known here; there is no
	// outstanding provider stream. Preserve the assembler's terminal framing
	// contract without weakening its checks for real gateway transports.
	return &ChatResponse{
		Answer: answer, Sources: sources, FinishReason: "stop",
		Generation: &GenerationMetadata{
			SchemaVersion: 2, FinishReason: "stop", ProviderFinishReason: "stop",
			TerminalSSE: true, DoneReceived: true, TerminalSSEAt: time.Now().UTC().Format(time.RFC3339Nano),
			Complete: true, SegmentCount: 1, ReasoningTokenSource: "unavailable",
		},
	}, nil
}

// generateRecordedRecall handles only the explicitly selected local recall
// adapter. Saved event text is already complete data, even when it contains
// unmatched Markdown. Never repair, continue, or interpret it as model output.
func generateRecordedRecall(ctx context.Context, request ChatRequest, prefix string, chat func(context.Context, ChatRequest, func(string)) (*ChatResponse, error), progress func(GenerationProgress)) (*ChatResponse, error) {
	if request.SourceScope != "recorded_conversation" || prefix != "" || chat == nil {
		return nil, errors.New("generation_invalid_request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var observed strings.Builder
	overflow := false
	response, err := chat(ctx, request, func(delta string) {
		if ctx.Err() != nil || overflow {
			return
		}
		if len(delta) > maxGenerationTextBytes-observed.Len() {
			overflow = true
			return
		}
		observed.WriteString(delta)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if response == nil || response.Generation == nil || !response.Generation.Complete || response.FinishReason != "stop" || response.Generation.FinishReason != "stop" {
		return nil, errors.New("generation_incomplete")
	}
	if overflow || len(response.Answer) > maxGenerationTextBytes || !utf8.ValidString(response.Answer) {
		return nil, errors.New("generation_text_limit")
	}
	if response.Answer == "" || response.Answer != observed.String() {
		return nil, errors.New("generation_inconsistent_response")
	}
	// Sources were sent by the adapter before its answer callback. Publish the
	// complete literal answer once through the normal live/revision-fenced
	// interaction progress path; Markdown parsing must not drop saved content.
	if progress != nil {
		progress(GenerationProgress{CommittedText: response.Answer, SpeechUnits: []string{response.Answer}, Metadata: *cloneGenerationMetadata(response.Generation)})
	}
	return response, nil
}
