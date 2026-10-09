package api

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/lucaswren/s2l/internal/model"
)

var tunNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

func validateSocksNode(n model.SocksNode) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(n.Addr))
	if err != nil || host == "" {
		return fmt.Errorf("addr must be host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("addr port must be between 1 and 65535")
	}
	if err := validateCredential("SOCKS5 username", n.Username); err != nil {
		return err
	}
	return validateCredential("SOCKS5 password", n.Password)
}

func validateMapping(m model.TunnelMapping) error {
	if m.NodeID == "" || m.TunName == "" || m.Subnet == "" || m.L2TPUser == "" || m.L2TPPass == "" {
		return fmt.Errorf("node_id, tun_name, subnet, l2tp_user and l2tp_pass are required")
	}
	if m.TableID < 1 || m.TableID > 255 {
		return fmt.Errorf("table_id must be between 1 and 255")
	}
	if !tunNamePattern.MatchString(m.TunName) {
		return fmt.Errorf("tun_name must be 1-15 letters, digits, '.', '_' or '-'")
	}
	_, subnet, err := net.ParseCIDR(m.Subnet)
	if err != nil || subnet.IP.To4() == nil {
		return fmt.Errorf("subnet must be a valid IPv4 CIDR")
	}
	if ones, bits := subnet.Mask.Size(); bits != 32 || ones > 30 {
		return fmt.Errorf("subnet must contain at least two usable IPv4 addresses (prefix /30 or shorter)")
	}
	if err := validateCredential("L2TP username", m.L2TPUser); err != nil {
		return err
	}
	return validateCredential("L2TP password", m.L2TPPass)
}

func validateCredential(label, value string) error {
	if len(value) > 255 {
		return fmt.Errorf("%s is longer than 255 bytes", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '"' || r == '\\' {
			return fmt.Errorf("%s contains an unsupported character", label)
		}
	}
	return nil
}

func subnetsOverlap(a, b string) bool {
	_, an, err := net.ParseCIDR(a)
	if err != nil {
		return false
	}
	_, bn, err := net.ParseCIDR(b)
	if err != nil || an.IP.To4() == nil || bn.IP.To4() == nil {
		return false
	}
	return an.Contains(bn.IP) || bn.Contains(an.IP)
}
