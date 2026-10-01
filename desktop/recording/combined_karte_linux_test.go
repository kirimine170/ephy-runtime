//go:build linux && karte_integration

package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This fixture runs Runtime stages in separate OS processes. The only Karte
// receiver is the real karte-ephy-control process command, using its native
// ProcessPending, receipt, record, and policy stores under one temporary root.
type combinedState struct {
	Original       Target `json:"original"`
	Corrected      Target `json:"corrected"`
	EventID        string `json:"event_id"`
	CorrectionID   string `json:"correction_id"`
	ConversationID string `json:"conversation_id"`
}

func TestCombinedKarteRuntimeProcessRoundTrip(t *testing.T) {
	phase := os.Getenv("EPHY_COMBINED_PHASE")
	if phase != "" {
		combinedChild(t, phase)
		return
	}
	control := os.Getenv("EPHY_KARTE_CONTROL_BIN")
	if control == "" {
		t.Fatal("EPHY_KARTE_CONTROL_BIN must name the bundled real Karte control binary")
	}
	base := t.TempDir()
	for _, dir := range []string{"data", "private-config", "runtime-home"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	process := func(args ...string) error {
		all := []string{"-data-root", filepath.Join(base, "data"), "-config-root", filepath.Join(base, "private-config")}
		all = append(all, args...)
		cmd := exec.CommandContext(ctx, control, all...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Karte control %s: %w: %s", args[len(args)-1], err, out)
		}
		return nil
	}
	if err := process("process"); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	workerError := make(chan error, 1)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := process("process"); err != nil {
					select {
					case workerError <- err:
					default:
					}
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	run := func(stage string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCombinedKarteRuntimeProcessRoundTrip$", "-test.v")
		cmd.Env = append(os.Environ(), "EPHY_COMBINED_PHASE="+stage, "EPHY_COMBINED_BASE="+base, "EPHY_KARTE_CONTROL_BIN="+control)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Runtime stage %s: %v: %s", stage, err, out)
		}
		select {
		case err := <-workerError:
			t.Fatal(err)
		default:
		}
	}
	run("save")
	run("recall_original") // the writer process is already gone
	run("append_correction")
	run("recall_corrected")
	grantPath := filepath.Join(base, "runtime-home", "grant.json")
	raw, err := os.ReadFile(grantPath)
	if err != nil {
		t.Fatal(err)
	}
	var grant map[string]any
	if err = json.Unmarshal(raw, &grant); err != nil {
		t.Fatal(err)
	}
	grant["enabled"] = false
	grant["policy_revision"] = 2
	grant["consent_epoch"] = 2
	raw, err = json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	revokedGrant := filepath.Join(base, "runtime-home", "revoked-grant.json")
	if err = os.WriteFile(revokedGrant, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = process("-grant", revokedGrant, "-producer-credential", filepath.Join(base, "runtime-home", "producer.json"), "configure"); err != nil {
		t.Fatal(err)
	}
	if err = process("process"); err != nil {
		t.Fatal(err)
	}
	run("revoked")
}

func combinedChild(t *testing.T, phase string) {
	base := os.Getenv("EPHY_COMBINED_BASE")
	control := os.Getenv("EPHY_KARTE_CONTROL_BIN")
	if base == "" || control == "" {
		t.Fatal("missing synthetic fixture paths")
	}
	data := filepath.Join(base, "data")
	options := Options{Home: filepath.Join(base, "runtime-home"), DataRoot: data, ControlPath: control, KarteConfigRoot: filepath.Join(base, "private-config")}
	options.Prepare = func(_ context.Context, path string) error {
		got, err := filepath.EvalSymlinks(path)
		want, wantedErr := filepath.EvalSymlinks(data)
		if err != nil || wantedErr != nil || got != want {
			return errors.New("fixture_root_denied")
		}
		return nil
	}
	options.Now = func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) }
	s, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	statePath := filepath.Join(base, "state.json")
	var state combinedState
	if phase != "save" {
		raw, readErr := os.ReadFile(statePath)
		if readErr != nil || json.Unmarshal(raw, &state) != nil {
			t.Fatalf("state unavailable: %v", readErr)
		}
	}
	saveState := func() {
		t.Helper()
		raw, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(statePath, raw, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	switch phase {
	case "save":
		if _, err = s.Configure(ctx, ConfigureRequest{DataRoot: data, Project: "synthetic-memory", Timezone: "UTC", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		state.ConversationID = uuid.NewString()
		key, beginErr := s.Begin("synthetic-input", state.ConversationID, "The rehearsal starts at 09:00 on Tuesday.", "text", nil)
		if beginErr != nil || key == "" {
			t.Fatalf("Begin: %v", beginErr)
		}
		state.EventID = s.turns[key].Items[0].EventID
		if !s.DispatchOnce(ctx) || s.Snapshot().Saved != 1 {
			t.Fatalf("real Karte receipt/readback absent: %+v", s.Snapshot())
		}
		state.Original = s.heads[state.ConversationID].Target
		if !validUUID(state.Original.DocID) || state.Original.Revision != 1 || len(state.Original.SHA256) != 64 {
			t.Fatal("invalid saved target")
		}
		saveState()
	case "recall_original", "recall_corrected":
		sources, recallErr := s.Recall(ctx, "rehearsal", 5)
		if recallErr != nil || len(sources) != 1 {
			t.Fatalf("real Karte recall: %v, %+v", recallErr, sources)
		}
		wantText, wantTarget, wantEvent := "The rehearsal starts at 09:00 on Tuesday.", state.Original, state.EventID
		if phase == "recall_corrected" {
			wantText, wantTarget, wantEvent = "The rehearsal starts at 10:00 on Tuesday.", state.Corrected, state.CorrectionID
		}
		if sources[0].Text != wantText || sources[0].Target != wantTarget || sources[0].Event.EventID != wantEvent || sources[0].ConversationID != state.ConversationID {
			t.Fatalf("wrong current assertion or citation: %+v", sources[0])
		}
	case "append_correction":
		original, readErr := s.Read(ctx, state.Original)
		if readErr != nil || len(original.Events) != 1 {
			t.Fatalf("original read: %v", readErr)
		}
		credential, key, loadErr := s.loadCredential()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		state.CorrectionID = uuid.NewString()
		originalEvent := original.Events[0]
		correction := Event{ConversationID: state.ConversationID, ScopeID: s.settings.ScopeID, ProducerID: s.settings.ProducerID, EventID: state.CorrectionID, Seq: originalEvent.Seq + 1, Revision: 1, TurnID: originalEvent.TurnID, Type: "correction", Text: "The rehearsal starts at 10:00 on Tuesday.", OccurredAt: options.Now().Format(time.RFC3339), Timezone: "UTC", LocalDate: original.Record.LocalDate, ConsentEpoch: 1, Corrects: &EventRef{EventID: state.EventID, EventRevision: 1}, CorrectionReason: "user_content"}
		proposal := Proposal{SchemaVersion: Version, CandidateID: uuid.NewString(), Operation: "append_events", LogicalKey: "conversation:" + state.ConversationID + ":1", ScopeID: s.settings.ScopeID, ProducerID: s.settings.ProducerID, Actor: Actor{Type: "ephy", ID: "ephy-runtime"}, PolicyID: s.settings.PolicyID, PolicyRevision: 1, ConsentEpoch: 1, ScopeGeneration: 1, Record: original.Record, Target: &state.Original, Events: []Event{correction}, CreatedAt: options.Now().Format(time.RFC3339), Auth: Auth{KeyID: credential.KeyID}}
		raw, signErr := sign(proposal, key)
		if signErr != nil {
			t.Fatal(signErr)
		}
		root, openErr := os.OpenRoot(data)
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer root.Close()
		if writeErr := atomicWrite(root, ".mdsys/ephy/outbox/v2/pending/"+proposal.CandidateID+".json", raw); writeErr != nil {
			t.Fatal(writeErr)
		}
		receiptRaw, waitErr := waitFile(ctx, root, ".mdsys/ephy/outbox/v2/receipts/"+proposal.CandidateID+".json", 64<<10)
		if waitErr != nil {
			t.Fatal(waitErr)
		}
		var receipt Receipt
		if strict(receiptRaw, &receipt) != nil || receipt.Status != "accepted" || receipt.CandidateID != proposal.CandidateID || receipt.Applied.DocID != state.Original.DocID || receipt.Applied.Revision != 2 || len(receipt.EventIDs) != 1 || receipt.EventIDs[0] != state.CorrectionID {
			t.Fatalf("correction receipt invalid: %+v", receipt)
		}
		state.Corrected = receipt.Applied
		current, readErr := s.Read(ctx, state.Corrected)
		if readErr != nil || len(current.Events) != 2 || current.Events[1].Text != correction.Text {
			t.Fatalf("correction readback: %v", readErr)
		}
		saveState()
	case "revoked":
		sources, recallErr := s.Recall(ctx, "rehearsal", 5)
		if recallErr == nil || len(sources) != 0 {
			t.Fatalf("revoked recall disclosed body: %v, %+v", recallErr, sources)
		}
		readback, readErr := s.Read(ctx, state.Corrected)
		if readErr == nil || readback.Markdown != "" || len(readback.Events) != 0 {
			t.Fatalf("revoked read disclosed body: %v, %+v", readErr, readback)
		}
	default:
		t.Fatal("unknown combined phase: " + strings.TrimSpace(phase))
	}
}
