package recording

import (
	"testing"

	"github.com/google/uuid"
)

func TestCandidateHasNoInventedUserAndPersistsPartialPlayback(t *testing.T) {
	s, options := testStore(t)
	id, err := s.BeginCandidate("candidate-run", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.turns[id].Items) != 0 {
		t.Fatal("candidate invented an input event")
	}
	if _, err = s.BeginCandidate("candidate-run", uuid.NewString()); err == nil {
		t.Fatal("duplicate candidate accepted")
	}
	a := Assistant{Generation: "completed", Playback: "interrupted", SpeechUnits: []SpeechUnit{{UnitID: "candidate-unit", State: "interrupted"}}}
	if err = s.Checkpoint(id, "合成の候補発言です．", a, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(id, a, true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	items := restored.turns[id].Items
	if len(items) != 1 || items[0].Event.Type != "assistant_result" || items[0].Event.InputKind != "" || items[0].Event.ASR != nil {
		t.Fatal("candidate became a fabricated user turn")
	}
	if items[0].Event.Assistant.Playback != "interrupted" {
		t.Fatal("partial playback became full delivery")
	}
}

func TestCandidateCrashRecoveryDoesNotClaimSpeech(t *testing.T) {
	s, options := testStore(t)
	id, err := s.BeginCandidate("candidate-crash", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	items := restored.turns[id].Items
	if len(items) != 1 || items[0].Event.Text != "" || items[0].Event.Type != "assistant_result" || items[0].Event.Assistant.Playback != "unknown" {
		t.Fatal("crashed candidate was recorded as heard")
	}
}
