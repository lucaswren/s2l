//go:build linux

package api

import (
	"os"
	"os/exec"
	"syscall"
)

// MTR may start mtr-packet; cancel the whole process group, not only its parent.
func configureProbeCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
