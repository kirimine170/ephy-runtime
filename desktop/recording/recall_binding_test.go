package recording

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Only synthetic data is rendered here, using Karte v2's existing front matter
// and item format. The independent pinned fixture below guards format drift.
func recallFixtureMarkdown(t *testing.T, proposal Proposal, target Target, events []Event) string {
	t.Helper()
	header := map[string]any{
		"doc_id": target.DocID, "project": "synthetic-memory", "kind": "note",
		"sensitivity": "internal", "tags": []string{"ephy:conversation"},
		"provenance_types": []string{"ephy"}, "authorship": "ephy", "title": proposal.Record.Title,
		"fixture_private_note": "PRIVATE_CANARY_DO_NOT_SEND_TO_MODEL",
		"runtime_record": map[string]any{
			"schema_version": Version, "scope_id": proposal.ScopeID, "producer_instance_id": proposal.ProducerID,
			"logical_record_key": proposal.LogicalKey, "revision": target.Revision, "record": proposal.Record,
			"adoption": Adoption{Mode: "policy", ActorID: proposal.Actor.ID, PolicyID: proposal.PolicyID, PolicyRevision: proposal.PolicyRevision, Decision: "scope_allowed"},
			"human_edited": false,
		},
	}
	// Match Render's JSON field names before serializing the YAML front matter.
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	front, err := yaml.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.Write(front)
	body.WriteString("---\n\n")
	for _, event := range events {
		raw, err = json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]json.RawMessage
		if err = json.Unmarshal(raw, &metadata); err != nil {
			t.Fatal(err)
		}
		delete(metadata, "text")
		raw, err = json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString("## " + event.Type + "\n<!-- karte-v2:item " + string(raw) + " -->\n")
		for _, line := range strings.Split(event.Text, "\n") {
			body.WriteString("> " + html.EscapeString(line) + "\n")
		}
		body.WriteString("<!-- karte-v2:end -->\n\n")
	}
	return body.String()
}

func pinnedRecallFixture(t *testing.T) (ReadResult, Settings) {
	t.Helper()
	var response readResponse
	if err := strict([]byte(pinnedKarteReadResponse), &response); err != nil || len(response.Results) != 1 {
		t.Fatalf("invalid pinned fixture: %v", err)
	}
	result := response.Results[0]
	settings := Settings{ScopeID: result.ScopeID, ProducerID: result.Events[0].ProducerID}
	if hash([]byte(result.Markdown)) != result.Target.SHA256 {
		t.Fatal("pinned canonical Markdown hash changed")
	}
	return result, settings
}

func TestRecallBindingPinnedKarteFixture(t *testing.T) {
	result, settings := pinnedRecallFixture(t)
	if err := validateRecallEvents(result.Events, result.Record, settings); err != nil {
		t.Fatal(err)
	}
	if err := validateRecallBinding(result, settings); err != nil {
		t.Fatal(err)
	}
}

func TestRecallBindingRejectsValidEventSubstitution(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReadResult)
	}{
		{"text", func(r *ReadResult) { r.Events[0].Text = "Substituted but otherwise valid text." }},
		{"event_id", func(r *ReadResult) { r.Events[0].EventID = uuid.NewString() }},
		{"turn_id", func(r *ReadResult) { r.Events[0].TurnID = uuid.NewString() }},
		{"asr", func(r *ReadResult) { r.Events[0].ASR.FinalRevision++ }},
		{"occurrence", func(r *ReadResult) { r.Events[0].OccurredAt = "2026-09-12T01:00:00Z" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, settings := pinnedRecallFixture(t)
			test.mutate(&result)
			if err := validateRecallEvents(result.Events, result.Record, settings); err != nil {
				t.Fatalf("regression must remain structurally valid: %v", err)
			}
			if hash([]byte(result.Markdown)) != result.Target.SHA256 {
				t.Fatal("regression changed the hash-bound document")
			}
			if err := validateRecallBinding(result, settings); err == nil {
				t.Fatal("accepted event payload absent from the canonical document")
			}
		})
	}
}

func TestRecallBindingEscapedTextAndNestedMetadata(t *testing.T) {
	result, settings := pinnedRecallFixture(t)
	result.Events[0].Text = "---\ndoc_id: fake\n<!-- karte-v2:end -->\n<!-- karte-v2:item {} -->\n> false anchor\n\"<>&'\" 雪☃️\r\n  preserve whitespace  \n"
	assistant := result.Events[0]
	assistant.EventID, assistant.Seq = uuid.NewString(), 2
	assistant.Type, assistant.InputKind, assistant.ASR = "assistant_result", "", nil
	assistant.Assistant = &Assistant{Generation: "completed", Display: "confirmed_full", Playback: "unknown", SpeechUnits: []SpeechUnit{}}
	result.Events = append(result.Events, assistant)
	proposal := Proposal{ScopeID: settings.ScopeID, ProducerID: settings.ProducerID, Record: result.Record}
	result.Markdown = recallFixtureMarkdown(t, proposal, result.Target, result.Events)
	result.Target.SHA256 = hash([]byte(result.Markdown))
	if err := validateRecallEvents(result.Events, result.Record, settings); err != nil {
		t.Fatal(err)
	}
	if err := validateRecallBinding(result, settings); err != nil {
		t.Fatal(err)
	}
	result.Events[1].Assistant.SpeechUnits = []SpeechUnit{{UnitID: "forged", State: "completed"}}
	if err := validateRecallBinding(result, settings); err == nil {
		t.Fatal("accepted substituted nested assistant metadata")
	}
}

