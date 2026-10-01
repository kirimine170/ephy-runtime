package recording

import (
	"errors"
	"time"
)

// validateRecallEvents mirrors the conversation identity and correction checks
// already enforced by Karte v2 proposals. validateRecallBinding separately
// binds these structurally valid events to the hash-verified Markdown.
func validateRecallEvents(events []Event, spec RecordSpec, settings Settings) error {
	if len(events) == 0 || len(events) > 256 || spec.Type != "conversation" || !validUUID(spec.ConversationID) {
		return errors.New("invalid_recall_events")
	}
	location, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return errors.New("invalid_recall_events")
	}
	effective := make(map[string]int64, len(events))
	lastSeq := int64(0)
	for index, event := range events {
		occurred, parseErr := time.Parse(time.RFC3339, event.OccurredAt)
		if !validUUID(event.EventID) || !validUUID(event.TurnID) || event.ConversationID != spec.ConversationID || event.ScopeID != settings.ScopeID || event.ProducerID != settings.ProducerID || event.Revision != 1 || event.ConsentEpoch != 1 || event.Timezone != spec.Timezone || event.LocalDate != spec.LocalDate || parseErr != nil || occurred.In(location).Format("2006-01-02") != spec.LocalDate || len(event.Text) > 64<<10 || event.Seq < 1 || effective[event.EventID] != 0 {
			return errors.New("invalid_recall_events")
		}
		if index > 0 && event.Seq != lastSeq+1 {
			return errors.New("invalid_recall_events")
		}
		lastSeq = event.Seq
		switch event.Type {
		case "user_final":
			if (event.InputKind != "text" && event.InputKind != "asr_final") || event.Corrects != nil || event.Assistant != nil {
				return errors.New("invalid_recall_events")
			}
		case "assistant_result":
			if event.Assistant == nil || event.Corrects != nil || event.InputKind != "" || event.ASR != nil {
				return errors.New("invalid_recall_events")
			}
		case "correction":
			if event.Corrects == nil || !validUUID(event.Corrects.EventID) || event.Corrects.EventRevision < 1 || effective[event.Corrects.EventID] != event.Corrects.EventRevision || (event.CorrectionReason != "asr_error" && event.CorrectionReason != "user_content") || event.Assistant != nil || event.ASR != nil {
				return errors.New("invalid_recall_events")
			}
			effective[event.Corrects.EventID]++
		default:
			return errors.New("invalid_recall_events")
		}
		effective[event.EventID] = event.Revision
	}
	return nil
}
