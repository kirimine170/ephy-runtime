package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testStore(t *testing.T) (*Store, Options) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	o := Options{Home: filepath.Join(base, "queue"), DataRoot: filepath.Join(base, "karte"), Now: func() time.Time { return time.Date(2026, 9, 12, 23, 59, 58, 0, time.FixedZone("JST", 9*3600)) }}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	s.settings.Configured = true
	s.settings.Enabled = true
	s.settings.ProducerID = uuid.NewString()
	s.settings.ScopeID = uuid.NewString()
	s.settings.PolicyID = uuid.NewString()
	if e = s.saveSettings(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s, o
}
func completed() Assistant {
	return Assistant{Generation: "completed", Display: "confirmed_full", Playback: "completed", SpeechUnits: []SpeechUnit{{UnitID: "unit-1", State: "completed"}}}
}
func TestFinalSurvivesRestartBeforeLLMAndOffDoesNotBackfill(t *testing.T) {
	s, o := testStore(t)
	conv := uuid.NewString()
	id, e := s.Begin("turn1", conv, "確定した発言", "asr_final", &ASR{Provider: "whisper-cpp", ModelRevision: "large-v3-turbo", FinalRevision: 7})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Begin("turn1", conv, "重複してはいけない", "text", nil); e == nil || e.Error() != "recording_duplicate_input" || len(s.turns) != 1 {
		t.Fatal(e)
	}
	if e = s.Checkpoint(id, "確認済みの接頭辞", Assistant{Generation: "failed", Playback: "unknown", SpeechUnits: []SpeechUnit{{UnitID: "unit1", State: "started"}}}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Configure(context.Background(), ConfigureRequest{Enabled: false}); e != nil {
		t.Fatal(e)
	}
	if e = s.Checkpoint(id, "OFF以降の本文", completed(), true); e != nil {
		t.Fatal(e)
	}
	if id, e = s.Begin("off", conv, "OFFの発言", "text", nil); id != "" || e != nil {
		t.Fatal("off input accepted")
	}
	s.Close()
	r, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if len(r.turns) != 1 {
		t.Fatal("missing turn")
	}
	for _, tr := range r.turns {
		if len(tr.Items) != 2 || tr.Draft != nil {
			t.Fatal("missing recovery outcome")
		}
		u, a := tr.Items[0].Event, tr.Items[1].Event
		if u.Text != "確定した発言" || a.Text != "確認済みの接頭辞" || a.Assistant.Display != "confirmed_prefix" {
			t.Fatal("lost committed text or off backfill")
		}
	}
	b, _ := json.Marshal(r.turns)
	for _, forbidden := range []string{"OFF以降", "OFFの発言", "重複して"} {
		if bytes.Contains(b, []byte(forbidden)) {
			t.Fatal("unexpected content")
		}
	}
}
func TestAllTurnsIndependentOfUIAndContextAndMidnight(t *testing.T) {
	s, _ := testStore(t)
	conv := uuid.NewString()
	for i := 0; i < 100; i++ {
		id, e := s.Begin(uuid.NewString(), conv, "synthetic user", "text", nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Checkpoint(id, "synthetic assistant", completed(), true); e != nil {
			t.Fatal(e)
		}
		if e = s.Finish(id, completed(), true); e != nil {
			t.Fatal(e)
		}
	}
	if len(s.turns) != 100 || s.seq[conv] != 200 || s.Snapshot().Local != 200 {
		t.Fatal("history limit truncated records")
	}
	for _, tr := range s.turns {
		for _, d := range tr.Items {
			if d.Event.LocalDate != "2026-09-12" {
				t.Fatal("timezone mismatch")
			}
		}
	}
	s.options.Now = func() time.Time { return time.Date(2026, 9, 13, 0, 1, 0, 0, time.FixedZone("JST", 9*3600)) }
	id, e := s.Begin("midnight", conv, "after midnight", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	if s.turns[id].Items[0].Event.LocalDate != "2026-09-13" {
		t.Fatal("new date not captured")
	}
}
func TestDiskFailureDoesNotAcknowledgeOrLosePriorInput(t *testing.T) {
	s, _ := testStore(t)
	conv := uuid.NewString()
	id, e := s.Begin("ok", conv, "preserve this", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	// A non-regular final path forces atomic save failure without relying on
	// permission bits that differ when the test runner has elevated access．
	bad := s.turnKey("bad")
	if e = s.root.Mkdir(filepath.Join("turns", bad+".json"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Begin("bad", conv, "not acknowledged", "text", nil); e == nil {
		t.Fatal("false durable acknowledgement")
	}
	if s.settings.Enabled || s.code != "recording_storage_failed" || s.turns[id].Items[0].Event.Text != "preserve this" {
		t.Fatal("failure did not pause safely")
	}
}
func TestQueueCapacityAndSingleWriter(t *testing.T) {
	s, o := testStore(t)
	if _, e := New(o); e == nil {
		t.Fatal("second writer acquired spool")
	}
	s.options.MaxBytes = 100
	if _, e := s.Begin("full", uuid.NewString(), "test", "text", nil); e == nil || e.Error() != "recording_queue_full" {
		t.Fatal(e)
	}
	if len(s.turns) != 0 || s.settings.Enabled {
		t.Fatal("full queue accepted input")
	}
}

func TestOpenAssistantsReserveCapacityBeforeFurtherInput(t *testing.T) {
	s, _ := testStore(t)
	s.options.MaxBytes = 1 << 20
	conv := s.settings.ConversationID
	accepted := 0
	for i := 0; i < 10; i++ {
		if _, e := s.Begin(uuid.NewString(), conv, "small final", "text", nil); e != nil {
			break
		}
		accepted++
	}
	if accepted == 0 || accepted > 2 || s.settings.Enabled || s.code != "recording_queue_full" {
		t.Fatal("open assistant capacity was oversubscribed")
	}
	if len(s.turns) != accepted {
		t.Fatal("existing accepted users were dropped")
	}
}
func TestUnknownCrashPlaybackAndConfirmedTextOnly(t *testing.T) {
	s, o := testStore(t)
	id, e := s.Begin("one", uuid.NewString(), "final only", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Checkpoint(id, "committed only", Assistant{Generation: "failed", Playback: "unknown", SpeechUnits: []SpeechUnit{{UnitID: "unit1", State: "started"}}}, false); e != nil {
		t.Fatal(e)
	}
	s.Close()
	r, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	for _, tr := range r.turns {
		a := tr.Items[1].Event.Assistant
		if a.Generation != "failed" || a.Playback != "unknown" || a.SpeechUnits[0].State != "unknown" {
			t.Fatal("crash invented completion")
		}
	}
}

func TestOffAfterFullGenerationPreservesKnownFullDisplay(t *testing.T) {
	s, _ := testStore(t)
	id, e := s.Begin("full-before-off", s.settings.ConversationID, "user", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Checkpoint(id, "whole confirmed answer", completed(), true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Configure(context.Background(), ConfigureRequest{Enabled: false}); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(id, completed(), true); e != nil {
		t.Fatal(e)
	}
	if s.turns[id].Items[1].Event.Assistant.Display != "confirmed_full" {
		t.Fatal("OFF rewrote an already observed full display")
	}
}
func TestPrivateDirectoryRejectsSymlinkAndGit(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	if e := os.Mkdir(filepath.Join(base, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := New(Options{Home: filepath.Join(base, "queue")}); e == nil {
		t.Fatal("queue allowed in git")
	}
	base2, _ := filepath.EvalSymlinks(t.TempDir())
	if e := os.Symlink(base2, filepath.Join(base2, "link")); e != nil {
		t.Fatal(e)
	}
	if _, e := New(Options{Home: filepath.Join(base2, "link", "queue")}); e == nil {
		t.Fatal("symlink queue allowed")
	}
}
func TestCanonicalSigningStrictness(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":1.5}`, `{"x":"\ud800"}`, `{"x":-0}`} {
		if _, e := CanonicalJSON([]byte(raw)); e == nil {
			t.Fatal("invalid canonical accepted")
		}
	}
	b, e := CanonicalJSON([]byte("{\"z\":\"日本語\u2028\",\"a\":2}"))
	if e != nil || !strings.HasPrefix(string(b), `{"a":2,`) {
		t.Fatal(e)
	}
}

func TestRecordingCrashChild(t *testing.T) {
	home := os.Getenv("EPHY_RECORDING_CRASH_TEST_HOME")
	if home == "" {
		t.Skip("subprocess fixture")
	}
	s, e := New(Options{Home: home})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Begin("killed-user", s.settings.ConversationID, "synthetic final before kill", "text", nil); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, "child-ready"), []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	select {}
}
func TestSIGKILLAfterUserFinalRecoversWithoutLLMOrDuplicate(t *testing.T) {
	s, o := testStore(t)
	s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingCrashChild$")
	cmd.Env = append(os.Environ(), "EPHY_RECORDING_CRASH_TEST_HOME="+o.Home)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(o.Home, "child-ready")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not preserve final")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = cmd.Wait()
	r, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if r.Snapshot().Local != 2 {
		t.Fatal("missing user or recovery outcome")
	}
	for _, tr := range r.turns {
		if tr.Items[0].Event.Text != "synthetic final before kill" || tr.Items[1].Event.Assistant.Generation != "failed" || tr.Items[1].Event.Assistant.Playback != "unknown" {
			t.Fatal("crash falsely completed assistant")
		}
	}
	if _, e = r.Begin("killed-user", r.settings.ConversationID, "repeated final", "text", nil); e == nil {
		t.Fatal("replayed final accepted")
	}
}

func TestContinuationDoesNotBackfillPreviousPrefix(t *testing.T) {
	s, _ := testStore(t)
	conv := s.settings.ConversationID
	id, e := s.Begin("original", conv, "user", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Checkpoint(id, "old prefix", completed(), false); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(id, completed(), false); e != nil {
		t.Fatal(e)
	}
	continued, e := s.BeginContinuation("original", "next", conv, len("old prefix"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Checkpoint(continued, "old prefixnew suffix", completed(), true); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(continued, completed(), true); e != nil {
		t.Fatal(e)
	}
	if len(s.turns[continued].Items) != 1 || s.turns[continued].Items[0].Event.Text != "new suffix" || s.turns[continued].TurnID != s.turns[id].TurnID {
		t.Fatal("continuation duplicated old body or user")
	}
	if key, e := s.BeginContinuation("unrecorded", "other", conv, 0); e != nil || key != "" {
		t.Fatal("unrecorded turn backfilled")
	}
}

func TestSegmentRolloverKeepsGlobalOrderAndDate(t *testing.T) {
	s, _ := testStore(t)
	conv := s.settings.ConversationID
	id, e := s.Begin("event", conv, "new user", "text", nil)
	if e != nil {
		t.Fatal(e)
	}
	tr := s.turns[id]
	item := tr.Items[0]
	for _, head := range []Head{
		{Seq: 256, Segment: 1, Spec: RecordSpec{Type: "conversation", ConversationID: conv, Segment: 1, Timezone: "Asia/Tokyo", LocalDate: item.Event.LocalDate}, Events: 256, Bytes: 50000},
		{Seq: 2, Segment: 1, Spec: RecordSpec{Type: "conversation", ConversationID: conv, Segment: 1, Timezone: "Asia/Tokyo", LocalDate: "2026-09-11"}, Events: 2, Bytes: 5000},
		{Seq: 2, Segment: 1, Spec: RecordSpec{Type: "conversation", ConversationID: conv, Segment: 1, Timezone: "Asia/Tokyo", LocalDate: item.Event.LocalDate}, Events: 2, Bytes: 1048000},
	} {
		s.heads[conv] = head
		item.Event.Seq = head.Seq + 1
		item.Seq = head.Seq + 1
		item.Proposal = nil
		if e = s.buildProposal(tr, item, make([]byte, 32), "fixture"); e != nil {
			t.Fatal(e)
		}
		var p Proposal
		if e = json.Unmarshal(item.Proposal, &p); e != nil {
			t.Fatal(e)
		}
		if p.Operation != "create_record" || p.Target != nil || p.Record.Segment != 2 || p.Events[0].Seq != head.Seq+1 {
			t.Fatal("rollover lost identity")
		}
	}
}
