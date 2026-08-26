package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type LocalModelArtifact struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	BackendModel string `json:"backend_model"`
	Quantization string `json:"quantization"`
	ContextSize  int    `json:"context_size"`
	Available    bool   `json:"available"`
}

type LocalAdapterArtifact struct {
	ID           string `json:"id"`
	BaseModelID  string `json:"base_model_id"`
	BaseSHA256   string `json:"base_sha256"`
	Available    bool   `json:"available"`
	Experimental bool   `json:"experimental"`
}

type LocalModelSelection struct {
	ModelID   string `json:"model_id"`
	AdapterID string `json:"adapter_id,omitempty"`
}

type LocalModelCatalog struct {
	Models        []LocalModelArtifact           `json:"models"`
	Adapters      []LocalAdapterArtifact         `json:"adapters"`
	Selections    map[string]LocalModelSelection `json:"selections"`
	Revision      string                         `json:"revision"`
	DeveloperMode bool                           `json:"developer_mode"`
}

type ImportLocalModelRequest struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	BaseModelID string `json:"base_model_id"`
}

type ApplyLocalModelRequest struct {
	Role      string `json:"role"`
	ModelID   string `json:"model_id"`
	AdapterID string `json:"adapter_id"`
}

func (a *App) modelRegistryCommand(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	python := filepath.Join(a.workspaceRoot, ".venv", "bin", "python")
	argv := append([]string{"-m", "packages.model_registry", "--root", a.workspaceRoot}, args...)
	cmd := exec.CommandContext(ctx, python, argv...)
	cmd.Dir = a.workspaceRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("model registry timed out")
		}
		return nil, fmt.Errorf("model registry failed: %s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (a *App) developerModeEnabled() bool {
	data, err := os.ReadFile(filepath.Join(a.workspaceRoot, "configs", "developer.local.json"))
	if err != nil {
		return false
	}
	var value struct {
		Enabled bool `json:"enabled"`
	}
	return json.Unmarshal(data, &value) == nil && value.Enabled
}

func (a *App) SetDeveloperMode(enabled bool) (bool, error) {
	a.modelLifecycleMu.Lock()
	defer a.modelLifecycleMu.Unlock()
	file := filepath.Join(a.workspaceRoot, "configs", "developer.local.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".developer-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(map[string]bool{"enabled": enabled})
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	return enabled, os.Rename(tmp.Name(), file)
}

func (a *App) GetLocalModelCatalog() (*LocalModelCatalog, error) {
	output, err := a.modelRegistryCommand("list")
	if err != nil {
		return nil, err
	}
	var catalog LocalModelCatalog
	if err := json.Unmarshal(output, &catalog); err != nil {
		return nil, err
	}
	catalog.DeveloperMode = a.developerModeEnabled()
	return &catalog, nil
}

func (a *App) ImportLocalModel(request ImportLocalModelRequest) (*LocalModelCatalog, error) {
	a.modelLifecycleMu.Lock()
	defer a.modelLifecycleMu.Unlock()
	if !a.developerModeEnabled() {
		return nil, fmt.Errorf("enable developer mode before importing a model")
	}
	path := request.Path
	if path == "" {
		var err error
		path, err = runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
			Title: "既存GGUFを登録", Filters: []runtime.FileFilter{{DisplayName: "GGUF", Pattern: "*.gguf"}},
		})
		if err != nil {
			return nil, err
		}
		if path == "" {
			return a.GetLocalModelCatalog()
		}
	}
	args := []string{"import", path, "--id", request.ID}
	if request.BaseModelID != "" {
		args = []string{"import-adapter", path, "--id", request.ID, "--base-model", request.BaseModelID}
	}
	if _, err := a.modelRegistryCommand(args...); err != nil {
		return nil, err
	}
	return a.GetLocalModelCatalog()
}

func selectionArgs(action, role string, selection LocalModelSelection) []string {
	args := []string{action, "--role", role}
	if selection.ModelID != "" {
		args = append(args, "--model-id", selection.ModelID)
	}
	if selection.AdapterID != "" {
		args = append(args, "--adapter-id", selection.AdapterID)
	}
	return args
}

// The same transaction is exercised with fake processes by unit tests．
type modelSwitchOperations struct {
	Stop   func() error
	Start  func() error
	Ready  func(LocalModelSelection) error
	Save   func(LocalModelSelection, string) (string, error)
	Reload func() error
}

func switchLocalModel(previous, next LocalModelSelection, revision string, running bool, ops modelSwitchOperations) error {
	if running {
		if err := ops.Stop(); err != nil {
			return err
		}
	}
	updatedRevision, err := ops.Save(next, revision)
	if err != nil {
		if running {
			err = errors.Join(err, ops.Start(), ops.Ready(previous))
		}
		return err
	}
	if running {
		err = ops.Start()
		if err == nil {
			err = ops.Ready(next)
		}
	}
	if err == nil {
		err = ops.Reload()
	}
	if err == nil {
		return nil
	}
	originalErr := err
	if running {
		if stopErr := ops.Stop(); stopErr != nil {
			return fmt.Errorf("replacement failed and could not stop: %w", errors.Join(originalErr, stopErr))
		}
	}
	if _, restoreErr := ops.Save(previous, updatedRevision); restoreErr != nil {
		return fmt.Errorf("replacement failed; previous selection could not be restored: %w", errors.Join(originalErr, restoreErr))
	}
	var restoreErr error
	if running {
		restoreErr = ops.Start()
		if restoreErr == nil {
			restoreErr = ops.Ready(previous)
		}
	}
	restoreErr = errors.Join(restoreErr, ops.Reload())
	if restoreErr != nil {
		return fmt.Errorf("previous selection restored but runtime recovery failed: %w", errors.Join(originalErr, restoreErr))
	}
	return fmt.Errorf("replacement failed; previous model restored: %w", originalErr)
}

