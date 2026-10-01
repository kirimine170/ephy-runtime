package recording

import (
	"encoding/json"
	"errors"
	"html"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateRecallBinding follows the conversation item grammar in Karte v2's
// internal/ephyrecordsv2/record.go. s.read has already verified the Markdown
// hash against the requested Target; the separately serialized envelope and
// Events must describe exactly that document before any event is selected.
func validateRecallBinding(result ReadResult, settings Settings) error {
	invalid := errors.New("invalid_recall_binding")
	if len(result.Markdown) > 1<<20 || !strings.HasPrefix(result.Markdown, "---\n") || len(result.Events) == 0 || len(result.Events) > 256 || result.Derivation != nil {
		return invalid
	}
	parts := strings.SplitN(result.Markdown[4:], "\n---\n\n", 2)
	if len(parts) != 2 {
		return invalid
	}
	// Use Karte's YAML-to-JSON conversion so RecordSpec uses the established
	// protocol field names. Unrelated front matter never reaches recall output.
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(parts[0]), &fields); err != nil {
		return invalid
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return invalid
	}
	var header struct {
		DocID string `json:"doc_id"`
		Title string `json:"title"`
		Meta  struct {
			SchemaVersion string     `json:"schema_version"`
			ScopeID       string     `json:"scope_id"`
			ProducerID    string     `json:"producer_instance_id"`
			Revision      int64      `json:"revision"`
			Record        RecordSpec `json:"record"`
		} `json:"runtime_record"`
	}
	if json.Unmarshal(raw, &header) != nil || header.DocID != result.Target.DocID || header.Meta.Revision != result.Target.Revision || header.Meta.SchemaVersion != Version || header.Meta.ScopeID != settings.ScopeID || header.Meta.ProducerID != settings.ProducerID || header.Meta.Record != result.Record || header.Title != result.Record.Title {
		return invalid
	}
	lines := strings.Split(parts[1], "\n")
	count := 0
	for i := 0; i < len(lines); {
		if i == len(lines)-1 && lines[i] == "" {
			break
		}
		if count >= len(result.Events) || !strings.HasPrefix(lines[i], "## ") || i+2 >= len(lines) {
			return invalid
		}
		label := strings.TrimPrefix(lines[i], "## ")
		i++
		const prefix = "<!-- karte-v2:item "
		const suffix = " -->"
		if !strings.HasPrefix(lines[i], prefix) || !strings.HasSuffix(lines[i], suffix) {
			return invalid
		}
		metadata := strings.TrimSuffix(strings.TrimPrefix(lines[i], prefix), suffix)
		var event Event
		if strict([]byte(metadata), &event) != nil || event.Text != "" || event.Type != label {
			return invalid
		}
		i++
		textLines := []string{}
		for i < len(lines) && strings.HasPrefix(lines[i], "> ") {
			textLines = append(textLines, html.UnescapeString(strings.TrimPrefix(lines[i], "> ")))
			i++
		}
		if len(textLines) == 0 || i+1 >= len(lines) || lines[i] != "<!-- karte-v2:end -->" || lines[i+1] != "" {
			return invalid
		}
		i += 2
		event.Text = strings.Join(textLines, "\n")
		if len(event.Text) > 64<<10 || !reflect.DeepEqual(event, result.Events[count]) {
			return invalid
		}
		count++
	}
	if count != len(result.Events) {
		return invalid
	}
	return nil
}
