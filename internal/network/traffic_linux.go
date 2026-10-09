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
	rx, err := read("rx_bytes")
	if err != nil {
		return model.TunTraffic{TunName: tunName}, err
	}
	tx, err := read("tx_bytes")
	if err != nil {
		return model.TunTraffic{TunName: tunName}, err
	}
	rxp, _ := read("rx_packets")
	txp, _ := read("tx_packets")
	return model.TunTraffic{
		TunName:   tunName,
		RxBytes:   rx,
		TxBytes:   tx,
		RxPackets: rxp,
		TxPackets: txp,
	}, nil
}
