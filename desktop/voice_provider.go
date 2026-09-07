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
	"strconv"
	"strings"
	"time"
	"unicode"
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

// NativeVoiceASR supports incremental PCM sessions and legacy complete-WAV transcription．
type NativeVoiceASR struct {
	streamStart  nativeASRStreamStart
	streamLimits nativeASRStreamLimits
	readiness    voiceReadinessCache
	executable   string
	locale       string
	osName       string
	run          voiceProcessRunner
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
	_, err := p.Readiness(ctx)
	return err
}

// Readiness is a non-prompting preflight．Successful states expire after five
// seconds，or one second while permission is undecided．Failures are never cached．
func (p *NativeVoiceASR) Readiness(ctx context.Context) (VoiceReadiness, error) {
	ctx, cancelReadiness := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReadiness()
	unavailable := func(err error) (VoiceReadiness, error) {
		code := "asr_failed"
		if errors.Is(err, context.Canceled) {
			code = "asr_canceled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "asr_timeout"
		} else if err != nil {
			code = err.Error()
		}
		return VoiceReadiness{State: "unavailable", ErrorCode: code}, err
	}
	if ctx.Err() != nil {
		return unavailable(ctx.Err())
	}
	if p.osName != "darwin" {
		p.readiness.invalidate()
		return unavailable(errors.New("asr_unavailable"))
	}
	if !voiceLocalePattern.MatchString(p.locale) || !filepath.IsAbs(p.executable) {
		p.readiness.invalidate()
		return unavailable(errors.New("invalid_voice_config"))
	}
	currentKey := func() (voiceReadinessKey, error) {
		info, err := os.Stat(p.executable)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return voiceReadinessKey{}, errors.New("asr_unavailable")
		}
		return voiceReadinessKey{configuration: p.executable + "\x00" + p.locale + "\x00" + p.osName, executable: info}, nil
	}
	result, err := p.readiness.get(ctx, currentKey, 5*time.Second, func(ctx context.Context) (VoiceReadiness, error) {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stdout, stderr, err := p.run(checkCtx, p.executable, []string{"--check", "--locale", p.locale}, nil)
		if err := asrProcessError(checkCtx, stderr, err); err != nil {
			return unavailable(err)
		}
		switch strings.TrimSpace(string(stdout)) {
		case "ready":
			return VoiceReadiness{State: "ready", CanStart: true}, nil
		case "permission_required":
			return VoiceReadiness{State: "permission_required", CanStart: true}, nil
		default:
			return unavailable(errors.New("asr_failed"))
		}
	})
	if err != nil {
		return unavailable(err)
	}
	return result, nil
}

