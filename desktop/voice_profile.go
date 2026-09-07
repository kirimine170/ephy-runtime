package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// These contracts contain public identity and supported delivery controls only．
// Reference material and reusable clone prompts belong to the inference node．
type SpeechStyle struct {
	Affect        string  `json:"affect"`
	Intensity     float64 `json:"intensity"`
	Pace          float64 `json:"pace"`
	PitchHint     float64 `json:"pitch_hint"`
	Volume        float64 `json:"volume"`
	PauseStyle    string  `json:"pause_style"`
	Interruptible bool    `json:"interruptible"`
}
type VoiceControl struct {
	Type   string   `json:"type"`
	Min    float64  `json:"min"`
	Max    float64  `json:"max"`
	Step   float64  `json:"step,omitempty"`
	Values []string `json:"values,omitempty"`
}
type VoiceCapabilities struct {
	Controls      map[string]VoiceControl `json:"controls"`
	Streaming     bool                    `json:"streaming"`
	StreamingMode string                  `json:"streaming_mode"`
	Interruptible bool                    `json:"interruptible"`
}
type VoiceProfile struct {
	VoiceProfileID    string            `json:"voice_profile_id"`
	DisplayName       string            `json:"display_name"`
	Provider          string            `json:"provider"`
	ModelRevision     string            `json:"model_revision"`
	Language          string            `json:"language"`
	ClonePromptDigest string            `json:"clone_prompt_digest"`
	ProvenanceID      string            `json:"provenance_id"`
	DefaultStyle      SpeechStyle       `json:"default_style"`
	Capabilities      VoiceCapabilities `json:"capabilities"`
	Available         bool              `json:"available"`
	ErrorCode         string            `json:"error_code,omitempty"`
}
type VoiceProfileCatalog struct {
	DefaultProfileID string         `json:"default_profile_id"`
	Profiles         []VoiceProfile `json:"profiles"`
	ErrorCode        string         `json:"error_code,omitempty"`
}
type SpeechOptions struct {
	VoiceProfileID string       `json:"voice_profile_id"`
	Style          *SpeechStyle `json:"style,omitempty"`
}
type SpeechRequest struct {
	SpeechStyle
	SpeechText     string `json:"speech_text"`
	VoiceProfileID string `json:"voice_profile_id"`
}
type VoiceSpeechProvider interface {
	VoiceTTS
	Profile() VoiceProfile
	StreamSpeech(context.Context, SpeechRequest, func([]byte) error) error
}
type voiceSpeechPreparer interface {
	PrepareSpeech(SpeechOptions) (*preparedSpeech, error)
}
type preparedSpeech struct {
	provider VoiceSpeechProvider
	profile  VoiceProfile
	style    SpeechStyle
}

