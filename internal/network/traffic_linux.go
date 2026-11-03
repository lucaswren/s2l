//go:build linux

package network

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

// ReadTunTraffic 读取 /sys/class/net/<tun>/statistics 流量计数
func ReadTunTraffic(tunName string) (model.TunTraffic, error) {
	base := filepath.Join("/sys/class/net", tunName, "statistics")
	read := func(name string) (uint64, error) {
		b, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	values := make(map[string]uint64, 8)
	for _, name := range []string{"rx_bytes", "tx_bytes", "rx_packets", "tx_packets", "rx_dropped", "tx_dropped", "rx_errors", "tx_errors"} {
		value, err := read(name)
		if err != nil {
			return model.TunTraffic{TunName: tunName}, err
		}
		values[name] = value
	}
	return model.TunTraffic{TunName: tunName, RxBytes: values["rx_bytes"], TxBytes: values["tx_bytes"], RxPackets: values["rx_packets"], TxPackets: values["tx_packets"], RxDropped: values["rx_dropped"], TxDropped: values["tx_dropped"], RxErrors: values["rx_errors"], TxErrors: values["tx_errors"]}, nil
}