func (p *NativeVoiceASR) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if !validVoiceWAV(audio, 60) {
		return "", errors.New("invalid_audio")
	}
	readiness, err := p.Readiness(ctx)
	if err != nil {
		return "", err
	}
	if readiness.State == "permission_required" {
		defer p.readiness.invalidate()
	}
	processCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stdout, stderr, err := p.run(processCtx, p.executable, []string{"--locale", p.locale}, audio)
	if err = asrProcessError(processCtx, stderr, err); err != nil {
		p.readiness.invalidate()
		return "", err
	}
	transcript := strings.TrimSpace(string(stdout))
	if transcript == "" || !utf8.ValidString(transcript) || len(transcript) > 64*1024 {
		p.readiness.invalidate()
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
	readiness  voiceReadinessCache
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

func (p *NativeVoiceTTS) Profile() VoiceProfile {
	return VoiceProfile{VoiceProfileID: "macos-kyoko", DisplayName: p.voice, Provider: "macos-say", ModelRevision: "installed-voice", Language: p.locale,
		DefaultStyle: defaultSpeechStyle(), Available: p.osName == "darwin", Capabilities: VoiceCapabilities{Streaming: true, StreamingMode: "phrase", Interruptible: true, Controls: map[string]VoiceControl{
			"pace": {Type: "number", Min: 0.5, Max: 2, Step: 0.1}, "volume": {Type: "number", Min: 0, Max: 1, Step: 0.1},
		}}}
}

func (p *NativeVoiceTTS) Ready(ctx context.Context) error {
	ctx, cancelReadiness := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReadiness()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.osName != "darwin" {
		p.readiness.invalidate()
		return errors.New("tts_unavailable")
	}
	if !voiceNamePattern.MatchString(p.voice) || !voiceLocalePattern.MatchString(p.locale) {
		p.readiness.invalidate()
		return errors.New("invalid_voice_config")
	}
	currentKey := func() (voiceReadinessKey, error) {
		info, _ := os.Stat(p.executable)
		return voiceReadinessKey{configuration: p.executable + "\x00" + p.voice + "\x00" + p.locale + "\x00" + p.osName, executable: info}, nil
	}
	_, err := p.readiness.get(ctx, currentKey, 30*time.Second, func(ctx context.Context) (VoiceReadiness, error) {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stdout, _, err := p.run(checkCtx, p.executable, []string{"-v", "?"}, nil)
		if checkCtx.Err() != nil {
			return VoiceReadiness{}, checkCtx.Err()
		}
		if err != nil {
			return VoiceReadiness{}, errors.New("tts_unavailable")
		}
		for _, line := range strings.Split(string(stdout), "\n") {
			match := installedVoicePattern.FindStringSubmatch(line)
			if len(match) == 3 && strings.TrimSpace(match[1]) == p.voice && strings.ReplaceAll(match[2], "_", "-") == p.locale {
				return VoiceReadiness{State: "ready", CanStart: true}, nil
			}
		}
		return VoiceReadiness{}, errors.New("tts_unavailable")
	})
	return err
}

func (p *NativeVoiceTTS) Stream(ctx context.Context, text string, emit func([]byte) error) error {
	return p.StreamSpeech(ctx, SpeechRequest{SpeechStyle: defaultSpeechStyle(), SpeechText: text, VoiceProfileID: p.Profile().VoiceProfileID}, emit)
}

func (p *NativeVoiceTTS) StreamSpeech(ctx context.Context, request SpeechRequest, emit func([]byte) error) error {
	text := request.SpeechText
	if request.VoiceProfileID != p.Profile().VoiceProfileID {
		return errors.New("voice_profile_unavailable")
	}
	if err := validateSpeechStyle(request.SpeechStyle, p.Profile()); err != nil {
		return err
	}
	if emit == nil || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 16000 {
		return errors.New("invalid_speech_text")
	}
	if err := p.Ready(ctx); err != nil {
		return err
	}
	for _, chunk := range splitVoiceSentences(text, 180) {
		if ctx.Err() != nil {
			p.readiness.invalidate()
			return ctx.Err()
		}
		audio, err := p.synthesizeStyled(ctx, chunk, request.SpeechStyle)
		if err != nil {
			p.readiness.invalidate()
			return err
		}
		if ctx.Err() != nil {
			p.readiness.invalidate()
			return ctx.Err()
		}
		if err := emit(audio); err != nil {
			return err
		}
	}
	return nil
}

func (p *NativeVoiceTTS) synthesize(ctx context.Context, text string) (audio []byte, resultErr error) {
	return p.synthesizeStyled(ctx, text, defaultSpeechStyle())
}

func (p *NativeVoiceTTS) synthesizeStyled(ctx context.Context, text string, style SpeechStyle) (audio []byte, resultErr error) {
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
	args := []string{"-v", p.voice, "-o", output, "--file-format=WAVE", "--data-format=LEI16@22050", "--channels=1", "-f", "-"}
	// Neutral retains the installed voice's natural rate．Explicit pace uses say's documented WPM control．
	if style.Pace != 1 {
		args = append(args, "-r", strconv.Itoa(int(175*style.Pace)))
	}
	_, _, err = p.run(processCtx, p.executable, args, []byte(text))
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
	if style.Volume != 1 {
		scaleSpeechPCM(audio, style.Volume)
	}
	// No raw audio file exists by the time the consumer receives a chunk．
	if err := os.RemoveAll(dir); err != nil {
		return nil, errors.New("tts_cleanup_failed")
	}
	return audio, nil
}

// Operates only on an already validated PCM16 WAV，preserving framing and duration．
func scaleSpeechPCM(audio []byte, volume float64) {
	for offset := 12; offset < len(audio); {
		size := int(binary.LittleEndian.Uint32(audio[offset+4 : offset+8]))
		start := offset + 8
		if string(audio[offset:offset+4]) == "data" {
			for i := start; i < start+size; i += 2 {
				value := int16(binary.LittleEndian.Uint16(audio[i : i+2]))
				binary.LittleEndian.PutUint16(audio[i:i+2], uint16(int16(float64(value)*volume)))
			}
		}
		offset = start + size + size%2
	}
}

func splitVoiceSentences(text string, maxRunes int) []string {
	// Do not pass say's embedded speech commands through from model output．
	text = strings.ReplaceAll(strings.ReplaceAll(text, "[[", ""), "]]", "")
	if maxRunes <= 0 {
		maxRunes = 180
	}
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		limit := min(maxRunes, len(runes))
		boundary, softBoundary := 0, 0
		for index, r := range runes[:limit] {
			if strings.ContainsRune("．。.!！?？\n", r) {
				boundary = index + 1
				break
			}
			if strings.ContainsRune("，、,;；:：", r) || unicode.IsSpace(r) {
				softBoundary = index + 1
			}
		}
		if boundary == 0 {
			boundary = limit
			if len(runes) > limit && softBoundary > 0 {
				boundary = softBoundary
			}
		}
		if chunk := strings.TrimSpace(string(runes[:boundary])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[boundary:]
	}
	return chunks
}
