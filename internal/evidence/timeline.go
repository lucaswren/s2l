package evidence

import (
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

type sessionDelta struct {
	Interface string    `json:"interface"`
	Index     int       `json:"index"`
	Peer      string    `json:"peer"`
	Connected time.Time `json:"connected_at"`
	RXBytes   *uint64   `json:"rx_bytes_delta"`
	TXBytes   *uint64   `json:"tx_bytes_delta"`
}

type mappingSample struct {
	At               time.Time      `json:"at"`
	CollectedAt      time.Time      `json:"collected_at"`
	SessionsAt       time.Time      `json:"sessions_at"`
	SessionsError    string         `json:"sessions_error,omitempty"`
	Sessions         []sessionDelta `json:"sessions"`
	NewProcessErrors *uint64        `json:"new_process_errors"`
	NewDNSFailures   *uint64        `json:"new_dns_failures"`
	LocalState       string         `json:"local_state"`
	TCPState         string         `json:"tcp_state"`
	TCPCheckedAt     time.Time      `json:"tcp_checked_at"`
	TCPStage         string         `json:"tcp_stage,omitempty"`
	DNSState         string         `json:"dns_state"`
	DNSCheckedAt     time.Time      `json:"dns_checked_at"`
	DNSMethod        string         `json:"dns_method,omitempty"`
}

func mappingTimeline(before, after []Sample, id, tun string) []mappingSample {
	samples := append(append([]Sample(nil), before...), after...)
	var rows []mappingSample
	var previous *Sample
	for i := range samples {
		sample := &samples[i]
		status := sample.Status
		health := status.UpstreamHealth[id]
		row := mappingSample{
			At: sample.At, CollectedAt: status.CollectedAt, SessionsAt: status.SessionsCheckedAt, SessionsError: status.SessionsError,
			Sessions: []sessionDelta{}, LocalState: health.LocalState,
			TCPState: health.TCP.State, TCPStage: health.TCP.Stage, TCPCheckedAt: health.TCP.CheckedAt,
			DNSState: health.DNS.State, DNSMethod: health.DNS.Method, DNSCheckedAt: health.DNS.CheckedAt,
		}
		fresh := previous != nil && sample.At.After(previous.At) && sample.At.Sub(previous.At) <= 30*time.Second
		if fresh && !previous.Status.CollectedAt.IsZero() && status.CollectedAt.After(previous.Status.CollectedAt) &&
			status.CollectedAt.Sub(previous.Status.CollectedAt) <= 30*time.Second {
			old := previous.Status.UpstreamHealth[id]
			pid := status.SingboxPIDs[tun]
			if pid > 0 && pid == previous.Status.SingboxPIDs[tun] && !health.Process.StartedAt.IsZero() && health.Process.StartedAt.Equal(old.Process.StartedAt) {
				row.NewProcessErrors = counterIncrease(old.Process.ErrorLines, health.Process.ErrorLines)
				row.NewDNSFailures = counterIncrease(old.Resolver.Failures, health.Resolver.Failures)
			}
		}
		for _, session := range status.Sessions {
			if session.MappingID != id || len(row.Sessions) >= 8 {
				continue
			}
			delta := sessionDelta{Interface: session.Interface, Index: session.Index, Peer: session.PeerIP, Connected: session.ConnectedAt}
			if fresh && status.SessionsError == "" && previous.Status.SessionsError == "" && !previous.Status.SessionsCheckedAt.IsZero() &&
				status.SessionsCheckedAt.After(previous.Status.SessionsCheckedAt) && status.SessionsCheckedAt.Sub(previous.Status.SessionsCheckedAt) <= 30*time.Second {
				for _, old := range previous.Status.Sessions {
					if sameSession(old, session) && old.Error == "" && session.Error == "" {
						delta.RXBytes = counterIncrease(old.RxBytes, session.RxBytes)
						delta.TXBytes = counterIncrease(old.TxBytes, session.TxBytes)
						break
					}
				}
			}
			row.Sessions = append(row.Sessions, delta)
		}
		rows = append(rows, row)
		previous = sample
	}
	return rows
}

func sameSession(a, b model.PPPSession) bool {
	return a.MappingID == b.MappingID && a.Index == b.Index && a.Interface == b.Interface &&
		a.PeerIP == b.PeerIP && a.ConnectedAt.Equal(b.ConnectedAt) && a.FirstSeen.Equal(b.FirstSeen)
}

func counterIncrease(before, after uint64) *uint64 {
	if after < before {
		return nil
	}
	delta := after - before
	return &delta
}