func defaultSpeechStyle() SpeechStyle {
	return SpeechStyle{Affect: "neutral", Intensity: 1, Pace: 1, Volume: 1, PauseStyle: "natural", Interruptible: true}
}
func copyVoiceProfile(profile VoiceProfile) VoiceProfile {
	controls := make(map[string]VoiceControl, len(profile.Capabilities.Controls))
	for name, control := range profile.Capabilities.Controls {
		control.Values = append([]string(nil), control.Values...)
		controls[name] = control
	}
	profile.Capabilities.Controls = controls
	return profile
}
func validateSpeechStyle(style SpeechStyle, profile VoiceProfile) error {
	invalid := errors.New("unsupported_voice_control")
	base := profile.DefaultStyle
	numbers := map[string][2]float64{"intensity": {style.Intensity, base.Intensity}, "pace": {style.Pace, base.Pace}, "pitch_hint": {style.PitchHint, base.PitchHint}, "volume": {style.Volume, base.Volume}}
	for name, values := range numbers {
		if math.IsNaN(values[0]) || math.IsInf(values[0], 0) {
			return invalid
		}
		control, supported := profile.Capabilities.Controls[name]
		if !supported {
			if values[0] != values[1] {
				return invalid
			}
			continue
		}
		if control.Type != "number" || values[0] < control.Min || values[0] > control.Max {
			return invalid
		}
	}
	for name, values := range map[string][2]string{"affect": {style.Affect, base.Affect}, "pause_style": {style.PauseStyle, base.PauseStyle}} {
		control, supported := profile.Capabilities.Controls[name]
		if !supported {
			if values[0] != values[1] {
				return invalid
			}
			continue
		}
		found := false
		for _, value := range control.Values {
			found = found || values[0] == value
		}
		if control.Type != "enum" || !found {
			return invalid
		}
	}
	// Explicit cancel is always honored．A non-interruptible delivery policy is a future gate．
	if !style.Interruptible || !profile.Capabilities.Interruptible {
		return invalid
	}
	return nil
}
func validateVoiceProfile(p VoiceProfile) error {
	if !interactionIdentifier.MatchString(p.VoiceProfileID) || !interactionIdentifier.MatchString(p.Provider) || !interactionMetadataID.MatchString(p.ModelRevision) || !voiceLocalePattern.MatchString(p.Language) || !utf8.ValidString(p.DisplayName) || strings.TrimSpace(p.DisplayName) == "" || utf8.RuneCountInString(p.DisplayName) > 80 || strings.ContainsAny(p.DisplayName, "\r\n\x00") || !p.Capabilities.Interruptible || !p.Capabilities.Streaming || p.Capabilities.StreamingMode != "phrase" || len(p.Capabilities.Controls) > 6 {
		return errors.New("invalid_voice_profile")
	}
	if p.ClonePromptDigest != "" {
		b, err := hex.DecodeString(p.ClonePromptDigest)
		if err != nil || len(b) != 32 {
			return errors.New("invalid_voice_profile")
		}
	}
	if p.ProvenanceID != "" && !interactionIdentifier.MatchString(p.ProvenanceID) {
		return errors.New("invalid_voice_profile")
	}
	neutral := defaultSpeechStyle()
	for name, c := range p.Capabilities.Controls {
		switch name {
		case "pace", "volume", "intensity", "pitch_hint":
			if c.Type != "number" || math.IsNaN(c.Min) || math.IsNaN(c.Max) || math.IsNaN(c.Step) || math.IsInf(c.Min, 0) || math.IsInf(c.Max, 0) || math.IsInf(c.Step, 0) || c.Min > c.Max || c.Min < -24 || c.Max > 24 || c.Step < 0 || c.Step > 24 {
				return errors.New("invalid_voice_profile")
			}
		case "affect", "pause_style":
			if c.Type != "enum" || len(c.Values) < 1 || len(c.Values) > 16 {
				return errors.New("invalid_voice_profile")
			}
			for _, v := range c.Values {
				if !interactionIdentifier.MatchString(v) || len(v) > 32 {
					return errors.New("invalid_voice_profile")
				}
			}
		default:
			return errors.New("invalid_voice_profile")
		}
	}
	// An unsupported setting must retain its neutral value，rather than claiming an ignored style．
	neutralProfile := copyVoiceProfile(p)
	neutralProfile.DefaultStyle = neutral
	if validateSpeechStyle(p.DefaultStyle, neutralProfile) != nil {
		return errors.New("invalid_voice_profile")
	}
	return nil
}
func (p *preparedSpeech) Identity() (string, string, string) {
	body, _ := json.Marshal(struct {
		Profile VoiceProfile
		Style   SpeechStyle
	}{p.profile, p.style})
	digest := sha256.Sum256(body)
	return p.profile.Provider, p.profile.ModelRevision, hex.EncodeToString(digest[:16])
}
func prepareSpeechProvider(tts VoiceTTS, options SpeechOptions) (*preparedSpeech, error) {
	if provider, ok := tts.(voiceSpeechPreparer); ok {
		return provider.PrepareSpeech(options)
	}
	if provider, ok := tts.(VoiceSpeechProvider); ok {
		return prepareProfile(provider, options)
	}
	if options.VoiceProfileID != "" || options.Style != nil {
		return nil, errors.New("voice_profile_unavailable")
	}
	return nil, nil // Existing test and third-party adapters retain their default contract．
}
func prepareProfile(provider VoiceSpeechProvider, options SpeechOptions) (*preparedSpeech, error) {
	profile := copyVoiceProfile(provider.Profile())
	if validateVoiceProfile(profile) != nil {
		return nil, errors.New("invalid_voice_profile")
	}
	if !profile.Available || (options.VoiceProfileID != "" && options.VoiceProfileID != profile.VoiceProfileID) {
		return nil, errors.New("voice_profile_unavailable")
	}
	style := profile.DefaultStyle
	if options.Style != nil {
		style = *options.Style
	}
	if err := validateSpeechStyle(style, profile); err != nil {
		return nil, err
	}
	return &preparedSpeech{provider: provider, profile: profile, style: style}, nil
}

