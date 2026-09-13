package main

import "testing"

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
