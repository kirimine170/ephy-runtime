package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"desktop/recording"
)

type scriptedRecaller struct {
	responses [][]recording.RecallSource
	errOn     int
	calls     int
}

func (s *scriptedRecaller) Recall(_ context.Context, query string, _ int) ([]recording.RecallSource, error) {
	if query != "rehearsal" {
		return nil, errors.New("wrong literal search term")
	}
	s.calls++
	if s.calls == s.errOn {
		return nil, errors.New("permission_blocked")
	}
	return s.responses[s.calls-1], nil
}

func TestFixedRecordedRecallCitationAndOutputAuthorization(t *testing.T) {
	source := recording.RecallSource{
		Target: recording.Target{DocID: "synthetic-doc", Revision: 2, SHA256: strings.Repeat("a", 64)},
		Event:  recording.EventRef{EventID: "synthetic-event", EventRevision: 1},
		Text:   "The rehearsal starts at 10:00.",
	}
	for _, test := range []struct {
		name       string
		store      scriptedRecaller
		wantOutput bool
	}{
		{"authorized", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {source}}}, true},
		{"revoked_before_output", scriptedRecaller{responses: [][]recording.RecallSource{{source}}, errOn: 2}, false},
		{"changed_before_output", scriptedRecaller{responses: [][]recording.RecallSource{{source}, {}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := ""
			response, err := fixedRecordedRecall(context.Background(), &test.store, "When does the rehearsal start?", func(text string) { output += text })
			if test.store.calls != 2 {
				t.Fatalf("expected authorization checks before generation and output: %d", test.store.calls)
			}
			if !test.wantOutput {
				if err == nil || response != nil || output != "" {
					t.Fatalf("revoked or changed record disclosed: %v, %+v, %q", err, response, output)
				}
				return
			}
			if err != nil || response == nil || output != "The rehearsal starts at 10:00. [R1]" || len(response.Sources) != 1 || response.Sources[0].ChunkID != "karte-v2:synthetic-doc:2:synthetic-event" {
				t.Fatalf("missing current citation: %v, %+v, %q", err, response, output)
			}
			citation := response.Sources[0].KarteRecordV2
			if citation == nil || citation.Target.SHA256 != strings.Repeat("a", 64) || citation.Target.Revision != 2 || citation.Event.EventID != "synthetic-event" || citation.Event.EventRevision != 1 {
				t.Fatalf("incomplete v2 citation binding: %+v", citation)
			}
			encoded, encodeErr := json.Marshal(response.Sources[0])
			var transported SearchItem
			if encodeErr != nil || json.Unmarshal(encoded, &transported) != nil || transported.KarteRecordV2 == nil || transported.KarteRecordV2.Target.SHA256 != citation.Target.SHA256 || transported.KarteRecordV2.Event.EventRevision != citation.Event.EventRevision {
				t.Fatalf("v2 citation lost across response JSON: %v, %s", encodeErr, encoded)
			}
		})
	}
}
