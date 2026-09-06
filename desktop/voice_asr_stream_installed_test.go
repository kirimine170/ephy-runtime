package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// This measures the installed Apple Speech process with synthetic Kyoko input．
// It opens no microphone，plays no audio，imports no asset，and logs no transcript．
// Permission prompting is a separate explicit opt-in from the measurement．
func TestC02InstalledStreamingASR(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("EPHY_C02_INSTALLED") != "1" {
		t.Skip("opt-in installed Apple Speech with synthetic PCM，no microphone")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	provider := NewNativeVoiceASR(detectWorkspaceRoot())
	var processExit, processSignal atomic.Int64
	processExit.Store(-999)
	var lastUpdate atomic.Value
	lastUpdate.Store("none")
	provider.streamStart = func(ctx context.Context, executable string, args []string) (*nativeASRProcess, error) {
		process, err := startNativeASRProcess(ctx, executable, args)
		if err != nil {
			return nil, err
		}
		wait := process.wait
		process.wait = func() error {
			err := wait()
			code := int64(0)
			if err != nil {
				code = -2
				var exitError *exec.ExitError
				if errors.As(err, &exitError) {
					code = int64(exitError.ExitCode())
					if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
						processSignal.Store(int64(status.Signal()))
					}
				}
			}
			processExit.Store(code)
			return err
		}
		return process, nil
	}
	defer func() {
		t.Logf("ASR_PROCESS_METADATA exit_code=%d signal=%d last_update=%s", processExit.Load(), processSignal.Load(), lastUpdate.Load().(string))
	}()
	readiness, err := provider.Readiness(ctx)
	t.Logf("readiness state=%s can_start=%t error_code=%s microphone_opened=false input_source=synthetic-kyoko", readiness.State, readiness.CanStart, readiness.ErrorCode)
	if err != nil || !readiness.CanStart {
		t.Skip("installed recognition unavailable，no latency measurements")
	}
	if readiness.State == "permission_required" && os.Getenv("EPHY_C02_ALLOW_PERMISSION") != "1" {
		t.Skip("Apple Speech permission required，latencies unavailable")
	}
	tts := NewNativeVoiceTTS()
	tts.tempRoot = t.TempDir()
	var pcm []byte
	rate := 0
	err = tts.Stream(ctx, "日本の春には桜が咲き，夏には花火を楽しみ，秋には紅葉が広がり，冬には雪が降ります．", func(wav []byte) error {
		if _, valid := voiceWAVDuration(wav, 60); !valid {
			return fmt.Errorf("invalid_synthetic_wav")
		}
		chunkRate, chunkPCM := c02InstalledPCM(wav)
		if chunkRate == 0 || (rate != 0 && rate != chunkRate) {
			return fmt.Errorf("invalid_synthetic_rate")
		}
		rate = chunkRate
		pcm = append(pcm, chunkPCM...)
		return nil
	})
	if err != nil {
		t.Fatal("synthetic synthesis failed", err)
	}
	if len(pcm) == 0 || len(pcm) > rate*2*30 {
		t.Fatal("synthetic audio violates finite test bounds")
	}
	defer clear(pcm)
	type measurement struct {
		Run                 int    `json:"run"`
		FirstPartialMS      *int64 `json:"first_partial_ms"`
		FirstStableMS       *int64 `json:"first_stable_ms"`
		FinalizationMS      int64  `json:"finalization_ms"`
		RevisionCount       int    `json:"revision_count"`
		Characters          int    `json:"characters"`
		ContainsFourSeasons bool   `json:"contains_four_seasons"`
		Provider            string `json:"provider"`
		ModelRevision       string `json:"model_revision"`
	}
	var partialLatencies, finalLatencies []int64
	for run := 1; run <= 5; run++ {
		var mu sync.Mutex
		var firstAudio time.Time
		m := measurement{Run: run}
		req := ASRSessionRequest{OperationID: fmt.Sprintf("c02-measure-operation-%d", run), SessionID: fmt.Sprintf("c02-measure-session-%d", run), TurnID: fmt.Sprintf("c02-measure-turn-%d", run), SegmentID: fmt.Sprintf("c02-measure-segment-%d", run), SampleRate: rate}
		session, err := provider.OpenSession(ctx, req, func(update ASRUpdate) {
			lastUpdate.Store(fmt.Sprintf("phase:%s,error_code:%s,provider:%s,model_revision:%s", update.Phase, update.ErrorCode, update.Provider, update.ModelRevision))
			mu.Lock()
			defer mu.Unlock()
			if update.OperationID != req.OperationID || update.SessionID != req.SessionID || update.TurnID != req.TurnID || update.SegmentID != req.SegmentID {
				t.Error("installed callback crossed identity boundary")
				return
			}
			m.RevisionCount++
			if !firstAudio.IsZero() {
				ms := time.Since(firstAudio).Milliseconds()
				if update.Phase == "partial" && strings.TrimSpace(update.Transcript) != "" && m.FirstPartialMS == nil {
					m.FirstPartialMS = &ms
				}
				if (update.Phase == "stable" || update.Phase == "final") && m.FirstStableMS == nil {
					m.FirstStableMS = &ms
				}
			}
		})
		if err != nil {
			t.Fatal("installed session startup failed", asrStreamError(err))
		}
		mu.Lock()
		firstAudio = time.Now()
		started := firstAudio
		mu.Unlock()
		step := rate / 20 * 2
		sequence := 0
		for offset := 0; offset < len(pcm); offset += step {
			sequence++
			end := min(offset+step, len(pcm))
			if err := session.Append(ctx, sequence, pcm[offset:end]); err != nil {
				session.Cancel()
				t.Fatal("installed streaming append failed", asrStreamError(err))
			}
			deadline := started.Add(time.Duration(end/2) * time.Second / time.Duration(rate))
			if delay := time.Until(deadline); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					session.Cancel()
					t.Fatal("installed measurement deadline")
				}
			}
		}
		endpoint := time.Now()
		final, err := session.Finish(ctx)
		finalization := time.Since(endpoint).Milliseconds()
		if err != nil {
			session.Cancel()
			t.Fatal("installed finalization failed", asrStreamError(err))
		}
		mu.Lock()
		m.FinalizationMS = finalization
		m.Characters = utf8.RuneCountInString(final.Transcript)
		m.Provider = final.Provider
		m.ModelRevision = final.ModelRevision
		m.ContainsFourSeasons = true
		for _, word := range []string{"春", "夏", "秋", "冬"} {
			m.ContainsFourSeasons = m.ContainsFourSeasons && strings.Contains(final.Transcript, word)
		}
		body, _ := json.Marshal(m)
		t.Logf("ASR_MEASUREMENT %s", body)
		if m.FirstPartialMS != nil {
			partialLatencies = append(partialLatencies, *m.FirstPartialMS)
		}
		mu.Unlock()
		finalLatencies = append(finalLatencies, finalization)
		session.Cancel()
		if final.Phase != "final" || final.Transcript == "" {
			t.Fatal("installed recognizer did not verify final")
		}
	}
	quantile := func(values []int64, p float64) int64 {
		if len(values) == 0 {
			return -1
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		return values[int(math.Ceil(float64(len(values))*p))-1]
	}
	t.Logf("ASR_PERCENTILES samples=5 partial_samples=%d first_partial_p50_ms=%d first_partial_p95_ms=%d finalization_p50_ms=%d finalization_p95_ms=%d input_source=synthetic-kyoko microphone_opened=false", len(partialLatencies), quantile(partialLatencies, .5), quantile(partialLatencies, .95), quantile(finalLatencies, .5), quantile(finalLatencies, .95))
	// Cancel one process，then verify the next session's identity and final．
	cancelCtx, cancelSession := context.WithCancel(ctx)
	req := ASRSessionRequest{OperationID: "c02-cancel-op", SessionID: "c02-cancel-session", TurnID: "c02-cancel-turn", SegmentID: "c02-cancel-segment", SampleRate: rate}
	s, err := provider.OpenSession(cancelCtx, req, func(ASRUpdate) {})
	if err != nil {
		t.Fatal(asrStreamError(err))
	}
	if err := s.Append(cancelCtx, 1, pcm[:min(rate/5*2, len(pcm))]); err != nil {
		cancelSession()
		s.Cancel()
		t.Fatal(asrStreamError(err))
	}
	cancelSession()
	s.Cancel()
	if _, err := s.Finish(ctx); err == nil {
		t.Fatal("canceled ASR became final")
	}
	req.OperationID = "c02-next-op"
	req.SessionID = "c02-next-session"
	req.TurnID = "c02-next-turn"
	req.SegmentID = "c02-next-segment"
	next, err := provider.OpenSession(ctx, req, func(update ASRUpdate) {
		if update.OperationID != req.OperationID || update.SessionID != req.SessionID {
			t.Error("old callback entered next session")
		}
	})
	if err != nil {
		t.Fatal(asrStreamError(err))
	}
	for offset, seq := 0, 1; offset < len(pcm); offset, seq = offset+maxASRPCMChunkBytes, seq+1 {
		if err := next.Append(ctx, seq, pcm[offset:min(offset+maxASRPCMChunkBytes, len(pcm))]); err != nil {
			next.Cancel()
			t.Fatal(asrStreamError(err))
		}
	}
	if _, err := next.Finish(ctx); err != nil {
		next.Cancel()
		t.Fatal(asrStreamError(err))
	}
	next.Cancel()
	entries, err := os.ReadDir(tts.tempRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary WAV remains")
	}
	t.Log("installed cancel_next_session=pass temporary_wavs=0 audio_playback=false")
}

func c02InstalledPCM(wav []byte) (int, []byte) {
	rate := 0
	var pcm []byte
	for offset := 12; offset+8 <= len(wav); {
		size := int(binary.LittleEndian.Uint32(wav[offset+4:]))
		start := offset + 8
		if size < 0 || start+size > len(wav) {
			return 0, nil
		}
		switch string(wav[offset : offset+4]) {
		case "fmt ":
			if size >= 16 {
				rate = int(binary.LittleEndian.Uint32(wav[start+4:]))
			}
		case "data":
			pcm = wav[start : start+size]
		}
		offset = start + size + size%2
	}
	return rate, pcm
}
