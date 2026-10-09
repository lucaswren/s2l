package speedtest

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// dialSOCKS5 经 SOCKS5 代理建立到 target（host:port）的 TCP 连接。
func dialSOCKS5(ctx context.Context, proxyAddr, username, password, target string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect proxy: %w", err)
	}
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
		return nil, fmt.Errorf("invalid target: %w", err)
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}

	// greeting
	if username != "" {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
			return nil, err
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return nil, err
		}
	}
	auth := make([]byte, 2)
	if _, err := io.ReadFull(conn, auth); err != nil {
		return nil, fmt.Errorf("socks greeting: %w", err)
	}
	if auth[0] != 0x05 {
		return nil, fmt.Errorf("socks version %d", auth[0])
	}
	switch auth[1] {
	case 0x00:
		// no auth
	case 0x02:
		if username == "" {
			return nil, fmt.Errorf("socks requires username/password")
		}
		req := []byte{0x01, byte(len(username))}
		req = append(req, username...)
		req = append(req, byte(len(password)))
		req = append(req, password...)
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		resp := make([]byte, 2)
		if _, err := io.ReadFull(conn, resp); err != nil {
			return nil, fmt.Errorf("socks auth: %w", err)
		}
		if resp[1] != 0x00 {
			return nil, fmt.Errorf("socks auth rejected")
		}
	case 0xff:
		return nil, fmt.Errorf("socks: no acceptable auth method")
	default:
		return nil, fmt.Errorf("socks: unsupported auth method %d", auth[1])
	}

	// CONNECT
	req := []byte{0x05, 0x01, 0x00}
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
			return nil, fmt.Errorf("hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, uint16(port))
	req = append(req, portBuf...)
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, fmt.Errorf("socks connect: %w", err)
	}
	if hdr[0] != 0x05 {
		return nil, fmt.Errorf("socks version %d", hdr[0])
	}
	if hdr[1] != 0x00 {
		return nil, fmt.Errorf("socks connect failed: code %d", hdr[1])
	}
	switch hdr[3] {
	case 0x01:
		_, err = io.CopyN(io.Discard, conn, 4+2)
	case 0x03:
		l := make([]byte, 1)
		if _, err = io.ReadFull(conn, l); err == nil {
			_, err = io.CopyN(io.Discard, conn, int64(l[0])+2)
		}
	case 0x04:
		_, err = io.CopyN(io.Discard, conn, 16+2)
	default:
		err = fmt.Errorf("socks: unknown atyp %d", hdr[3])
	}
	if err != nil {
		return nil, fmt.Errorf("socks connect reply: %w", err)
	}

	_ = conn.SetDeadline(time.Time{})
	ok = true
	return conn, nil
}
