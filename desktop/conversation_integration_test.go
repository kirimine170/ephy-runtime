package main

import (
	"encoding/binary"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in: uses installed GGUF weights, owns its processes, and never edits the asset checkout．
func TestLiveLocalModelSwitch(t *testing.T) {
	if os.Getenv("EPHY_MODEL_INTEGRATION") != "1" {
		t.Skip("set EPHY_MODEL_INTEGRATION=1 and EPHY_RUNTIME_ASSET_ROOT for local Metal integration")
	}
	assets := os.Getenv("EPHY_RUNTIME_ASSET_ROOT")
	if !filepath.IsAbs(assets) {
		t.Fatal("EPHY_RUNTIME_ASSET_ROOT must be absolute")
	}
	for _, port := range []string{"8000", "8081"} {
		if portListening(port) {
			t.Fatalf("port %s is occupied; no external process will be stopped", port)
		}
	}
	cwd, _ := os.Getwd()
	source := filepath.Dir(cwd)
	root := t.TempDir()
	for _, directory := range []string{"apps", "packages", "configs", "prompts", "scripts"} {
		err := filepath.WalkDir(filepath.Join(source, directory), func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			if strings.Contains(entry.Name(), ".local.") || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			relative, _ := filepath.Rel(source, name)
			destination := filepath.Join(root, relative)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0o700)
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, data, 0o700)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{".venv", "llama.cpp"} {
		if err := os.Symlink(filepath.Join(assets, directory), filepath.Join(root, directory)); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(filepath.Join(root, ".venv/bin/python"), "scripts/init_local_ephy.py", "--private-root", filepath.Join(root, "private"))
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize: %s: %v", output, err)
	}
	a := newTestAppAtWorkspace(root)
	t.Cleanup(func() {
		if _, err := a.StopGateway(); err != nil {
			t.Error(err)
		}
		if _, err := a.StopFast(); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.SetDeveloperMode(true); err != nil {
		t.Fatal(err)
	}
	for id, relative := range map[string]string{
		"qwen3-8b":    "qwen3-8b-gguf/Qwen3-8B-Q6_K.gguf",
		"qwen3.8-27b": "qwen3.8-27b-gguf/Qwen3.8-27B-Q4_K_M.gguf",
	} {
		t.Log("importing existing", id)
		if _, err := a.ImportLocalModel(ImportLocalModelRequest{ID: id, Path: filepath.Join(assets, "llama.cpp/models", relative)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "qwen3-8b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.startConversation(); err != nil {
		t.Log(strings.Join(a.fastLogs, "\n"), strings.Join(a.gatewayLogs, "\n"))
		t.Fatal(err)
	}
	var health struct {
		Enabled bool `json:"ephy_enabled"`
	}
	if err := a.getJSON("/health", &health); err != nil || !health.Enabled {
		t.Fatal("Profile not active", err)
	}
	chat := func(stream bool) {
		t.Helper()
		answer, err := a.Chat(ChatRequest{Mode: "fast", Prompt: "あなたの名前と一人称を使って，短く自己紹介してください．", MaxTokens: 180, Stream: stream})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("stream=%v answer=%s", stream, answer.Answer)
		if !strings.Contains(answer.Answer, "エフィ") || !strings.Contains(answer.Answer, "わたし") {
			t.Fatal("Profile was not reflected in the response")
		}
	}
	chat(false)
	chat(true)
	t.Log("switching to existing Qwen3.8-27B")
	if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "qwen3.8-27b"}); err != nil {
		t.Fatal(err)
	}
	chat(false)
	// A valid container header with no tensors gets through import but must fail backend startup．
	bad := make([]byte, 24)
	copy(bad, "GGUF")
	binary.LittleEndian.PutUint32(bad[4:], 3)
	invalid := filepath.Join(root, "invalid.gguf")
	if err := os.WriteFile(invalid, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportLocalModel(ImportLocalModelRequest{ID: "invalid-model", Path: invalid}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "invalid-model"}); err == nil || !strings.Contains(err.Error(), "previous model restored") {
		t.Fatalf("expected successful rollback: %v", err)
	}
	catalog, err := a.GetLocalModelCatalog()
	if err != nil || catalog.Selections["fast"].ModelID != "qwen3.8-27b" {
		t.Fatal("selection was not restored", err)
	}
	chat(true)
	t.Log("switching back to Qwen3-8B")
	if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "qwen3-8b"}); err != nil {
		t.Fatal(err)
	}
	chat(false)
	if adapterPath := os.Getenv("EPHY_TEST_ADAPTER_GGUF"); adapterPath != "" {
		if _, err := a.ImportLocalModel(ImportLocalModelRequest{ID: "experimental-style", Path: adapterPath, BaseModelID: "qwen3-8b"}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "qwen3-8b", AdapterID: "experimental-style"}); err != nil {
			t.Fatal(err)
		}
		adapters := func() int {
			response, err := http.Get("http://127.0.0.1:8081/lora-adapters")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var loaded []struct {
				Scale float64 `json:"scale"`
			}
			if err := json.NewDecoder(response.Body).Decode(&loaded); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatal("adapter inspection failed")
			}
			for _, entry := range loaded {
				if entry.Scale != 1 {
					t.Fatal("adapter is not active")
				}
			}
			return len(loaded)
		}
		if adapters() != 1 {
			t.Fatal("LoRA was not loaded")
		}
		answer, err := a.Chat(ChatRequest{Mode: "fast", Prompt: "こんにちは．短く自己紹介をお願いします．", MaxTokens: 100, Stream: true})
		if err != nil || strings.TrimSpace(answer.Answer) == "" {
			t.Fatal("adapter inference failed", err)
		}
		t.Log("experimental LoRA loaded and streamed; this is not a quality approval")
		if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "qwen3-8b"}); err != nil {
			t.Fatal(err)
		}
		if adapters() != 0 {
			t.Fatal("LoRA remained active after disabling")
		}
		chat(true)
	}
}
