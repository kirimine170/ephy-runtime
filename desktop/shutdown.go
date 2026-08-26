package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Only process groups created by this Desktop instance are stopped．No port/PID-file discovery．
func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	a.closing = true
	for _, cmd := range []*exec.Cmd{a.fastCmd, a.workCmd, a.codeCmd, a.embeddingCmd, a.watchCmd, a.gatewayCmd} {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil && pgid == cmd.Process.Pid {
			if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
				fmt.Fprintln(os.Stderr, "managed runtime shutdown:", err)
			}
		}
	}
	a.mu.Unlock()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		stopped := a.fastCmd == nil && a.workCmd == nil && a.codeCmd == nil && a.embeddingCmd == nil && a.watchCmd == nil && a.gatewayCmd == nil
		a.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "managed runtime shutdown timed out; inspect the previous Desktop's processes before restarting")
}
