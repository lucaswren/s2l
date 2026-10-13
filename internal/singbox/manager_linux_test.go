//go:build linux

package singbox

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExitedProcessDoesNotUseLiveReusedPID(t *testing.T) {
	m := NewManager("")
	p := &managedProc{pid: os.Getpid(), done: make(chan struct{})}
	close(p.done)
	m.procs["tun101"] = p
	if m.PID("tun101") != 0 || len(m.PIDs()) != 0 {
		t.Fatal("exited child was reported alive using a live PID")
	}
}

func TestReapSignalsExitBeforeTakingManagerLock(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &managedProc{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	m := NewManager("")
	m.procs["tun101"] = p
	m.mu.Lock()
	finished := make(chan struct{})
	go func() { m.reap("tun101", p); close(finished) }()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		m.mu.Unlock()
		t.Fatal("exit detection blocked behind manager lock")
	}
	if p.running() {
		t.Error("exited child is still running")
	}
	m.mu.Unlock()
	<-finished
}
