package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPackagedAppFindsRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"configs", "scripts", "desktop/build/bin/ephy-runtime.app/Contents/MacOS"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"configs/models.yaml", "scripts/start_gateway.sh"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if actual := findRuntimeRoot(filepath.Join(root, "desktop/build/bin/ephy-runtime.app/Contents/MacOS")); actual != root {
		t.Fatal(actual)
	}
	if actual := findRuntimeRoot(t.TempDir()); actual != "" {
		t.Fatal("unrelated directory accepted", actual)
	}
}

func TestDetachedAppUsesExplicitRuntimeWithoutChangingDataRoot(t *testing.T) {
	t.Setenv("EPHY_RUNTIME_ROOT", "")
	fallback := detectWorkspaceRoot()
	root := filepath.Join(t.TempDir(), "runtime with spaces")
	for _, name := range []string{"configs/models.yaml", "scripts/start_gateway.sh"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EPHY_RUNTIME_ROOT", root+string(filepath.Separator))
	if got := detectWorkspaceRoot(); got != root {
		t.Fatal("detached bundle lost its explicit settings and data root", got)
	}
	for _, invalid := range []string{"relative/root", t.TempDir(), filepath.Join(root, "missing-child")} {
		t.Setenv("EPHY_RUNTIME_ROOT", invalid)
		if got := detectWorkspaceRoot(); got != fallback {
			t.Fatal("invalid override selected an unrelated or ancestor directory", got)
		}
	}
}

func TestStartupPreparesSelectedWhisperWithoutOpeningVoiceInput(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"configs/models.yaml", "scripts/start_gateway.sh"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EPHY_RUNTIME_ROOT", root)
	t.Setenv("EPHY_START_CONVERSATION", "")
	t.Setenv("EPHY_ASR_CONFIG", filepath.Join(root, "missing-asr.json"))
	for _, provider := range []string{"whisper-cpp", "macos-speech", ""} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("EPHY_ASR_PROVIDER", provider)
			privateRoot, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("EPHY_RECORDING_HOME", filepath.Join(privateRoot, "recording"))
			a := NewApp()
			a.startup(context.Background())
			defer a.shutdown(context.Background())
			if provider != "whisper-cpp" {
				if a.interaction != nil {
					t.Fatal("default ASR unexpectedly initialized during startup")
				}
				return
			}
			if a.interaction == nil {
				t.Fatal("Whisper preparation waited for the Conversation view")
			}
			asr, ok := a.interaction.asr.(*WhisperVoiceASR)
			if !ok {
				t.Fatal("selected Whisper provider was not prepared")
			}
			readiness, err := asr.Readiness(context.Background())
			if err == nil || readiness.ErrorCode != "invalid_voice_config" {
				t.Fatal("missing config must stay blocked without a fallback", readiness, err)
			}
			if asr.active != nil || asr.process != nil {
				t.Fatal("startup opened an input or launched with invalid assets")
			}
		})
	}
}

func TestShutdownStopsOwnedProcessesAndRejectsNewStarts(t *testing.T) {
	a := newTestAppWithWorkspace(t)
	owned, external := exec.Command("sleep", "60"), exec.Command("sleep", "60")
	for _, cmd := range []*exec.Cmd{owned, external} {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
	}
	defer external.Wait()
	defer external.Process.Kill()
	a.fastCmd, a.fastRunning = owned, true
	go a.waitFastProcess(owned)
	a.shutdown(context.Background())
	a.mu.Lock()
	stopped := a.fastCmd == nil && !a.fastRunning
	a.mu.Unlock()
	if !stopped {
		t.Fatal("owned model did not stop")
	}
	if err := external.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("external process was stopped", err)
	}
	for _, start := range []func() (*RuntimeStatus, error){a.StartFast, a.StartWork, a.StartCode, a.StartGateway, a.StartEmbedding} {
		if _, err := start(); err == nil {
			t.Fatal("startup allowed after shutdown")
		}
	}
	if _, err := a.StartWatch(WatchRequest{}); err == nil {
		t.Fatal("watch startup allowed after shutdown")
	}
}
