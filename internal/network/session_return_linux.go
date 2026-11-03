//go:build linux

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/model"
)

const sessionReturnMetric = "50"

func sessionPeerAddress(m model.TunnelMapping) string {
	peer, _ := l2tp.ClientIPFromSubnet(m.Subnet)
	return peer
}

func sessionReturnRuleArgs(m model.TunnelMapping) []string {
	return []string{"iif", m.TunName, "to", sessionPeerAddress(m) + "/32", "table", strconv.Itoa(m.TableID),
		"priority", strconv.Itoa(9000 + m.TableID), "protocol", routeProtocol}
}

func sessionReturnRouteArgs(m model.TunnelMapping, iface string) []string {
	return []string{sessionPeerAddress(m) + "/32", "dev", iface, "scope", "link",
		"table", strconv.Itoa(m.TableID), "metric", sessionReturnMetric, "protocol", routeProtocol}
}

func isSessionReturnRule(m model.TunnelMapping, r resourceRule) bool {
	return !r.Foreign && fmt.Sprint(r.Protocol) == routeProtocol && fmt.Sprint(r.Table) == strconv.Itoa(m.TableID) &&
		r.Priority == 9000+m.TableID && r.IIF == m.TunName && r.OIF == "" && r.Mark == nil &&
		(r.Src == "" || r.Src == "all") && (r.To == sessionPeerAddress(m) || r.To == sessionPeerAddress(m)+"/32")
}

func isSessionReturnRoute(m model.TunnelMapping, r resourceRoute) bool {
	return !r.Foreign && fmt.Sprint(r.Protocol) == routeProtocol && r.Metric == 50 &&
		strings.HasPrefix(r.Dev, "ppp") && interfaceNamePattern.MatchString(r.Dev) &&
		(r.Type == "" || r.Type == "unicast") && r.Gateway == "" && len(r.Multipath) == 0 &&
		(r.Dst == sessionPeerAddress(m) || r.Dst == sessionPeerAddress(m)+"/32")
}

func pinSessionReturn(ctx context.Context, m model.TunnelMapping, row model.PPPSession) error {
	rules, routes, err := routeSnapshot(ctx, m)
	if err != nil {
		return err
	}
	if err := validateRouteOwnership(m, rules, routes); err != nil {
		return err
	}
	if err := runContext(ctx, "ip", append([]string{"route", "replace"}, sessionReturnRouteArgs(m, row.Interface)...)...); err != nil {
		return err
	}
	for _, rule := range rules {
		if isSessionReturnRule(m, rule) {
			return nil
		}
	}
	return runContext(ctx, "ip", append([]string{"rule", "add"}, sessionReturnRuleArgs(m)...)...)
}

func verifySessionReturn(m model.TunnelMapping, owner model.PPPSession, ruleJSON, routeJSON []byte) error {
	iface, err := net.InterfaceByName(owner.Interface)
	if err != nil || iface.Index != owner.Index {
		return nil // Reconciliation owns changes to PPP session identity.
	}
	var rules []resourceRule
	var routes []resourceRoute
	if err := json.Unmarshal(ruleJSON, &rules); err != nil {
		return err
	}
	if err := json.Unmarshal(routeJSON, &routes); err != nil {
		return err
	}
	found := false
	for _, rule := range rules {
		found = found || isSessionReturnRule(m, rule)
	}
	if !found {
		return missingState("PPP return policy rule missing", "ip", append([]string{"rule", "add"}, sessionReturnRuleArgs(m)...)...)
	}
	for _, route := range routes {
		if isSessionReturnRoute(m, route) && route.Dev == owner.Interface {
			return nil
		}
	}
	return missingState("PPP return route missing", "ip", append([]string{"route", "replace"}, sessionReturnRouteArgs(m, owner.Interface)...)...)
}
