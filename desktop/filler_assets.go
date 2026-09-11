package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// Assets are private，pre-generated and never inserted into SpeechUnits．
type fillerAsset struct {
	Candidate  string  `json:"candidate"`
	File       string  `json:"file"`
	SHA256     string  `json:"sha256"`
	DurationMS float64 `json:"duration_ms"`
	Approved   bool    `json:"approved"`
}
type fillerManifest struct {
	SchemaVersion       int           `json:"schema_version"`
	Enabled             bool          `json:"enabled"`
	HeadphonesConfirmed bool          `json:"headphones_confirmed"`
	Profile             VoiceProfile  `json:"profile"`
	Assets              []fillerAsset `json:"assets"`
}
type FillerAudio struct {
	// Candidate never crosses the live bridge or trace．
	Kind        string  `json:"kind"`
	AudioBase64 string  `json:"audio_base64"`
	DurationMS  float64 `json:"duration_ms"`
}

func readFillerPrivate(path string, maxBytes int64) ([]byte, error) {
	invalid := errors.New("filler_asset_unavailable")
	clean, err := filepath.EvalSymlinks(path)
	if err != nil || clean != filepath.Clean(path) {
		return nil, invalid
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > maxBytes {
		return nil, invalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm()&0077 != 0 {
		return nil, invalid
	}
	// Stat_t layout differs between platforms．Fail closed without a link count．
	stat := reflect.Indirect(reflect.ValueOf(after.Sys()))
	if !stat.IsValid() || stat.Kind() != reflect.Struct {
		return nil, invalid
	}
	links := stat.FieldByName("Nlink")
	if !links.IsValid() || !links.CanUint() || links.Uint() != 1 {
		return nil, invalid
	}
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return nil, invalid
	}
	return body, nil
}

func loadFillerAssets(root string, speech *preparedSpeech) ([]FillerAudio, error) {
	invalid := errors.New("filler_asset_unavailable")
	if root == "" || !filepath.IsAbs(root) || speech == nil || speech.profile.Provider != "irodori-tts" || speech.profile.SynthesisConfigDigest == "" || speech.style != defaultSpeechStyle() {
		return nil, invalid
	}
	directory, err := os.Lstat(root)
	if err != nil || !directory.IsDir() || directory.Mode().Perm()&0077 != 0 {
		return nil, invalid
	}
	body, err := readFillerPrivate(filepath.Join(root, "manifest.json"), 64<<10)
	var manifest fillerManifest
	if err != nil || decodeSpeechJSON(body, &manifest) != nil || manifest.SchemaVersion != 1 || !manifest.Enabled || !manifest.HeadphonesConfirmed || len(manifest.Assets) < 1 || len(manifest.Assets) > 2 {
		return nil, invalid
	}
	p, expected := manifest.Profile, speech.profile
	if validateVoiceProfile(p) != nil || p.VoiceProfileID != expected.VoiceProfileID || p.Provider != expected.Provider || p.ModelRevision != expected.ModelRevision || p.Language != expected.Language || p.ReferenceGroupDigest != expected.ReferenceGroupDigest || p.ProvenanceID != expected.ProvenanceID || p.SynthesisConfigDigest != expected.SynthesisConfigDigest || p.DefaultStyle != defaultSpeechStyle() {
		return nil, invalid
	}
	assets := []FillerAudio{}
	seen := map[string]bool{}
	for _, asset := range manifest.Assets {
		if (asset.Candidate != "hesitation_etto" && asset.Candidate != "hesitation_eeto") || seen[asset.Candidate] || asset.File != asset.Candidate+".wav" {
			return nil, invalid
		}
		seen[asset.Candidate] = true
		if !asset.Approved {
			continue
		}
		audio, err := readFillerPrivate(filepath.Join(root, asset.File), 150<<10)
		digest := sha256.Sum256(audio)
		duration, valid := voiceWAVDuration(audio, 2)
		actual := float64(duration) / float64(time.Millisecond)
		if err != nil || !valid || actual < 150 || actual > 1500 || asset.DurationMS < actual-1 || asset.DurationMS > actual+1 || hex.EncodeToString(digest[:]) != asset.SHA256 {
			return nil, invalid
		}
		assets = append(assets, FillerAudio{Kind: "hesitation", AudioBase64: base64.StdEncoding.EncodeToString(audio), DurationMS: actual})
	}
	if len(assets) == 0 {
		return nil, invalid
	}
	return assets, nil
}
