// Package recallselect chooses current user assertions from a v2 event list.
// It is intentionally independent of storage and credentials.
package recallselect

import (
	"errors"
	"strings"
	"unicode"
)

type Event struct {
	ID         string
	Type       string
	Text       string
	CorrectsID string
}

// Indices excludes superseded events, assistant output, and hits found only
// in the record's history. Karte search may match any old Markdown text.
func Indices(events []Event, query string) ([]int, error) {
	corrected := make(map[string]bool)
	latestCorrection := make(map[string]string)
	root := make(map[string]string)
	for _, event := range events {
		switch event.Type {
		case "user_final":
			root[event.ID] = event.ID
		case "correction":
			ancestor, ok := root[event.CorrectsID]
			if !ok {
				return nil, errors.New("invalid_correction_lineage")
			}
			root[event.ID] = ancestor
		}
		if event.CorrectsID != "" {
			corrected[event.CorrectsID] = true
			latestCorrection[event.CorrectsID] = event.ID
		}
	}
	selected := make([]int, 0)
	currentByRoot := make(map[string]int)
	for index, event := range events {
		if corrected[event.ID] || (event.Type != "user_final" && event.Type != "correction") || (event.CorrectsID != "" && latestCorrection[event.CorrectsID] != event.ID) {
			continue
		}
		currentByRoot[root[event.ID]]++
		if currentByRoot[root[event.ID]] > 1 {
			return nil, errors.New("ambiguous_correction_lineage")
		}
		if strings.Contains(strings.ToLower(event.Text), strings.ToLower(query)) {
			selected = append(selected, index)
		}
	}
	return selected, nil
}

// SearchTerm selects one bounded lexical term for Karte's literal search.
// It is a deterministic stub until a reviewed query planner is available.
func SearchTerm(question string) string {
	stop := map[string]bool{"what": true, "when": true, "where": true, "which": true, "does": true, "did": true, "the": true, "this": true, "that": true, "start": true, "starts": true, "time": true}
	best := ""
	for _, word := range strings.FieldsFunc(question, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len([]rune(word)) >= 3 && !stop[strings.ToLower(word)] && len([]rune(word)) > len([]rune(best)) {
			best = word
		}
	}
	return best
}
