//go:build linux

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/network"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const pppSocket = "/run/s2l/ppp-events.sock"

func pppIdentity(pid int) (string, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil {
		return "", e
	}
	name := strings.Split(string(b), "\x00")[0]
	if filepath.Base(name) != "pppd" {
		return "", fmt.Errorf("not pppd")
	}
	b, e = os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	i := strings.LastIndex(string(b), ") ")
	if i < 0 {
		return "", fmt.Errorf("invalid pid stat")
	}
	fields := strings.Fields(string(b)[i+2:])
	if len(fields) < 20 {
		return "", fmt.Errorf("invalid pid stat")
	}
	return fields[19], nil
}
func (s *MappingService) listenPPP(ctx context.Context) {
	if e := os.MkdirAll(filepath.Dir(pppSocket), 0700); e != nil {
		return
	}
	dir, err := os.Lstat(filepath.Dir(pppSocket))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 || dir.Mode().Perm()&0077 != 0 {
		return
	}
	// Probe first: never unlink another live daemon's socket.
	if c, e := net.DialTimeout("unixgram", pppSocket, time.Second); e == nil {
		c.Close()
		return
	}
	_ = os.Remove(pppSocket)
	c, e := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: pppSocket, Net: "unixgram"})
	if e != nil {
		return
	}
	defer c.Close()
	defer os.Remove(pppSocket)
	_ = os.Chmod(pppSocket, 0600)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	buf := make([]byte, 2048)
	for {
		n, _, err := c.ReadFromUnix(buf)
		if err != nil {
			return
		}
		var ev PPPEvent
		if n < len(buf) && json.Unmarshal(buf[:n], &ev) == nil {
			_ = s.acceptPPP(ev)
		}
	}
}
func (s *MappingService) DisconnectSession(ctx context.Context, iface, peer string, index int) error {
	return s.disconnectSession(ctx, iface, peer, index, "administrator")
}
func (s *MappingService) disconnectSession(ctx context.Context, iface, peer string, index int, reason string) error {
	if !pppName.MatchString(iface) {
		return fmt.Errorf("invalid interface")
	}
	rows, err := network.ReadPeersContext(ctx, s.store.ListMappings())
	if err != nil {
		return err
	}
	found := false
	for _, r := range rows {
		if r.Interface == iface && r.PeerIP == peer && r.Index == index && r.MappingID != "" {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("会话已变化，请刷新")
	}
	s.sessions.mu.Lock()
	ev, ok := s.sessions.active[iface]
	s.sessions.mu.Unlock()
	if !ok || ev.Peer != peer || ev.Index != index {
		return fmt.Errorf("缺少可验证的拨号事件，不能断开此会话")
	}
	process, processErr := os.FindProcess(ev.PID)
	if processErr != nil {
		return processErr
	}
	defer process.Release()
	identity, err := pppIdentity(ev.PID)
	if err != nil || identity != ev.Identity {
		return fmt.Errorf("会话进程已变化，请刷新")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	s.sessions.mu.Lock()
	current, exists := s.sessions.active[iface]
	if !exists || current.Identity != ev.Identity || current.Index != index {
		s.sessions.mu.Unlock()
		return fmt.Errorf("会话已变化，请刷新")
	}
	current.Requested = reason
	s.sessions.active[iface] = current
	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		s.sessions.active[iface] = ev
	}
	s.sessions.mu.Unlock()
	if err != nil {
		return err
	}
	s.operations.Audit("session-disconnect", iface, "已请求断开会话 "+strconv.Itoa(index))
	return nil
}
