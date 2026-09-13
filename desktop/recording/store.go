// Package recording is a private，bounded delivery spool．Karte owns all reads
// of conversation content；the spool exposes only delivery metadata to the UI．
package recording

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxQueueBytes = 256 << 20
const maxPendingEvents = 10000
const maxTextBytes = 64 << 10

type Options struct {
	Home, DataRoot, ControlPath string
	KarteConfigRoot             string
	Prepare                     func(context.Context, string) error
	Now                         func() time.Time
	// No automatic dispatcher in deterministic tests．Production calls Start．
	MaxBytes int64
}
type Settings struct {
	Configured     bool   `json:"configured"`
	Enabled        bool   `json:"enabled"`
	DataRoot       string `json:"data_root"`
	Project        string `json:"project"`
	Timezone       string `json:"timezone"`
	ConversationID string `json:"conversation_id"`
	ScopeID        string `json:"scope_id"`
	ProducerID     string `json:"producer_instance_id"`
	PolicyID       string `json:"policy_id"`
	Epoch          int64  `json:"recording_epoch"`
}
type ConfigureRequest struct {
	DataRoot string `json:"data_root"`
	Project  string `json:"project"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}
type Head struct {
	Seq     int64      `json:"event_seq"`
	Segment int64      `json:"segment_no"`
	Spec    RecordSpec `json:"record"`
	Target  Target     `json:"target"`
	Events  int        `json:"events"`
	Bytes   int        `json:"bytes"`
}
type completion struct {
	ConversationID string `json:"conversation_id"`
	At             int64  `json:"at"`
}
type draft struct {
	SkipBytes int       `json:"skip_bytes"`
	Text      string    `json:"text"`
	State     Assistant `json:"assistant"`
	Complete  bool      `json:"complete"`
	Full      bool      `json:"confirmed_full"`
	Epoch     int64     `json:"epoch"`
}
type delivery struct {
	Seq          int64           `json:"seq"`
	EventID      string          `json:"event_id"`
	Event        *Event          `json:"event,omitempty"`
	Proposal     json.RawMessage `json:"proposal,omitempty"`
	Receipt      *Receipt        `json:"receipt,omitempty"`
	Record       RecordSpec      `json:"record"`
	RecordBytes  int             `json:"record_bytes"`
	RecordEvents int             `json:"record_events"`
	Verified     bool            `json:"verified"`
	State        string          `json:"state"`
	Code         string          `json:"code,omitempty"`
	Attempts     int             `json:"attempts"`
	Next         time.Time       `json:"next"`
}
type turn struct {
	Key            string      `json:"key"`
	ConversationID string      `json:"conversation_id"`
	TurnID         string      `json:"turn_id"`
	Draft          *draft      `json:"draft,omitempty"`
	Items          []*delivery `json:"items"`
}
type RecordStatus struct {
	ConversationID string `json:"conversation_id"`
	Saved          int64  `json:"saved"`
	Local          int    `json:"local"`
	Pending        int    `json:"pending"`
	Failed         int    `json:"failed"`
	State          string `json:"state"`
	Code           string `json:"code,omitempty"`
	Target         Target `json:"target"`
}
type Status struct {
	Settings        Settings       `json:"settings"`
	State           string         `json:"state"`
	Code            string         `json:"code,omitempty"`
	Saved           int64          `json:"saved"`
	Local           int            `json:"local"`
	Pending         int            `json:"pending"`
	Failed          int            `json:"failed"`
	QueueBytes      int64          `json:"queue_bytes"`
	CapacityWarning bool           `json:"capacity_warning"`
	Records         []RecordStatus `json:"records"`
}
type Store struct {
	mu         sync.Mutex
	dispatchMu sync.Mutex
	root       *os.Root
	lock       *os.File
	options    Options
	settings   Settings
	heads      map[string]Head
	completed  map[string]completion
	turns      map[string]*turn
	seq        map[string]int64
	bytes      int64
	code       string
	closed     bool
	cancel     context.CancelFunc
	done       chan struct{}
}

func New(o Options) (*Store, error) {
	if o.Home == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		o.Home = filepath.Join(base, "Ephy", "recording")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = maxQueueBytes
	}
	root, err := privateRoot(o.Home)
	if err != nil {
		return nil, err
	}
	lock, err := lockQueue(root)
	if err != nil {
		root.Close()
		return nil, errors.New("recording_already_running")
	}
	s := &Store{root: root, lock: lock, options: o, heads: map[string]Head{}, completed: map[string]completion{}, turns: map[string]*turn{}, seq: map[string]int64{}}
	s.settings = Settings{DataRoot: o.DataRoot, Project: "ephy-conversations", Timezone: "Asia/Tokyo", ConversationID: uuid.NewString(), Epoch: 1}
	for _, item := range []struct {
		name  string
		value any
	}{{"settings.json", &s.settings}, {"heads.json", &s.heads}, {"completed.json", &s.completed}} {
		b, err := readLimited(root, item.name, 16<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || json.Unmarshal(b, item.value) != nil {
			s.Close()
			return nil, errors.New("recording_state_unavailable")
		}
	}
	if err := s.validateState(); err != nil {
		s.Close()
		return nil, err
	}
	if err := root.MkdirAll("turns", 0700); err != nil {
		s.Close()
		return nil, err
	}
	d, err := root.Open("turns")
	if err != nil {
		s.Close()
		return nil, err
	}
	files, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		s.Close()
		return nil, err
	}
	for conv, head := range s.heads {
		s.seq[conv] = head.Seq
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".pending-") {
			_ = root.Remove(filepath.Join("turns", f.Name()))
			continue
		}
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := readLimited(root, filepath.Join("turns", f.Name()), 2<<20)
		var t turn
		if err != nil || json.Unmarshal(b, &t) != nil || t.Key+".json" != f.Name() || !validUUID(t.Key) || !validUUID(t.ConversationID) || !validUUID(t.TurnID) || !s.validateTurn(&t) {
			s.Close()
			return nil, errors.New("recording_queue_unavailable")
		}
		for _, item := range t.Items {
			if !item.Verified && item.State == "delivery_pending" {
				item.Next = time.Time{}
				item.Attempts = 0
			}
		}
		s.turns[t.Key] = &t
		for _, item := range t.Items {
			if item.Seq > s.seq[t.ConversationID] {
				s.seq[t.ConversationID] = item.Seq
			}
			if item.Verified {
				s.advanceHead(&t, item)
			}
		}
	}
	if err := s.saveHeads(); err != nil {
		s.Close()
		return nil, err
	}
	// A crash cannot prove completion or what the listener heard．The committed
	// checkpoint survives；a generation that already completed keeps that fact．
	keys := make([]string, 0, len(s.turns))
	for k := range s.turns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := s.turns[k]
		if t.Draft != nil {
			a := t.Draft.State
			a.Generation = "failed"
			if t.Draft.Complete {
				a.Generation = "completed"
			}
			if a.Playback != "completed" && a.Playback != "not_started" {
				a.Playback = "unknown"
			}
			for i := range a.SpeechUnits {
				if a.SpeechUnits[i].State == "started" {
					a.SpeechUnits[i].State = "unknown"
				}
			}
			if err := s.finishLocked(t, a, false); err != nil {
				s.Close()
				return nil, err
			}
		}
		if err := s.cleanup(t); err != nil {
			s.Close()
			return nil, err
		}
	}
	if err := s.saveSettings(); err != nil {
		s.Close()
		return nil, err
	}
	s.recountBytes()
	return s, nil
}
func validUUID(id string) bool {
	u, e := uuid.Parse(id)
	return e == nil && u != uuid.Nil && u.String() == id
}
func (s *Store) saveJSON(name string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return atomicWrite(s.root, name, b)
}
func (s *Store) saveSettings() error    { return s.saveJSON("settings.json", s.settings) }
func (s *Store) saveHeads() error       { return s.saveJSON("heads.json", s.heads) }
func (s *Store) saveTurn(t *turn) error { return s.saveJSON(filepath.Join("turns", t.Key+".json"), t) }
func (s *Store) recountBytes() {
	var total int64
	for _, dir := range []string{".", "turns"} {
		d, e := s.root.Open(dir)
		if e != nil {
			continue
		}
		entries, _ := d.ReadDir(-1)
		d.Close()
		for _, f := range entries {
			if st, e := f.Info(); e == nil && st.Mode().IsRegular() {
				total += st.Size()
			}
		}
	}
	s.bytes = total
}

// Reserve each open assistant and each not-yet-materialized proposal．Many
// concurrent accepted users must not oversubscribe the finite spool．
func (s *Store) hasCapacity(textBytes int, extraEvents int) bool {
	s.recountBytes()
	reserved := int64(textBytes*12 + 32<<10)
	if extraEvents > 0 {
		reserved += maxTextBytes*6 + 64<<10
	}
	pending := extraEvents
	for _, t := range s.turns {
		if t.Draft != nil {
			reserved += maxTextBytes*6 + 64<<10
			pending++
		}
		for _, d := range t.Items {
			if d.Verified {
				continue
			}
			pending++
			if len(d.Proposal) == 0 {
				b, _ := json.Marshal(d.Event)
				reserved += int64(len(b) + 8<<10)
			}
		}
	}
	return s.bytes+reserved <= s.options.MaxBytes && pending <= maxPendingEvents
}
func (s *Store) pause(code string) error {
	s.code = code
	s.settings.Enabled = false
	s.settings.Epoch++
	_ = s.saveSettings()
	return errors.New(code)
}
func (s *Store) turnKey(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(s.settings.ProducerID+"\x00"+key)).String()
}
func (s *Store) conversation(id string) string {
	if validUUID(id) {
		return id
	}
	if id == "" {
		return s.settings.ConversationID
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("ephy-conversation\x00"+s.settings.ProducerID+"\x00"+id)).String()
}
func (s *Store) event(t *turn, typ, text string) Event {
	loc, _ := time.LoadLocation(s.settings.Timezone)
	now := s.options.Now()
	s.seq[t.ConversationID]++
	return Event{ConversationID: t.ConversationID, ScopeID: s.settings.ScopeID, ProducerID: s.settings.ProducerID, EventID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(t.Key+"\x00"+typ)).String(), Seq: s.seq[t.ConversationID], Revision: 1, TurnID: t.TurnID, Type: typ, Text: text, OccurredAt: now.Format(time.RFC3339Nano), Timezone: s.settings.Timezone, LocalDate: now.In(loc).Format("2006-01-02"), ConsentEpoch: 1}
}
func itemFor(e Event) *delivery {
	return &delivery{Seq: e.Seq, EventID: e.EventID, Event: &e, State: "locally_preserved"}
}

// Begin returns only after the final input and an empty recovery checkpoint are
// durable in one atomic turn file．No ASR partial or raw input type exists here．
func (s *Store) Begin(key, conversationID, text, inputKind string, asr *ASR) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("recording_closed")
	}
	if !s.settings.Enabled {
		return "", nil
	}
	id := s.turnKey(key)
	if _, ok := s.turns[id]; ok {
		return "", errors.New("recording_duplicate_input")
	}
	if _, ok := s.completed[id]; ok {
		return "", errors.New("recording_duplicate_input")
	}
	if !s.settings.Configured {
		return "", s.pause("recording_unconfigured")
	}
	if len(text) == 0 || len(text) > maxTextBytes || !utf8.ValidString(text) || (inputKind != "text" && inputKind != "asr_final") || (inputKind == "asr_final" && (asr == nil || !tokenID(asr.Provider) || !tokenID(asr.ModelRevision) || asr.FinalRevision < 1)) {
		return "", s.pause("recording_input_invalid")
	}
	if !s.hasCapacity(len(text), 2) {
		return "", s.pause("recording_queue_full")
	}
	t := &turn{Key: id, ConversationID: s.conversation(conversationID), TurnID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("turn\x00"+id)).String(), Draft: &draft{Epoch: s.settings.Epoch, State: Assistant{Generation: "failed", Display: "none", Playback: "unknown", SpeechUnits: []SpeechUnit{}}}, Items: []*delivery{}}
	e := s.event(t, "user_final", text)
	e.InputKind = inputKind
	e.ASR = asr
	t.Items = append(t.Items, itemFor(e))
	if err := s.saveTurn(t); err != nil {
		s.seq[t.ConversationID]--
		return "", s.pause("recording_storage_failed")
	}
	s.turns[id] = t
	return id, nil
}

// Checkpoint accepts already committed text only．Call at utterance boundaries，
// generation completion and playback ACKs，never for speculative tokens．
func (s *Store) Checkpoint(key, text string, a Assistant, complete bool) error {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turns[key]
	if t == nil || t.Draft == nil {
		return nil
	}
	d := t.Draft
	previous := *d
	if s.settings.Enabled && d.Epoch == s.settings.Epoch {
		if d.SkipBytes > 0 {
			if len(text) < d.SkipBytes {
				return s.pause("recording_continuation_invalid")
			}
			text = text[d.SkipBytes:]
		}
		if len(text) > maxTextBytes || !utf8.ValidString(text) {
			return s.pause("recording_answer_capacity")
		}
		d.Text = text
		d.Full = complete
	}
	d.State = a
	d.State.SpeechUnits = append([]SpeechUnit{}, a.SpeechUnits...)
	d.Complete = complete
	if err := s.saveTurn(t); err != nil {
		*d = previous
		return s.pause("recording_storage_failed")
	}
	return nil
}
func (s *Store) Finish(key string, a Assistant, complete bool) error {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turns[key]
	if t == nil || t.Draft == nil {
		return nil
	}
	return s.finishLocked(t, a, complete)
}
func (s *Store) finishLocked(t *turn, a Assistant, complete bool) error {
	d := t.Draft
	if a.SpeechUnits == nil {
		a.SpeechUnits = []SpeechUnit{}
	}
	a.Display = "confirmed_prefix"
	if d.Text == "" {
		a.Display = "none"
	} else if d.Full {
		a.Display = "confirmed_full"
	}
	e := s.event(t, "assistant_result", d.Text)
	e.Assistant = &a
	t.Items = append(t.Items, itemFor(e))
	t.Draft = nil
	if err := s.saveTurn(t); err != nil {
		t.Draft = d
		t.Items = t.Items[:len(t.Items)-1]
		s.seq[t.ConversationID]--
		return s.pause("recording_storage_failed")
	}
	return nil
}
func (s *Store) advanceHead(t *turn, d *delivery) {
	if !d.Verified || d.Receipt == nil || s.heads[t.ConversationID].Seq >= d.Seq {
		return
	}
	s.heads[t.ConversationID] = Head{Seq: d.Seq, Segment: d.Record.Segment, Spec: d.Record, Target: d.Receipt.Applied, Events: d.RecordEvents, Bytes: d.RecordBytes}
}
func (s *Store) cleanup(t *turn) error {
	if t.Draft != nil {
		return nil
	}
	for _, d := range t.Items {
		if !d.Verified {
			return nil
		}
	}
	s.completed[t.Key] = completion{ConversationID: t.ConversationID, At: s.options.Now().Unix()}
	for k, c := range s.completed {
		if c.At < s.options.Now().Add(-24*time.Hour).Unix() {
			delete(s.completed, k)
		}
	}
	if len(s.completed) > 10000 {
		keys := make([]string, 0, len(s.completed))
		for k := range s.completed {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return s.completed[keys[i]].At < s.completed[keys[j]].At })
		for _, k := range keys[:len(keys)-10000] {
			delete(s.completed, k)
		}
	}
	if err := s.saveJSON("completed.json", s.completed); err != nil {
		return err
	}
	if err := removeFile(s.root, filepath.Join("turns", t.Key+".json")); err != nil {
		return err
	}
	delete(s.turns, t.Key)
	return nil
}
func (s *Store) SetConversation(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validUUID(id) {
		id = uuid.NewString()
	}
	s.settings.ConversationID = id
	return id, s.saveSettings()
}
func (s *Store) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recountBytes()
	out := Status{Settings: s.settings, State: "recording_off", Code: s.code, QueueBytes: s.bytes, CapacityWarning: s.bytes > s.options.MaxBytes*8/10, Records: []RecordStatus{}}
	records := map[string]*RecordStatus{}
	for id, h := range s.heads {
		records[id] = &RecordStatus{ConversationID: id, Saved: h.Seq, Target: h.Target, State: "canonical_saved"}
	}
	for _, t := range s.turns {
		r := records[t.ConversationID]
		if r == nil {
			r = &RecordStatus{ConversationID: t.ConversationID}
			records[t.ConversationID] = r
		}
		for _, d := range t.Items {
			if d.Verified {
				continue
			}
			r.Local++
			if d.State == "save_failed" || d.State == "permission_blocked" || d.State == "conflict" {
				r.Failed++
				r.Code = d.Code
				r.State = d.State
			} else {
				r.Pending++
				if r.Failed == 0 {
					r.State = d.State
				}
			}
		}
	}
	for _, r := range records {
		out.Records = append(out.Records, *r)
		out.Saved += r.Saved
		out.Local += r.Local
		out.Pending += r.Pending
		out.Failed += r.Failed
	}
	sort.Slice(out.Records, func(i, j int) bool { return out.Records[i].ConversationID < out.Records[j].ConversationID })
	if s.settings.Enabled {
		out.State = "ready"
	}
	if out.Saved > 0 && out.Pending == 0 {
		out.State = "canonical_saved"
	}
	if out.Pending > 0 {
		out.State = "delivery_pending"
	}
	if out.Failed > 0 || out.Code != "" {
		out.State = "save_failed"
	}
	return out
}
func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.lock != nil {
		unlockQueue(s.lock)
	}
	if s.root != nil {
		s.root.Close()
	}
}

// A continuation records only newly committed content and keeps the original
// turn identity．The previous prefix，including any OFF interval，is not copied．
func (s *Store) BeginContinuation(originalRequestID, requestID, conversationID string, skipBytes int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.settings.Enabled || s.closed {
		return "", nil
	}
	original := s.turnKey(originalRequestID)
	conv := s.conversation(conversationID)
	prior, live := s.turns[original]
	done, finished := s.completed[original]
	if (!live || prior.ConversationID != conv) && (!finished || done.ConversationID != conv) {
		return "", nil
	}
	id := s.turnKey(requestID)
	if _, ok := s.turns[id]; ok {
		return "", errors.New("recording_duplicate_input")
	}
	if _, ok := s.completed[id]; ok {
		return "", errors.New("recording_duplicate_input")
	}
	if skipBytes < 0 || skipBytes > 128<<10 {
		return "", errors.New("recording_continuation_invalid")
	}
	if !s.hasCapacity(0, 1) {
		return "", s.pause("recording_queue_full")
	}
	t := &turn{Key: id, ConversationID: conv, TurnID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("turn\x00"+original)).String(), Draft: &draft{SkipBytes: skipBytes, Epoch: s.settings.Epoch, State: Assistant{Generation: "failed", Playback: "unknown", SpeechUnits: []SpeechUnit{}}}, Items: []*delivery{}}
	if err := s.saveTurn(t); err != nil {
		return "", s.pause("recording_storage_failed")
	}
	s.turns[id] = t
	return id, nil
}
