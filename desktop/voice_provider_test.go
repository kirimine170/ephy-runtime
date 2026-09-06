package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func providerTestWAV() []byte {
	pcm := make([]byte, 320)
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint32(16000))
	binary.Write(&wav, binary.LittleEndian, uint32(32000))
	binary.Write(&wav, binary.LittleEndian, uint16(2))
	binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

func TestVoiceWAVValidation(t *testing.T) {
	valid := providerTestWAV()
	if !validVoiceWAV(valid, 60) {
		t.Fatal("valid mono PCM16 WAV rejected")
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"truncated":         func(b []byte) []byte { return b[:len(b)-1] },
		"stereo":            func(b []byte) []byte { b[22] = 2; return b },
		"float":             func(b []byte) []byte { b[20] = 3; return b },
		"wrong byte rate":   func(b []byte) []byte { b[28] ^= 0xff; return b },
		"wrong RIFF length": func(b []byte) []byte { b[4] = 0; return b },
		"chunk overflow":    func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:44], 0xffffffff); return b },
	} {
		t.Run(name, func(t *testing.T) {
			if validVoiceWAV(mutate(append([]byte(nil), valid...)), 60) {
				t.Fatal("invalid WAV accepted")
			}
		})
	}
	if validVoiceWAV(valid, 0) {
		t.Fatal("duration boundary not enforced")
	}
}

func asrTestProvider(t *testing.T, runner voiceProcessRunner) *NativeVoiceASR {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ephy-asr")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	return &NativeVoiceASR{executable: path, locale: "ja-JP", osName: "darwin", run: runner}
}

func TestNativeVoiceASRStdinAndReadiness(t *testing.T) {
	calls := 0
	p := asrTestProvider(t, func(ctx context.Context, executable string, args []string, input []byte) ([]byte, []byte, error) {
		calls++
		if calls == 1 {
			if args[0] != "--check" || len(input) != 0 {
				t.Fatal("readiness must not include audio or request recognition")
			}
			return []byte("permission_required\n"), nil, nil
		}
		if !bytes.Equal(input, providerTestWAV()) || strings.Join(args, " ") != "--locale ja-JP" {
			t.Fatal("audio must use stdin and explicit locale")
		}
		return []byte(" synthetic transcript \n"), nil, nil
	})
	text, err := p.Transcribe(context.Background(), providerTestWAV())
	if err != nil || text != "synthetic transcript" || calls != 2 {
		t.Fatalf("unexpected result: %q %v calls=%d", text, err, calls)
	}
}

func TestNativeVoiceASRFailureCodesAndCancellation(t *testing.T) {
	for _, code := range []string{"asr_on_device_unavailable", "asr_permission_denied", "asr_permission_restricted", "private diagnostic must not leak"} {
		t.Run(code, func(t *testing.T) {
			p := asrTestProvider(t, func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
				return nil, []byte(code), errors.New("private process error")
			})
			err := p.Ready(context.Background())
			want := code
			if strings.HasPrefix(code, "private") {
				want = "asr_failed"
			}
			if err == nil || err.Error() != want {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	called := false
	p := asrTestProvider(t, func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
		called = true
		return nil, nil, nil
	})
	if _, err := p.Transcribe(context.Background(), []byte("invalid")); err == nil || called {
		t.Fatal("invalid audio reached the helper")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Ready(ctx); !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled readiness reached the helper")
	}
}

func TestNativeVoiceTTSChunksAndCleanup(t *testing.T) {
	tempRoot := t.TempDir()
	var synthesized []string
	var paths []string
	p := &NativeVoiceTTS{executable: "fixture-say", voice: "Kyoko", locale: "ja-JP", osName: "darwin", tempRoot: tempRoot}
	p.run = func(ctx context.Context, command string, args []string, input []byte) ([]byte, []byte, error) {
		if len(args) == 2 {
			return []byte("Kyoko              ja_JP    # sample\n"), nil, nil
		}
		if strings.Join(args[4:], " ") != "--file-format=WAVE --data-format=LEI16@22050 --channels=1 -f -" {
			t.Fatalf("unexpected output arguments: %v", args)
		}
		path := args[3]
		paths = append(paths, path)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("temporary WAV must be private")
		}
		parentInfo, err := os.Stat(filepath.Dir(path))
		if err != nil || parentInfo.Mode().Perm() != 0700 {
			t.Fatal("temporary directory must be private")
		}
		synthesized = append(synthesized, string(input))
		return nil, nil, os.WriteFile(path, providerTestWAV(), 0600)
	}
	emitted := 0
	err := p.Stream(context.Background(), "一文です．次です！"+strings.Repeat("あ", 190), func(audio []byte) error {
		emitted++
		if !validVoiceWAV(audio, 60) {
			t.Fatal("callback did not receive a complete WAV")
		}
		for _, path := range paths {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("raw audio still exists at callback time")
			}
		}
		return nil
	})
	if err != nil || emitted != 4 || len(synthesized) != 4 {
		t.Fatalf("err=%v emitted=%d chunks=%d", err, emitted, len(synthesized))
	}
	if synthesized[2] != strings.Repeat("あ", 180) || synthesized[3] != strings.Repeat("あ", 10) {
		t.Fatal("long committed utterance did not respect the provider chunk bound")
	}
	entries, _ := os.ReadDir(tempRoot)
	if len(entries) != 0 {
		t.Fatal("temporary audio directory was retained")
	}
}

