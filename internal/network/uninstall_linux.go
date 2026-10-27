//go:build linux

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func CheckUninstallMappings(mappings []model.TunnelMapping) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("requires Linux root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tables := map[string]bool{}
	for _, m := range mappings {
		if err := validateMapping(m); err != nil {
			return err
		}
		if err := checkRouteOwnership(ctx, m); err != nil {
			return err
		}
		tables[strconv.Itoa(m.TableID)] = true
	}
	// Missing/corrupt state must not leave unaccounted S2L policy routes behind.
	for _, args := range [][]string{{"-N", "-j", "rule", "show"}, {"-N", "-j", "route", "show", "table", "all", "protocol", routeProtocol}} {
		text, err := outputContext(ctx, "ip", args...)
		if err != nil {
			return err
		}
		var entries []struct {
			Protocol any `json:"protocol"`
			Table    any `json:"table"`
		}
		if err := json.Unmarshal([]byte(text), &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if fmt.Sprint(entry.Protocol) == routeProtocol && !tables[fmt.Sprint(entry.Table)] {
				return fmt.Errorf("untracked S2L route/rule; restore mapping state before uninstalling")
			}
		}
	}
	return nil
}

func CleanupStoppedMappings(mappings []model.TunnelMapping) error {
	if err := CheckUninstallMappings(mappings); err != nil {
		return err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "ppp") {
			return fmt.Errorf("PPP interface still present; guards retained")
		}
		for _, m := range mappings {
			if iface.Name == m.TunName {
				return fmt.Errorf("mapping TUN still present; stop its process first")
			}
		}
	}
	for _, m := range mappings {
		if err := DelFirewall(m); err != nil {
			return err
		}
		if err := DelPolicyRoute(m); err != nil {
			return err
		}
	}
	return nil // Global guards remain until the uninstall coordinator removes them.
}
