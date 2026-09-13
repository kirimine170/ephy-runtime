package recording

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
)

type capabilities struct {
	ProtocolVersion string   `json:"protocol_version"`
	RecordSchema    string   `json:"record_schema"`
	Enabled         bool     `json:"enabled"`
	Operations      []string `json:"operations"`
	Policies        []struct {
		ScopeID      string `json:"scope_id"`
		PolicyID     string `json:"policy_id"`
		Revision     int64  `json:"policy_revision"`
		ConsentEpoch int64  `json:"consent_epoch"`
		Generation   int64  `json:"scope_generation"`
	} `json:"policies"`
}
type credential struct {
	ProducerID string `json:"producer_instance_id"`
	KeyID      string `json:"key_id"`
	Key        string `json:"key"`
}
type readRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	RequestID       string `json:"request_id"`
	Operation       string `json:"operation"`
	ScopeID         string `json:"scope_id"`
	ProducerID      string `json:"producer_instance_id"`
	Actor           Actor  `json:"actor"`
	PolicyID        string `json:"policy_id"`
	PolicyRevision  int64  `json:"policy_revision"`
	ConsentEpoch    int64  `json:"consent_epoch"`
	ScopeGeneration int64  `json:"scope_generation"`
	CreatedAt       string `json:"created_at"`
	Auth            Auth   `json:"auth"`
	Query           any    `json:"query"`
	Target          Target `json:"target"`
}
type ReadResult struct {
	Target     Target      `json:"target"`
	Record     RecordSpec  `json:"record"`
	ScopeID    string      `json:"scope_id"`
	State      string      `json:"state"`
	SourceRefs []SourceRef `json:"source_refs"`
	Markdown   string      `json:"markdown,omitempty"`
	Events     []Event     `json:"events,omitempty"`
	Derivation *Derivation `json:"derivation,omitempty"`
}
type readResponse struct {
	ProtocolVersion string       `json:"protocol_version"`
	RequestID       string       `json:"request_id"`
	Status          string       `json:"status"`
	Results         []ReadResult `json:"results"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func sign(v any, key []byte) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, e
	}
	var auth map[string]string
	if e = json.Unmarshal(m["auth"], &auth); e != nil {
		return nil, e
	}
	delete(auth, "mac")
	m["auth"], _ = json.Marshal(auth)
	b, e := encodeCanonical(m)
	if e != nil {
		return nil, e
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	auth["mac"] = hex.EncodeToString(mac.Sum(nil))
	m["auth"], _ = json.Marshal(auth)
	return encodeCanonical(m)
}
func proposalHash(raw []byte) (string, error) {
	var m map[string]json.RawMessage
	if e := json.Unmarshal(raw, &m); e != nil {
		return "", e
	}
	var a map[string]string
	if e := json.Unmarshal(m["auth"], &a); e != nil {
		return "", e
	}
	delete(a, "mac")
	m["auth"], _ = json.Marshal(a)
	b, e := encodeCanonical(m)
	return hash(b), e
}
func (s *Store) loadCredential() (credential, []byte, error) {
	var c credential
	b, e := readLimited(s.root, "producer.json", 4096)
	if e != nil {
		return c, nil, errors.New("recording_credential_unavailable")
	}
	if e = strict(b, &c); e != nil || c.ProducerID != s.settings.ProducerID || !tokenID(c.KeyID) {
		return c, nil, errors.New("recording_credential_unavailable")
	}
	key, e := hex.DecodeString(c.Key)
	if e != nil || len(key) != 32 {
		return c, nil, errors.New("recording_credential_unavailable")
	}
	return c, key, nil
}
func loadCapabilities(root *os.Root, settings Settings) (capabilities, error) {
	var c capabilities
	b, e := readLimited(root, ".mdsys/context/v2/capabilities.json", 1<<20)
	if e != nil {
		return c, errors.New("karte_offline")
	}
	if e = strict(b, &c); e != nil || c.ProtocolVersion != Version || c.RecordSchema != Version {
		return c, errors.New("unsupported_protocol")
	}
	for _, required := range []string{"create_record", "append_events", "read"} {
		found := false
		for _, op := range c.Operations {
			found = found || op == required
		}
		if !found {
			return c, errors.New("unsupported_protocol")
		}
	}
	if !settings.Configured {
		return c, nil
	}
	for _, p := range c.Policies {
		if p.ScopeID == settings.ScopeID && p.PolicyID == settings.PolicyID && p.Revision == 1 && p.ConsentEpoch == 1 && p.Generation == 1 && c.Enabled {
			return c, nil
		}
	}
	return c, errors.New("permission_blocked")
}

// Configure is invoked only by the local human settings UI．The model tool
// catalog has no recording control API，credential or access to either root．
func (s *Store) Configure(ctx context.Context, r ConfigureRequest) (Status, error) {
	if !r.Enabled {
		s.mu.Lock()
		s.settings.Enabled = false
		s.settings.Epoch++
		err := s.saveSettings()
		s.mu.Unlock()
		return s.Snapshot(), err
	}
	s.dispatchMu.Lock()
	s.mu.Lock()
	finish := func(e error) (Status, error) { s.mu.Unlock(); s.dispatchMu.Unlock(); return s.Snapshot(), e }
	if s.code == "recording_storage_failed" {
		return finish(errors.New("recording_restart_required"))
	}
	rootPath, e := filepath.Abs(r.DataRoot)
	if e != nil {
		return finish(errors.New("invalid_data_root"))
	}
	rootPath, e = filepath.EvalSymlinks(rootPath)
	if e != nil {
		return finish(errors.New("invalid_data_root"))
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`).MatchString(r.Project) {
		return finish(errors.New("invalid_recording_project"))
	}
	if _, e = time.LoadLocation(r.Timezone); e != nil {
		return finish(errors.New("invalid_recording_timezone"))
	}
	if s.settings.Configured && (rootPath != s.settings.DataRoot || r.Project != s.settings.Project || r.Timezone != s.settings.Timezone) {
		return finish(errors.New("recording_destination_locked"))
	}
	if s.options.Prepare == nil {
		return finish(errors.New("recording_access_guard_unavailable"))
	}
	if e = s.options.Prepare(ctx, rootPath); e != nil {
		return finish(errors.New("recording_access_guard_unavailable"))
	}
	root, e := os.OpenRoot(rootPath)
	if e != nil {
		return finish(errors.New("invalid_data_root"))
	}
	defer root.Close()
	if _, e = loadCapabilities(root, Settings{}); e != nil {
		return finish(e)
	}
	if !s.settings.Configured {
		if s.settings.ScopeID == "" {
			s.settings.ScopeID = uuid.NewString()
			s.settings.ProducerID = uuid.NewString()
			s.settings.PolicyID = uuid.NewString()
		}
		s.settings.DataRoot = rootPath
		s.settings.Project = r.Project
		s.settings.Timezone = r.Timezone
		s.settings.Enabled = false
		if e = s.saveSettings(); e != nil {
			return finish(e)
		}
		grant := map[string]any{"schema_version": Version, "policy_id": s.settings.PolicyID, "policy_revision": 1, "enabled": true, "user_id": "local-user", "actor_id": "ephy-runtime", "producer_instance_id": s.settings.ProducerID, "scope_id": s.settings.ScopeID, "storage_area_id": "runtime-conversations", "project": r.Project, "records": map[string]any{"conversation": map[string]any{"kind": "note", "sensitivity": "internal", "tags": []string{"ephy:conversation"}, "provenance_types": []string{"ephy"}}}, "operations": []string{"create_record", "append_events"}, "denied_tags": []string{}, "valid_from": s.options.Now().Add(-time.Minute).Format(time.RFC3339), "consent_epoch": 1, "scope_generation": 1, "storage": true, "training": false, "external_transfer": false, "key_id": "runtime-v2"}
		if e = s.saveJSON("grant.json", grant); e != nil {
			return finish(e)
		}
		if s.options.ControlPath == "" {
			return finish(errors.New("karte_control_unavailable"))
		}
		// A crash after Configure may leave a valid registration．Check it before
		// retrying so a retry never rotates or silently re-enables a revoked grant．
		cmd := s.control(ctx, rootPath, "capabilities")
		b, err := cmd.Output()
		var c capabilities
		registered := false
		if err == nil && strict(b, &c) == nil {
			for _, p := range c.Policies {
				if p.ScopeID == s.settings.ScopeID && p.PolicyID == s.settings.PolicyID && p.Revision == 1 && p.ConsentEpoch == 1 && p.Generation == 1 {
					registered = true
				}
			}
		}
		if !registered {
			// Existing producer credential means a previous registration attempt．
			// Configure rejects stale revisions itself；never increment them here．
			cmd = s.control(ctx, rootPath, "-grant", filepath.Join(s.options.Home, "grant.json"), "-producer-credential", filepath.Join(s.options.Home, "producer.json"), "configure")
			if e = cmd.Run(); e != nil {
				return finish(errors.New("karte_registration_failed"))
			}
		}
		s.settings.Configured = true
		if e = s.saveSettings(); e != nil {
			return finish(e)
		}
	} else if _, e = loadCapabilities(root, s.settings); e != nil {
		return finish(e)
	}
	if !s.settings.Enabled {
		s.settings.Epoch++
	}
	s.settings.Enabled = true
	s.code = ""
	return finish(s.saveSettings())
}

