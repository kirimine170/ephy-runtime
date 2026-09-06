package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// Processes receive speech only on stdin．Their diagnostics never cross the UI boundary．
type voiceProcessRunner func(context.Context, string, []string, []byte) ([]byte, []byte, error)

func runVoiceProcess(ctx context.Context, executable string, args []string, input []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

var voiceLocalePattern = regexp.MustCompile(`^[a-z]{2,3}[-_][A-Z]{2}$`)
var voiceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 ()_-]{0,79}$`)
var installedVoicePattern = regexp.MustCompile(`^(.+?)\s+([a-z]{2,3}_[A-Z]{2})\s+#`)

func voiceLocale() string {
	locale := strings.TrimSpace(os.Getenv("EPHY_VOICE_LOCALE"))
	if locale == "" {
		locale = "ja-JP"
	}
	return strings.ReplaceAll(locale, "_", "-")
}

// NativeVoiceASR transcribes a complete mono PCM16 WAV using the local Speech helper．
type NativeVoiceASR struct {
	executable string
	locale     string
	osName     string
	run        voiceProcessRunner
}

func NewNativeVoiceASR(root string) *NativeVoiceASR {
	executable := strings.TrimSpace(os.Getenv("EPHY_ASR_HELPER"))
	if executable == "" {
		executable = filepath.Join(root, "bin", "ephy-asr")
	}
	return &NativeVoiceASR{executable: executable, locale: voiceLocale(), osName: runtime.GOOS, run: runVoiceProcess}
}

func (p *NativeVoiceASR) Identity() (string, string, string) {
	return "macos-speech", p.locale, "on-device"
}

func (p *NativeVoiceASR) Ready(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.osName != "darwin" {
		return errors.New("asr_unavailable")
	}
	if !voiceLocalePattern.MatchString(p.locale) || !filepath.IsAbs(p.executable) {
		return errors.New("invalid_voice_config")
	}
	info, err := os.Stat(p.executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("asr_unavailable")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, stderr, err := p.run(checkCtx, p.executable, []string{"--check", "--locale", p.locale}, nil)
	return asrProcessError(checkCtx, stderr, err)
}

func (p *NativeVoiceASR) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if !validVoiceWAV(audio, 60) {
		return "", errors.New("invalid_audio")
	}
	if err := p.Ready(ctx); err != nil {
		return "", err
	}
	processCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stdout, stderr, err := p.run(processCtx, p.executable, []string{"--locale", p.locale}, audio)
	if err = asrProcessError(processCtx, stderr, err); err != nil {
		return "", err
	}
	transcript := strings.TrimSpace(string(stdout))
	if transcript == "" || !utf8.ValidString(transcript) || len(transcript) > 64*1024 {
		return "", errors.New("asr_empty_transcript")
	}
	return transcript, nil
}

func asrProcessError(ctx context.Context, stderr []byte, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	switch strings.TrimSpace(string(stderr)) {
	case "asr_permission_denied", "asr_permission_restricted", "asr_on_device_unavailable", "asr_unavailable", "asr_timeout", "asr_empty_transcript", "invalid_audio", "invalid_voice_config":
		return errors.New(strings.TrimSpace(string(stderr)))
	default:
		return errors.New("asr_failed")
	}
}

// NativeVoiceTTS emits independently playable WAV chunks synthesized by installed macOS voices．
type NativeVoiceTTS struct {
	executable string
	voice      string
	locale     string
	osName     string
	tempRoot   string
	run        voiceProcessRunner
}

func NewNativeVoiceTTS() *NativeVoiceTTS {
	voice := strings.TrimSpace(os.Getenv("EPHY_TTS_VOICE"))
	if voice == "" {
		voice = "Kyoko"
	}
	return &NativeVoiceTTS{executable: "/usr/bin/say", voice: voice, locale: voiceLocale(), osName: runtime.GOOS, run: runVoiceProcess}
}

func (p *NativeVoiceTTS) Identity() (string, string, string) {
	return "macos-say", p.voice, "pcm16-22050-" + p.locale
}

