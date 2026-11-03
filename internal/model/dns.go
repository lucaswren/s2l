package model

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const DefaultDNSDoHURL = "https://1.1.1.1/dns-query"
const DefaultDNSTCPAddress = "8.8.8.8:53"

// These IPCP addresses are intercepted on the mapping's TUN, not public
// listeners or direct fallback resolvers. Each mapping selects its actual
// resolver through its fixed SOCKS5 upstream.
func ClientDNSAddresses() []string { return []string{"1.1.1.1", "8.8.8.8"} }

// DNSOptions is comparable so probes share only identical resolver policy.
type DNSOptions struct {
	Mode       string
	DoHURL     string
	TCPAddress string
}

func (o DNSOptions) WithDefaults() DNSOptions {
	if o.Mode == "" {
		o.Mode = "auto"
	}
	if o.DoHURL == "" {
		o.DoHURL = DefaultDNSDoHURL
	}
	if o.TCPAddress == "" {
		o.TCPAddress = DefaultDNSTCPAddress
	}
	return o
}

func (m TunnelMapping) EffectiveDNSOptions() DNSOptions {
	return (DNSOptions{Mode: m.DNSMode, DoHURL: m.DNSDoHURL, TCPAddress: m.DNSTCPAddress}).WithDefaults()
}

func (o DNSOptions) Validate() error {
	o = o.WithDefaults()
	switch o.Mode {
	case "auto", "doh", "tcp":
	default:
		return fmt.Errorf("invalid dns_mode")
	}
	// Endpoints are visible in configuration views. Do not accept URL secrets.
	// Resolver names are sent to SOCKS5, never resolved on the gateway.
	u, err := url.Parse(o.DoHURL)
	if err != nil || len(o.DoHURL) > 2048 || unsafeDNSText(o.DoHURL) || u.Scheme != "https" || u.Opaque != "" || u.User != nil || strings.Contains(o.DoHURL, "#") || u.RawQuery != "" || u.ForceQuery || !dnsHost(u.Hostname()) || unsafeDNSText(u.Path) || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !dnsPort(u.Port())) {
		return fmt.Errorf("dns_doh_url must be an HTTPS URL without credentials, query, fragment or whitespace")
	}
	host, port, err := net.SplitHostPort(o.TCPAddress)
	if err != nil || len(o.TCPAddress) > 320 || !dnsHost(host) || !dnsPort(port) {
		return fmt.Errorf("dns_tcp_address must be host:port (bracket IPv6 addresses); scoped addresses are not supported")
	}
	return nil
}

func unsafeDNSText(s string) bool {
	return strings.ContainsAny(s, "\\") || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func dnsPort(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	p, err := strconv.Atoi(s)
	return err == nil && p > 0 && p <= 65535
}

func dnsHost(s string) bool {
	if s == "" || len(s) > 253 || unsafeDNSText(s) || strings.Contains(s, "%") {
		return false
	}
	if ip := net.ParseIP(s); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast()
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}
