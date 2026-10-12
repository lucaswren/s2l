package speedtest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

type HealthReport struct{ TCP, UDP, DNS model.ProtocolHealth }

// All target traffic is carried by SOCKS5; UDP goes only to its negotiated relay.
func ProbeHealth(ctx context.Context, node model.SocksNode) HealthReport {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var report HealthReport
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); err := CheckHealth(ctx, node); report.TCP = protocolResult("https", err) }()
	go func() { defer wg.Done(); report.UDP = checkUDPHealth(ctx, node) }()
	go func() { defer wg.Done(); report.DNS = checkDNSHealth(ctx, node) }()
	wg.Wait()
	return report
}

func protocolResult(method string, err error) model.ProtocolHealth {
	result := model.ProtocolHealth{State: "healthy", Method: method, CheckedAt: time.Now()}
	if err != nil {
		result.State = "unavailable"
		result.Error = err.Error()
	}
	return result
}

func checkDNSHealth(ctx context.Context, node model.SocksNode) model.ProtocolHealth {
	return checkDNSHealthTargets(ctx, node, "https://1.1.1.1/dns-query", "8.8.8.8:53")
}

func checkDNSHealthTargets(ctx context.Context, node model.SocksNode, dohTarget, tcpTarget string) model.ProtocolHealth {
	query, err := dnsQuery()
	if err != nil {
		return protocolResult("doh", fmt.Errorf("DNS probe could not be initialized"))
	}
	attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
	err = exchangeDoH(attempt, node, dohTarget, query)
	cancel()
	if err == nil {
		return protocolResult("doh", nil)
	}
	attempt, cancel = context.WithTimeout(ctx, 3*time.Second)
	err = exchangeTCPDNS(attempt, node, tcpTarget, query)
	cancel()
	if err == nil {
		return model.ProtocolHealth{State: "degraded", Method: "tcp-fallback", Error: "Primary DoH failed; TCP DNS probe succeeded; the active resolver is not automatically switched", CheckedAt: time.Now()}
	}
	return protocolResult("doh/tcp", fmt.Errorf("DoH and TCP DNS forwarding or response validation failed"))
}

func exchangeDoH(ctx context.Context, node model.SocksNode, target string, query []byte) error {
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
		return dialSOCKS5(c, node.Addr, node.Username, node.Password, addr)
	}}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(query))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || resp.StatusCode != 200 || media != "application/dns-message" {
		return fmt.Errorf("invalid DoH response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(body) > 4096 {
		return fmt.Errorf("invalid DoH body")
	}
	return validateDNS(query, body)
}

func exchangeTCPDNS(ctx context.Context, node model.SocksNode, target string, query []byte) error {
	conn, err := dialSOCKS5(ctx, node.Addr, node.Username, node.Password, target)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	frame := make([]byte, 2)
	binary.BigEndian.PutUint16(frame, uint16(len(query)))
	frame = append(frame, query...)
	if _, err = io.Copy(conn, bytes.NewReader(frame)); err != nil {
		return err
	}
	var size [2]byte
	if _, err = io.ReadFull(conn, size[:]); err != nil {
		return err
	}
	length := int(binary.BigEndian.Uint16(size[:]))
	if length < 12 || length > 4096 {
		return fmt.Errorf("invalid DNS frame size")
	}
	response := make([]byte, length)
	if _, err = io.ReadFull(conn, response); err != nil {
		return err
	}
	return validateDNS(query, response)
}

func checkUDPHealth(ctx context.Context, node model.SocksNode) model.ProtocolHealth {
	attempt, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	control, relay, err := dialSOCKSCommand(attempt, node.Addr, node.Username, node.Password, 3, "0.0.0.0:0")
	if err != nil {
		var reply *socksReplyError
		if errors.As(err, &reply) && reply.code == 7 {
			return model.ProtocolHealth{State: "unsupported", Method: "udp-associate", Error: "Proxy explicitly rejects UDP ASSOCIATE as unsupported", CheckedAt: time.Now()}
		}
		return protocolResult("udp-associate", fmt.Errorf("SOCKS5 UDP association failed"))
	}
	controlDone := make(chan struct{})
	go func() { var b [1]byte; _, _ = control.Read(b[:]); close(controlDone) }()
	defer func() { control.Close(); <-controlDone }()
	host, port, err := net.SplitHostPort(relay)
	number, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || number < 1 || number > 65535 {
		return protocolResult("udp-associate", fmt.Errorf("Invalid SOCKS5 UDP relay address"))
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host, _, err = net.SplitHostPort(control.RemoteAddr().String())
		if err != nil {
			return protocolResult("udp-associate", fmt.Errorf("Invalid proxy peer address"))
		}
	}
	relay = net.JoinHostPort(host, port)
	d := net.Dialer{}
	udp, err := d.DialContext(attempt, "udp", relay)
	if err != nil {
		return protocolResult("udp-associate", fmt.Errorf("SOCKS5 UDP relay is unreachable"))
	}
	defer udp.Close()
	stop := context.AfterFunc(attempt, func() { udp.Close(); control.Close() })
	defer stop()
	for _, target := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		query, err := dnsQuery()
		if err != nil {
			break
		}
		deadline := time.Now().Add(2 * time.Second)
		if limit, ok := attempt.Deadline(); ok && limit.Before(deadline) {
			deadline = limit
		}
		udp.SetDeadline(deadline)
		frame, err := udpRequest(target, query)
		if err != nil {
			break
		}
		if _, err = udp.Write(frame); err != nil {
			continue
		}
		buffer := make([]byte, 65535)
		for {
			n, err := udp.Read(buffer)
			if err != nil {
				break
			}
			payload, err := udpResponse(buffer[:n], target)
			if err == nil && validateDNS(query, payload) == nil {
				select {
				case <-controlDone:
					return protocolResult("udp-associate", fmt.Errorf("SOCKS5 UDP control connection closed"))
				default:
				}
				return protocolResult("dns-udp", nil)
			}
			// Ignore unrelated, spoofed, fragmented or malformed packets until deadline.
		}
	}
	return protocolResult("dns-udp", fmt.Errorf("No valid DNS response through the SOCKS5 UDP relay"))
}

func udpRequest(target string, payload []byte) ([]byte, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, fmt.Errorf("UDP probe target must be IPv4")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("invalid UDP target port")
	}
	frame := append([]byte{0, 0, 0, 1}, ip...)
	frame = append(frame, byte(number>>8), byte(number))
	return append(frame, payload...), nil
}

func udpResponse(packet []byte, target string) ([]byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return nil, fmt.Errorf("invalid or fragmented UDP relay packet")
	}
	reader := bytes.NewReader(packet[4:])
	source, err := readSOCKSAddress(reader, packet[3])
	if err != nil || source != target {
		return nil, fmt.Errorf("unexpected UDP response source")
	}
	return packet[len(packet)-reader.Len():], nil
}
