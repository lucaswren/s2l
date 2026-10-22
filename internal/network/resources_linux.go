//go:build linux

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"strconv"
	"strings"
	"time"
)

type resourceRule struct {
	Src      string `json:"src"`
	SrcLen   *int   `json:"srclen"`
	Table    any    `json:"table"`
	Priority int    `json:"priority"`
	IIF      string `json:"iif"`
	OIF      string `json:"oif"`
	Mark     any    `json:"fwmark"`
	To       string `json:"dst"`
}
type resourceRoute struct {
	Dst       string          `json:"dst"`
	Dev       string          `json:"dev"`
	Type      string          `json:"type"`
	Metric    int             `json:"metric"`
	Gateway   string          `json:"gateway"`
	Multipath json.RawMessage `json:"nexthops"`
}

func routeSnapshot(ctx context.Context, m model.TunnelMapping) ([]resourceRule, []resourceRoute, error) {
	text, err := outputContext(ctx, "ip", "-N", "-j", "rule", "show")
	if err != nil {
		return nil, nil, err
	}
	var rules []resourceRule
	if err = json.Unmarshal([]byte(text), &rules); err != nil {
		return nil, nil, err
	}
	text, err = outputContext(ctx, "ip", "-j", "route", "show", "table", strconv.Itoa(m.TableID))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "fib table does not exist") {
			text = "[]"
		} else {
			return nil, nil, err
		}
	}
	var routes []resourceRoute
	if err = json.Unmarshal([]byte(text), &routes); err != nil {
		return nil, nil, err
	}
	return rules, routes, nil
}
func validateRouteOwnership(m model.TunnelMapping, rules []resourceRule, routes []resourceRoute) error {
	for _, r := range rules {
		table := fmt.Sprint(r.Table)
		if table != strconv.Itoa(m.TableID) && r.Priority != 10000+m.TableID {
			continue
		}
		src := r.Src
		if r.SrcLen != nil && !strings.Contains(src, "/") {
			src = fmt.Sprintf("%s/%d", src, *r.SrcLen)
		}
		if table != strconv.Itoa(m.TableID) || canonicalSubnet(src) != m.Subnet || r.Priority != 10000+m.TableID || r.IIF != "" || r.OIF != "" || r.Mark != nil || (r.To != "" && r.To != "all") {
			return fmt.Errorf("routing table or rule priority is used by another configuration")
		}
	}
	for _, r := range routes {
		guard := r.Dst == "default" && r.Type == "unreachable" && r.Metric == 32767 && r.Dev == ""
		live := r.Dst == "default" && r.Dev == m.TunName && r.Metric == 100 && (r.Type == "" || r.Type == "unicast")
		if (!guard && !live) || r.Gateway != "" || len(r.Multipath) > 0 {
			return fmt.Errorf("routing table %d contains unmanaged routes", m.TableID)
		}
	}
	return nil
}
func checkRouteOwnership(ctx context.Context, m model.TunnelMapping) error {
	rules, routes, err := routeSnapshot(ctx, m)
	if err != nil {
		return err
	}
	return validateRouteOwnership(m, rules, routes)
}

// CheckResources runs before any cleanup, account write or process start.
func CheckResources(m model.TunnelMapping, ownInterface bool) error {
	if err := ValidateMapping(m); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := checkRouteOwnership(ctx, m); err != nil {
		return err
	}
	_, subnet, _ := net.ParseCIDR(m.Subnet)
	addresses, err := outputContext(ctx, "ip", "-j", "address", "show")
	if err != nil {
		return err
	}
	var interfaces []struct {
		Name      string `json:"ifname"`
		Addresses []struct {
			Local  string `json:"local"`
			Prefix int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err = json.Unmarshal([]byte(addresses), &interfaces); err != nil {
		return err
	}
	for _, iface := range interfaces {
		if iface.Name == m.TunName {
			if !ownInterface {
				return fmt.Errorf("interface %s already exists and is not owned by this process", m.TunName)
			}
			continue
		}
		if strings.HasPrefix(iface.Name, "ppp") {
			continue
		}
		for _, addr := range iface.Addresses {
			ip := net.ParseIP(addr.Local)
			if ip == nil || ip.To4() == nil {
				continue
			}
			_, other, err := net.ParseCIDR(fmt.Sprintf("%s/%d", addr.Local, addr.Prefix))
			if err != nil {
				return err
			}
			if subnet.Contains(other.IP) || other.Contains(subnet.IP) {
				return fmt.Errorf("subnet overlaps host interface %s", iface.Name)
			}
		}
	}
	text, err := outputContext(ctx, "ip", "-j", "route", "show", "table", "main")
	if err != nil {
		return err
	}
	var routes []resourceRoute
	if err = json.Unmarshal([]byte(text), &routes); err != nil {
		return err
	}
	for _, r := range routes {
		if r.Dst == "default" || r.Dst == "" || r.Dev == m.TunName || strings.HasPrefix(r.Dev, "ppp") {
			continue
		}
		value := r.Dst
		if !strings.Contains(value, "/") {
			value += "/32"
		}
		_, other, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("invalid host route snapshot")
		}
		if subnet.Contains(other.IP) || other.Contains(subnet.IP) {
			return fmt.Errorf("subnet overlaps host route %s", r.Dst)
		}
	}
	return nil
}
