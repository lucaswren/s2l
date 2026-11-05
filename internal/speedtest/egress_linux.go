package speedtest

import (
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

func wanDialer(device string) (*net.Dialer, error) {
	if err := validation.Node(model.SocksNode{Egress: "wan", WANInterface: device}); err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(device)
	if err != nil || iface.Flags&net.FlagUp == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
		return nil, fmt.Errorf("configured WAN interface is unavailable")
	}
	return &net.Dialer{Timeout: 10 * time.Second, Control: func(_, _ string, raw syscall.RawConn) error {
		var bindErr error
		if err := raw.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, device)
		}); err != nil {
			return err
		}
		return bindErr
	}}, nil
}
