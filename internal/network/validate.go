package network

import (
	"fmt"
	"regexp"

	"github.com/lucaswren/s2l/internal/model"
)

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

func ValidateMapping(m model.TunnelMapping) error {
	if err := m.ValidateOptions(); err != nil {
		return err
	}
	if m.TunName == "" {
		return fmt.Errorf("tun_name is required")
	}
	if !interfaceNamePattern.MatchString(m.TunName) {
		return fmt.Errorf("tun_name must be 1-15 letters, digits, '.', '_' or '-'")
	}
	if m.TableID < 1 || m.TableID > 252 {
		return fmt.Errorf("table_id must be between 1 and 252; 253-255 are reserved by Linux")
	}
	if m.Subnet == "" {
		return fmt.Errorf("subnet is required")
	}
	if _, err := model.TunnelSubnet(m.Subnet); err != nil {
		return err
	}
	return nil
}

func validateMapping(m model.TunnelMapping) error { return ValidateMapping(m) }
