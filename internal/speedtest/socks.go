package speedtest

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// dialSOCKS5 经 SOCKS5 代理建立到 target（host:port）的 TCP 连接。
func dialSOCKS5(ctx context.Context, proxyAddr, username, password, target string) (net.Conn, error) {
	conn, _, err := dialSOCKSCommand(ctx, proxyAddr, username, password, 1, target)
	return conn, err
}

type socksReplyError struct{ code byte }

func (e *socksReplyError) Error() string {
	return fmt.Sprintf("SOCKS request rejected: code %d", e.code)
}

func dialSOCKSCommand(ctx context.Context, proxyAddr, username, password string, command byte, target string) (net.Conn, string, error) {
	if len(username) > 255 || len(password) > 255 {
		return nil, "", fmt.Errorf("SOCKS credentials exceed protocol limits")
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, "", fmt.Errorf("connect proxy: %w", err)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()

	if deadline, okd := ctx.Deadline(); okd {
		_ = conn.SetDeadline(deadline)
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, "", fmt.Errorf("invalid target: %w", err)
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid port: %w", err)
	}

	// greeting
	if username != "" {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
			return nil, "", err
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return nil, "", err
		}
	}
	auth := make([]byte, 2)
	if _, err := io.ReadFull(conn, auth); err != nil {
		return nil, "", fmt.Errorf("socks greeting: %w", err)
	}
	if auth[0] != 0x05 {
		return nil, "", fmt.Errorf("socks version %d", auth[0])
	}
	switch auth[1] {
	case 0x00:
		if username != "" {
			return nil, "", fmt.Errorf("SOCKS selected an unoffered authentication method")
		}
	case 0x02:
		if username == "" {
			return nil, "", fmt.Errorf("socks requires username/password")
		}
		req := []byte{0x01, byte(len(username))}
		req = append(req, username...)
		req = append(req, byte(len(password)))
		req = append(req, password...)
		if _, err := conn.Write(req); err != nil {
			return nil, "", err
		}
		resp := make([]byte, 2)
		if _, err := io.ReadFull(conn, resp); err != nil {
			return nil, "", fmt.Errorf("socks auth: %w", err)
		}
		if resp[0] != 1 || resp[1] != 0x00 {
			return nil, "", fmt.Errorf("socks auth rejected")
		}
	case 0xff:
		return nil, "", fmt.Errorf("socks: no acceptable auth method")
	default:
		return nil, "", fmt.Errorf("socks: unsupported auth method %d", auth[1])
	}

	// CONNECT
	req := []byte{0x05, command, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, "", fmt.Errorf("hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, uint16(port))
	req = append(req, portBuf...)
	if _, err := conn.Write(req); err != nil {
		return nil, "", err
	}

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, "", fmt.Errorf("socks connect: %w", err)
	}
	if hdr[0] != 0x05 || hdr[2] != 0 {
		return nil, "", fmt.Errorf("socks version %d", hdr[0])
	}
	if hdr[1] != 0x00 {
		return nil, "", &socksReplyError{code: hdr[1]}
	}

	bound, err := readSOCKSAddress(conn, hdr[3])
	if err != nil {
		return nil, "", fmt.Errorf("SOCKS reply address: %w", err)
	}

	_ = conn.SetDeadline(time.Time{})
	ok = true
	return conn, bound, nil
}

func readSOCKSAddress(reader io.Reader, kind byte) (string, error) {
	var host string
	switch kind {
	case 1, 4:
		size := 4
		if kind == 4 {
			size = 16
		}
		raw := make([]byte, size)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", err
		}
		host = net.IP(raw).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return "", err
		}
		if size[0] == 0 {
			return "", fmt.Errorf("empty host")
		}
		raw := make([]byte, int(size[0]))
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", err
		}
		host = string(raw)
	default:
		return "", fmt.Errorf("unsupported address type")
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))), nil
}
