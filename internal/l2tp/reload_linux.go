//go:build linux

package l2tp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Credentials are read by each new pppd session. Mapping/comment updates do
// not require restarting xl2tpd, whose upstream SIGHUP handler is a no-op.
func Reload() error { return EnsureDaemon(false) }

func AllowStructuralChange() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	active, err := daemonActive(ctx)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf("xl2tpd global configuration changed; stop xl2tpd in a maintenance window before retrying")
	}
	return nil
}

func daemonActive(ctx context.Context) (bool, error) {
	state, err := exec.CommandContext(ctx, "systemctl", "is-active", "xl2tpd").CombinedOutput()
	if ctx.Err() != nil {
		return false, fmt.Errorf("check xl2tpd state: %w", ctx.Err())
	}
	switch strings.TrimSpace(string(state)) {
	case "active":
		if err == nil {
			return true, nil
		}
	case "inactive", "failed":
		return false, nil
	case "activating", "deactivating", "reloading":
		return false, fmt.Errorf("xl2tpd is changing state; retry later")
	}
	return false, fmt.Errorf("cannot determine xl2tpd state: %v (%s)", err, strings.TrimSpace(string(state)))
}
func EnsureDaemon(structuralChange bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	active, err := daemonActive(ctx)
	if err != nil {
		return err
	}
	if active {
		if structuralChange {
			return fmt.Errorf("xl2tpd global configuration changed; stop xl2tpd during a maintenance window and retry; existing sessions were not restarted")
		}
		return nil
	}
	if err := os.MkdirAll("/var/run/xl2tpd", 0755); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "start", "xl2tpd").CombinedOutput(); err != nil {
		return fmt.Errorf("start xl2tpd: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
