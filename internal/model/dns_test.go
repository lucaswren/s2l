package model

import "testing"

func TestDNSDefaultsAndEndpointValidation(t *testing.T) {
	defaults := (TunnelMapping{}).EffectiveDNSOptions()
	if defaults.Mode != "auto" || defaults.DoHURL != DefaultDNSDoHURL || defaults.TCPAddress != DefaultDNSTCPAddress {
		t.Fatal("legacy DNS defaults changed", defaults)
	}
	for _, options := range []DNSOptions{
		{},
		{Mode: "doh", DoHURL: "https://resolver.example:8443/custom-dns"},
		{Mode: "tcp", TCPAddress: "dns.internal:5353"},
		{DoHURL: "https://[2001:db8::53]/dns-query", TCPAddress: "[2001:db8::53]:53"},
		{TCPAddress: "10.1.0.53:53"}, // Private destinations are reached by SOCKS5.
	} {
		if err := options.Validate(); err != nil {
			t.Fatalf("valid resolver rejected: %+v: %v", options, err)
		}
	}
	for _, value := range []string{"http://dns.example/query", "https:///query", "https://u:p@dns.example/query", "https://dns.example/query?token=x", "https://dns.example/query#fragment", "https://dns.example:/query", "https://dns.example:65536/query", "https://dns.example/%0aquery", "https://bad host/query", "https://[fe80::1%25eth0]/query", "https://0.0.0.0/query", "https://dns.example/\\query"} {
		if (DNSOptions{DoHURL: value}).Validate() == nil {
			t.Fatalf("unsafe DoH endpoint accepted: %q", value)
		}
	}
	for _, value := range []string{"dns.example", "dns.example:0", "dns.example:+53", "dns.example:65536", "tcp://dns.example:53", "[fe80::1%eth0]:53", "dns.example:53\n", "bad host:53", "dns..example:53", "224.0.0.1:53"} {
		if (DNSOptions{TCPAddress: value}).Validate() == nil {
			t.Fatalf("unsafe TCP DNS endpoint accepted: %q", value)
		}
	}
}
