package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func assemblyResponse(answer, reason string) *ChatResponse {
	terminal := reason == "stop" || reason == "length" || reason == "tool_calls"
	return &ChatResponse{Answer: answer, FinishReason: reason, Thinking: "private reasoning must not escape", Raw: map[string]string{"private": "body"}, Generation: &GenerationMetadata{SchemaVersion: 2, FinishReason: reason, ProviderFinishReason: reason, TerminalSSE: terminal, DoneReceived: terminal, Complete: reason == "stop", TerminalSSEAt: time.Now().UTC().Format(time.RFC3339Nano)}}
}

func TestGenerationConservativeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text, safe string
		final, balanced  bool
	}{
		{"word fragment", "unfinished", "", false, true},
		{"Japanese prefix", "完成。未完語", "完成。", false, true},
		{"English prefix", "Done. unfinished", "Done.", false, true},
		{"decimal", "3.141", "", false, true},
		{"open emphasis", "**未完。", "", false, false},
		{"closed emphasis", "**完成。** 次", "**完成。**", false, true},
		{"heading", "# Heading\npartial", "# Heading\n", false, true},
		{"list line", "1. complete item\npartial", "1. complete item\n", false, true},
		{"parenthesized list line", "1) complete item\npartial", "1) complete item\n", false, true},
		{"empty bullet", "- ", "", true, false},
		{"empty numbered item", "説明。\n1. ", "説明。\n", true, false},
		{"empty numbered line", "説明。\n1. \n", "説明。\n", false, false},
		{"open fence", "```go\ncall()\n", "", false, false},
		{"closed fence", "```go\ncall()\n```\npartial", "```go\ncall()\n```\n", false, true},
		{"short closing fence", "````go\ncall()\n```\n", "", false, false},
		{"inline code", "`unfinished", "", false, false},
		{"link", "[label](https://example.test", "", false, false},
		{"quote", "「未完。", "", false, false},
		{"short stop", "Hello", "Hello", true, true},
		{"identifier", "foo_bar=1", "foo_bar=1", true, true},
		{"multiplication", "2 * 3", "2 * 3", true, true},
		{"pending marker", "Hello **", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boundaries, balanced := generationBoundaries(tc.text, tc.final)
			end := 0
			if len(boundaries) > 0 {
				end = boundaries[len(boundaries)-1]
			}
			if tc.text[:end] != tc.safe || balanced != tc.balanced {
				t.Fatalf("safe=%q balanced=%v", tc.text[:end], balanced)
			}
		})
	}
}

func TestGenerationSpeechUnitsContainOnlyClosedProse(t *testing.T) {
	text := "# Heading\n**Complete sentence.**\n```go\nsecretCode()\n```\n[Useful label](https://example.test)"
	var progress []GenerationProgress
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		for _, r := range text {
			emit(string(r))
		}
		return assemblyResponse(text, "stop"), nil
	}, func(p GenerationProgress) { progress = append(progress, p) })
	if err != nil || response.Answer != text || !response.Generation.Complete {
		t.Fatal(response, err)
	}
	var speech []string
	for _, p := range progress {
		speech = append(speech, p.SpeechUnits...)
	}
	spoken := strings.Join(speech, " ")
	for _, forbidden := range []string{"#", "**", "```", "secretCode", "https:"} {
		if strings.Contains(spoken, forbidden) {
			t.Fatalf("speech contains %q", forbidden)
		}
	}
	for _, wanted := range []string{"Heading", "Complete sentence.", "Useful label"} {
		if !strings.Contains(spoken, wanted) {
			t.Fatalf("speech missed %q", wanted)
		}
	}
	if response.Thinking != "" || response.Raw != nil {
		t.Fatal("assembler returned private reasoning/raw response")
	}
}

