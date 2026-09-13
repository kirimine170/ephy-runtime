package recording

import (
	"errors"

	"github.com/google/uuid"
)

// BeginCandidate creates only an assistant recovery checkpoint．C2 already
// accepts assistant_result without user_final (as with continuation outputs)．
// An observed conversation is never fabricated into a user question．The normal
// Checkpoint/Finish path records generation and actual playback separately．
func (s *Store) BeginCandidate(key, conversationID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("recording_closed")
	}
	if !s.settings.Enabled {
		return "", nil
	}
	if !s.settings.Configured {
		return "", s.pause("recording_unconfigured")
	}
	id := s.turnKey(key)
	if _, exists := s.turns[id]; exists {
		return "", errors.New("recording_duplicate_input")
	}
	if _, exists := s.completed[id]; exists {
		return "", errors.New("recording_duplicate_input")
	}
	if !s.hasCapacity(0, 1) {
		return "", s.pause("recording_queue_full")
	}
	t := &turn{Key: id, ConversationID: s.conversation(conversationID),
		TurnID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("turn\x00"+id)).String(),
		Draft:  &draft{Epoch: s.settings.Epoch, State: Assistant{Generation: "failed", Display: "none", Playback: "unknown", SpeechUnits: []SpeechUnit{}}},
		Items:  []*delivery{}}
	if err := s.saveTurn(t); err != nil {
		return "", s.pause("recording_storage_failed")
	}
	s.turns[id] = t
	return id, nil
}