func TestNativeVoiceTTSFailureAndCancellationCleanAudio(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancelRun], func(t *testing.T) {
			tempRoot := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &NativeVoiceTTS{voice: "Kyoko", locale: "ja-JP", osName: "darwin", tempRoot: tempRoot}
			p.run = func(ctx context.Context, _ string, args []string, _ []byte) ([]byte, []byte, error) {
				if len(args) == 2 {
					return []byte("Kyoko ja_JP # sample"), nil, nil
				}
				if err := os.WriteFile(args[3], providerTestWAV(), 0600); err != nil {
					t.Fatal(err)
				}
				if cancelRun {
					cancel()
					return nil, nil, ctx.Err()
				}
				return nil, []byte("sensitive detail"), errors.New("sensitive detail")
			}
			err := p.Stream(ctx, "synthetic", func([]byte) error { t.Fatal("failed synthesis emitted audio"); return nil })
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unsanitized error: %v", err)
			}
			if cancelRun && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			entries, _ := os.ReadDir(tempRoot)
			if len(entries) != 0 {
				t.Fatal("failed synthesis retained audio")
			}
		})
	}
}

func TestNativeVoiceConfigurationAndUnavailable(t *testing.T) {
	for _, test := range []struct{ voice, locale, osName, installed, want string }{
		{"Kyoko", "ja-JP", "linux", "", "tts_unavailable"},
		{"--voice", "ja-JP", "darwin", "", "invalid_voice_config"},
		{"Kyoko", "ja-JP;command", "darwin", "", "invalid_voice_config"},
		{"Kyoko", "ja-JP", "darwin", "Samantha en_US # sample", "tts_unavailable"},
		{"Kyoko", "ja-JP", "darwin", "Kyoko en_US # sample", "tts_unavailable"},
	} {
		p := &NativeVoiceTTS{voice: test.voice, locale: test.locale, osName: test.osName, run: func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
			return []byte(test.installed), nil, nil
		}}
		if err := p.Ready(context.Background()); err == nil || err.Error() != test.want {
			t.Fatalf("unexpected readiness: %v", err)
		}
	}
}

func TestVoiceProcessCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err := runVoiceProcess(ctx, "/bin/sh", []string{"-c", "exec sleep 30"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("process cancellation did not stop promptly: %v", err)
	}
}

func TestNativeVoiceTTSInstalledSmoke(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("EPHY_VOICE_INTEGRATION") != "1" {
		t.Skip("opt-in local voice synthesis; no playback")
	}
	p := NewNativeVoiceTTS()
	p.tempRoot = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	emitted := 0
	err := p.Stream(ctx, "テストです．", func(wav []byte) error {
		emitted++
		if !validVoiceWAV(wav, 60) {
			t.Fatal("installed provider returned invalid WAV")
		}
		t.Logf("installed local TTS produced %d bytes of validated PCM16 mono WAV", len(wav))
		return nil
	})
	if err != nil || emitted != 1 {
		t.Fatalf("installed provider: %v chunks=%d", err, emitted)
	}
	entries, _ := os.ReadDir(p.tempRoot)
	if len(entries) != 0 {
		t.Fatal("installed provider retained temporary audio")
	}
}