func (s *Store) buildProposal(t *turn, d *delivery, key []byte, keyID string) error {
	e := d.Event
	head := s.heads[t.ConversationID]
	segment := head.Segment
	spec := head.Spec
	target := &head.Target
	op := "append_events"
	// HTML escaping can expand each text byte sixfold．Reserve metadata too．
	eventBytes, _ := json.Marshal(e)
	estimate := len(e.Text)*6 + len(eventBytes)*2 + 8192
	if segment == 0 || head.Events >= 256 || head.Bytes+estimate > 1<<20 || spec.LocalDate != e.LocalDate || spec.Timezone != e.Timezone {
		segment++
		op = "create_record"
		target = nil
		spec = RecordSpec{Type: "conversation", Title: "Ephy conversation " + e.LocalDate, ConversationID: t.ConversationID, Segment: segment, Timezone: e.Timezone, LocalDate: e.LocalDate}
	}
	p := Proposal{SchemaVersion: Version, CandidateID: uuid.NewString(), Operation: op, LogicalKey: fmt.Sprintf("conversation:%s:%d", t.ConversationID, segment), ScopeID: s.settings.ScopeID, ProducerID: s.settings.ProducerID, Actor: Actor{Type: "ephy", ID: "ephy-runtime"}, PolicyID: s.settings.PolicyID, PolicyRevision: 1, ConsentEpoch: 1, ScopeGeneration: 1, Record: spec, Target: target, Events: []Event{*e}, CreatedAt: s.options.Now().Format(time.RFC3339Nano), Auth: Auth{KeyID: keyID}}
	b, err := sign(p, key)
	if err != nil {
		return err
	}
	previousProposal, previousRecord := d.Proposal, d.Record
	d.Proposal = b
	d.Record = spec
	if err := s.saveTurn(t); err != nil {
		d.Proposal, d.Record = previousProposal, previousRecord
		return err
	}
	return nil
}
func waitFile(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, e := readLimited(root, name, limit)
		if e == nil {
			return b, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("karte_offline")
		case <-ticker.C:
		}
	}
}
func (s *Store) read(ctx context.Context, root *os.Root, settings Settings, target Target, key []byte, keyID string) (ReadResult, error) {
	var result ReadResult
	if _, e := loadCapabilities(root, settings); e != nil {
		return result, e
	}
	id := uuid.NewString()
	q := readRequest{ProtocolVersion: Version, RequestID: id, Operation: "read", ScopeID: settings.ScopeID, ProducerID: settings.ProducerID, Actor: Actor{Type: "ephy", ID: "ephy-runtime"}, PolicyID: settings.PolicyID, PolicyRevision: 1, ConsentEpoch: 1, ScopeGeneration: 1, CreatedAt: s.options.Now().Format(time.RFC3339Nano), Auth: Auth{KeyID: keyID}, Target: target}
	b, e := sign(q, key)
	if e != nil {
		return result, e
	}
	requestPath := ".mdsys/context/v2/requests/" + id + ".json"
	responsePath := ".mdsys/context/v2/responses/" + id + ".json"
	if e = atomicWrite(root, requestPath, b); e != nil {
		return result, e
	}
	defer removeFile(root, responsePath)
	raw, e := waitFile(ctx, root, responsePath, 2<<20)
	if e != nil {
		return result, e
	}
	var response readResponse
	if e = strict(raw, &response); e != nil || response.ProtocolVersion != Version || response.RequestID != id {
		return result, errors.New("invalid_readback")
	}
	if response.Status != "ok" || len(response.Results) != 1 {
		return result, errors.New("permission_blocked")
	}
	result = response.Results[0]
	if result.Target != target || result.ScopeID != settings.ScopeID || hash([]byte(result.Markdown)) != target.SHA256 {
		return result, errors.New("invalid_readback")
	}
	if _, e = loadCapabilities(root, settings); e != nil {
		return ReadResult{}, e
	}
	return result, nil
}
func (s *Store) Read(ctx context.Context, target Target) (ReadResult, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	s.mu.Lock()
	settings := s.settings
	c, key, e := s.loadCredential()
	s.mu.Unlock()
	if e != nil {
		return ReadResult{}, e
	}
	root, e := os.OpenRoot(settings.DataRoot)
	if e != nil {
		return ReadResult{}, errors.New("karte_offline")
	}
	defer root.Close()
	return s.read(ctx, root, settings, target, key, c.KeyID)
}
func (s *Store) attempt(ctx context.Context, t *turn, d *delivery) error {
	s.mu.Lock()
	settings := s.settings
	c, key, e := s.loadCredential()
	s.mu.Unlock()
	if e != nil {
		return e
	}
	if s.options.Prepare == nil || s.options.Prepare(ctx, settings.DataRoot) != nil {
		return errors.New("recording_access_guard_unavailable")
	}
	root, e := os.OpenRoot(settings.DataRoot)
	if e != nil {
		return errors.New("karte_offline")
	}
	defer root.Close()
	if _, e = loadCapabilities(root, settings); e != nil {
		return e
	}
	s.mu.Lock()
	if len(d.Proposal) == 0 {
		e = s.buildProposal(t, d, key, c.KeyID)
	}
	raw := append([]byte{}, d.Proposal...)
	s.mu.Unlock()
	if e != nil {
		return errors.New("recording_storage_failed")
	}
	var p Proposal
	if e = strict(raw, &p); e != nil {
		return errors.New("invalid_proposal")
	}
	expectedHash, e := proposalHash(raw)
	if e != nil {
		return e
	}
	receiptName := ".mdsys/ephy/outbox/v2/receipts/" + p.CandidateID + ".json"
	rawReceipt, e := readLimited(root, receiptName, 64<<10)
	if errors.Is(e, os.ErrNotExist) {
		if e = atomicWrite(root, ".mdsys/ephy/outbox/v2/pending/"+p.CandidateID+".json", raw); e != nil {
			return errors.New("karte_write_failed")
		}
		// A receipt and a rejection can race with shutdown；an accepted receipt
		// wins only after complete identity checks and a fresh authorized read．
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			rawReceipt, e = readLimited(root, receiptName, 64<<10)
			if e == nil {
				break
			}
			if !errors.Is(e, os.ErrNotExist) {
				return errors.New("invalid_receipt")
			}
			if b, err := readLimited(root, ".mdsys/ephy/outbox/v2/rejected/"+p.CandidateID+".result.json", 64<<10); err == nil {
				var r struct {
					SchemaVersion string `json:"schema_version"`
					CandidateID   string `json:"candidate_id"`
					Status        string `json:"status"`
					Code          string `json:"code"`
				}
				if strict(b, &r) == nil && r.CandidateID == p.CandidateID && tokenID(r.Code) {
					return errors.New(r.Code)
				}
				return errors.New("invalid_receipt")
			}
			select {
			case <-ctx.Done():
				return errors.New("karte_offline")
			case <-ticker.C:
			}
		}
	} else if e != nil {
		return errors.New("invalid_receipt")
	}
	var receipt Receipt
	docID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("karte-record-v2\x00"+p.ScopeID+"\x00"+p.ProducerID+"\x00"+p.LogicalKey)).String()
	rev := int64(1)
	if p.Target != nil {
		rev = p.Target.Revision + 1
	}
	if strict(rawReceipt, &receipt) != nil || receipt.SchemaVersion != Version || receipt.CandidateID != p.CandidateID || receipt.ProposalHash != expectedHash || receipt.Status != "accepted" || receipt.Applied.DocID != docID || receipt.Applied.Revision != rev || len(receipt.EventIDs) != 1 || receipt.EventIDs[0] != d.EventID || receipt.Adoption != (Adoption{Mode: "policy", ActorID: p.Actor.ID, PolicyID: p.PolicyID, PolicyRevision: p.PolicyRevision, Decision: "scope_allowed"}) {
		return errors.New("invalid_receipt")
	}
	readback, e := s.read(ctx, root, settings, receipt.Applied, key, c.KeyID)
	if e != nil {
		return e
	}
	expectedEvent, _ := encodeCanonical(p.Events[0])
	matches := 0
	for _, v := range readback.Events {
		if v.EventID == d.EventID {
			b, err := encodeCanonical(v)
			if err != nil || !bytes.Equal(b, expectedEvent) {
				return errors.New("invalid_readback")
			}
			matches++
		}
	}
	if matches != 1 || readback.Record != p.Record {
		return errors.New("invalid_readback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The verified receipt is durable before advancing the head or deleting
	// source bytes．Recovery can finish any of these local crash boundaries．
	previous := *d
	d.Receipt = &receipt
	d.RecordBytes = len(readback.Markdown)
	d.RecordEvents = len(readback.Events)
	d.Verified = true
	d.State = "canonical_saved"
	d.Code = ""
	d.Event = nil
	d.Proposal = nil
	if e = s.saveTurn(t); e != nil {
		*d = previous
		return errors.New("recording_storage_failed")
	}
	s.advanceHead(t, d)
	if e = s.saveHeads(); e != nil {
		return errors.New("recording_storage_failed")
	}
	if e = s.cleanup(t); e != nil {
		return errors.New("recording_storage_failed")
	}
	return nil
}

// DispatchOnce serializes each conversation globally across day/capacity
// segments．Independent conversations can proceed past a blocked predecessor．
func (s *Store) DispatchOnce(ctx context.Context) bool {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	s.mu.Lock()
	type work struct {
		t *turn
		d *delivery
	}
	byConversation := map[string]work{}
	for _, t := range s.turns {
		for _, d := range t.Items {
			if d.Verified {
				continue
			}
			old, ok := byConversation[t.ConversationID]
			if !ok || d.Seq < old.d.Seq {
				byConversation[t.ConversationID] = work{t, d}
			}
		}
	}
	keys := []string{}
	for k := range byConversation {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var selected *work
	for _, k := range keys {
		w := byConversation[k]
		if w.d.State == "permission_blocked" || w.d.State == "conflict" || w.d.State == "save_failed" || w.d.Next.After(s.options.Now()) {
			continue
		}
		selected = &w
		break
	}
	if selected == nil || s.closed || s.code == "recording_storage_failed" {
		s.mu.Unlock()
		return false
	}
	t, d := selected.t, selected.d
	d.State = "delivery_pending"
	if err := s.saveTurn(t); err != nil {
		_ = s.pause("recording_storage_failed")
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	e := s.attempt(attemptCtx, t, d)
	cancel()
	if e == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	code := e.Error()
	if !tokenID(code) {
		code = "recording_delivery_failed"
	}
	d.Code = code
	d.Attempts++
	switch code {
	case "permission_blocked", "permission_denied", "stale_consent", "scope_disabled", "not_available", "configuration_required":
		d.State = "permission_blocked"
		_ = s.pause("permission_blocked")
	case "conflict", "record_identity_changed", "id_reuse", "event_sequence_gap", "classification_changed":
		d.State = "conflict"
	case "unsupported_protocol", "invalid_receipt", "invalid_readback", "invalid_proposal", "recording_storage_failed", "recording_credential_unavailable":
		d.State = "save_failed"
		if code == "recording_storage_failed" {
			_ = s.pause(code)
		}
	case "karte_offline", "karte_write_failed", "recording_access_guard_unavailable", "recording_delivery_failed":
		d.State = "delivery_pending"
		seconds := 5 << min(d.Attempts-1, 6)
		if seconds > 300 {
			seconds = 300
		}
		if d.Attempts >= 8 {
			seconds = 900
			d.Attempts = 0
		}
		// Stable bounded jitter，without storing random contents in diagnostics．
		jitter := time.Duration(s.options.Now().UnixNano()%1000) * time.Millisecond
		d.Next = s.options.Now().Add(time.Duration(seconds)*time.Second + jitter)
	default:
		d.State = "save_failed"
	}
	if err := s.saveTurn(t); err != nil {
		s.code = "recording_storage_failed"
	}
	return true
}
func (s *Store) Retry() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == "recording_storage_failed" {
		return errors.New("recording_restart_required")
	}
	s.code = ""
	for _, t := range s.turns {
		for _, d := range t.Items {
			if !d.Verified {
				d.State = "locally_preserved"
				d.Next = time.Time{}
				d.Attempts = 0
				d.Code = ""
			}
		}
		if e := s.saveTurn(t); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil || s.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for i := 0; i < 20 && ctx.Err() == nil; i++ {
					if !s.DispatchOnce(ctx) {
						break
					}
				}
			}
		}
	}()
}

func (s *Store) control(ctx context.Context, root string, args ...string) *exec.Cmd {
	all := []string{"-data-root", root}
	if s.options.KarteConfigRoot != "" {
		all = append(all, "-config-root", s.options.KarteConfigRoot)
	}
	return exec.CommandContext(ctx, s.options.ControlPath, append(all, args...)...)
}
