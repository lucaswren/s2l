package api

import "github.com/lucaswren/s2l/internal/model"

func redactNode(n model.SocksNode) model.SocksNode {
	n.Password = ""
	return n
}

func redactNodes(nodes []model.SocksNode) []model.SocksNode {
	out := make([]model.SocksNode, len(nodes))
	for i, n := range nodes {
		out[i] = redactNode(n)
	}
	return out
}

func redactMapping(m model.TunnelMapping) model.TunnelMapping {
	m.L2TPPass = ""
	return m
}

func redactMappings(mappings []model.TunnelMapping) []model.TunnelMapping {
	out := make([]model.TunnelMapping, len(mappings))
	for i, m := range mappings {
		out[i] = redactMapping(m)
	}
	return out
}
