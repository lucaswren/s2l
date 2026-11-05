//go:build !linux

package speedtest

import (
	"fmt"
	"net"
)

func wanDialer(string) (*net.Dialer, error) {
	return nil, fmt.Errorf("WAN egress requires Linux")
}
