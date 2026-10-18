//go:build !linux

package api

import "os/exec"

func configureProbeCommand(cmd *exec.Cmd) {}