func TestGenerationLengthRollsBackAndContinuesSameConversation(t *testing.T) {
	history := []GatewayMessage{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier answer"}}
	req := ChatRequest{Prompt: "original request", Messages: history, Mode: "work", SessionID: "same-session", SessionMode: "voice", MaxTokens: 512, ModelID: "reasoner", ProviderID: "local", ConfigurationID: "fixed"}
	var requests []ChatRequest
	var spoken []string
	response, err := assembleGeneration(context.Background(), req, GenerationLimits{}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
		requests = append(requests, request)
		text, reason := "Alpha. unfinished fragment", "length"
		if len(requests) == 2 {
			text, reason = "Alpha. Beta.", "stop"
		}
		for _, r := range text {
			emit(string(r))
		}
		return assemblyResponse(text, reason), nil
	}, func(p GenerationProgress) { spoken = append(spoken, p.SpeechUnits...) })
	if err != nil || response.Answer != "Alpha. Beta." || !response.Generation.Complete || len(requests) != 2 {
		t.Fatal(response, err, len(requests))
	}
	if response.Generation.SegmentCount != 2 || response.Generation.ContinuationCount != 1 || response.Generation.OutputBudget != 512 || response.Generation.TotalTokenBudget != 1536 {
		t.Fatal(response.Generation)
	}
	if response.Generation.CompletionTokensObserved || response.Generation.CompletionTokens != 0 {
		t.Fatal("unknown usage claimed as measured")
	}
	for _, r := range requests {
		if r.Mode != req.Mode || r.ModelID != req.ModelID || r.SessionID != req.SessionID || r.ConfigurationID != req.ConfigurationID || r.MaxTokens != 512 || !strings.Contains(r.generationInstruction, "384") {
			t.Fatal("lost route/session/budget", r)
		}
		if r.generationRoutingMessageCount != len(history)+1 {
			t.Fatal("continuation leaked into routing")
		}
	}
	want := append(append([]GatewayMessage(nil), history...), GatewayMessage{Role: "user", Content: req.Prompt}, GatewayMessage{Role: "assistant", Content: "Alpha."})
	if !reflect.DeepEqual(requests[1].generationMessages, want) || !reflect.DeepEqual(req.Messages, history) {
		t.Fatal("history mutation or duplicate turn", requests[1].generationMessages)
	}
	if strings.Contains(strings.Join(spoken, " "), "unfinished") || strings.Count(strings.Join(spoken, " "), "Alpha.") != 1 {
		t.Fatal("speculation or duplicate speech", spoken)
	}
}

func TestGenerationContinuationAndRepairAreFinite(t *testing.T) {
	for _, tc := range []struct {
		name, reason, text string
		maxCalls           int
	}{
		{"length", "length", "Safe. broken", 3},
		{"broken stop", "stop", "Safe. **broken", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "original", MaxTokens: 512}, GenerationLimits{}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
				calls++
				if calls > tc.maxCalls {
					t.Fatal("unbounded continuation")
				}
				if calls > 1 && strings.Contains(request.generationMessages[len(request.generationMessages)-1].Content, "broken") {
					t.Fatal("uncommitted fragment entered conversation")
				}
				emit(tc.text)
				return assemblyResponse(tc.text, tc.reason), nil
			}, nil)
			if err != nil || response.Generation.Complete || calls != tc.maxCalls || strings.Contains(response.Answer, "broken") {
				t.Fatal(response, err, calls)
			}
		})
	}
	calls := 0
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "original", MaxTokens: 512}, GenerationLimits{}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
		calls++
		text := "**broken"
		if calls == 2 {
			if !strings.Contains(request.generationInstruction, "single repair") {
				t.Fatal("missing repair instruction")
			}
			text = "Repaired"
		}
		emit(text)
		return assemblyResponse(text, "stop"), nil
	}, nil)
	if err != nil || response.Answer != "Repaired" || !response.Generation.Complete || calls != 2 {
		t.Fatal(response, err, calls)
	}
}

func TestGenerationTotalBudgetChargesUnknownUsage(t *testing.T) {
	var budgets []int
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "original", MaxTokens: 512}, GenerationLimits{MaxTotalTokens: 600}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
		budgets = append(budgets, request.MaxTokens)
		text := "Complete. partial"
		emit(text)
		return assemblyResponse(text, "length"), nil
	}, nil)
	if err != nil || response.Generation.Complete || !reflect.DeepEqual(budgets, []int{512, 88}) {
		t.Fatal(response, err, budgets)
	}
	if response.Generation.TotalTokenBudget != 600 || response.Generation.CompletionTokensObserved {
		t.Fatal(response.Generation)
	}
}

