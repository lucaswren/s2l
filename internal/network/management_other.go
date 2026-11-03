//go:build !linux

package network

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
)

func (c *Controller) SetManagementAccess(a model.ManagementAccess) error {
	if !a.Enabled {
		return nil
	}
	return fmt.Errorf("PPP management access requires Linux")
}
