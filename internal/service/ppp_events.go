package service

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PPPEvent struct {
	Kind      string    `json:"kind"`
	Interface string    `json:"interface"`
	Peer      string    `json:"peer"`
	PID       int       `json:"pid"`
	At        time.Time `json:"at"`
}
type sessionEvent struct {
	PPPEvent
	Identity   string
	Index      int
	Requested  string
	ObservedAt time.Time
}
type sessionTracker struct {
	mu     sync.Mutex
	active map[string]sessionEvent
}

var pppName = regexp.MustCompile(`^ppp[0-9]+$`)

func (s *MappingService) acceptPPP(e PPPEvent) error {
	if !pppName.MatchString(e.Interface) || net.ParseIP(e.Peer) == nil || e.PID < 2 || (e.Kind != "up" && e.Kind != "down") || e.At.IsZero() || time.Since(e.At) > time.Minute || time.Until(e.At) > time.Minute {
		return fmt.Errorf("invalid PPP event")
	}
	mapping := ""
	limit := 0
	for _, m := range s.store.ListMappings() {
		_, subnet, err := net.ParseCIDR(m.Subnet)
		if err == nil && subnet.Contains(net.ParseIP(e.Peer)) {
			mapping = m.ID
			limit = m.EffectiveMaxSessions()
			break
		}
	}
	if mapping == "" {
		return fmt.Errorf("unmanaged PPP peer")
	}
	index := 0
	if e.Kind == "up" {
		data, err := os.ReadFile(filepath.Join("/sys/class/net", e.Interface, "ifindex"))
		if err != nil {
			return err
		}
		index, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || index <= 0 {
			return fmt.Errorf("invalid interface index")
		}
	}
	identity, err := pppIdentity(e.PID)
	if err != nil && e.Kind == "up" {
		return err
	}
	s.sessions.mu.Lock()
	if s.sessions.active == nil {
		s.sessions.active = map[string]sessionEvent{}
	}
	old, exists := s.sessions.active[e.Interface]
	if e.Kind == "up" {
		if exists && old.Identity == identity && old.Index == index && !e.At.After(old.At) {
			s.sessions.mu.Unlock()
			return nil
		}
		s.sessions.active[e.Interface] = sessionEvent{PPPEvent: e, Identity: identity, Index: index, ObservedAt: time.Now()}
	} else if exists && old.PID == e.PID && old.Peer == e.Peer && !e.At.Before(old.At) && (err != nil || identity == old.Identity) {
		delete(s.sessions.active, e.Interface)
	} else {
		s.sessions.mu.Unlock()
		return nil
	}
	s.sessions.mu.Unlock()
	detail := e.Interface + " / " + e.Peer
	if e.Kind == "down" {
		if old.Requested == "administrator" {
			detail += " / 管理员请求断开"
		} else if old.Requested == "limit" {
			detail += " / 超过 PPP 会话上限"
		} else {
			detail += " / 未提供可确认的断线原因"
		}
	}
	s.operations.Audit("ppp-"+e.Kind, mapping, detail)
	s.peerMu.Lock()
	s.peersAt = time.Time{}
	s.peerMu.Unlock()
	if e.Kind == "up" && limit > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rows, readErr := network.ReadPeersContext(ctx, s.store.ListMappings())
		if readErr != nil {
			s.operations.Audit("session-limit", mapping, "会话上限检查失败，未自动断开")
		} else {
			// Interface creation order preserves earlier sessions even when several
			// ip-up events queue before the listener can process the first one.
			managed := []model.PPPSession{}
			for _, row := range rows {
				if row.MappingID == mapping {
					managed = append(managed, row)
				}
			}
			sort.Slice(managed, func(i, j int) bool { return managed[i].Index < managed[j].Index })
			for rank, row := range managed {
				if row.Interface == e.Interface && row.PeerIP == e.Peer && row.Index == index && rank >= limit {
					disconnectErr := s.disconnectSession(ctx, e.Interface, e.Peer, index, "limit")
					detail := "已拒绝超出上限的新会话"
					if disconnectErr != nil {
						detail = "会话超过上限，但断开失败"
					}
					s.operations.Audit("session-limit", mapping, detail)
					break
				}
			}
		}
	}

	return nil
}
func (s *MappingService) enrichSessions(rows []model.PPPSession) {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	live := map[string]int{}
	for _, row := range rows {
		live[row.Interface] = row.Index
	}
	for iface, e := range s.sessions.active {
		if live[iface] != e.Index && time.Since(e.ObservedAt) > time.Minute {
			delete(s.sessions.active, iface)
		}
	}
	for i := range rows {
		e, ok := s.sessions.active[rows[i].Interface]
		if ok && e.Peer == rows[i].PeerIP && e.Index == rows[i].Index {
			rows[i].ConnectedAt = e.At
			rows[i].Disconnectable = true
		}
	}
}