func TestGenerationUsageMetadataNeverInventsUnknownTotals(t *testing.T) {
	for _, missing := range []bool{false, true} {
		calls := 0
		response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
			calls++
			text, reason := "First. tail", "length"
			if calls == 2 {
				text, reason = " Second.", "stop"
			}
			emit(text)
			response := assemblyResponse(text, reason)
			response.Generation.CompletionTokens = 10
			response.Generation.CompletionTokensObserved = true
			reasoning := 3
			response.Generation.ReasoningTokens = &reasoning
			response.Generation.ReasoningTokenSource = "provider"
			if missing && calls == 1 {
				response.Generation.CompletionTokensObserved = false
				response.Generation.ReasoningTokens = nil
			}
			return response, nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.Generation.CompletionTokensObserved == missing {
			t.Fatal("invalid total observation flag")
		}
		if missing && response.Generation.ReasoningTokens != nil {
			t.Fatal("partial reasoning usage claimed as total")
		}
		if !missing && (response.Generation.ReasoningTokens == nil || *response.Generation.ReasoningTokens != 6 || response.Generation.CompletionTokens != 20) {
			t.Fatal(response.Generation)
		}
	}
}

func TestGenerationCancelAndLateCallbacksNeverContinue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var late func(string)
	var progress []GenerationProgress
	calls := 0
	response, err := assembleGeneration(ctx, ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		calls++
		late = emit
		emit("Safe. unfinished")
		emit(" late.")
		return assemblyResponse("Safe. unfinished late.", "length"), nil
	}, func(p GenerationProgress) { progress = append(progress, p); cancel() })
	before := len(progress)
	late("forbidden late callback.")
	if err == nil || response.Generation.FinishReason != "canceled" || response.Generation.Complete || calls != 1 || len(progress) != before || response.Answer != "Safe." {
		t.Fatal(response, err, calls, len(progress))
	}
}

func TestGenerationLateConcurrentCallbacksAreInactiveAfterReturn(t *testing.T) {
	var late func(string)
	count := 0
	_, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		late = emit
		emit("Complete.")
		return assemblyResponse("Complete.", "stop"), nil
	}, func(GenerationProgress) { count++ })
	if err != nil {
		t.Fatal(err)
	}
	before := count
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); late("Late.") }()
	}
	group.Wait()
	if count != before {
		t.Fatal("late callback changed completed output")
	}
}

func TestGenerationFailuresKeepSafeTextAndTerminalReason(t *testing.T) {
	for _, reason := range []string{"transport_eof", "unknown", "tool_calls", "timeout"} {
		calls := 0
		response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
			calls++
			emit("Safe. unsafe tail")
			response := assemblyResponse("Safe. unsafe tail", reason)
			return response, errors.New("provider private body must not escape")
		}, nil)
		if err == nil || strings.Contains(err.Error(), "private") || response.Answer != "Safe." || response.Generation.FinishReason != reason || calls != 1 || response.Generation.Complete {
			t.Fatal(response, err, calls)
		}
		metadata, _ := json.Marshal(response.Generation)
		if strings.Contains(string(metadata), "Safe") || strings.Contains(string(metadata), "private") {
			t.Fatal("body leaked into metadata")
		}
	}
}

func TestGenerationLargeDeltaAndManyPreviewsRetainExactFinalText(t *testing.T) {
	for _, text := range []string{strings.Repeat("あ", 1500), strings.Repeat("x", 200) + ".", strings.Repeat("はい。", 400)} {
		var last string
		count := 0
		var spoken strings.Builder
		response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
			for _, r := range text {
				emit(string(r))
			}
			return assemblyResponse(text, "stop"), nil
		}, func(p GenerationProgress) {
			count++
			if !strings.HasPrefix(p.CommittedText, last) {
				t.Fatal("committed prefix rolled back")
			}
			last = p.CommittedText
			for _, unit := range p.SpeechUnits {
				spoken.WriteString(unit)
			}
		})
		if err != nil || response.Answer != text || last != text || !utf8.ValidString(last) || count > 64 || !response.Generation.Complete {
			t.Fatalf("count=%d length=%d final=%d err=%v", count, len(text), len(last), err)
		}
		if strings.ReplaceAll(spoken.String(), "\n", "") != text {
			t.Fatal("speech units lost or duplicated safe content")
		}
	}
}

