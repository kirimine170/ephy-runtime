package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fillerRuntimeFixture(t *testing.T) (*App, *interactionTurn, string) {
	t.Helper()
	bundle, speech, _ := fillerFixture(t)
	t.Setenv("EPHY_FILLER_BUNDLE", bundle)
	t.Setenv("EPHY_FILLER_CONDITION", "synthetic-host-warm-headphones")
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	e := NewInteractionEngine(testVoiceASR{}, testVoiceTTS{}, testVoiceChat, nil, root)
	t.Cleanup(e.Close)
	s := startTestInteraction(t, e)
	turn := e.turns[s.OperationID]
	turn.speech = speech
	turn.fillerIdentityReady = true
	turn.request.Chat.Prompt = "PRIVATE_PROMPT_SENTINEL"
	turn.request.Chat.ModelID = "synthetic-model"
	turn.request.Chat.ConfigurationID = "config-one"
	turn.events = []InteractionTraceEvent{{Name: "llm_requested", MonotonicMS: 20}, {Name: "llm_first_token", MonotonicMS: 900},
		{Name: "tts_requested", MonotonicMS: 1100}, {Name: "tts_first_chunk", MonotonicMS: 4970}}
	return &App{interaction: e}, turn, root
}
func TestFillerLiveSetupRespectsCalibrationAndRevisionBudget(t *testing.T) {
	a, turn, _ := fillerRuntimeFixture(t)
	e := a.interaction
	key := fillerScope(turn)
	samples := make([]FillerTiming, 100)
	for i := range samples {
		samples[i] = FillerTiming{20, 900, 1100, 4970, 5000, 880, 3870}
	}
	if err := e.saveFillerStoreLocked(fillerStore{Groups: map[string]fillerGroup{key: {Samples: samples, Updated: time.Now().Unix()}}}); err != nil {
		t.Fatal(err)
	}
	setup := a.GetInteractionFiller(turn.snapshot.OperationID, 1)
	if !setup.Enabled || len(setup.Assets) != 1 || len(setup.Samples) != 100 {
		t.Fatalf("setup: %s", setup.Status)
	}
	if a.GetInteractionFiller(turn.snapshot.OperationID, 1).Enabled || a.GetInteractionFiller(turn.snapshot.OperationID, 2).Enabled {
		t.Fatal("duplicate setup or continuation enabled")
	}
	turn.request.Chat.ConfigurationID = "changed"
	if fillerScope(turn) == key {
		t.Fatal("model configuration did not invalidate calibration")
	}
}
func TestFillerTelemetryIsBoundedPrivateAndDoesNotMutateConversation(t *testing.T) {
	a, turn, root := fillerRuntimeFixture(t)
	op := turn.snapshot.OperationID
	setup := a.GetInteractionFiller(op, 1)
	if setup.Enabled {
		t.Fatal("unmeasured filler enabled")
	}
	before, _ := json.Marshal(turn.request.Chat)
	turn.chunks[1] = &playbackChunk{}
	sample := FillerTiming{20, 900, 1100, 4970, 5000, 1, 1}
	if err := a.RecordInteractionFillerTiming(op, 1, sample); err != nil {
		t.Fatal(err)
	}
	a.RecordInteractionFillerTiming(op, 1, sample)
	for i := 0; i < 300; i++ {
		a.RecordInteractionFillerTrace(op, 1, FillerTrace{"filler_started", 3610})
	}
	if a.RecordInteractionFillerTrace(op, 1, FillerTrace{"PRIVATE_AUDIO_SENTINEL", 1}) == nil {
		t.Fatal("arbitrary trace accepted")
	}
	data, err := os.ReadFile(filepath.Join(root, "filler-calibration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_") || strings.Contains(string(data), "audio_base64") || strings.Contains(string(data), "hesitation_etto") {
		t.Fatal("payload retained")
	}
	store := a.interaction.readFillerStoreLocked()
	group := store.Groups[turn.fillerScope]
	if len(group.Samples) != 1 || group.Samples[0].LLMttftMS != 880 || group.Samples[0].TTSLatencyMS != 3870 || len(group.Trace) != 12 {
		t.Fatalf("unbounded or fabricated timings: %+v", group)
	}
	after, _ := json.Marshal(turn.request.Chat)
	if string(before) != string(after) {
		t.Fatal("filler changed reasoning request")
	}
	for _, event := range turn.events {
		if strings.HasPrefix(event.Name, "filler_") {
			t.Fatal("filler polluted answer trace")
		}
	}
}
