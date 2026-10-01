package recording

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSyntheticRecallRestartCorrectionAndRevocation(t *testing.T) {
	s, options := testStore(t)
	s.options.Prepare = func(_ context.Context, root string) error {
		physical, err := filepath.EvalSymlinks(root)
		if err != nil || physical != options.DataRoot {
			return errors.New("fixture_access_denied")
		}
		return nil
	}
	s.settings.DataRoot = options.DataRoot
	s.settings.ConversationID = uuid.NewString()
	if err := s.saveSettings(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(options.DataRoot, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(options.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	key := make([]byte, 32)
	if err = s.saveJSON("producer.json", credential{ProducerID: s.settings.ProducerID, KeyID: "fixture", Key: hex.EncodeToString(key)}); err != nil {
		t.Fatal(err)
	}
	capabilitiesJSON := func(enabled bool) []byte {
		b, _ := json.Marshal(map[string]any{
			"protocol_version": Version, "record_schema": Version, "enabled": enabled,
			"operations": []string{"create_record", "append_events", "read", "search"},
			"policies":   []any{map[string]any{"scope_id": s.settings.ScopeID, "policy_id": s.settings.PolicyID, "policy_revision": 1, "consent_epoch": 1, "scope_generation": 1}},
		})
		return b
	}
	if err = atomicWrite(root, ".mdsys/context/v2/capabilities.json", capabilitiesJSON(true)); err != nil {
		t.Fatal(err)
	}
	conversationID := s.settings.ConversationID
	turnKey, err := s.Begin("synthetic-turn", conversationID, "The rehearsal starts at 09:00 on Tuesday.", "text", nil)
	if err != nil {
		t.Fatal(err)
	}
	turn := s.turns[turnKey]
	item := turn.Items[0]
	first := *item.Event
	if err = s.buildProposal(turn, item, key, "fixture"); err != nil {
		t.Fatal(err)
	}
	proposalBytes := append([]byte{}, item.Proposal...)
	var proposal Proposal
	if err = strict(proposalBytes, &proposal); err != nil {
		t.Fatal(err)
	}
	proposalDigest, err := proposalHash(proposalBytes)
	if err != nil {
		t.Fatal(err)
	}
	correctionID := uuid.NewString()
	correction := first
	correction.EventID = correctionID
	correction.Seq++
	correction.Type = "correction"
	correction.Text = "The rehearsal starts at 10:00 on Tuesday."
	correction.InputKind = ""
	correction.Corrects = &EventRef{EventID: first.EventID, EventRevision: first.Revision}
	correction.CorrectionReason = "user_content"
	targets := []Target{
		{DocID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("karte-record-v2\x00"+proposal.ScopeID+"\x00"+proposal.ProducerID+"\x00"+proposal.LogicalKey)).String(), Revision: 1},
		{DocID: "", Revision: 2},
	}
	targets[1].DocID = targets[0].DocID
	markdown := []string{
		recallFixtureMarkdown(t, proposal, targets[0], []Event{first}),
		recallFixtureMarkdown(t, proposal, targets[1], []Event{first, correction}),
	}
	for index := range targets {
		targets[index].SHA256 = hash([]byte(markdown[index]))
	}
	var phase atomic.Int32
	var readRequests atomic.Int32
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		seen := map[string]bool{}
		receiptSent := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !receiptSent {
				pending := filepath.Join(options.DataRoot, ".mdsys/ephy/outbox/v2/pending", proposal.CandidateID+".json")
				if raw, readErr := os.ReadFile(pending); readErr == nil && bytes.Equal(raw, proposalBytes) {
					receipt := Receipt{SchemaVersion: Version, CandidateID: proposal.CandidateID, ProposalHash: proposalDigest, Status: "accepted", Applied: targets[0], EventIDs: []string{first.EventID}, Adoption: Adoption{Mode: "policy", ActorID: proposal.Actor.ID, PolicyID: proposal.PolicyID, PolicyRevision: proposal.PolicyRevision, Decision: "scope_allowed"}}
					body, _ := json.Marshal(receipt)
					if atomicWrite(root, ".mdsys/ephy/outbox/v2/receipts/"+proposal.CandidateID+".json", body) == nil {
						receiptSent = true
					}
				}
			}
			files, _ := filepath.Glob(filepath.Join(options.DataRoot, ".mdsys/context/v2/requests/*.json"))
			for _, path := range files {
				if seen[path] {
					continue
				}
				raw, readErr := os.ReadFile(path)
				if readErr != nil {
					continue
				}
				var q struct {
					RequestID string `json:"request_id"`
					Operation string `json:"operation"`
				}
				if json.Unmarshal(raw, &q) != nil {
					continue
				}
				seen[path] = true
				step := phase.Load()
				index := 0
				if step >= 1 {
					index = 1
				}
				result := ReadResult{Target: targets[index], Record: proposal.Record, ScopeID: s.settings.ScopeID, State: "active", SourceRefs: []SourceRef{}}
				status := "ok"
				if q.Operation == "read" {
					readRequests.Add(1)
					if step == 2 {
						status = "not_available"
					} else {
						result.Markdown = markdown[index]
						result.Events = []Event{first}
						if index == 1 {
							result.Events = append(result.Events, correction)
						}
						if step == 3 {
							result.Events[0].ScopeID = uuid.NewString()
						}
						if step == 4 {
							result.Events[1].Corrects = &EventRef{EventID: first.EventID, EventRevision: 99}
						}
						if step == 5 {
							result.Events[1].Revision = 2
						}
						// These mutations remain structurally valid while the
						// canonical Markdown and its Target hash are unchanged.
						if step == 6 {
							result.Events[1].EventID = uuid.NewString()
						}
						if step == 7 {
							result.Events[1].Text = "The rehearsal starts at 23:00 on Tuesday."
						}
						if step == 8 {
							result.Events[1].TurnID = uuid.NewString()
						}
						if step == 9 {
							result.Events = result.Events[:1]
						}
						if step == 10 {
							result.Events[0].EventID = uuid.NewString()
							result.Events[1].Corrects = &EventRef{EventID: result.Events[0].EventID, EventRevision: 1}
						}
					}
				}
				results := []ReadResult{result}
				if status != "ok" {
					results = []ReadResult{}
				}
				body, _ := json.Marshal(readResponse{ProtocolVersion: Version, RequestID: q.RequestID, Status: status, Results: results})
				_ = atomicWrite(root, ".mdsys/context/v2/responses/"+q.RequestID+".json", body)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer func() { close(stop); <-done }()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if !s.DispatchOnce(ctx) || s.Snapshot().Saved != 1 {
		t.Fatal("synthetic Runtime save was not receipt- and readback-verified")
	}
	// A new Store uses only its persisted synthetic credential and Karte state.
	s.Close()
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	check := func(want string, revision int64, eventID string) {
		t.Helper()
		sources, recallErr := restarted.Recall(ctx, "rehearsal", 5)
		if recallErr != nil || len(sources) != 1 {
			t.Fatalf("recall failed: %v, %+v", recallErr, sources)
		}
		if sources[0].Text != want || sources[0].Target.Revision != revision || sources[0].Event.EventID != eventID || strings.Contains(sources[0].Text, "PRIVATE_CANARY") {
			t.Fatalf("wrong current source: %+v", sources[0])
		}
	}
	check(first.Text, 1, first.EventID)
	phase.Store(1)
	check(correction.Text, 2, correctionID)
	phase.Store(2)
	if sources, recallErr := restarted.Recall(ctx, "rehearsal", 5); recallErr == nil || len(sources) != 0 {
		t.Fatalf("read failure disclosed memory: %v, %+v", recallErr, sources)
	}
	badPhases := []int32{3, 4, 5, 6, 7, 8, 9, 10}
	for _, bad := range badPhases {
		phase.Store(bad)
		if sources, recallErr := restarted.Recall(ctx, "rehearsal", 5); recallErr == nil || len(sources) != 0 {
			t.Fatalf("invalid event binding disclosed memory: %v, %+v", recallErr, sources)
		}
	}
	if readRequests.Load() != int32(4+len(badPhases)) {
		t.Fatal("search result was used without read")
	}
	phase.Store(1)
	if _, err = restarted.Configure(ctx, ConfigureRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	check(correction.Text, 2, correctionID)
	if err = atomicWrite(root, ".mdsys/context/v2/capabilities.json", capabilitiesJSON(false)); err != nil {
		t.Fatal(err)
	}
	if sources, recallErr := restarted.Recall(ctx, "rehearsal", 5); recallErr == nil || len(sources) != 0 {
		t.Fatalf("revoked source disclosed: %v, %+v", recallErr, sources)
	}
}
