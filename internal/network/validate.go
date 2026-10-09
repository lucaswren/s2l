package network

import (
	"fmt"
	"net"
	"regexp"

	"github.com/lucaswren/s2l/internal/model"
)

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

func ValidateMapping(m model.TunnelMapping) error {
	if m.TunName == "" {
		return fmt.Errorf("tun_name is required")
	}
	if !interfaceNamePattern.MatchString(m.TunName) {
		return fmt.Errorf("tun_name must be 1-15 letters, digits, '.', '_' or '-'")
	}
	if m.TableID < 1 || m.TableID > 255 {
		return fmt.Errorf("table_id must be between 1 and 255")
	}
	if m.Subnet == "" {
		return fmt.Errorf("subnet is required")
	}
	_, subnet, err := net.ParseCIDR(m.Subnet)
	if err != nil || subnet.IP.To4() == nil {
		return fmt.Errorf("invalid IPv4 subnet %q", m.Subnet)
	}
	if ones, bits := subnet.Mask.Size(); bits != 32 || ones > 30 {
		return fmt.Errorf("subnet must contain at least two usable IPv4 addresses")
	}
	return nil
}

func validateMapping(m model.TunnelMapping) error { return ValidateMapping(m) }
