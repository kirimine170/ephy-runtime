package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxGenerationTextBytes = 128 << 10

func effectiveGenerationLimits(request ChatRequest, limits GenerationLimits) GenerationLimits {
	if limits.SegmentTokens <= 0 {
		limits.SegmentTokens = request.MaxTokens
	}
	if limits.SegmentTokens <= 0 {
		limits.SegmentTokens = 512
	}
	if limits.SegmentTokens > 32768 {
		limits.SegmentTokens = 32768
	}
	if limits.MaxSegments <= 0 || limits.MaxSegments > 3 {
		limits.MaxSegments = 3
	}
	if limits.MaxTotalTokens <= 0 || limits.MaxTotalTokens > limits.SegmentTokens*limits.MaxSegments {
		limits.MaxTotalTokens = limits.SegmentTokens * limits.MaxSegments
	}
	if limits.SoftTargetPercent <= 0 {
		limits.SoftTargetPercent = 75
	}
	limits.SoftTargetPercent = max(70, min(80, limits.SoftTargetPercent))
	return limits
}

// assembleGeneration owns the display transcript and publishes only monotonic,
// structurally closed units．Pending tails are never conversation messages or speech．
// The chat adapter remains responsible for provider-specific terminal metadata．
func assembleGeneration(ctx context.Context, request ChatRequest, limits GenerationLimits, initialPrefix string, chat func(context.Context, ChatRequest, func(string)) (*ChatResponse, error), progress func(GenerationProgress)) (*ChatResponse, error) {
	limits = effectiveGenerationLimits(request, limits)
	metadata := GenerationMetadata{SchemaVersion: 2, FinishReason: "unknown", ProviderFinishReason: "unknown", OutputBudget: limits.SegmentTokens, TotalTokenBudget: limits.MaxTotalTokens, MaxSegments: limits.MaxSegments, SoftTargetPercent: limits.SoftTargetPercent, ReasoningTokenSource: "unavailable"}
	committed := initialPrefix
	delivered := initialPrefix
	var pendingSpeech []string
	progressCount := 0
	emitProgress := func(final bool) {
		if progress == nil || ctx.Err() != nil || committed == delivered || (!final && progressCount >= 63) {
			return
		}
		units := []string{}
		if len(pendingSpeech) > 0 {
			units = append(units, strings.Join(pendingSpeech, "\n"))
		}
		progress(GenerationProgress{CommittedText: committed, SpeechUnits: units, Metadata: *cloneGenerationMetadata(&metadata)})
		pendingSpeech = nil
		delivered = committed
		progressCount++
	}
	result := &ChatResponse{Answer: committed, Generation: cloneGenerationMetadata(&metadata)}
	finish := func(code string) (*ChatResponse, error) {
		emitProgress(true)
		if err := ctx.Err(); err != nil {
			metadata.Complete = false
			metadata.FinishReason = generationContextReason(err)
			code = "generation_" + metadata.FinishReason
		}
		result.Answer, result.FinishReason = committed, metadata.FinishReason
		result.Thinking, result.Raw = "", nil
		result.Generation = cloneGenerationMetadata(&metadata)
		if code != "" {
			return result, errors.New(code)
		}
		return result, nil
	}
	if chat == nil || !utf8.ValidString(initialPrefix) || len(initialPrefix) > maxGenerationTextBytes {
		return finish("generation_invalid_request")
	}
	if _, balanced := generationBoundaries(initialPrefix, true); !balanced {
		return finish("generation_invalid_prefix")
	}
	originalMessages := append([]GatewayMessage(nil), request.Messages...)
	spent := 0
	repaired := false
	allCompletionObserved, allReasoningObserved := true, true
	for segment := 0; segment < limits.MaxSegments && spent < limits.MaxTotalTokens; segment++ {
		if err := ctx.Err(); err != nil {
			metadata.FinishReason = generationContextReason(err)
			return finish("generation_" + metadata.FinishReason)
		}
		budget := min(limits.SegmentTokens, limits.MaxTotalTokens-spent)
		metadata.SegmentCount, metadata.ContinuationCount = segment+1, segment
		metadata.TerminalSSE, metadata.DoneReceived, metadata.Complete = false, false, false
		segmentRequest := request
		segmentRequest.MaxTokens, segmentRequest.Stream = budget, true
		segmentRequest.generationMessages = nil
		segmentRequest.generationRoutingMessageCount = len(originalMessages) + 1
		segmentRequest.generationInstruction = fmt.Sprintf("Complete the response naturally within approximately %d output tokens. The hard output budget remains %d tokens. Preserve the requested reasoning and task requirements. End sentences and Markdown structures cleanly; avoid starting material that cannot be completed within this budget.", budget*limits.SoftTargetPercent/100, budget)
		if segment > 0 || initialPrefix != "" {
			segmentRequest.generationMessages = append([]GatewayMessage(nil), originalMessages...)
			segmentRequest.generationMessages = append(segmentRequest.generationMessages, GatewayMessage{Role: "user", Content: request.Prompt})
			if committed != "" {
				segmentRequest.generationMessages = append(segmentRequest.generationMessages, GatewayMessage{Role: "assistant", Content: committed})
			}
			segmentRequest.generationInstruction += " Continue the assistant response after its committed prefix. Output only new continuation text; do not repeat the prefix, add a new user turn, or describe this continuation instruction. Any incomplete tail was discarded: regenerate from the last complete boundary."
			if repaired {
				segmentRequest.generationInstruction += " The preceding segment stopped with an unfinished Markdown structure. This is the single repair attempt; finish a structurally complete response from the committed boundary."
			}
		}
		base := committed
		var callbackMu sync.Mutex
		accepting := true
		raw := ""
		overflow := false
		overlap, overlapResolved := 0, base == ""
		publish := func(final bool, allowTail bool) {
			if !overlapResolved {
				overlap, overlapResolved = generationOverlap(base, raw, final)
				if !overlapResolved {
					return
				}
			}
			candidate := base + raw[overlap:]
			if len(candidate) > maxGenerationTextBytes {
				overflow = true
				return
			}
			boundaries, _ := generationBoundaries(candidate, allowTail)
			var units []string
			previous := len(committed)
			for _, end := range boundaries {
				if end <= previous {
					continue
				}
				if !strings.HasPrefix(candidate[:end], committed) {
					overflow = true
					return
				}
				if unit := generationSpeechText(candidate[previous:end]); unit != "" {
					units = append(units, unit)
				}
				previous = end
			}
			if previous <= len(committed) {
				return
			}
			committed = candidate[:previous]
			if metadata.FirstVisibleContentAt == "" && strings.TrimSpace(committed) != "" {
				metadata.FirstVisibleContentAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
			pendingSpeech = append(pendingSpeech, units...)
			emitProgress(false)
		}
		response, err := chat(ctx, segmentRequest, func(delta string) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if !accepting || ctx.Err() != nil || delta == "" {
				return
			}
			if metadata.FirstRawDeltaAt == "" {
				metadata.FirstRawDeltaAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if len(raw)+len(delta) > maxGenerationTextBytes {
				overflow = true
				return
			}
			raw += delta
			if !utf8.ValidString(raw) {
				return
			}
			publish(false, false)
		})
		callbackMu.Lock()
		accepting = false
		callbackMu.Unlock()
		charge := budget // Missing usage is charged conservatively, never reported as observed usage．
		if response != nil {
			result.Sources, result.WebSearchStatus, result.KarteContextStatus = response.Sources, response.WebSearchStatus, response.KarteContextStatus
			metadata.FinishReason = normalizedFinishReason(response.FinishReason)
			metadata.ProviderFinishReason = metadata.FinishReason
			if terminal := response.Generation; terminal != nil {
				metadata.FinishReason = normalizedFinishReason(terminal.FinishReason)
				metadata.ProviderFinishReason = normalizedFinishReason(terminal.ProviderFinishReason)
				metadata.TerminalSSE, metadata.DoneReceived, metadata.TerminalSSEAt = terminal.TerminalSSE, terminal.DoneReceived, terminal.TerminalSSEAt
				metadata.FirstRawDeltaAt = earlierGenerationTimestamp(metadata.FirstRawDeltaAt, terminal.FirstRawDeltaAt)
				metadata.FirstVisibleContentAt = earlierGenerationTimestamp(metadata.FirstVisibleContentAt, terminal.FirstVisibleContentAt)
				if terminal.CompletionTokensObserved && terminal.CompletionTokens >= 0 {
					metadata.CompletionTokens += terminal.CompletionTokens
					charge = terminal.CompletionTokens
				} else {
					allCompletionObserved = false
				}
				reasoningSource := terminal.ReasoningTokenSource
				sourceKnown := reasoningSource == "provider" || reasoningSource == "retokenized"
				if allReasoningObserved && terminal.ReasoningTokens != nil && *terminal.ReasoningTokens >= 0 && sourceKnown && (segment == 0 || metadata.ReasoningTokenSource == reasoningSource) {
					if metadata.ReasoningTokens == nil {
						zero := 0
						metadata.ReasoningTokens = &zero
					}
					*metadata.ReasoningTokens += *terminal.ReasoningTokens
					metadata.ReasoningTokenSource = reasoningSource
				} else {
					allReasoningObserved = false
					metadata.ReasoningTokens = nil
					metadata.ReasoningTokenSource = "unavailable"
				}
			} else {
				allCompletionObserved, allReasoningObserved = false, false
				metadata.ReasoningTokens = nil
				metadata.ReasoningTokenSource = "unavailable"
			}
			metadata.CompletionTokensObserved = allCompletionObserved
			if strings.HasPrefix(response.Answer, raw) {
				raw = response.Answer
			} else if response.Answer != raw {
				return finish("generation_inconsistent_response")
			}
		}
		if charge >= limits.MaxTotalTokens-spent {
			spent = limits.MaxTotalTokens
		} else {
			spent += charge
		}
		if ctx.Err() != nil {
			metadata.FinishReason = generationContextReason(ctx.Err())
			return finish("generation_" + metadata.FinishReason)
		}
		if overflow || len(raw) > maxGenerationTextBytes || !utf8.ValidString(raw) {
			return finish("generation_text_limit")
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				metadata.FinishReason = generationContextReason(err)
			}
			// A failed transport can still return a canonical committed-safe prefix．
			publish(true, false)
			return finish("generation_" + metadata.FinishReason)
		}
		if response == nil {
			return finish("generation_missing_response")
		}
		if strings.TrimSpace(response.Answer) == "" {
			return finish("llm_empty_result")
		}
		// An interchangeable adapter must supply both terminal facts．Do not trust
		// a contradictory Complete flag or continue a stream whose framing ended early．
		if (metadata.FinishReason == "stop" || metadata.FinishReason == "length" || metadata.FinishReason == "tool_calls") && (!metadata.TerminalSSE || !metadata.DoneReceived) {
			metadata.FinishReason = "transport_eof"
			publish(true, false)
			return finish("generation_transport_eof")
		}
		completeStop := metadata.FinishReason == "stop" && response.Generation != nil && response.Generation.Complete && metadata.TerminalSSE && metadata.DoneReceived
		if !overlapResolved {
			overlap, overlapResolved = generationOverlap(base, raw, true)
		}
		_, balanced := generationBoundaries(base+raw[overlap:], true)
		publish(true, completeStop && balanced)
		if completeStop && balanced {
			metadata.Complete = true
			return finish("")
		}
		if metadata.FinishReason == "length" && metadata.TerminalSSE && metadata.DoneReceived {
			continue
		}
		if completeStop && !balanced && !repaired {
			repaired = true
			continue
		}
		if completeStop {
			return finish("")
		}
		return finish("generation_incomplete")
	}
	return finish("")
}

func generationContextReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "canceled"
}

func earlierGenerationTimestamp(current, candidate string) string {
	next, err := time.Parse(time.RFC3339Nano, candidate)
	if err != nil {
		return current
	}
	previous, err := time.Parse(time.RFC3339Nano, current)
	if err != nil || next.Before(previous) {
		return candidate
	}
	return current
}

// Delay overlap decisions while the new segment might still be repeating a
// complete suffix．Only exact text at sentence/line boundaries is removable．
func generationOverlap(prefix, raw string, final bool) (int, bool) {
	if prefix == "" {
		return 0, true
	}
	best, waiting := 0, false
	for offset := range prefix {
		if offset > 0 {
			previous, _ := utf8.DecodeLastRuneInString(prefix[:offset])
			if !strings.ContainsRune("。．.!！?？\n", previous) {
				continue
			}
		}
		candidate := prefix[offset:]
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, raw) && len(raw) < len(candidate) {
			waiting = true
		}
		if strings.HasPrefix(raw, candidate) && len(candidate) > best {
			best = len(candidate)
		}
	}
	if waiting && !final {
		return 0, false
	}
	return best, true
}

// generationBoundaries recognizes conservative display/speech commit boundaries．
// No boundary inside a fence, inline code, link, bracket or emphasis is exposed．
// A validated stop can commit a balanced final sentence without punctuation．
func generationBoundaries(text string, allowTail bool) ([]int, bool) {
	var boundaries []int
	var brackets []rune
	var emphasis []string
	fence, inline := "", ""
	structurallyValid, pendingSentence := true, false
	appendBoundary := func(end int) {
		if end > 0 && (len(boundaries) == 0 || boundaries[len(boundaries)-1] != end) {
			boundaries = append(boundaries, end)
		}
	}
	for i := 0; i < len(text); {
		lineStart := i == 0 || text[i-1] == '\n'
		if lineStart {
			lineEnd := strings.IndexByte(text[i:], '\n')
			if lineEnd < 0 {
				lineEnd = len(text)
			} else {
				lineEnd += i
			}
			line := text[i:lineEnd]
			// A marker without its item is an unfinished structure, including when
			// a provider inserts a newline after it．Do not publish that line first．
			if fence == "" && inline == "" && len(emphasis) == 0 && len(brackets) == 0 && generationEmptyListLine.MatchString(line) {
				structurallyValid = false
				i = lineEnd
				if i < len(text) {
					i++
				}
				continue
			}
			trimmed := strings.TrimLeft(line, " ")
			indent := len(line) - len(trimmed)
			marker := ""
			if indent <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
				length := 1
				for length < len(trimmed) && trimmed[length] == trimmed[0] {
					length++
				}
				if length >= 3 {
					marker = trimmed[:length]
				}
			}
			if fence != "" || (marker != "" && inline == "" && len(emphasis) == 0 && len(brackets) == 0) {
				if fence == "" {
					fence = marker
				} else if marker != "" && marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(trimmed[len(marker):]) == "" {
					fence = ""
				}
				i = lineEnd
				if i < len(text) {
					i++
					if fence == "" {
						appendBoundary(i)
					}
				}
				pendingSentence = false
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			structurallyValid = false
			break
		}
		end := i + size
		previous := rune(0)
		if i > 0 {
			previous, _ = utf8.DecodeLastRuneInString(text[:i])
		}
		next := rune(0)
		if end < len(text) {
			next, _ = utf8.DecodeRuneInString(text[end:])
		}
		if r == '\\' && inline == "" {
			if end < len(text) {
				_, n := utf8.DecodeRuneInString(text[end:])
				i = end + n
				continue
			}
			structurallyValid = false
		}
		if r == '`' {
			for end < len(text) && text[end] == '`' {
				end++
			}
			marker := text[i:end]
			if inline == "" {
				inline = marker
			} else if inline == marker {
				inline = ""
			}
			i = end
			continue
		}
		if inline != "" {
			i = end
			continue
		}
		if r == '*' || r == '_' || r == '~' {
			for end < len(text) && rune(text[end]) == r {
				end++
			}
			marker := text[i:end]
			if end < len(text) {
				next, _ = utf8.DecodeRuneInString(text[end:])
			} else {
				next = 0
			}
			linePrefix := text[strings.LastIndex(text[:i], "\n")+1 : i]
			listMarker := r == '*' && len(marker) == 1 && strings.TrimSpace(linePrefix) == "" && unicode.IsSpace(next)
			inWord := (unicode.IsLetter(previous) || unicode.IsDigit(previous)) && (unicode.IsLetter(next) || unicode.IsDigit(next))
			if !listMarker && !inWord && (r != '~' || len(marker) == 2) {
				if len(emphasis) > 0 && emphasis[len(emphasis)-1] == marker && !unicode.IsSpace(previous) {
					emphasis = emphasis[:len(emphasis)-1]
				} else if next != 0 && !unicode.IsSpace(next) {
					emphasis = append(emphasis, marker)
				} else if next == 0 && (previous == 0 || unicode.IsSpace(previous)) {
					structurallyValid = false
				}
			}
		}
		switch r {
		case '[', '(', '{', '「', '『', '（', '【', '“':
			brackets = append(brackets, r)
		case ']', ')', '}', '」', '』', '）', '】', '”':
			linePrefix := strings.TrimSpace(text[strings.LastIndex(text[:i], "\n")+1 : i])
			orderedMarker := r == ')' && len(brackets) == 0 && linePrefix != "" && strings.Trim(linePrefix, "0123456789") == "" && unicode.IsSpace(next)
			if orderedMarker {
				break
			}
			pairs := map[rune]rune{']': '[', ')': '(', '}': '{', '」': '「', '』': '『', '）': '（', '】': '【', '”': '“'}
			if len(brackets) == 0 || brackets[len(brackets)-1] != pairs[r] {
				structurallyValid = false
			} else {
				brackets = brackets[:len(brackets)-1]
			}
		case '"':
			if len(brackets) > 0 && brackets[len(brackets)-1] == '"' {
				brackets = brackets[:len(brackets)-1]
			} else if previous == 0 || unicode.IsSpace(previous) || unicode.IsPunct(previous) {
				brackets = append(brackets, '"')
			}
		}
		if strings.ContainsRune("。．.!！?？", r) {
			decimal := r == '.' && unicode.IsDigit(previous) && unicode.IsDigit(next)
			linePrefix := strings.TrimSpace(text[strings.LastIndex(text[:i], "\n")+1 : i])
			listNumber := r == '.' && linePrefix != "" && strings.Trim(linePrefix, "0123456789") == "" && unicode.IsSpace(next)
			if !decimal && !listNumber {
				pendingSentence = true
			}
		}
		balanced := structurallyValid && inline == "" && len(brackets) == 0 && len(emphasis) == 0
		if balanced && r == '\n' {
			appendBoundary(end)
			pendingSentence = false
		}
		if balanced && pendingSentence && (end < len(text) && unicode.IsSpace(next) || strings.ContainsRune("。．！!？?", r)) {
			appendBoundary(end)
			pendingSentence = false
		}
		i = end
	}
	balanced := structurallyValid && fence == "" && inline == "" && len(brackets) == 0 && len(emphasis) == 0
	if allowTail && balanced {
		appendBoundary(len(text))
	}
	return boundaries, balanced
}

var generationLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
var generationEmptyListLine = regexp.MustCompile(`^[ \t]*(?:[-+*]|[0-9]{1,9}[.)])[ \t]*$`)
var generationLineMarker = regexp.MustCompile(`(?m)^\s{0,3}(?:#{1,6}\s+|(?:[-+*]|[0-9]+[.)])\s+)`)
var generationEmphasis = regexp.MustCompile(`\*([^*\n]+)\*|_([^_\n]+)_`)

func generationSpeechText(unit string) string {
	var prose strings.Builder
	fence := ""
	for _, line := range strings.SplitAfter(unit, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			length := 1
			for length < len(trimmed) && trimmed[length] == trimmed[0] {
				length++
			}
			if length >= 3 {
				if fence == "" {
					fence = trimmed[:length]
				} else if trimmed[0] == fence[0] && length >= len(fence) && strings.TrimSpace(trimmed[length:]) == "" {
					fence = ""
				}
				continue
			}
		}
		if fence == "" {
			prose.WriteString(line)
		}
	}
	text := generationLink.ReplaceAllString(prose.String(), "$1")
	text = generationLineMarker.ReplaceAllString(text, "")
	text = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "").Replace(text)
	text = generationEmphasis.ReplaceAllString(text, "$1$2")
	// Emoji-only decorations have no speech payload．Keep them in committed UI
	// text and history，but omit them before enqueue so a non-spoken response can
	// follow the same tts_skipped path as a code-only response．Provider controls
	// and embedded emoji remain the selected speech adapter's responsibility．
	if speechWithoutEmoji(text) == "" {
		return ""
	}
	return strings.TrimSpace(text)
}

func speechWithoutEmoji(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if (r >= 0x1F1E6 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) ||
			r == 0x200D || r == 0xFE0E || r == 0xFE0F {
			return -1
		}
		return r
	}, text))
}
