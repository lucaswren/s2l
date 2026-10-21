package config

import (
	"github.com/lucaswren/s2l/internal/model"
	"testing"
)

func TestPPPManagementCannotExposeUnauthenticatedLoopbackPanel(t *testing.T) {
	cfg := Default()
	cfg.PPPManagement = model.ManagementAccess{Enabled: true, DestinationIP: "198.18.10.1", SourceCIDRs: []string{"10.0.101.0/24"}, TCPPorts: []int{38866, 51234}}
	if cfg.Validate() == nil {
		t.Fatal("unauthenticated loopback panel allowed through PPP")
	}
	cfg.AdminUser = "admin"
	cfg.AdminPass = "test-password-long"
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	cfg.PPPManagement.Enabled = false
	cfg.AdminUser = ""
	cfg.AdminPass = ""
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
}
