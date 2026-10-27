//go:build linux

package network

import (
	"context"
	"fmt"
	"net"
	"os"
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
	for _, m := range mappings {
		if err := validateMapping(m); err != nil {
			return err
		}
		if err := checkRouteOwnership(ctx, m); err != nil {
			return err
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
