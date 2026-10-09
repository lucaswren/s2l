//go:build !linux

package network

import (
	"fmt"
	"time"
)

func WaitInterface(name string, timeout time.Duration) error {
	return fmt.Errorf("WaitInterface is only supported on linux")
}