func TestGenerationManualContinuationPreservesPrefixAndBoundedRequest(t *testing.T) {
	var spoken []string
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "original", MaxTokens: 512}, GenerationLimits{}, "Already complete.", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
		if len(request.generationMessages) != 2 || request.generationMessages[0].Role != "user" || request.generationMessages[1].Content != "Already complete." {
			t.Fatal("invalid manual continuation", request.generationMessages)
		}
		emit("Already complete. New answer")
		return assemblyResponse("Already complete. New answer", "stop"), nil
	}, func(p GenerationProgress) { spoken = append(spoken, p.SpeechUnits...) })
	if err != nil || response.Answer != "Already complete. New answer" || strings.Contains(strings.Join(spoken, " "), "Already") || response.Generation.TotalTokenBudget != 1536 {
		t.Fatal(response, err, spoken)
	}
}

func TestGenerationEmptyStopAndMissingTerminalAreNotCompleted(t *testing.T) {
	for _, tc := range []struct {
		text     string
		terminal bool
	}{{"", true}, {"Short", false}} {
		response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, _ func(string)) (*ChatResponse, error) {
			r := assemblyResponse(tc.text, "stop")
			r.Generation.TerminalSSE = tc.terminal
			return r, nil
		}, nil)
		if err == nil || response.Generation.Complete {
			t.Fatal(response, err)
		}
	}
}

func TestGenerationFirstRawTimingIncludesEarlierReasoningAndSoftLimits(t *testing.T) {
	early := "2026-09-06T00:00:00Z"
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic", MaxTokens: 512}, GenerationLimits{SoftTargetPercent: 99, MaxSegments: 100}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
		if !strings.Contains(request.generationInstruction, "409") {
			t.Fatal("soft target not capped")
		}
		emit("Done")
		r := assemblyResponse("Done", "stop")
		r.Generation.FirstRawDeltaAt = early
		r.Generation.FirstVisibleContentAt = "2026-09-06T00:00:01Z"
		return r, nil
	}, nil)
	if err != nil || response.Generation.FirstRawDeltaAt != early || response.Generation.SoftTargetPercent != 80 || response.Generation.MaxSegments != 3 {
		t.Fatal(response, err)
	}
	if response.Generation.FirstVisibleContentAt != "2026-09-06T00:00:01Z" {
		t.Fatal("lost first visible content delta timestamp")
	}
}

func TestGenerationMixedReasoningSourcesDoNotClaimProviderTotal(t *testing.T) {
	calls := 0
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		calls++
		text, reason := "First. tail", "length"
		if calls == 2 {
			text, reason = " Second.", "stop"
		}
		emit(text)
		response := assemblyResponse(text, reason)
		count := 3
		response.Generation.ReasoningTokens = &count
		response.Generation.ReasoningTokenSource = "provider"
		if calls == 2 {
			response.Generation.ReasoningTokenSource = "retokenized"
		}
		return response, nil
	}, nil)
	if err != nil || response.Generation.ReasoningTokens != nil || response.Generation.ReasoningTokenSource != "unavailable" {
		t.Fatal(response, err)
	}
}

func TestGenerationCancelDuringFinalCommitWinsOverStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response, err := assembleGeneration(ctx, ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		emit("Short answer")
		return assemblyResponse("Short answer", "stop"), nil
	}, func(GenerationProgress) { cancel() })
	if err == nil || response.Generation.Complete || response.Generation.FinishReason != "canceled" {
		t.Fatal(response, err)
	}
}

func TestGenerationCodeOnlyHasNoSpeechAndDoesNotRequireAudio(t *testing.T) {
	text := "````text\n```\nnotSpeech()\n```\n````"
	units := 0
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		emit(text)
		return assemblyResponse(text, "stop"), nil
	}, func(p GenerationProgress) { units += len(p.SpeechUnits) })
	if err != nil || !response.Generation.Complete || response.Answer != text || units != 0 {
		t.Fatal(response, err, units)
	}
}

