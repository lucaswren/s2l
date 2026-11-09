package evidence

import (
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestMappingTimelineUsesFreshSamplesFromSameSession(t *testing.T) {
	now := time.Now()
	sample := func(offset time.Duration, index int, bytes uint64) Sample {
		at := now.Add(offset)
		return Sample{At: at, Status: model.SystemStatus{
			CollectedAt: at, SessionsCheckedAt: at,
			Sessions:       []model.PPPSession{{MappingID: "us", Index: index, Interface: "ppp0", PeerIP: "10.0.102.2", RxBytes: bytes, TxBytes: bytes * 2}},
			SingboxPIDs:    map[string]int{"tun102": 7},
			UpstreamHealth: map[string]model.UpstreamHealth{"us": {Process: model.ProcessStatus{StartedAt: now, ErrorLines: bytes}}},
		}}
	}
	first, second := sample(0, 3, 100), sample(5*time.Second, 3, 150)
	stale := sample(10*time.Second, 3, 150)
	stale.Status.SessionsCheckedAt = second.Status.SessionsCheckedAt
	replaced := sample(15*time.Second, 4, 200)
	reset := sample(20*time.Second, 4, 10)
	rows := mappingTimeline([]Sample{first, second, stale}, []Sample{replaced, reset}, "us", "tun102")
	if rows[0].Sessions[0].RXBytes != nil || rows[1].Sessions[0].RXBytes == nil || *rows[1].Sessions[0].RXBytes != 50 ||
		rows[1].Sessions[0].TXBytes == nil || *rows[1].Sessions[0].TXBytes != 100 || rows[1].NewProcessErrors == nil || *rows[1].NewProcessErrors != 50 {
		t.Fatalf("invalid baseline or delta: %+v", rows)
	}
	for _, row := range rows[2:] {
		if row.Sessions[0].RXBytes != nil || row.Sessions[0].TXBytes != nil {
			t.Fatalf("invalid samples produced traffic: %+v", row)
		}
	}
}
