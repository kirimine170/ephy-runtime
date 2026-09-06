package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

func durationTestWAV(seconds, sampleRate int) []byte {
	dataBytes := seconds * sampleRate * 2
	wav := make([]byte, 44+dataBytes)
	copy(wav, providerTestWAV()[:44])
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint32(wav[40:44], uint32(dataBytes))
	return wav
}

func TestValidatedWAVDurationAndBounds(t *testing.T) {
	for _, seconds := range []int{31, 45, 60} {
		for _, rate := range []int{8000, 22050, 48000} {
			duration, valid := voiceWAVDuration(durationTestWAV(seconds, rate), 60)
			if !valid || duration != time.Duration(seconds)*time.Second {
				t.Fatalf("duration %d s at %d Hz: %s valid=%v", seconds, rate, duration, valid)
			}
		}
	}
	for name, wav := range map[string][]byte{
		"over duration": durationTestWAV(61, 48000),
		"truncated":     durationTestWAV(60, 22050)[:44],
		"header only":   []byte("RIFFxxxxWAVE"),
	} {
		if duration, valid := voiceWAVDuration(wav, 60); valid || duration != 0 {
			t.Fatalf("%s supplied a trusted duration", name)
		}
	}
	if _, valid := voiceWAVDuration(providerTestWAV(), -1); valid {
		t.Fatal("negative duration bound accepted")
	}
}

func TestLongWAVPlaybackDeadlineUsesValidatedRemainingDuration(t *testing.T) {
	for _, seconds := range []int{31, 45, 60} {
		t.Run(fmt.Sprintf("%ds", seconds), func(t *testing.T) {
			wav := durationTestWAV(seconds, 48000)
			delivered := make(chan InteractionEvent, 1)
			tts := testVoiceTTS{stream: func(_ context.Context, _ string, emit func([]byte) error) error {
				return emit(wav)
			}}
			engine := NewInteractionEngine(testVoiceASR{}, tts, testVoiceChat, func(event InteractionEvent) {
				if event.Kind == "audio" {
					delivered <- event
				}
			}, t.TempDir())
			defer engine.Close()
			engine.Timeouts.Playback = time.Second
			start := startTestInteraction(t, engine)
			if err := engine.Commit(start.OperationID, nil, "synthetic input"); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-delivered:
				if event.Sequence != 1 || event.OperationID != start.OperationID {
					t.Fatal("wrong playback identity")
				}
			case <-time.After(2 * time.Second):
				snapshot, _ := engine.Snapshot(start.OperationID)
				t.Fatalf("valid long WAV was not delivered: state=%s code=%s", snapshot.State, snapshot.ErrorCode)
			}
			if err := engine.Playback(start.OperationID, 1, "started"); err != nil {
				t.Fatal(err)
			}
			engine.mu.Lock()
			turn := engine.turns[start.OperationID]
			chunk := turn.chunks[1]
			window := turn.playbackDeadline.Sub(chunk.startedAt)
			// Advance the synthetic playback timeline past the previous 30 s cap．
			chunk.startedAt = chunk.startedAt.Add(-30 * time.Second)
			engine.playbackDeadlineLocked(turn)
			deadline := turn.playbackDeadline
			remaining := time.Until(deadline)
			engine.mu.Unlock()
			if window != time.Duration(seconds+1)*time.Second || remaining <= time.Duration(seconds-30)*time.Second {
				t.Fatalf("duration=%d s window=%s remaining=%s", seconds, window, remaining)
			}
			if err := engine.Playback(start.OperationID, 1, "started"); err != nil {
				t.Fatal(err)
			}
			engine.mu.Lock()
			unchanged := turn.playbackDeadline.Equal(deadline)
			engine.mu.Unlock()
			if !unchanged {
				t.Fatal("duplicate start ACK extended the deadline")
			}
			if err := engine.Playback(start.OperationID, 1, "stopped"); err != nil {
				t.Fatal(err)
			}
			awaitInteraction(t, engine, start.OperationID, "COMPLETED")
		})
	}
}

func TestPlaybackDeadlineDoesNotSlideWithLaterChunks(t *testing.T) {
	now := time.Unix(100, 0)
	chunks := map[int]*playbackChunk{1: {duration: 45 * time.Second}}
	first := nextPlaybackDeadline(chunks, now, 5*time.Second)
	chunks[2] = &playbackChunk{duration: 60 * time.Second}
	if !first.Equal(now.Add(5*time.Second)) || !nextPlaybackDeadline(chunks, now.Add(time.Second), 5*time.Second).Equal(first) {
		t.Fatal("new synthesis extended the pending start deadline")
	}
	chunks[1].started, chunks[1].startedAt = true, now.Add(time.Second)
	playing := nextPlaybackDeadline(chunks, now.Add(time.Second), 5*time.Second)
	if !playing.Equal(now.Add(51*time.Second)) || !nextPlaybackDeadline(chunks, now.Add(31*time.Second), 5*time.Second).Equal(playing) {
		t.Fatal("remaining playback duration was not measured from the first ACK")
	}
	chunks[1].stopped = true
	if !nextPlaybackDeadline(chunks, now.Add(46*time.Second), 5*time.Second).Equal(now.Add(51 * time.Second)) {
		t.Fatal("queued chunk did not receive its own bounded start margin")
	}
	chunks[2].started, chunks[2].startedAt = true, now.Add(47*time.Second)
	if !nextPlaybackDeadline(chunks, now.Add(47*time.Second), time.Hour).Equal(now.Add(137 * time.Second)) {
		t.Fatal("playback margin was not bounded to 30 seconds")
	}
	chunks[2].stopped = true
	if !nextPlaybackDeadline(chunks, now, time.Second).IsZero() {
		t.Fatal("finished playback retained a deadline")
	}
}
