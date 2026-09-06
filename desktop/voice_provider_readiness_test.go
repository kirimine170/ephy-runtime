package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSplitVoiceSentencesBoundsSixteenThousandRunes(t *testing.T) {
	text := strings.Repeat("春🌸", 8000)
	for _, maxRunes := range []int{0, 1, 17, 180} {
		chunks := splitVoiceSentences(text, maxRunes)
		bound := maxRunes
		if bound <= 0 {
			bound = 180
		}
		if strings.Join(chunks, "") != text {
			t.Fatal("rune splitting changed the utterance")
		}
		for _, chunk := range chunks {
			if chunk == "" || !utf8.ValidString(chunk) || utf8.RuneCountInString(chunk) > bound {
				t.Fatalf("invalid chunk at bound %d", bound)
			}
		}
	}
	for _, tc := range []struct {
		text  string
		bound int
		first string
	}{
		{"春です。夏の長い説明", 8, "春です。"},
		{"春の説明，夏の長い説明", 8, "春の説明，"},
		{"spring summer autumn", 10, "spring"},
		{"春🌸夏🌻秋🍁冬⛄", 3, "春🌸夏"},
	} {
		got := splitVoiceSentences(tc.text, tc.bound)
		if len(got) == 0 || got[0] != tc.first {
			t.Fatalf("boundary priority: %q → %#v", tc.text, got)
		}
	}
	for _, chunk := range splitVoiceSentences(" \n [[rate 100]]春です。\t", 180) {
		if chunk == "" || strings.Contains(chunk, "[[") || strings.Contains(chunk, "]]") {
			t.Fatal("say delimiter or empty chunk escaped")
		}
	}
}

func TestNativeASRReadinessRejectsUnknownSuccessfulStdoutWithoutCaching(t *testing.T) {
	for _, stdout := range []string{"", "PRIVATE unexpected stdout", "ready\npermission_required", "\xff"} {
		calls := 0
		p := asrTestProvider(t, func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
			calls++
			return []byte(stdout), nil, nil
		})
		for index := 0; index < 2; index++ {
			result, err := p.Readiness(context.Background())
			if err == nil || err.Error() != "asr_failed" || result.State != "unavailable" || result.CanStart || result.ErrorCode != "asr_failed" {
				t.Fatalf("unknown stdout succeeded: %#v %v", result, err)
			}
		}
		if calls != 2 {
			t.Fatal("readiness failure was cached")
		}
	}
}

func testVoiceShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func realASRReadinessFixture(t *testing.T) (*NativeVoiceASR, string, string, *atomic.Int64) {
	t.Helper()
	dir := t.TempDir()
	state, calls, executable := filepath.Join(dir, "state"), filepath.Join(dir, "calls"), filepath.Join(dir, "ephy-asr")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--check" ]; then
  printf 'c' >> %s
else
  printf 't' >> %s
  /bin/cat >/dev/null
fi
state=$(/bin/cat %s)
if [ "$state" = "denied" ]; then
  printf 'asr_permission_denied\n' >&2
  exit 1
fi
if [ "$1" = "--check" ]; then printf '%%s\n' "$state"; else printf 'synthetic transcript\n'; fi
`, testVoiceShellQuote(calls), testVoiceShellQuote(calls), testVoiceShellQuote(state))
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	p := &NativeVoiceASR{executable: executable, locale: "ja-JP", osName: "darwin", run: runVoiceProcess}
	p.readiness.now = func() time.Time { return time.Unix(0, clock.Load()) }
	return p, state, calls, clock
}

func voiceProcessCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNativeASRReadyAndTranscribeShareOneActualPreparationProcess(t *testing.T) {
	p, _, calls, _ := realASRReadinessFixture(t)
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := p.Readiness(context.Background()); err != nil || result.State != "ready" || !result.CanStart {
		t.Fatalf("%#v %v", result, err)
	}
	if _, err := p.Transcribe(context.Background(), providerTestWAV()); err != nil {
		t.Fatal(err)
	}
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := voiceProcessCalls(t, calls); got != "ct" {
		t.Fatalf("duplicated preparation processes: %q", got)
	}
}

func TestNativeASRCacheRechecksTTLPermissionConfigExecutableAndFailure(t *testing.T) {
	p, state, calls, clock := realASRReadinessFixture(t)
	ready := func() {
		t.Helper()
		if err := p.Ready(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ready()
	clock.Add(int64(5 * time.Second))
	ready()
	p.locale = "en-US" // Configuration changes occur before further calls，never concurrently．
	ready()
	if err := os.Chmod(p.executable, 0500); err != nil {
		t.Fatal(err)
	}
	ready()
	data, _ := os.ReadFile(p.executable)
	if err := os.WriteFile(p.executable+".replacement", data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p.executable+".replacement", p.executable); err != nil {
		t.Fatal(err)
	}
	ready()
	if got := voiceProcessCalls(t, calls); got != "ccccc" {
		t.Fatalf("config/file/TTL did not recheck: %q", got)
	}
	if err := os.WriteFile(state, []byte("denied"), 0600); err != nil {
		t.Fatal(err)
	}
	// Permission revocation between successful preflight and recognition must
	// invalidate a cached success immediately when the provider reports it．
	if _, err := p.Transcribe(context.Background(), providerTestWAV()); err == nil || err.Error() != "asr_permission_denied" {
		t.Fatalf("permission failure: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := p.Ready(context.Background()); err == nil {
			t.Fatal("permission failure cached as success")
		}
	}
	if got := voiceProcessCalls(t, calls); got != "ccccctcc" {
		t.Fatalf("failure was cached or missing: %q", got)
	}
	if err := os.WriteFile(state, []byte("permission_required"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := p.Readiness(context.Background())
	if err != nil || result.State != "permission_required" || !result.CanStart {
		t.Fatalf("permission preflight: %#v %v", result, err)
	}
	ready()
	if err := os.WriteFile(state, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(time.Second))
	result, err = p.Readiness(context.Background())
	if err != nil || result.State != "ready" {
		t.Fatalf("permission TTL did not refresh: %#v %v", result, err)
	}
	if got := voiceProcessCalls(t, calls); got != "ccccctcccc" {
		t.Fatalf("unexpected permission probe count: %q", got)
	}
}

func TestNativeReadinessParallelCallsShareProbeAndWaiterCancelIsIsolated(t *testing.T) {
	var calls atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	p := asrTestProvider(t, func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-release:
			return []byte("ready"), nil, nil
		}
	})
	first := make(chan error, 1)
	go func() { first <- p.Ready(context.Background()) }()
	<-entered
	waitCtx, cancelWaiter := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { waiter <- p.Ready(waitCtx) }()
	cancelWaiter()
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatal("waiter cancellation lost")
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := p.Ready(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	group.Wait()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("parallel preparation duplicated: %d", calls.Load())
	}
}

func TestNativeReadinessOwnerCancelDoesNotCacheFailure(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{})
	p := asrTestProvider(t, func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}
		return []byte("ready"), nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- p.Ready(ctx) }()
	<-entered
	second := make(chan error, 1)
	go func() { second <- p.Ready(context.Background()) }()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("owner cancellation lost")
	}
	if err := <-second; err != nil {
		t.Fatal("other caller inherited owner's cancellation")
	}
	if calls.Load() != 2 {
		t.Fatalf("canceled preparation was retained: %d", calls.Load())
	}
}

func TestNativeTTSPreparesOnceAcrossStreamsAndRechecksFailureTTLAndConfig(t *testing.T) {
	dir := t.TempDir()
	executable, calls, audio, state := filepath.Join(dir, "fixture-say"), filepath.Join(dir, "calls"), filepath.Join(dir, "fixture.wav"), filepath.Join(dir, "state")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$2" = "?" ]; then
  printf 'c' >> %s
  printf 'Kyoko ja_JP # sample\nOther ja_JP # sample\n'
else
  printf 's' >> %s
  /bin/cat >/dev/null
  if [ "$(/bin/cat %s)" = "fail" ]; then exit 1; fi
  /bin/cp %s "$4"
fi
`, testVoiceShellQuote(calls), testVoiceShellQuote(calls), testVoiceShellQuote(state), testVoiceShellQuote(audio))
	for path, content := range map[string][]byte{executable: []byte(script), audio: providerTestWAV(), state: []byte("ready")} {
		if err := os.WriteFile(path, content, 0700); err != nil {
			t.Fatal(err)
		}
	}
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	p := &NativeVoiceTTS{executable: executable, voice: "Kyoko", locale: "ja-JP", osName: "darwin", run: runVoiceProcess, tempRoot: dir}
	p.readiness.now = func() time.Time { return time.Unix(0, clock.Load()) }
	ready := func() {
		t.Helper()
		if err := p.Ready(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	stream := func(text string) {
		t.Helper()
		if err := p.Stream(context.Background(), text, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	ready()
	stream("一文です。次です！")
	stream("三文目です。")
	if got := voiceProcessCalls(t, calls); got != "csss" {
		t.Fatalf("repeated TTS preparation process: %q", got)
	}
	clock.Add(int64(30 * time.Second))
	ready()
	p.voice = "Other"
	ready()
	if err := os.Chmod(executable, 0500); err != nil {
		t.Fatal(err)
	}
	ready()
	if err := os.WriteFile(state, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.Stream(context.Background(), "失敗。", func([]byte) error { t.Fatal("failed synthesis emitted audio"); return nil }); err == nil {
		t.Fatal("synthesis failure accepted")
	}
	ready()
	if got := voiceProcessCalls(t, calls); got != "cssscccsc" {
		t.Fatalf("TTS config/TTL/failure did not invalidate: %q", got)
	}
}

func TestNativeTTSParallelPreparationAndCancelInvalidation(t *testing.T) {
	var checks atomic.Int64
	var cancelSynthesis atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	p := &NativeVoiceTTS{executable: "fixture-say", voice: "Kyoko", locale: "ja-JP", osName: "darwin", tempRoot: t.TempDir()}
	p.run = func(ctx context.Context, _ string, args []string, _ []byte) ([]byte, []byte, error) {
		if len(args) == 2 {
			if checks.Add(1) == 1 {
				close(entered)
				<-release
			}
			return []byte("Kyoko ja_JP # sample"), nil, nil
		}
		cancelSynthesis.Store(true)
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	first := make(chan error, 1)
	go func() { first <- p.Ready(context.Background()) }()
	<-entered
	var group sync.WaitGroup
	for index := 0; index < 24; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := p.Ready(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	group.Wait()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 1 {
		t.Fatalf("parallel TTS readiness duplicate: %d", checks.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := p.Stream(ctx, "取り消し。", func([]byte) error { t.Fatal("canceled synthesis emitted audio"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel lost: %v", err)
	}
	if !cancelSynthesis.Load() {
		t.Fatal("synthesis never reached cancel test")
	}
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 2 {
		t.Fatal("cancel left stale TTS preparation cached")
	}
}

func TestNativeReadinessInvalidationDuringProbeCannotReturnStaleSuccess(t *testing.T) {
	for _, change := range []string{"invalidation", "executable replacement"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int64
			entered, release := make(chan struct{}), make(chan struct{})
			p := asrTestProvider(t, func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
					return []byte("ready"), nil, nil
				}
				return nil, []byte("asr_permission_denied"), errors.New("private provider detail")
			})
			results := make(chan error, 2)
			go func() { results <- p.Ready(context.Background()) }()
			<-entered
			go func() { results <- p.Ready(context.Background()) }()
			if change == "invalidation" {
				p.readiness.invalidate()
			} else {
				if err := os.WriteFile(p.executable+".new", []byte("changed executable"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(p.executable+".new", p.executable); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			for i := 0; i < 2; i++ {
				if err := <-results; err == nil || err.Error() != "asr_permission_denied" {
					t.Fatalf("stale success returned after %s: %v", change, err)
				}
			}
			if calls.Load() < 2 {
				t.Fatal("invalidated in-flight preparation did not recheck")
			}
		})
	}
}

func TestNativeASRPermissionRequiredExpiresAndRecognitionRechecksPermission(t *testing.T) {
	p, state, calls, _ := realASRReadinessFixture(t)
	if err := os.WriteFile(state, []byte("permission_required"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := p.Readiness(context.Background()); err != nil || result.State != "permission_required" {
		t.Fatalf("%#v %v", result, err)
	}
	if _, err := p.Transcribe(context.Background(), providerTestWAV()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := p.Readiness(context.Background()); err != nil || result.State != "ready" {
		t.Fatalf("permission transition retained: %#v %v", result, err)
	}
	if got := voiceProcessCalls(t, calls); got != "ctc" {
		t.Fatalf("recognition did not invalidate undecided permission: %q", got)
	}
}
