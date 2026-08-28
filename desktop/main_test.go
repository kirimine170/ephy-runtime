package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestApplicationOptionsUseEphyIdentityAndSingleInstanceLock(t *testing.T) {
	app := NewApp()
	applicationOptions := newApplicationOptions(app)

	if applicationOptions.Title != ephyRuntimeTitle {
		t.Fatalf("unexpected application title: %q", applicationOptions.Title)
	}
	if applicationOptions.SingleInstanceLock == nil {
		t.Fatal("single instance lock is not configured")
	}
	if applicationOptions.SingleInstanceLock.UniqueId != ephyRuntimeSingleInstanceID {
		t.Fatalf("unexpected single instance ID: %q", applicationOptions.SingleInstanceLock.UniqueId)
	}
	if applicationOptions.SingleInstanceLock.OnSecondInstanceLaunch == nil {
		t.Fatal("second instance callback is not configured")
	}

	applicationOptions.SingleInstanceLock.OnSecondInstanceLaunch(options.SecondInstanceData{})
}

func TestWailsProjectIdentityMatchesRuntime(t *testing.T) {
	data, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Name           string `json:"name"`
		OutputFilename string `json:"outputfilename"`
		Info           struct {
			ProductName string `json:"productName"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Name != "ephy-runtime" {
		t.Fatalf("unexpected project name: %q", config.Name)
	}
	if config.OutputFilename != "ephy-runtime" {
		t.Fatalf("unexpected output filename: %q", config.OutputFilename)
	}
	if config.Info.ProductName != ephyRuntimeTitle {
		t.Fatalf("unexpected product name: %q", config.Info.ProductName)
	}
}
