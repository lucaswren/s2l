package speedtest

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func CheckWANEgress(node model.SocksNode) error {
	if !node.IsWAN() {
		return nil
	}
	_, err := wanDialer(node.WANInterface)
	return err
}

// DialNode never changes the configured egress after an error.
func DialNode(ctx context.Context, node model.SocksNode, target string) (net.Conn, error) {
	if !node.IsWAN() {
		if node.Egress != "" && node.Egress != "socks5" {
			return nil, fmt.Errorf("invalid egress type")
		}
		return DialSOCKS5(ctx, node.Addr, node.Username, node.Password, target)
	}
	d, err := wanDialer(node.WANInterface)
	if err != nil {
		return nil, err
	}
	// Bootstrap over TCP on the selected interface: WAN probes and custom DNS
	// hostnames must not depend on UDP 53 or the system's default-route resolver.
	resolverDialer := *d
	d.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return resolverDialer.DialContext(ctx, "tcp4", "1.1.1.1:53")
	}}
	return d.DialContext(ctx, "tcp4", target)
}

func checkWANUDP(ctx context.Context, node model.SocksNode) model.ProtocolHealth {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	d, err := wanDialer(node.WANInterface)
	if err != nil {
		return protocolResult("wan-dns-udp", err)
	}
	conn, err := d.DialContext(ctx, "udp4", "1.1.1.1:53")
	if err != nil {
		return protocolResult("wan-dns-udp", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	query, err := dnsQuery()
	if err != nil {
		return protocolResult("wan-dns-udp", err)
	}
	if _, err = conn.Write(query); err != nil {
		return protocolResult("wan-dns-udp", err)
	}
	buffer := make([]byte, 4096)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			return protocolResult("wan-dns-udp", err)
		}
		if validateDNS(query, buffer[:n]) == nil {
			return protocolResult("wan-dns-udp", nil)
		}
	}
}
