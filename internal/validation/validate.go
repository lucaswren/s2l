package validation

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
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func Node(n model.SocksNode) error {
	if n.ID != "" && (!idPattern.MatchString(n.ID) || n.ID == "." || n.ID == "..") {
		return fmt.Errorf("node id must be 1-128 letters, digits, '.', '_' or '-'")
	}
	host, portText, err := net.SplitHostPort(strings.TrimSpace(n.Addr))
	if err != nil || host == "" {
		return fmt.Errorf("addr must be host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("addr port must be between 1 and 65535")
	}
	if err := Credential("SOCKS5 username", n.Username); err != nil {
		return err
	}
	return Credential("SOCKS5 password", n.Password)
}

func Mapping(m model.TunnelMapping) error {
	if err := m.ValidateOptions(); err != nil {
		return err
	}
	if m.ID != "" && (!idPattern.MatchString(m.ID) || m.ID == "." || m.ID == "..") {
		return fmt.Errorf("mapping id must be 1-128 letters, digits, '.', '_' or '-'")
	}
	if m.NodeID == "" || m.TunName == "" || m.Subnet == "" || m.L2TPUser == "" || m.L2TPPass == "" {
		return fmt.Errorf("node_id, tun_name, subnet, l2tp_user and l2tp_pass are required")
	}
	if m.TableID < 1 || m.TableID > 252 {
		return fmt.Errorf("table_id must be between 1 and 252; 253-255 are reserved by Linux")
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
	if err := Credential("L2TP username", m.L2TPUser); err != nil {
		return err
	}
	return Credential("L2TP password", m.L2TPPass)
}

func Credential(label, value string) error {
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

func SubnetsOverlap(a, b string) bool {
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

// SameMappingSettings compares the effective runtime configuration, excluding
// lifecycle status and diagnostics. Defaults and their explicit values agree.
func SameMappingSettings(a, b model.TunnelMapping) bool {
	normalize := func(m model.TunnelMapping) model.TunnelMapping {
		m.Status = ""
		m.Group = ""
		m.ErrorMsg = ""
		m.MTU = m.EffectiveMTU()
		m.MSS = m.EffectiveMSS()
		m.QueueLength = m.EffectiveQueueLength()
		m.UDPPolicy = m.EffectiveUDPPolicy()
		m.DNSMode = m.EffectiveDNSMode()
		return m
	}
	return normalize(a) == normalize(b)
}