func TestRecallBindingRejectsChangedEnvelopeAndMalformedItems(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReadResult)
	}{
		{"document", func(r *ReadResult) { r.Target.DocID = uuid.NewString() }},
		{"revision", func(r *ReadResult) { r.Target.Revision++ }},
		{"record", func(r *ReadResult) { r.Record.Segment++ }},
		{"missing_event", func(r *ReadResult) { r.Events = nil }},
		{"extra_event", func(r *ReadResult) { r.Events = append(r.Events, r.Events[0]) }},
		{"plain_text", func(r *ReadResult) { r.Markdown = r.Events[0].Text }},
		{"item_label", func(r *ReadResult) { r.Markdown = strings.Replace(r.Markdown, "## user_final", "## correction", 1) }},
		{"item_end", func(r *ReadResult) { r.Markdown = strings.Replace(r.Markdown, "<!-- karte-v2:end -->", "", 1) }},
		{"extra_body", func(r *ReadResult) { r.Markdown += "unbound body\n" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, settings := pinnedRecallFixture(t)
			test.mutate(&result)
			if err := validateRecallBinding(result, settings); err == nil {
				t.Fatal("accepted mismatched or malformed canonical record")
			}
		})
	}
}

// Unmodified Karte fixture from d877f3bf34a7c39459f6817aee1221b37d6c51d8:
// schemas/karte-context/v2/fixtures/read-response.json (synthetic data only).
const pinnedKarteReadResponse = `{
  "protocol_version": "2.0",
  "request_id": "e46a9272-45e6-512a-925b-cc2d9dbb9157",
  "status": "ok",
  "results": [
    {
      "target": {
        "doc_id": "5996543a-0f50-51b9-8af7-58ec757d7a6f",
        "revision": 1,
        "sha256": "33d8a0d76e4b9b1b27596e5fa95c1c7892cf3b74b5433b8178f8daad281ef85c"
      },
      "record": {
        "record_type": "conversation",
        "title": "合成会話",
        "conversation_id": "11111111-1111-4111-8111-111111111111",
        "segment_no": 1,
        "timezone": "Asia/Tokyo",
        "local_date": "2026-09-12"
      },
      "scope_id": "22222222-2222-4222-8222-222222222222",
      "state": "active",
      "source_refs": [],
      "markdown": "---\nauthorship: ephy\ndoc_id: 5996543a-0f50-51b9-8af7-58ec757d7a6f\nkind: note\nproject: synthetic-diary\nprovenance_types:\n    - canonical\nruntime_record:\n    adoption:\n        actor_id: runtime-fixture\n        decision: scope_allowed\n        mode: policy\n        policy_id: 66666666-6666-4666-8666-666666666666\n        policy_revision: 1\n    human_edited: false\n    logical_record_key: conversation:11111111-1111-4111-8111-111111111111:1\n    producer_instance_id: 33333333-3333-4333-8333-333333333333\n    record:\n        conversation_id: 11111111-1111-4111-8111-111111111111\n        local_date: \"2026-09-12\"\n        record_type: conversation\n        segment_no: 1\n        timezone: Asia/Tokyo\n        title: 合成会話\n    revision: 1\n    schema_version: \"2.0\"\n    scope_id: 22222222-2222-4222-8222-222222222222\nsensitivity: internal\ntags:\n    - ephy:conversation\ntitle: 合成会話\n---\n\n## user_final\n\u003c!-- karte-v2:item {\"asr\":{\"provider\":\"synthetic\",\"model_revision\":\"fixture-1\",\"final_revision\":3},\"consent_epoch\":1,\"conversation_id\":\"11111111-1111-4111-8111-111111111111\",\"event_id\":\"44444444-4444-4444-8444-444444444444\",\"event_revision\":1,\"event_seq\":1,\"event_type\":\"user_final\",\"input_kind\":\"asr_final\",\"local_date\":\"2026-09-12\",\"occurred_at\":\"2026-09-12T00:00:00Z\",\"producer_instance_id\":\"33333333-3333-4333-8333-333333333333\",\"scope_id\":\"22222222-2222-4222-8222-222222222222\",\"timezone\":\"Asia/Tokyo\",\"turn_id\":\"55555555-5555-4555-8555-555555555555\"} --\u003e\n\u003e 次回は冬の観測を相談したい．\n\u003c!-- karte-v2:end --\u003e\n\n",
      "events": [
        {
          "conversation_id": "11111111-1111-4111-8111-111111111111",
          "scope_id": "22222222-2222-4222-8222-222222222222",
          "producer_instance_id": "33333333-3333-4333-8333-333333333333",
          "event_id": "44444444-4444-4444-8444-444444444444",
          "event_seq": 1,
          "event_revision": 1,
          "turn_id": "55555555-5555-4555-8555-555555555555",
          "event_type": "user_final",
          "input_kind": "asr_final",
          "text": "次回は冬の観測を相談したい．",
          "occurred_at": "2026-09-12T00:00:00Z",
          "timezone": "Asia/Tokyo",
          "local_date": "2026-09-12",
          "asr": {
            "provider": "synthetic",
            "model_revision": "fixture-1",
            "final_revision": 3
          },
          "consent_epoch": 1
        }
      ]
    }
  ]
}
`
