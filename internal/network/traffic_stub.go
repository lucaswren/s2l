//go:build !linux

package network

import "github.com/lucaswren/s2l/internal/model"

func ReadTunTraffic(tunName string) (model.TunTraffic, error) {
	return model.TunTraffic{TunName: tunName}, nil
}
