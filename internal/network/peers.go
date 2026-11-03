package network

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

func ParsePeers(data []byte, mappings []model.TunnelMapping) ([]model.PPPSession, error) {
	var interfaces []struct {
		Name      string   `json:"ifname"`
		Index     int      `json:"ifindex"`
		MTU       int      `json:"mtu"`
		Queue     int      `json:"txqlen"`
		Flags     []string `json:"flags"`
		Addresses []struct {
			Peer    string `json:"peer"`
			Address string `json:"address"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(data, &interfaces); err != nil {
		return nil, err
	}
	sessions := []model.PPPSession{}
	for _, iface := range interfaces {
		if !strings.HasPrefix(iface.Name, "ppp") || strings.HasPrefix(iface.Name, "pppst") {
			continue
		}
		point := false
		for _, flag := range iface.Flags {
			if flag == "POINTOPOINT" {
				point = true
			}
		}
		if !point {
			continue
		}
		for _, a := range iface.Addresses {
			peer := a.Peer
			// iproute2 emits the point-to-point destination as "address" on
			// current Linux versions. Never substitute "local" (the server).
			if peer == "" {
				peer = a.Address
			}
			ip := net.ParseIP(strings.Split(peer, "/")[0])
			if ip == nil || ip.To4() == nil {
				continue
			}
			row := model.PPPSession{Interface: iface.Name, Index: iface.Index, PeerIP: ip.String(), MTU: iface.MTU, QueueLength: iface.Queue}
			for _, m := range mappings {
				_, n, err := net.ParseCIDR(m.Subnet)
				if err == nil && n.Contains(ip) {
					row.MappingID = m.ID
					break
				}
			}
			sessions = append(sessions, row)
			break
		}
	}
	return sessions, nil
}
