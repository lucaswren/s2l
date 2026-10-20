package service

import (
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
)

// One shared snapshot per monitor cycle, also reused by status and PPP tuning.
// An unsuccessful collection is unknown, never a fabricated empty online list.
func (s *MappingService) peerSnapshot() ([]model.PPPSession, time.Time, string) {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	now := time.Now()
	if !s.peersAt.IsZero() && now.Sub(s.peersAt) < 5*time.Second {
		return append([]model.PPPSession{}, s.peers...), s.peersAt, s.peersError
	}
	rows, err := network.ReadPeers(s.store.ListMappings())
	s.peersAt = now
	if err != nil {
		s.peersError = "PPP 会话采集失败，等待下次采集"
		return append([]model.PPPSession{}, s.peers...), s.peersAt, s.peersError
	}
	for i := range rows {
		rows[i].FirstSeen = now
		for _, old := range s.peers {
			if old.Index == rows[i].Index && old.Interface == rows[i].Interface && old.PeerIP == rows[i].PeerIP {
				rows[i].FirstSeen = old.FirstSeen
				break
			}
		}
	}
	s.enrichSessions(rows)
	s.peers = rows
	s.peersError = ""
	return append([]model.PPPSession{}, rows...), now, ""
}
