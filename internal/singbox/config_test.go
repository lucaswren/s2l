package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestGenerateConfig(t *testing.T) {
	node := model.SocksNode{
		ID:       "n1",
		Name:     "node-a",
		Addr:     "1.2.3.4:1080",
		Username: "u",
		Password: "p",
	}
	mapping := model.TunnelMapping{
		ID:      "m1",
		NodeID:  "n1",
		TunName: "tun101",
		TableID: 101,
		Subnet:  "10.0.101.0/24",
	}

	data, err := GenerateConfig(node, mapping)
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json: %v", err)
	}

	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %#v", cfg["inbounds"])
	}
	in := inbounds[0].(map[string]any)
	if in["type"] != "tun" {
		t.Fatalf("inbound type = %v", in["type"])
	}
	if in["interface_name"] != "tun101" {
		t.Fatalf("interface_name = %v", in["interface_name"])
	}
	if in["auto_route"] != false {
		t.Fatalf("auto_route should be false, got %v", in["auto_route"])
	}
	if int(in["mtu"].(float64)) != 1400 {
		t.Fatalf("mtu = %v", in["mtu"])
	}
	addrs, _ := in["address"].([]any)
	if len(addrs) == 0 || addrs[0] != "172.19.101.1/30" {
		t.Fatalf("address = %#v", in["address"])
	}

	outbounds, ok := cfg["outbounds"].([]any)
	if !ok || len(outbounds) < 1 {
		t.Fatalf("expected outbounds, got %#v", cfg["outbounds"])
	}
	for _, outbound := range outbounds {
		if outbound.(map[string]any)["type"] == "direct" {
			t.Fatal("direct outbound must not be available")
		}
	}
	if cfg["route"].(map[string]any)["final"] != "proxy" {
		t.Fatal("traffic must use proxy")
	}
	for _, server := range cfg["dns"].(map[string]any)["servers"].([]any) {
		if server.(map[string]any)["detour"] != "proxy" {
			t.Fatal("DNS must use proxy")
		}
	}
	proxy := outbounds[0].(map[string]any)
	if proxy["type"] != "socks" {
		t.Fatalf("outbound type = %v", proxy["type"])
	}
	if proxy["server"] != "1.2.3.4" {
		t.Fatalf("server = %v", proxy["server"])
	}
	if int(proxy["server_port"].(float64)) != 1080 {
		t.Fatalf("server_port = %v", proxy["server_port"])
	}
	if _, exists := in["sniff"]; exists {
		t.Fatal("legacy inbound sniffing would run before early rejection")
	}
	if _, exists := in["sniff_override_destination"]; exists {
		t.Fatal("legacy destination override must not be emitted")
	}
	dns, ok := cfg["dns"].(map[string]any)
	if !ok {
		t.Fatal("missing dns")
	}
	if dns["final"] != "doh" {
		t.Fatalf("dns.final = %v", dns["final"])
	}
	route := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	if len(rules) != 4 {
		t.Fatalf("expected early DNS, rejection, sniffing and protocol DNS rules, got %v", rules)
	}
	if rules[0].(map[string]any)["action"] != "hijack-dns" || rules[0].(map[string]any)["port"] != float64(53) {
		t.Fatal("DNS interception must precede HTTPS UDP rejection")
	}
	quic := rules[1].(map[string]any)
	if quic["network"] != "udp" || quic["port"] != float64(443) || quic["action"] != "reject" || quic["method"] != "default" || quic["no_drop"] != true {
		t.Fatalf("HTTPS UDP must fail promptly without blocking other UDP: %v", quic)
	}
	if rules[2].(map[string]any)["action"] != "sniff" {
		t.Fatal("sniffing must follow early rejection")
	}
	if rules[3].(map[string]any)["protocol"] != "dns" || rules[3].(map[string]any)["action"] != "hijack-dns" {
		t.Fatal("nonstandard-port DNS must remain intercepted after sniffing")
	}

	if !strings.Contains(ConfigPath("tun101"), "s2l-tun101.json") {
		t.Fatalf("unexpected config path: %s", ConfigPath("tun101"))
	}
}

func TestGenerateConfigInvalidAddr(t *testing.T) {
	_, err := GenerateConfig(model.SocksNode{Addr: "bad"}, model.TunnelMapping{TunName: "tun1", TableID: 1})
	if err == nil {
		t.Fatal("expected error for invalid addr")
	}
}

func TestRuntimeDNSAndMappingOptions(t *testing.T) {
	for _, policy := range []string{"allow", "reject-quic", "reject"} {
		m := model.TunnelMapping{TunName: "tun101", TableID: 101, MTU: 1280, MSS: 1100, UDPPolicy: policy}
		data, err := GenerateConfig(model.SocksNode{Addr: "1.2.3.4:1080"}, m, "127.0.0.1:19053")
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		json.Unmarshal(data, &cfg)
		if cfg["inbounds"].([]any)[0].(map[string]any)["mtu"] != float64(1280) {
			t.Fatal("mapping MTU ignored")
		}
		dns := cfg["dns"].(map[string]any)
		server := dns["servers"].([]any)[0].(map[string]any)
		if dns["final"] != "local-proxy" || server["address"] != "tcp://127.0.0.1:19053" {
			t.Fatal("live resolver not wired")
		}
		if server["detour"] != "dns-loopback" {
			t.Fatal("DNS would use default SOCKS outbound to reach loopback")
		}
		for _, value := range cfg["outbounds"].([]any) {
			out := value.(map[string]any)
			if out["type"] == "direct" && (out["override_address"] != "127.0.0.1" || out["override_port"] != float64(19053)) {
				t.Fatal("direct outbound not confined to resolver")
			}
		}
		rules := cfg["route"].(map[string]any)["rules"].([]any)
		if policy == "allow" {
			if len(rules) != 3 {
				t.Fatal("allow policy still rejects UDP")
			}
		} else {
			rule := rules[1].(map[string]any)
			_, hasPort := rule["port"]
			if hasPort != (policy == "reject-quic") {
				t.Fatal("incorrect UDP rejection scope")
			}
		}
	}
	if _, err := GenerateConfig(model.SocksNode{Addr: "1.2.3.4:1080"}, model.TunnelMapping{TunName: "tun101"}, "8.8.8.8:53"); err == nil {
		t.Fatal("non-local resolver accepted")
	}
}
