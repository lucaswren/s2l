//go:build linux

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

// VerifyPeerRoutes asks the kernel to resolve forwarded packets, not locally
// originated traffic. This detects earlier policy rules and wrong PPP return
// routes without guessing which third-party routes are safe to delete.
// No packets are transmitted and no routing entries are changed.
func VerifyPeerRoutes(ctx context.Context, m model.TunnelMapping, peers []model.PPPSession) error {
	if err := validateMapping(m); err != nil {
		return err
	}
	_, subnet, _ := net.ParseCIDR(m.Subnet)
	for _, p := range peers {
		if p.MappingID != m.ID {
			continue
		}
		ip := net.ParseIP(p.PeerIP)
		if ip.To4() == nil || !subnet.Contains(ip) || !interfaceNamePattern.MatchString(p.Interface) || !strings.HasPrefix(p.Interface, "ppp") {
			return fmt.Errorf("invalid PPP route lookup input")
		}
		for _, target := range []string{"1.1.1.1", "8.8.8.8"} {
			dev, table, err := forwardedRoute(ctx, target, ip.String(), p.Interface)
			if err != nil {
				return err
			}
			if dev != m.TunName || table != strconv.Itoa(m.TableID) {
				return fmt.Errorf("PPP %s -> %s uses %s table %s; expected %s table %d", p.Interface, target, dev, table, m.TunName, m.TableID)
			}
		}
		dev, _, err := forwardedRoute(ctx, ip.String(), "1.1.1.1", m.TunName)
		if err != nil {
			return err
		}
		if dev != p.Interface {
			return fmt.Errorf("return route to %s uses %s; expected %s", p.PeerIP, dev, p.Interface)
		}
	}
	return ctx.Err()
}

func forwardedRoute(ctx context.Context, target, source, iface string) (string, string, error) {
	text, err := outputContext(ctx, "ip", "-4", "-N", "-j", "route", "get", target, "from", source, "iif", iface)
	if err != nil {
		return "", "", fmt.Errorf("forwarded route lookup: %w", err)
	}
	var rows []struct {
		Dev   string `json:"dev"`
		Table any    `json:"table"`
	}
	if json.Unmarshal([]byte(text), &rows) != nil || len(rows) != 1 || rows[0].Dev == "" {
		return "", "", fmt.Errorf("invalid forwarded route lookup result")
	}
	return rows[0].Dev, fmt.Sprint(rows[0].Table), nil
}
