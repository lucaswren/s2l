//go:build !linux

package network

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
)

func CheckUninstallMappings([]model.TunnelMapping) error {
	return fmt.Errorf("uninstall requires Linux")
}
func CleanupStoppedMappings([]model.TunnelMapping) error {
	return fmt.Errorf("uninstall requires Linux")
}
