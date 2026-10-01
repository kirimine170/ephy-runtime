package recording

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"desktop/recording/recallselect"
	"github.com/google/uuid"
)

// RecallSource is a current, policy-authorized conversation event. The source
// identity is sufficient for a citation without exposing the credential or the
// record's full Markdown (which also contains superseded events).
type RecallSource struct {
	Target         Target   `json:"target"`
	ConversationID string   `json:"conversation_id"`
	TurnID         string   `json:"turn_id"`
	Event          EventRef `json:"event"`
	Text           string   `json:"text"`
}

const maxRecallSources = 3
const maxRecallTextBytes = 6000

type searchQuery struct {
	Text        string   `json:"text"`
	RecordTypes []string `json:"record_types"`
	Limit       int      `json:"limit"`
}

type searchRequest struct {
	ProtocolVersion string      `json:"protocol_version"`
	RequestID       string      `json:"request_id"`
	Operation       string      `json:"operation"`
	ScopeID         string      `json:"scope_id"`
	ProducerID      string      `json:"producer_instance_id"`
	Actor           Actor       `json:"actor"`
	PolicyID        string      `json:"policy_id"`
	PolicyRevision  int64       `json:"policy_revision"`
	ConsentEpoch    int64       `json:"consent_epoch"`
	ScopeGeneration int64       `json:"scope_generation"`
	CreatedAt       string      `json:"created_at"`
	Auth            Auth        `json:"auth"`
	Query           searchQuery `json:"query"`
	Target          any         `json:"target"`
}

// Recall searches Karte v2 and reads each current hit again before returning
// any text. A failed or denied read is never replaced by a search snippet.
// Recording OFF does not revoke access to records saved under the grant.
func (s *Store) Recall(ctx context.Context, query string, limit int) ([]RecallSource, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 2048 || limit < 1 || limit > 20 {
		return nil, errors.New("invalid_recall_query")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	s.mu.Lock()
	settings := s.settings
	c, key, err := s.loadCredential()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(settings.DataRoot)
	if err != nil {
		return nil, errors.New("karte_offline")
	}
	defer root.Close()
	cap, err := loadCapabilities(root, settings)
	if err != nil {
		return nil, err
	}
	searchAllowed := false
	for _, operation := range cap.Operations {
		searchAllowed = searchAllowed || operation == "search"
	}
	if !searchAllowed {
		return nil, errors.New("unsupported_protocol")
	}
	hits, err := s.searchRecall(ctx, root, settings, key, c.KeyID, query, limit)
	if err != nil {
		return nil, err
	}
	selected := make([]RecallSource, 0)
	selectedBytes := 0
	for _, hit := range hits {
		if hit.ScopeID != settings.ScopeID || hit.Record.Type != "conversation" || hit.State != "active" || hit.Markdown != "" || len(hit.Events) != 0 || !validUUID(hit.Target.DocID) || hit.Target.Revision < 1 || len(hit.Target.SHA256) != 64 {
			return nil, errors.New("invalid_recall_response")
		}
		current, readErr := s.read(ctx, root, settings, hit.Target, key, c.KeyID)
		if readErr != nil {
			return nil, readErr
		}
		if current.State != "active" || current.Record != hit.Record || validateRecallEvents(current.Events, current.Record, settings) != nil {
			return nil, errors.New("invalid_recall_response")
		}
		views := make([]recallselect.Event, len(current.Events))
		for index, event := range current.Events {
			views[index] = recallselect.Event{ID: event.EventID, Type: event.Type, Text: event.Text}
			if event.Corrects != nil {
				views[index].CorrectsID = event.Corrects.EventID
			}
		}
		indices, selectErr := recallselect.Indices(views, query)
		if selectErr != nil {
			return nil, selectErr
		}
		for _, index := range indices {
			event := current.Events[index]
			if len(event.Text) > maxRecallTextBytes-selectedBytes || len(selected) >= maxRecallSources {
				continue
			}
			selected = append(selected, RecallSource{
				Target: current.Target, ConversationID: event.ConversationID,
				TurnID: event.TurnID, Event: EventRef{EventID: event.EventID, EventRevision: event.Revision},
				Text: event.Text,
			})
			selectedBytes += len(event.Text)
		}
	}
	// A read by target may legitimately return an older historical revision if
	// the document changes between search and read. Re-search under current
	// policy and reject the entire selection if any target has moved.
	currentHits, err := s.searchRecall(ctx, root, settings, key, c.KeyID, query, limit)
	if err != nil || len(currentHits) != len(hits) {
		return nil, errors.New("stale_recall")
	}
	for index, hit := range hits {
		if currentHits[index].Target != hit.Target || currentHits[index].State != "active" {
			return nil, errors.New("stale_recall")
		}
	}
	if _, err = loadCapabilities(root, settings); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *Store) searchRecall(ctx context.Context, root *os.Root, settings Settings, key []byte, keyID, query string, limit int) ([]ReadResult, error) {
	id := uuid.NewString()
	request := searchRequest{
		ProtocolVersion: Version, RequestID: id, Operation: "search",
		ScopeID: settings.ScopeID, ProducerID: settings.ProducerID,
		Actor:    Actor{Type: "ephy", ID: "ephy-runtime"},
		PolicyID: settings.PolicyID, PolicyRevision: 1, ConsentEpoch: 1,
		ScopeGeneration: 1, CreatedAt: s.options.Now().Format(time.RFC3339Nano),
		Auth:  Auth{KeyID: keyID},
		Query: searchQuery{Text: query, RecordTypes: []string{"conversation"}, Limit: limit},
	}
	raw, err := sign(request, key)
	if err != nil {
		return nil, err
	}
	requestPath := ".mdsys/context/v2/requests/" + id + ".json"
	responsePath := ".mdsys/context/v2/responses/" + id + ".json"
	if err = atomicWrite(root, requestPath, raw); err != nil {
		return nil, err
	}
	defer removeFile(root, responsePath)
	responseRaw, err := waitFile(ctx, root, responsePath, 2<<20)
	if err != nil {
		return nil, err
	}
	var response readResponse
	if err = strict(responseRaw, &response); err != nil || response.ProtocolVersion != Version || response.RequestID != id || response.Status != "ok" || len(response.Results) > limit {
		return nil, errors.New("invalid_recall_response")
	}
	return response.Results, nil
}
