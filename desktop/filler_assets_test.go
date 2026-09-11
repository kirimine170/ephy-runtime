package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fillerFixture(t *testing.T) (string, *preparedSpeech, fillerManifest) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	os.Chmod(root, 0700)
	profile := irodoriTestProfile()
	profile.SynthesisConfigDigest = strings.Repeat("a", 64)
	speech := &preparedSpeech{profile: profile, style: defaultSpeechStyle()}
	// Reuse the native PCM encoder used by existing speech fixtures．
	audio := durationTestWAV(1, 16000)
	digest := sha256.Sum256(audio)
	duration, _ := voiceWAVDuration(audio, 2)
	manifest := fillerManifest{SchemaVersion: 1, Enabled: true, HeadphonesConfirmed: true, Profile: profile,
		Assets: []fillerAsset{{Candidate: "hesitation_etto", File: "hesitation_etto.wav", SHA256: hex.EncodeToString(digest[:]), DurationMS: float64(duration) / float64(time.Millisecond), Approved: true}}}
	os.WriteFile(filepath.Join(root, "hesitation_etto.wav"), audio, 0600)
	writeFillerManifest(t, root, manifest)
	return root, speech, manifest
}
func writeFillerManifest(t *testing.T, root string, m fillerManifest) {
	t.Helper()
	body, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestFillerAssetsFailClosedOnReviewIdentityAndIntegrity(t *testing.T) {
	root, speech, manifest := fillerFixture(t)
	assets, err := loadFillerAssets(root, speech)
	if err != nil || len(assets) != 1 {
		t.Fatalf("%v %v", assets, err)
	}
	for _, change := range []func(*fillerManifest){
		func(m *fillerManifest) { m.Enabled = false }, func(m *fillerManifest) { m.HeadphonesConfirmed = false },
		func(m *fillerManifest) { m.Assets[0].Approved = false }, func(m *fillerManifest) { m.Profile.ReferenceGroupDigest = strings.Repeat("f", 64) },
		func(m *fillerManifest) { m.Profile.SynthesisConfigDigest = strings.Repeat("f", 64) },
		func(m *fillerManifest) { m.Assets[0].File = "../other.wav" }, func(m *fillerManifest) { m.Assets[0].SHA256 = strings.Repeat("0", 64) },
		func(m *fillerManifest) { m.Assets[0].Candidate = "checking_now" }, func(m *fillerManifest) { m.Assets[0].DurationMS = 3000 },
	} {
		candidate := manifest
		candidate.Assets = append([]fillerAsset(nil), manifest.Assets...)
		change(&candidate)
		writeFillerManifest(t, root, candidate)
		if _, err := loadFillerAssets(root, speech); err == nil {
			t.Fatal("unsafe asset accepted")
		}
	}
	writeFillerManifest(t, root, manifest)
	speech.style.Pace = .85
	if _, err := loadFillerAssets(root, speech); err == nil {
		t.Fatal("unreviewed delivery accepted")
	}
}
func TestFillerAssetsRejectLinksPermissionsAndUnknownFields(t *testing.T) {
	root, speech, _ := fillerFixture(t)
	target := filepath.Join(root, "hesitation_etto.wav")
	if err := os.Link(target, filepath.Join(root, "linked.wav")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFillerAssets(root, speech); err == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(filepath.Join(root, "linked.wav"))
	os.Chmod(target, 0644)
	if _, err := loadFillerAssets(root, speech); err == nil {
		t.Fatal("public audio accepted")
	}
	os.Chmod(target, 0600)
	manifest := filepath.Join(root, "manifest.json")
	body, _ := os.ReadFile(manifest)
	body = append([]byte(`{"text":"not allowed",`), body[1:]...)
	os.WriteFile(manifest, body, 0600)
	if _, err := loadFillerAssets(root, speech); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBackchannelAssetsRequireSeparateApprovalAndKeepTheirKind(t *testing.T) {
	root, speech, manifest := fillerFixture(t)
	ack := manifest.Assets[0]
	ack.Candidate, ack.File, ack.Approved = "backchannel_hai", "backchannel_hai.wav", false
	audio, err := os.ReadFile(filepath.Join(root, "hesitation_etto.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ack.File), audio, 0600); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets, ack)
	writeFillerManifest(t, root, manifest)
	assets, err := loadFillerAssets(root, speech)
	if err != nil || len(assets) != 1 {
		t.Fatal("unapproved acknowledgement was not isolated")
	}
	manifest.Assets[1].Approved = true
	writeFillerManifest(t, root, manifest)
	assets, err = loadFillerAssets(root, speech)
	if err != nil || len(assets) != 2 || assets[1].Kind != "backchannel" {
		t.Fatal("approved acknowledgement missing")
	}
	manifest.Assets[1].SHA256 = strings.Repeat("0", 64)
	writeFillerManifest(t, root, manifest)
	if _, err := loadFillerAssets(root, speech); err == nil {
		t.Fatal("changed acknowledgement accepted")
	}
}

func TestConversationalBackchannelAllowsThreeSecondsWithoutExtendingFiller(t *testing.T) {
	for _, tc := range []struct {
		candidate string
		seconds   int
		allowed   bool
	}{
		{"backchannel_gomen_iiyo", 3, true},
		{"backchannel_gomen_iiyo", 4, false},
		{"hesitation_etto", 2, false},
		{"backchannel_unknown", 2, false},
	} {
		t.Run(tc.candidate+string(rune('0'+tc.seconds)), func(t *testing.T) {
			root, speech, manifest := fillerFixture(t)
			audio := durationTestWAV(tc.seconds, 48000)
			digest := sha256.Sum256(audio)
			asset := fillerAsset{Candidate: tc.candidate, File: tc.candidate + ".wav", SHA256: hex.EncodeToString(digest[:]), DurationMS: float64(tc.seconds * 1000), Approved: true}
			manifest.Assets = []fillerAsset{asset}
			if err := os.WriteFile(filepath.Join(root, asset.File), audio, 0600); err != nil {
				t.Fatal(err)
			}
			writeFillerManifest(t, root, manifest)
			assets, err := loadFillerAssets(root, speech)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
			if tc.allowed && (len(assets) != 1 || assets[0].Kind != "backchannel" || assets[0].DurationMS != 3000) {
				t.Fatal("wrong backchannel")
			}
		})
	}
}