func TestGenerationContradictoryTerminalMetadataCannotCompleteOrContinue(t *testing.T) {
	for _, reason := range []string{"stop", "length"} {
		for _, framing := range []struct{ terminal, done bool }{{true, false}, {false, true}, {false, false}} {
			calls := 0
			var speech []string
			response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
				calls++
				text := "Safe. unfinished"
				emit(text)
				response := assemblyResponse(text, reason)
				response.Generation.Complete = true
				response.Generation.TerminalSSE = framing.terminal
				response.Generation.DoneReceived = framing.done
				return response, nil
			}, func(p GenerationProgress) { speech = append(speech, p.SpeechUnits...) })
			if err == nil || err.Error() != "generation_transport_eof" || response.Generation.Complete || response.Generation.FinishReason != "transport_eof" || response.Generation.ProviderFinishReason != reason || calls != 1 || response.Answer != "Safe." {
				t.Fatalf("reason=%s terminal=%v done=%v calls=%d response=%#v err=%v", reason, framing.terminal, framing.done, calls, response, err)
			}
			if strings.Contains(strings.Join(speech, " "), "unfinished") {
				t.Fatal("uncommitted tail reached speech")
			}
		}
	}
}

func TestGenerationEmptyListTailReceivesOneRepairWithoutPrematureCommit(t *testing.T) {
	for _, unfinished := range []string{"説明。\n1. ", "説明。\n1. \n", "- ", "* \n", "1) \n"} {
		calls := 0
		var snapshots []string
		var units []string
		response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "synthetic"}, GenerationLimits{}, "", func(_ context.Context, request ChatRequest, emit func(string)) (*ChatResponse, error) {
			calls++
			text := unfinished
			if calls == 2 {
				if !strings.Contains(request.generationInstruction, "single repair") {
					t.Fatal("empty item did not request repair")
				}
				text = "1. 完成。"
			}
			for _, r := range text {
				emit(string(r))
			}
			return assemblyResponse(text, "stop"), nil
		}, func(p GenerationProgress) {
			snapshots = append(snapshots, p.CommittedText)
			units = append(units, p.SpeechUnits...)
		})
		if err != nil || calls != 2 || !response.Generation.Complete || !strings.HasSuffix(response.Answer, "1. 完成。") {
			t.Fatal(response, err, calls)
		}
		for _, snapshot := range snapshots {
			trimmed := strings.TrimRight(snapshot, "\n")
			last := trimmed[strings.LastIndex(trimmed, "\n")+1:]
			if generationEmptyListLine.MatchString(last) {
				t.Fatalf("published unfinished item %q", last)
			}
		}
		for _, unit := range units {
			if generationEmptyListLine.MatchString(unit) {
				t.Fatal("empty marker reached TTS")
			}
		}
	}
}

func TestGenerationOverlapRemovesOnlyByteIdenticalSuffixPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix, raw string
		overlap     int
	}{
		{"Alpha.", "Alpha. Beta.", len("Alpha.")},
		{"Alpha. ", "Alpha. Beta.", len("Alpha. ")},
		{"Alpha.", "\nAlpha. Beta.", 0},
		{"Alpha.\n", "Alpha. Beta.", 0},
		{"Alpha. ", "Alpha.Beta.", 0},
		{"Alpha. Beta.", " Beta. Next.", len(" Beta.")},
	} {
		count, ready := generationOverlap(tc.prefix, tc.raw, true)
		if !ready || count != tc.overlap {
			t.Fatalf("prefix=%q raw=%q overlap=%d ready=%v", tc.prefix, tc.raw, count, ready)
		}
		if count > 0 && tc.prefix[len(tc.prefix)-count:] != tc.raw[:count] {
			t.Fatal("overlap was not byte-identical")
		}
	}
	_, ready := generationOverlap("Alpha.", "Alp", false)
	if ready {
		t.Fatal("committed an unresolved exact overlap")
	}
	response, err := assembleGeneration(context.Background(), ChatRequest{Prompt: "original"}, GenerationLimits{}, "Alpha.\n", func(_ context.Context, _ ChatRequest, emit func(string)) (*ChatResponse, error) {
		emit("Alpha. Beta.")
		return assemblyResponse("Alpha. Beta.", "stop"), nil
	}, nil)
	if err != nil || response.Answer != "Alpha.\nAlpha. Beta." {
		t.Fatal("normalized away a whitespace difference", response, err)
	}
}
