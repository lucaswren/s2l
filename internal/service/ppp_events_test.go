package service

import (
	"github.com/lucaswren/s2l/internal/model"
	"testing"
	"time"
)

func TestReusedPPPInterfaceCannotInheritOldSession(t *testing.T) {
	s := &MappingService{}
	s.sessions.active = map[string]sessionEvent{"ppp0": {PPPEvent: PPPEvent{Interface: "ppp0", Peer: "10.0.101.2", At: time.Now()}, Index: 5, ObservedAt: time.Now().Add(-2 * time.Minute)}}
	rows := []model.PPPSession{{Interface: "ppp0", PeerIP: "10.0.101.2", Index: 9}}
	s.enrichSessions(rows)
	if rows[0].Disconnectable || !rows[0].ConnectedAt.IsZero() {
		t.Fatal(rows)
	}
	if len(s.sessions.active) != 0 {
		t.Fatal("stale event retained")
	}
}
