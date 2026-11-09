package api

import (
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

func validateSocksNode(n model.SocksNode) error { return validation.Node(n) }
func validateMapping(m model.TunnelMapping) error {
	if err := validation.Mapping(m); err != nil {
		return err
	}
	return model.ValidateTUNNamespace(m.TunName)
}
func subnetsOverlap(a, b string) bool { return validation.SubnetsOverlap(a, b) }
