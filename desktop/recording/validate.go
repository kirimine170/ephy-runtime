package recording

import (
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"
)

func validHash(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == value
}
func validTarget(t Target) bool { return validUUID(t.DocID) && t.Revision > 0 && validHash(t.SHA256) }
func (s *Store) validateState() error {
	c := s.settings
	if !validUUID(c.ConversationID) || c.Epoch < 1 || s.heads == nil || s.completed == nil {
		return errors.New("recording_state_unavailable")
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return errors.New("recording_state_unavailable")
	}
	if c.Configured && (!validUUID(c.ScopeID) || !validUUID(c.ProducerID) || !validUUID(c.PolicyID) || c.DataRoot == "") {
		return errors.New("recording_state_unavailable")
	}
	if c.Enabled && !c.Configured {
		return errors.New("recording_state_unavailable")
	}
	for conv, h := range s.heads {
		if !validUUID(conv) || h.Seq < 1 || h.Segment < 1 || h.Spec.ConversationID != conv || !validTarget(h.Target) || h.Events < 1 || h.Events > 256 || h.Bytes < 1 || h.Bytes > 1<<20 {
			return errors.New("recording_state_unavailable")
		}
	}
	return nil
}
func (s *Store) validateTurn(t *turn) bool {
	if len(t.Items) > 2 || (len(t.Items) == 0 && t.Draft == nil) {
		return false
	}
	if d := t.Draft; d != nil {
		if len(d.Text) > maxTextBytes || !utf8.ValidString(d.Text) || d.Epoch < 1 || d.SkipBytes < 0 || d.SkipBytes > 128<<10 {
			return false
		}
	}
	for _, d := range t.Items {
		if d == nil || d.Seq < 1 || !validUUID(d.EventID) {
			return false
		}
		if d.Verified {
			r := d.Receipt
			if r == nil || r.SchemaVersion != Version || !validUUID(r.CandidateID) || !validHash(r.ProposalHash) || !validTarget(r.Applied) || r.Status != "accepted" || len(r.EventIDs) != 1 || r.EventIDs[0] != d.EventID || d.Record.ConversationID != t.ConversationID || d.RecordEvents < 1 || d.RecordEvents > 256 || d.RecordBytes < 1 || d.RecordBytes > 1<<20 || d.Event != nil || len(d.Proposal) != 0 {
				return false
			}
			continue
		}
		e := d.Event
		if e == nil || e.EventID != d.EventID || e.Seq != d.Seq || e.Revision != 1 || e.TurnID != t.TurnID || e.ConversationID != t.ConversationID || e.ScopeID != s.settings.ScopeID || e.ProducerID != s.settings.ProducerID || (e.Type != "user_final" && e.Type != "assistant_result") || len(e.Text) > maxTextBytes || !utf8.ValidString(e.Text) {
			return false
		}
	}
	return true
}
