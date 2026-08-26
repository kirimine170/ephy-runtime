package main

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
)

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
