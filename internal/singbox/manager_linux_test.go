//go:build linux

package singbox

import (
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"os/exec"
	"sync"
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

func TestRuntimeSnapshotNeverMixesProcessGenerations(t *testing.T) {
	m := NewManager("")
	a := &managedProc{pid: 100, done: make(chan struct{}), exits: 7, log: &processLog{status: model.ProcessStatus{LogBytes: 100}}}
	b := &managedProc{pid: 200, done: make(chan struct{}), exits: 20, log: &processLog{status: model.ProcessStatus{LogBytes: 200}}}
	m.procs["tun101"] = a
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			m.mu.Lock()
			if i%2 == 0 {
				m.procs["tun101"] = a
			} else {
				m.procs["tun101"] = b
			}
			m.mu.Unlock()
		}
	}()
	for i := 0; i < 10000; i++ {
		stats := m.RuntimeSnapshot()["tun101"]
		if (stats.PID == 100 && (stats.Process.LogBytes != 100 || stats.Process.Exits != 7)) || (stats.PID == 200 && (stats.Process.LogBytes != 200 || stats.Process.Exits != 20)) {
			t.Error("snapshot mixed process generations")
		}
	}
	wg.Wait()
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

func TestProcessExitDistinguishesRequestedStop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested bool
		command   string
		exits     uint64
	}{
		{name: "unexpected clean exit", command: "exit 0", exits: 1},
		{name: "unexpected failure", command: "exit 7", exits: 1},
		{name: "requested stop", requested: true, command: "kill -TERM $$"},
		{name: "failure during requested stop", requested: true, command: "exit 7", exits: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", tc.command)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			p := &managedProc{cmd: cmd, done: make(chan struct{}), stopRequested: tc.requested}
			NewManager("").reap("tun101", p)
			status := p.processStatus()
			if status.Exits != tc.exits || (status.LastUnexpectedExitAt != nil) != (tc.exits != 0) ||
				(status.LastUnexpectedExit != "") != (tc.exits != 0) {
				t.Fatalf("unexpected process status: %+v", status)
			}
			replacement := &managedProc{}
			replacement.inheritLifecycle(p)
			next := replacement.processStatus()
			var restarts uint64
			if tc.requested && tc.exits == 0 {
				restarts = 1
			}
			if next.Exits != status.Exits || next.LastUnexpectedExit != status.LastUnexpectedExit || next.Restarts != restarts {
				t.Fatalf("unexpected replacement status: %+v", next)
			}
		})
	}
}
