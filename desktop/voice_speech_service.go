package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The endpoint is transport configuration，never a reference-material location．
// A remote inference node can be reached through an explicit loopback SSH tunnel．
type speechServiceClient struct {
	endpoint string
	client   *http.Client
}

func newSpeechServiceClient() *speechServiceClient {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("EPHY_TTS_ENDPOINT")), "/")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8767"
	}
	u, err := url.Parse(endpoint)
	valid := err == nil && u.Scheme == "http" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
	if !valid {
		endpoint = ""
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 5 * time.Second
	return &speechServiceClient{endpoint: endpoint, client: &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("tts_service_unavailable") }}}
}
func (c *speechServiceClient) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	if c.endpoint == "" {
		return nil, errors.New("invalid_voice_config")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("tts_service_unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("tts_service_unavailable")
	}
	if res.StatusCode != 200 {
		defer res.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 1025))
		var failure struct {
			ErrorCode string `json:"error_code"`
		}
		if readErr == nil && len(body) <= 1024 && decodeSpeechJSON(body, &failure) == nil && knownSpeechError(failure.ErrorCode) {
			return nil, errors.New(failure.ErrorCode)
		}
		return nil, errors.New("tts_service_unavailable")
	}
	return res, nil
}
func decodeSpeechJSON(data []byte, out any) error {
	if !utf8.Valid(data) {
		return errors.New("tts_stream_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return errors.New("tts_stream_invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("tts_stream_invalid")
	}
	return nil
}
func (c *speechServiceClient) profiles(ctx context.Context) (VoiceProfileCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	res, err := c.request(ctx, http.MethodGet, "/v1/voice-profiles", nil)
	if err != nil {
		return VoiceProfileCatalog{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
	var result VoiceProfileCatalog
	if err != nil || len(body) > 64*1024 || decodeSpeechJSON(body, &result) != nil || len(result.Profiles) > 16 || (result.ErrorCode != "" && !knownSpeechError(result.ErrorCode)) {
		return result, errors.New("invalid_voice_profile")
	}
	seen := map[string]bool{}
	for _, p := range result.Profiles {
		if validateVoiceProfile(p) != nil || seen[p.VoiceProfileID] || (p.ErrorCode != "" && !knownSpeechError(p.ErrorCode)) {
			return VoiceProfileCatalog{}, errors.New("invalid_voice_profile")
		}
		seen[p.VoiceProfileID] = true
	}
	return result, nil
}

type ServiceVoiceTTS struct {
	client  *speechServiceClient
	profile VoiceProfile
}

func (p *ServiceVoiceTTS) Profile() VoiceProfile { return copyVoiceProfile(p.profile) }
func (p *ServiceVoiceTTS) Ready(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !p.profile.Available {
		return errors.New("voice_profile_unavailable")
	}
	return nil // The inference service validates the pinned profile for every request．
}
func (p *ServiceVoiceTTS) Stream(ctx context.Context, text string, emit func([]byte) error) error {
	return p.StreamSpeech(ctx, SpeechRequest{SpeechStyle: p.profile.DefaultStyle, SpeechText: text, VoiceProfileID: p.profile.VoiceProfileID}, emit)
}
func (p *ServiceVoiceTTS) StreamSpeech(ctx context.Context, request SpeechRequest, emit func([]byte) error) error {
	if emit == nil || !utf8.ValidString(request.SpeechText) || utf8.RuneCountInString(request.SpeechText) > 16000 || strings.TrimSpace(request.SpeechText) == "" {
		return errors.New("invalid_speech_text")
	}
	if request.VoiceProfileID != p.profile.VoiceProfileID {
		return errors.New("voice_profile_unavailable")
	}
	if err := validateSpeechStyle(request.SpeechStyle, p.profile); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	phrases, err := splitCommittedSpeechPhrases(request.SpeechText, 180)
	if err != nil {
		return err
	}
	for _, phrase := range phrases {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		request.SpeechText = phrase
		if err := p.streamPhrase(ctx, request, emit); err != nil {
			return err
		}
	}
	return nil
}

// Unlike the native provider's legacy rune fallback，a cloned phrase is never
// cut through an unbroken word．Oversized units require text fallback．
func splitCommittedSpeechPhrases(text string, limit int) ([]string, error) {
	var phrases []string
	runes := []rune(text)
	for len(runes) > 0 {
		end := min(limit, len(runes))
		soft := 0
		sentence := 0
		for i, r := range runes[:end] {
			if strings.ContainsRune("．。.!！?？\n", r) {
				sentence = i + 1
				break
			}
			if strings.ContainsRune("，、,;；:：", r) || unicode.IsSpace(r) {
				soft = i + 1
			}
		}
		if sentence > 0 {
			end = sentence
		} else if len(runes) > limit {
			if soft == 0 {
				return nil, errors.New("invalid_speech_text")
			}
			end = soft
		}
		if phrase := strings.TrimSpace(string(runes[:end])); phrase != "" {
			phrases = append(phrases, phrase)
		}
		runes = runes[end:]
	}
	return phrases, nil
}
func knownSpeechError(code string) bool {
	switch code {
	case "tts_unavailable", "tts_service_unavailable", "tts_failed", "tts_timeout", "tts_canceled", "tts_incomplete", "tts_invalid_audio", "tts_audio_limit", "tts_stream_invalid", "tts_stream_eof", "voice_profile_unavailable", "invalid_voice_profile", "unsupported_voice_control", "invalid_speech_text", "invalid_voice_config", "voice_profile_changed", "tts_busy", "tts_model_revision_mismatch", "tts_asset_invalid":
		return true
	}
	return false
}
func (p *ServiceVoiceTTS) streamPhrase(ctx context.Context, request SpeechRequest, emit func([]byte) error) error {
	id := interactionID("speech_")
	payload := struct {
		SpeechRequest
		RequestID         string `json:"request_id"`
		ModelRevision     string `json:"model_revision"`
		ClonePromptDigest string `json:"clone_prompt_digest"`
	}{request, id, p.profile.ModelRevision, p.profile.ClonePromptDigest}
	body, _ := json.Marshal(payload)
	res, err := p.client.request(ctx, http.MethodPost, "/v1/speech", body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if strings.Split(res.Header.Get("Content-Type"), ";")[0] != "application/x-ndjson" {
		return errors.New("tts_stream_invalid")
	}
	reader := bufio.NewReaderSize(&speechResponseReader{reader: res.Body, remaining: 24 << 20}, 64<<10)
	sequence, total, completed := 0, 0, false
	for {
		line, err := readSpeechLine(reader, 12<<20)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == io.EOF {
			if completed {
				return nil
			}
			return errors.New("tts_stream_eof")
		}
		if err != nil {
			return errors.New("tts_stream_eof")
		}
		if completed {
			return errors.New("tts_stream_invalid")
		}
		var frame struct {
			Type         string `json:"type"`
			RequestID    string `json:"request_id"`
			Sequence     int    `json:"sequence"`
			WAVBase64    string `json:"wav_base64"`
			FinishReason string `json:"finish_reason"`
			ErrorCode    string `json:"error_code"`
		}
		if decodeSpeechJSON(line, &frame) != nil || frame.RequestID != id {
			return errors.New("tts_stream_invalid")
		}
		switch frame.Type {
		case "audio":
			if frame.Sequence != sequence+1 || sequence >= 64 || frame.FinishReason != "" || frame.ErrorCode != "" {
				return errors.New("tts_stream_invalid")
			}
			audio, err := base64.StdEncoding.Strict().DecodeString(frame.WAVBase64)
			if err != nil || !validVoiceWAV(audio, 60) {
				return errors.New("tts_invalid_audio")
			}
			total += len(audio)
			if total > 16<<20 {
				return errors.New("tts_audio_limit")
			}
			sequence++
			if err := emit(audio); err != nil {
				return err
			}
		case "completed":
			if sequence == 0 || frame.Sequence != sequence || frame.FinishReason != "stop" || frame.WAVBase64 != "" || frame.ErrorCode != "" {
				return errors.New("tts_stream_invalid")
			}
			completed = true
		case "error":
			if !knownSpeechError(frame.ErrorCode) {
				return errors.New("tts_failed")
			}
			return errors.New(frame.ErrorCode)
		default:
			return errors.New("tts_stream_invalid")
		}
	}
}

// A size limit must not manufacture the transport EOF required for completion．
type speechResponseReader struct {
	reader    io.Reader
	remaining int
}

func (r *speechResponseReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.remaining+1 {
		buffer = buffer[:r.remaining+1]
	}
	n, err := r.reader.Read(buffer)
	if n > r.remaining {
		return 0, errors.New("tts_audio_limit")
	}
	r.remaining -= n
	return n, err
}
func readSpeechLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, errors.New("tts_stream_invalid")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) > 0 {
			return nil, errors.New("tts_stream_eof")
		}
		return line, err
	}
}
