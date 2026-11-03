package l2tp

import (
	"fmt"
	"net"
)

// LocalIPFromSubnet PPP 服务端地址：网段第一个可用地址（网络地址+1）
func LocalIPFromSubnet(subnet string) (string, error) {
	ip, ipNet, err := baseIPv4(subnet)
	if err != nil {
		return "", err
	}
	ip[3]++
	if !ipNet.Contains(ip) {
		return "", fmt.Errorf("subnet too small for local ip: %s", subnet)
	}
	return ip.String(), nil
}

// ClientIPFromSubnet 拨号客户端地址：网段第二个可用地址（网络地址+2）
func ClientIPFromSubnet(subnet string) (string, error) {
	ip, ipNet, err := baseIPv4(subnet)
	if err != nil {
		return "", err
	}
	ip[3] += 2
	if !ipNet.Contains(ip) {
		return "", fmt.Errorf("subnet too small for client ip: %s", subnet)
	}
	return ip.String(), nil
}

func baseIPv4(subnet string) (net.IP, *net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid subnet: %w", err)
	}
	ip4 := ipNet.IP.To4()
	if ip4 == nil {
		return nil, nil, fmt.Errorf("only IPv4 subnet supported")
	}
	ip := make(net.IP, 4)
	copy(ip, ip4)
	return ip, ipNet, nil
}
