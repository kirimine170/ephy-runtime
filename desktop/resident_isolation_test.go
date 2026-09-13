package main

import "testing"

func TestDedicatedArtifactCannotFallBackToNormalLaunch(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "")
	if validateResidentLaunch("isolated") == nil {
		t.Fatal("unconfigured dedicated app entered normal startup")
	}
	if err := validateResidentLaunch(""); err != nil {
		t.Fatal("normal build acquired a resident launch requirement", err)
	}
	for key, value := range map[string]string{
		"EPHY_RESIDENT": "1", "EPHY_RESIDENT_ENABLED": "1",
		"EPHY_RESIDENT_NAMESPACE": "resident-feedback", "EPHY_RESIDENT_STATE_ROOT": t.TempDir(),
		"EPHY_RUNTIME_ROOT": t.TempDir(), "EPHY_GATEWAY_URL": "http://127.0.0.1:18900",
	} {
		t.Setenv(key, value)
	}
	if err := validateResidentLaunch("isolated"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EPHY_GATEWAY_URL", "http://127.0.0.1:8000")
	if validateResidentLaunch("isolated") == nil {
		t.Fatal("dedicated artifact accepted normal gateway")
	}
}

func TestResidentCannotMutateSharedModelServices(t *testing.T) {
	t.Setenv("EPHY_RESIDENT", "1")
	app := NewApp()
	app.workspaceRoot = t.TempDir()
	for _, action := range []func() (*RuntimeStatus, error){app.StartFast, app.StopFast, app.StartWork, app.StopWork, app.StartCode, app.StopCode, app.StartGateway, app.StopGateway, app.StartEmbedding, app.StopEmbedding, app.StartQdrant, app.StopQdrant} {
		if _, err := action(); err == nil {
			t.Fatal("resident service mutation was permitted")
		}
	}
	if _, err := app.ApplyLocalModel(ApplyLocalModelRequest{}); err == nil {
		t.Fatal("shared model replacement was permitted")
	}
	t.Setenv("EPHY_RESIDENT", "0")
	if err := residentServiceGuard(); err != nil {
		t.Fatal("normal lifecycle changed", err)
	}
}