func modelPort(role string) (string, error) {
	switch role {
	case "fast":
		return "8081", nil
	case "work":
		return "8082", nil
	case "code":
		return "8083", nil
	default:
		return "", fmt.Errorf("only fast/work/code may be switched")
	}
}

func portListening(port string) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func (a *App) roleProcess(role string) (**exec.Cmd, *bool, func(string), func(*exec.Cmd)) {
	switch role {
	case "fast":
		return &a.fastCmd, &a.fastRunning, a.appendFastLog, a.waitFastProcess
	case "work":
		return &a.workCmd, &a.workRunning, a.appendWorkLog, a.waitWorkProcess
	default:
		return &a.codeCmd, &a.codeRunning, a.appendCodeLog, a.waitCodeProcess
	}
}

func (a *App) ApplyLocalModel(request ApplyLocalModelRequest) (result *LocalModelCatalog, resultErr error) {
	a.modelLifecycleMu.Lock()
	defer a.modelLifecycleMu.Unlock()
	if !a.developerModeEnabled() {
		return nil, fmt.Errorf("enable developer mode before switching a model")
	}
	port, err := modelPort(request.Role)
	if err != nil {
		return nil, err
	}
	next := LocalModelSelection{ModelID: request.ModelID, AdapterID: request.AdapterID}
	catalog, err := a.GetLocalModelCatalog()
	if err != nil {
		return nil, err
	}
	previous := catalog.Selections[request.Role]
	if previous == next {
		return catalog, nil
	}
	if _, err := a.modelRegistryCommand(selectionArgs("check", request.Role, next)...); err != nil {
		return nil, err
	}
	cmdRef, runningRef, logFn, waitFn := a.roleProcess(request.Role)
	a.mu.Lock()
	running := *runningRef && *cmdRef != nil
	a.mu.Unlock()
	if !running && portListening(port) {
		return nil, fmt.Errorf("port %s is owned by an external process; stop its launcher first, then start the model from Runtime", port)
	}
	gatewayURL, err := url.Parse(a.baseURL)
	if err != nil || gatewayURL.Scheme != "http" ||
		(gatewayURL.Hostname() != "127.0.0.1" && gatewayURL.Hostname() != "localhost") || gatewayURL.Port() != "8000" {
		return nil, fmt.Errorf("model switching requires the local Gateway at 127.0.0.1:8000")
	}
	gatewayActive := portListening("8000")
	if gatewayActive {
		var lease struct {
			Token string `json:"token"`
		}
		if err := a.postJSON("/v1/admin/model-transition/begin", map[string]string{}, &lease); err != nil {
			return nil, fmt.Errorf("could not reserve model transition: %w", err)
		}
		defer func() {
			if err := a.postJSON("/v1/admin/model-transition/end", map[string]string{"token": lease.Token}, &map[string]any{}); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("model transition could not be released: %w", err))
			}
		}()
	}
	ops := modelSwitchOperations{
		Stop: func() error {
			_, err := a.stopModelProcessLocked(request.Role, cmdRef, runningRef, logFn)
			return err
		},
		Start: func() error {
			capture := a.captureFastStream
			if request.Role == "work" {
				capture = a.captureWorkStream
			}
			if request.Role == "code" {
				capture = a.captureCodeStream
			}
			_, err := a.startModelProcessLocked("scripts/start_llama_"+request.Role+".sh", request.Role, cmdRef, runningRef, logFn, capture, waitFn)
			return err
		},
		Ready: func(selection LocalModelSelection) error {
			alias := ""
			for _, model := range catalog.Models {
				if model.ID == selection.ModelID {
					alias = model.BackendModel
				}
			}
			return waitLocalModelReady(port, alias, 180*time.Second, func() bool {
				a.mu.Lock()
				defer a.mu.Unlock()
				return *runningRef
			})
		},
		Save: func(selection LocalModelSelection, revision string) (string, error) {
			args := append(selectionArgs("select", request.Role, selection), "--expected-revision", revision)
			data, err := a.modelRegistryCommand(args...)
			if err != nil {
				return "", err
			}
			var result struct {
				Revision string `json:"revision"`
			}
			err = json.Unmarshal(data, &result)
			return result.Revision, err
		},
		Reload: func() error {
			if !gatewayActive {
				return nil
			}
			var result map[string]any
			return a.postJSON("/v1/admin/reload", map[string]string{}, &result)
		},
	}
	if err := switchLocalModel(previous, next, catalog.Revision, running, ops); err != nil {
		return nil, err
	}
	return a.GetLocalModelCatalog()
}

func waitLocalModelReady(port, alias string, timeout time.Duration, alive ...func() bool) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(alive) > 0 && !alive[0]() {
			return fmt.Errorf("selected model process exited before becoming ready")
		}
		response, err := client.Get("http://127.0.0.1:" + port + "/v1/models")
		if err == nil {
			var models ModelListResponse
			decodeErr := json.NewDecoder(response.Body).Decode(&models)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil {
				for _, model := range models.Data {
					if alias == "" || model.ID == alias {
						return nil
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("selected model did not become ready before timeout")
}
