package model

import "testing"

func TestManagementExceptionValidation(t *testing.T) {
	good := ManagementAccess{Enabled: true, DestinationIP: "156.245.235.230", SourceCIDRs: []string{"10.0.101.0/24"}, TCPPorts: []int{38866, 51234}}
	if good.Validate() != nil || (ManagementAccess{}).Validate() != nil {
		t.Fatal("valid configuration rejected")
	}
	for _, bad := range []ManagementAccess{
		{Enabled: true, DestinationIP: "0.0.0.0", SourceCIDRs: good.SourceCIDRs, TCPPorts: good.TCPPorts},
		{Enabled: true, DestinationIP: good.DestinationIP, SourceCIDRs: []string{"0.0.0.0/0"}, TCPPorts: good.TCPPorts},
		{Enabled: true, DestinationIP: good.DestinationIP, SourceCIDRs: []string{"10.0.101.2/24"}, TCPPorts: good.TCPPorts},
		{Enabled: true, DestinationIP: good.DestinationIP, SourceCIDRs: good.SourceCIDRs, TCPPorts: []int{22, 22}},
		{Enabled: true, DestinationIP: good.DestinationIP, SourceCIDRs: good.SourceCIDRs, TCPPorts: []int{0}},
	} {
		if bad.Validate() == nil {
			t.Fatal(bad)
		}
	}
}