// Catalog refresh replaces adapters atomically．An active operation owns its immutable prepared profile．
type VoiceTTSRegistry struct {
	mu        sync.Mutex
	refreshMu sync.Mutex
	native    *NativeVoiceTTS
	remote    *speechServiceClient
	providers map[string]VoiceSpeechProvider
	defaultID string
	catalog   VoiceProfileCatalog
	checked   time.Time
}

func NewVoiceTTSRegistry() *VoiceTTSRegistry {
	native := NewNativeVoiceTTS()
	r := &VoiceTTSRegistry{native: native, remote: newSpeechServiceClient(), defaultID: "macos-kyoko", providers: map[string]VoiceSpeechProvider{}}
	if os.Getenv("EPHY_TTS_BEARER_TOKEN") != "" || os.Getenv("EPHY_TTS_ENDPOINT") != "" {
		r.defaultID = "" // A configured service must resolve its default before Start．
	}
	r.providers[native.Profile().VoiceProfileID] = native
	r.catalog = VoiceProfileCatalog{DefaultProfileID: r.defaultID, Profiles: []VoiceProfile{native.Profile()}}
	return r
}
func (r *VoiceTTSRegistry) Ready(ctx context.Context) error { return r.native.Ready(ctx) }
func (r *VoiceTTSRegistry) Stream(ctx context.Context, text string, emit func([]byte) error) error {
	return r.native.Stream(ctx, text, emit)
}
func (r *VoiceTTSRegistry) PrepareSpeech(options SpeechOptions) (*preparedSpeech, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := options.VoiceProfileID
	if id == "" {
		id = r.defaultID
	}
	provider := r.providers[id]
	if provider == nil {
		return nil, errors.New("voice_profile_unavailable")
	}
	return prepareProfile(provider, options)
}
func (r *VoiceTTSRegistry) Profiles(ctx context.Context) VoiceProfileCatalog {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	if time.Since(r.checked) < 5*time.Second {
		result := cloneVoiceCatalog(r.catalog)
		r.mu.Unlock()
		return result
	}
	r.mu.Unlock()
	result := VoiceProfileCatalog{DefaultProfileID: r.defaultID, Profiles: []VoiceProfile{r.native.Profile()}}
	providers := map[string]VoiceSpeechProvider{r.native.Profile().VoiceProfileID: r.native}
	if r.remote != nil {
		remote, err := r.remote.profiles(ctx)
		if err != nil {
			result.ErrorCode = "tts_service_unavailable"
		} else {
			result.ErrorCode = remote.ErrorCode
			if remote.DefaultProfileID != "" {
				result.DefaultProfileID = remote.DefaultProfileID
			}
			for _, p := range remote.Profiles {
				if p.VoiceProfileID == r.native.Profile().VoiceProfileID {
					result.ErrorCode = "invalid_voice_profile"
					continue
				}
				result.Profiles = append(result.Profiles, p)
				providers[p.VoiceProfileID] = &ServiceVoiceTTS{client: r.remote, profile: copyVoiceProfile(p)}
			}
		}
	}
	r.mu.Lock()
	if provider := providers[result.DefaultProfileID]; provider == nil || !provider.Profile().Available {
		if result.ErrorCode == "" {
			result.ErrorCode = "voice_profile_unavailable"
		}
	}
	r.defaultID = result.DefaultProfileID
	r.providers = providers
	r.catalog = result
	if result.ErrorCode == "" {
		r.checked = time.Now()
	} else {
		r.checked = time.Time{}
	}
	r.mu.Unlock()
	return cloneVoiceCatalog(result)
}
func cloneVoiceCatalog(c VoiceProfileCatalog) VoiceProfileCatalog {
	c.Profiles = append([]VoiceProfile(nil), c.Profiles...)
	for i := range c.Profiles {
		c.Profiles[i] = copyVoiceProfile(c.Profiles[i])
	}
	return c
}
