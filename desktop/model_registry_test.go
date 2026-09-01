package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeveloperModeDefaultsOffAndPersists(t *testing.T) {
	a := newTestAppWithWorkspace(t)
	if a.developerModeEnabled() {
		t.Fatal("developer mode must default off")
	}
	if _, err := a.ApplyLocalModel(ApplyLocalModelRequest{Role: "fast", ModelID: "base"}); err == nil {
		t.Fatal("switch should be gated before invoking Python")
	}
	if enabled, err := a.SetDeveloperMode(true); err != nil || !enabled {
		t.Fatal(enabled, err)
	}
	b := newTestAppAtWorkspace(a.workspaceRoot)
	if !b.developerModeEnabled() {
		t.Fatal("developer mode was not persisted")
	}
	if _, err := b.SetDeveloperMode(false); err != nil {
		t.Fatal(err)
	}
	if a.developerModeEnabled() {
		t.Fatal("developer mode did not turn off")
	}
}

func TestModelSwitchTransaction(t *testing.T) {
	old := LocalModelSelection{ModelID: "old", AdapterID: "old-style"}
	next := LocalModelSelection{ModelID: "new"}
	for _, tc := range []struct {
		name    string
		running bool
		failure string
		want    []string
	}{
		{"running", true, "", []string{"stop", "save:new", "start", "ready:new", "reload"}},
		{"stopped", false, "", []string{"save:new", "reload"}},
		{"failed new backend", true, "ready", []string{"stop", "save:new", "start", "ready:new", "stop", "save:old", "start", "ready:old", "reload"}},
		{"failed reload", true, "reload", []string{"stop", "save:new", "start", "ready:new", "reload", "stop", "save:old", "start", "ready:old", "reload"}},
		{"failed stop", true, "stop", []string{"stop"}},
		{"concurrent edit", true, "save", []string{"stop", "save:new", "start", "ready:old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			reloadCalls := 0
			ops := modelSwitchOperations{
				Stop: func() error {
					calls = append(calls, "stop")
					if tc.failure == "stop" {
						return fmt.Errorf("stop failed")
					}
					return nil
				},
				Start: func() error { calls = append(calls, "start"); return nil },
				Ready: func(s LocalModelSelection) error {
					calls = append(calls, "ready:"+s.ModelID)
					if s == next && tc.failure == "ready" {
						return fmt.Errorf("bad model")
					}
					return nil
				},
				Save: func(s LocalModelSelection, revision string) (string, error) {
					calls = append(calls, "save:"+s.ModelID)
					if s == next && revision != "old-revision" {
						t.Fatal("incorrect initial CAS")
					}
					if s == old && revision != "new-revision" {
						t.Fatal("rollback must use the replacement revision")
					}
					if tc.failure == "save" {
						return "", fmt.Errorf("concurrent selection update")
					}
					return "new-revision", nil
				},
				Reload: func() error {
					calls = append(calls, "reload")
					reloadCalls++
					if tc.failure == "reload" && reloadCalls == 1 {
						return fmt.Errorf("reload failed")
					}
					return nil
				},
			}
			err := switchLocalModel(old, next, "old-revision", tc.running, ops)
			if (err != nil) != (tc.failure != "") {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls=%v want=%v", calls, tc.want)
			}
		})
	}
}

func TestModelReadinessChecksExpectedAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"selected-model"}]}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	if err := waitLocalModelReady(u.Port(), "selected-model", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitLocalModelReady(u.Port(), "other-model", time.Millisecond); err == nil {
		t.Fatal("wrong backend accepted")
	}
}

func TestOldModelWaiterCannotClearReplacement(t *testing.T) {
	a := NewApp()
	old := exec.Command("true")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	replacement := exec.Command("true")
	a.fastCmd, a.fastRunning = replacement, true
	a.waitModelProcess(old, &a.fastCmd, &a.fastRunning, a.appendFastLog, "fast")
	if a.fastCmd != replacement || !a.fastRunning {
		t.Fatal("old waiter cleared replacement")
	}
}

func TestModelRoleAndArgumentValidation(t *testing.T) {
	if _, err := modelPort("embedding"); err == nil {
		t.Fatal("embedding switch requires a reindex workflow")
	}
	args := selectionArgs("select", "fast", LocalModelSelection{ModelID: "base", AdapterID: "style"})
	if !strings.Contains(strings.Join(args, " "), "--adapter-id style") {
		t.Fatal(args)
	}
}

func TestModelReadinessUsesProfileTimeout(t *testing.T) {
	catalog := &LocalModelCatalog{Models: []LocalModelArtifact{
		{ID: "qwen3-8b", BackendModel: "fast-model", StartupTimeoutSeconds: 180},
		{ID: "qwen3.8-27b", BackendModel: "large-model", StartupTimeoutSeconds: 420},
	}}
	alias, timeout := modelReadiness(catalog, LocalModelSelection{ModelID: "qwen3.8-27b"})
	if alias != "large-model" || timeout != 420*time.Second {
		t.Fatalf("alias=%q timeout=%s", alias, timeout)
	}
	alias, timeout = modelReadiness(catalog, LocalModelSelection{ModelID: "unknown"})
	if alias != "" || timeout != 180*time.Second {
		t.Fatalf("fallback alias=%q timeout=%s", alias, timeout)
	}
}
