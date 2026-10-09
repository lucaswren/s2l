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
	if in["sniff"] != true {
		t.Fatalf("sniff should be true")
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
	if len(rules) == 0 {
		t.Fatal("expected dns hijack rule")
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
