package main

import "testing"

func TestExtractContentFromChoiceContainerSupportsContentParts(t *testing.T) {
	raw := map[string]any{
		"choices": []any{
			map[string]any{
				"delta": map[string]any{
					"content": []any{
						map[string]any{"text": "こん"},
						map[string]any{"text": "にちは"},
					},
				},
			},
		},
	}

	got := extractContentFromChoiceContainer(raw)
	if got != "こんにちは" {
		t.Fatalf("extractContentFromChoiceContainer() = %q, want %q", got, "こんにちは")
	}
}

func TestExtractFinishReasonReturnsChoiceFinishReason(t *testing.T) {
	raw := map[string]any{
		"choices": []any{
			map[string]any{
				"finish_reason": "length",
			},
		},
	}

	got := extractFinishReason(raw)
	if got != "length" {
		t.Fatalf("extractFinishReason() = %q, want %q", got, "length")
	}
}

func TestParseStreamChunkCapturesFinishReason(t *testing.T) {
	chunk, err := parseStreamChunk("message", `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
	if err != nil {
		t.Fatalf("parseStreamChunk() error = %v", err)
	}
	if chunk.Answer != "done" {
		t.Fatalf("chunk.Answer = %q, want %q", chunk.Answer, "done")
	}
	if chunk.FinishReason != "stop" {
		t.Fatalf("chunk.FinishReason = %q, want %q", chunk.FinishReason, "stop")
	}
}

func TestParseStreamErrorIncludesBackendModel(t *testing.T) {
	err := parseStreamError(`{"error":"backend returned 503","model":"qwen3-30b-a3b"}`)
	if err == nil {
		t.Fatal("parseStreamError() error = nil, want error")
	}
	want := "qwen3-30b-a3b: backend returned 503"
	if err.Error() != want {
		t.Fatalf("parseStreamError() = %q, want %q", err.Error(), want)
	}
}

func TestParseStreamErrorHandlesMalformedPayload(t *testing.T) {
	err := parseStreamError("connection closed")
	if err == nil || err.Error() != "gateway stream failed: connection closed" {
		t.Fatalf("parseStreamError() = %v", err)
	}
}

func TestExtractKarteContextStatusAndDocumentIdentity(t *testing.T) {
	raw := map[string]any{
		"karte_context_status": map[string]any{"status": "ok", "source_count": float64(1)},
		"sources": []any{
			map[string]any{
				"chunk_id": "karte:doc:context-001", "doc_id": "doc:context-001",
				"source_path":   "content/projects/ephy/decision/2026-09/context.md",
				"relative_path": "content/projects/ephy/decision/2026-09/context.md",
				"heading_path":  []any{}, "project": "ephy", "kind": "decision",
				"tags": []any{"architecture"}, "sensitivity": "internal",
				"chunk_text": "Karte owns Personal Context.", "score": float64(10),
				"source_type": "karte_context", "source_id": "K1",
			},
		},
	}

	status := extractKarteContextStatus(raw)
	if status == nil || status.Status != "ok" || status.SourceCount != 1 {
		t.Fatalf("unexpected Karte context status: %#v", status)
	}
	sources := extractChatSources(raw)
	if len(sources) != 1 || sources[0].DocID != "doc:context-001" || sources[0].Sensitivity != "internal" {
		t.Fatalf("Karte source identity was not preserved: %#v", sources)
	}
}
