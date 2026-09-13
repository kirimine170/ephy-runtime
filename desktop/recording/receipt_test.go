package recording

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReceiptAndReadbackMismatchNeverReportSaved(t *testing.T) {
	for _, kind := range []string{"candidate", "proposal_hash", "event", "revision", "adoption", "readback_hash", "readback_event"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := testStore(t)
			s.options.Prepare = func(context.Context, string) error { return nil }
			data := s.settings.DataRoot
			if e := os.MkdirAll(data, 0700); e != nil {
				t.Fatal(e)
			}
			root, e := os.OpenRoot(data)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			key := make([]byte, 32)
			if e = s.saveJSON("producer.json", credential{ProducerID: s.settings.ProducerID, KeyID: "fixture", Key: hex.EncodeToString(key)}); e != nil {
				t.Fatal(e)
			}
			cap := map[string]any{"protocol_version": Version, "record_schema": Version, "enabled": true, "operations": []string{"create_record", "append_events", "read"}, "policies": []any{map[string]any{"scope_id": s.settings.ScopeID, "policy_id": s.settings.PolicyID, "policy_revision": 1, "consent_epoch": 1, "scope_generation": 1}}}
			b, _ := json.Marshal(cap)
			if e = atomicWrite(root, ".mdsys/context/v2/capabilities.json", b); e != nil {
				t.Fatal(e)
			}
			id, e := s.Begin("one", s.settings.ConversationID, "synthetic user", "text", nil)
			if e != nil {
				t.Fatal(e)
			}
			tr := s.turns[id]
			d := tr.Items[0]
			if e = s.buildProposal(tr, d, key, "fixture"); e != nil {
				t.Fatal(e)
			}
			var p Proposal
			json.Unmarshal(d.Proposal, &p)
			digest, _ := proposalHash(d.Proposal)
			markdown := "synthetic canonical bytes"
			target := Target{DocID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("karte-record-v2\x00"+p.ScopeID+"\x00"+p.ProducerID+"\x00"+p.LogicalKey)).String(), Revision: 1, SHA256: hash([]byte(markdown))}
			receipt := Receipt{SchemaVersion: Version, CandidateID: p.CandidateID, ProposalHash: digest, Status: "accepted", Applied: target, EventIDs: []string{d.EventID}, Adoption: Adoption{Mode: "policy", ActorID: p.Actor.ID, PolicyID: p.PolicyID, PolicyRevision: 1, Decision: "scope_allowed"}}
			switch kind {
			case "candidate":
				receipt.CandidateID = uuid.NewString()
			case "proposal_hash":
				receipt.ProposalHash = hash([]byte("wrong"))
			case "event":
				receipt.EventIDs = []string{uuid.NewString()}
			case "revision":
				receipt.Applied.Revision = 2
			case "adoption":
				receipt.Adoption.Decision = "wrong"
			}
			b, _ = json.Marshal(receipt)
			if e = atomicWrite(root, ".mdsys/ephy/outbox/v2/receipts/"+p.CandidateID+".json", b); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						files, _ := filepath.Glob(filepath.Join(data, ".mdsys/context/v2/requests/*.json"))
						if len(files) == 0 {
							continue
						}
						raw, err := os.ReadFile(files[0])
						if err != nil {
							continue
						}
						var q readRequest
						if json.Unmarshal(raw, &q) != nil {
							continue
						}
						result := ReadResult{Target: target, Record: p.Record, ScopeID: p.ScopeID, State: "active", SourceRefs: []SourceRef{}, Markdown: markdown, Events: []Event{p.Events[0]}}
						if kind == "readback_hash" {
							result.Markdown = "changed bytes"
						}
						if kind == "readback_event" {
							result.Events[0].Text = "different user"
						}
						b, _ := json.Marshal(readResponse{ProtocolVersion: Version, RequestID: q.RequestID, Status: "ok", Results: []ReadResult{result}})
						_ = atomicWrite(root, ".mdsys/context/v2/responses/"+q.RequestID+".json", b)
						return
					}
				}
			}()
			s.DispatchOnce(ctx)
			cancel()
			<-done
			st := s.Snapshot()
			if st.Saved != 0 || st.Failed != 1 || d.Verified || d.Event == nil {
				t.Fatalf("mismatch accepted: %+v", st)
			}
			expected := "invalid_receipt"
			if kind == "readback_hash" || kind == "readback_event" {
				expected = "invalid_readback"
			}
			if d.Code != expected {
				t.Fatalf("unexpected rejection: %s", d.Code)
			}
		})
	}
}
