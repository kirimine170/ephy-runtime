package recallselect

import "testing"

func TestCurrentEventsExcludeHistoryAndAssistant(t *testing.T) {
	events := []Event{
		{ID: "old", Type: "user_final", Text: "The rehearsal starts at 09:00."},
		{ID: "assistant", Type: "assistant_result", Text: "The rehearsal starts at 09:00."},
		{ID: "new", Type: "correction", Text: "The rehearsal starts at 10:00.", CorrectsID: "old"},
	}
	indices, err := Indices(events, "rehearsal")
	if err != nil || len(indices) != 1 || indices[0] != 2 {
		t.Fatalf("selected superseded or assistant event: %v", indices)
	}
	if indices, err := Indices(events, "09:00"); err != nil || len(indices) != 0 {
		t.Fatalf("old search hit became current context: %v", indices)
	}
}

func TestSearchTermForLiteralKarteSearch(t *testing.T) {
	if got := SearchTerm("When does the rehearsal start?"); got != "rehearsal" {
		t.Fatalf("wrong search term: %q", got)
	}
}

func TestLaterCorrectionSupersedesEarlierCorrection(t *testing.T) {
	events := []Event{
		{ID: "old", Type: "user_final", Text: "The rehearsal starts at 09:00."},
		{ID: "first-correction", Type: "correction", Text: "The rehearsal starts at 10:00.", CorrectsID: "old"},
		{ID: "second-correction", Type: "correction", Text: "The rehearsal starts at 11:00.", CorrectsID: "old"},
	}
	indices, err := Indices(events, "rehearsal")
	if err != nil || len(indices) != 1 || indices[0] != 2 {
		t.Fatalf("earlier correction remained current: %v", indices)
	}
}

func TestBranchedCorrectionLineageFailsClosed(t *testing.T) {
	events := []Event{
		{ID: "A", Type: "user_final", Text: "The rehearsal starts at 09:00."},
		{ID: "B", Type: "correction", Text: "The rehearsal starts at 10:00.", CorrectsID: "A"},
		{ID: "C", Type: "correction", Text: "The rehearsal starts at 11:00.", CorrectsID: "B"},
		{ID: "D", Type: "correction", Text: "The rehearsal starts at 12:00.", CorrectsID: "A"},
	}
	if indices, err := Indices(events, "rehearsal"); err == nil || len(indices) != 0 {
		t.Fatalf("ambiguous lineage returned current assertions: %v, %v", indices, err)
	}
	if indices, err := Indices(events[:3], "rehearsal"); err != nil || len(indices) != 1 || indices[0] != 2 {
		t.Fatalf("linear correction did not resolve to C: %v, %v", indices, err)
	}
}