func (p *NativeVoiceTTS) Ready(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.osName != "darwin" {
		return errors.New("tts_unavailable")
	}
	if !voiceNamePattern.MatchString(p.voice) || !voiceLocalePattern.MatchString(p.locale) {
		return errors.New("invalid_voice_config")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, _, err := p.run(checkCtx, p.executable, []string{"-v", "?"}, nil)
	if checkCtx.Err() != nil {
		return checkCtx.Err()
	}
	if err != nil {
		return errors.New("tts_unavailable")
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		match := installedVoicePattern.FindStringSubmatch(line)
		if len(match) == 3 && strings.TrimSpace(match[1]) == p.voice && strings.ReplaceAll(match[2], "_", "-") == p.locale {
			return nil
		}
	}
	return errors.New("tts_unavailable")
}

func (p *NativeVoiceTTS) Stream(ctx context.Context, text string, emit func([]byte) error) error {
	if emit == nil || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 16000 {
		return errors.New("invalid_speech_text")
	}
	if err := p.Ready(ctx); err != nil {
		return err
	}
	for _, chunk := range splitVoiceSentences(text, 180) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		audio, err := p.synthesize(ctx, chunk)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := emit(audio); err != nil {
			return err
		}
	}
	return nil
}

func (p *NativeVoiceTTS) synthesize(ctx context.Context, text string) (audio []byte, resultErr error) {
	// A private directory protects the file even if say recreates it with its own mode．
	dir, err := os.MkdirTemp(p.tempRoot, "ephy-tts-")
	if err != nil {
		return nil, errors.New("tts_failed")
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			audio = nil
			resultErr = errors.Join(resultErr, errors.New("tts_cleanup_failed"))
		}
	}()
	output := filepath.Join(dir, "speech.wav")
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.New("tts_failed")
	}
	if err = file.Close(); err != nil {
		return nil, errors.New("tts_failed")
	}
	processCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _, err = p.run(processCtx, p.executable, []string{"-v", p.voice, "-o", output, "--file-format=WAVE", "--data-format=LEI16@22050", "--channels=1", "-f", "-"}, []byte(text))
	if processCtx.Err() != nil {
		return nil, processCtx.Err()
	}
	if err != nil {
		return nil, errors.New("tts_failed")
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8*1024*1024 || os.Chmod(output, 0600) != nil {
		return nil, errors.New("tts_invalid_audio")
	}
	audio, err = os.ReadFile(output)
	if err != nil || !validVoiceWAV(audio, 60) {
		return nil, errors.New("tts_invalid_audio")
	}
	// No raw audio file exists by the time the consumer receives a chunk．
	if err := os.RemoveAll(dir); err != nil {
		return nil, errors.New("tts_cleanup_failed")
	}
	return audio, nil
}

func splitVoiceSentences(text string, limit int) []string {
	// Do not pass say's embedded speech commands through from model output．
	text = strings.ReplaceAll(strings.ReplaceAll(text, "[[", ""), "]]", "")
	var chunks []string
	var buffer []rune
	flush := func() {
		if chunk := strings.TrimSpace(string(buffer)); chunk != "" {
			chunks = append(chunks, chunk)
		}
		buffer = nil
	}
	for _, r := range text {
		buffer = append(buffer, r)
		if strings.ContainsRune("．。.!！?？\n", r) || len(buffer) >= limit {
			flush()
		}
	}
	flush()
	return chunks
}

func validVoiceWAV(audio []byte, maxSeconds int) bool {
	if len(audio) < 44 || len(audio) > 8*1024*1024 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(audio[4:8]))+8 != uint64(len(audio)) {
		return false
	}
	var sampleRate uint32
	var dataBytes int
	hasFormat, hasData := false, false
	for offset := 12; offset < len(audio); {
		if offset+8 > len(audio) {
			return false
		}
		size := int(binary.LittleEndian.Uint32(audio[offset+4 : offset+8]))
		start := offset + 8
		if size > len(audio)-start {
			return false
		}
		switch string(audio[offset : offset+4]) {
		case "fmt ":
			if hasFormat || size < 16 {
				return false
			}
			format := audio[start : start+size]
			sampleRate = binary.LittleEndian.Uint32(format[4:8])
			if binary.LittleEndian.Uint16(format[:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint16(format[14:16]) != 16 || binary.LittleEndian.Uint16(format[12:14]) != 2 || sampleRate < 8000 || sampleRate > 48000 || binary.LittleEndian.Uint32(format[8:12]) != sampleRate*2 {
				return false
			}
			hasFormat = true
		case "data":
			if hasData || size == 0 || size%2 != 0 {
				return false
			}
			dataBytes, hasData = size, true
		}
		offset = start + size + size%2
		if offset > len(audio) {
			return false
		}
	}
	return hasFormat && hasData && uint64(dataBytes) <= uint64(sampleRate)*2*uint64(maxSeconds)
}
