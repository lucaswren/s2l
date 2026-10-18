package api

import (
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

func validateSocksNode(n model.SocksNode) error    { return validation.Node(n) }
func validateMapping(m model.TunnelMapping) error  { return validation.Mapping(m) }
func validateCredential(label, value string) error { return validation.Credential(label, value) }
func subnetsOverlap(a, b string) bool              { return validation.SubnetsOverlap(a, b) }
