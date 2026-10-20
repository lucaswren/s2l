//go:build linux

package network

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"time"
)

func ReadPeers(mappings []model.TunnelMapping) ([]model.PPPSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ReadPeersContext(ctx, mappings)
}
func ReadPeersContext(ctx context.Context, mappings []model.TunnelMapping) ([]model.PPPSession, error) {
	data, err := outputContext(ctx, "ip", "-j", "address", "show")
	if err != nil {
		return nil, err
	}
	rows, err := ParsePeers([]byte(data), mappings)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		stats, e := ReadTunTraffic(rows[i].Interface)
		if e != nil {
			rows[i].Error = "接口统计不可用"
			continue
		}
		rows[i].RxBytes = stats.RxBytes
		rows[i].TxBytes = stats.TxBytes
		rows[i].Dropped = stats.RxDropped + stats.TxDropped
		rows[i].Errors = stats.RxErrors + stats.TxErrors
	}
	return rows, nil
}
