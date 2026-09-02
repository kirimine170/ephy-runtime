package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func conversationReadiness(catalog *LocalModelCatalog) (string, time.Duration) {
	if catalog == nil {
		return "", 180 * time.Second
	}
	return modelReadiness(catalog, catalog.Selections["fast"])
}

func conversationFastOwnership(managed, listening bool) string {
	if managed {
		return "desktop"
	}
	if listening {
		return "external"
	}
	return "start"
}

// The conversation MVP owns only Fast and Gateway, keeping the other models unloaded．
func (a *App) startConversation() (*StackActionResponse, error) {
	if a.baseURL != "http://127.0.0.1:8000" && a.baseURL != "http://localhost:8000" {
		return nil, fmt.Errorf("conversation startup requires the local Gateway on port 8000")
	}
	a.mu.Lock()
	managed := a.fastRunning && a.fastCmd != nil
	a.mu.Unlock()
	fastOwnership := conversationFastOwnership(managed, portListening("8081"))
	var catalog *LocalModelCatalog
	if current, err := a.GetLocalModelCatalog(); err == nil {
		catalog = current
	}
	alias, timeout := conversationReadiness(catalog)
	if fastOwnership == "external" {
		if err := waitLocalModelReady("8081", alias, min(timeout, 30*time.Second)); err != nil {
			return nil, fmt.Errorf("external Fast endpoint is not ready: %w", err)
		}
	} else {
		if _, err := a.StartFast(); err != nil {
			return nil, err
		}
		if err := waitLocalModelReady("8081", alias, timeout, func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.fastRunning
		}); err != nil {
			return nil, err
		}
	}
	if !portListening("8000") {
		if _, err := a.StartGateway(); err != nil {
			return nil, err
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(a.baseURL + "/health")
		if err == nil {
			var health HealthResponse
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && health.Status == "ok" &&
				(health.Service == "ephy-runtime-gateway" || health.Service == "local-llm-workbench-gateway") {
				fastStatus := "ready"
				if fastOwnership == "external" {
					fastStatus = "external_ready"
				}
				return &StackActionResponse{Status: "ready", Steps: map[string]any{"fast": fastStatus, "gateway": "ready"}}, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("Gateway did not become ready; check Runtime logs")
}
