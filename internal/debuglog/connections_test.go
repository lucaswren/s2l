package debuglog

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"strings"
	"testing"
	"time"
)

func TestConnectionsCorrelateChunksAndRemainBounded(t *testing.T) {
	c := &Collector{}
	source := "sing-box//run/s2l/s2l-tun101.log"
	c.Add(source, []byte("INFO [1 0ms] inbound connection from 10.0.0.2:123\n"))
	if len(c.Analysis().Connections) != 0 {
		t.Fatal("collected with debug off")
	}
	c.Set(true)
	for _, line := range []string{"INFO [1 0ms] inbound packet connection from 10.0.0.2:123\n", "INFO [1 0ms] inbound packet connection to 1.1.1.1:123\n", "ERROR [1 2ms] connection: socks5: request re", "jected, code=9\n"} {
		c.Add(source, []byte(line))
	}
	a := c.Analysis()
	if len(a.Connections) != 1 || len(a.FailedTargets) != 1 || a.Connections[0].ReplyCode != "9" || a.Connections[0].Protocol != "udp" || a.Connections[0].Source != "10.0.0.2:123" {
		t.Fatal(a)
	}
	for i := 2; i < connectionLimit+20; i++ {
		c.Add(source, []byte(fmt.Sprintf("INFO [%d 0ms] inbound connection to 2.2.2.2:443\n", i)))
	}
	a = c.Analysis()
	if len(a.Connections) != connectionLimit || a.DroppedConnections == 0 {
		t.Fatal("unbounded connection tracking")
	}
	c.Set(false)
	c.Set(true)
	if len(c.Analysis().Connections) != 0 {
		t.Fatal("old connections survived new session")
	}
}
func TestProtocolChangesRecordedEvenWhenOverallStateIsUnchanged(t *testing.T) {
	c := &Collector{}
	c.Set(true)
	now := time.Now()
	h := model.UpstreamHealth{State: "degraded", TCP: model.ProtocolHealth{State: "healthy"}, UDP: model.ProtocolHealth{State: "unavailable"}}
	s := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"m": h}}
	c.RecordStatus(s, Resources{}, now)
	h.UDP.State = "healthy"
	s.UpstreamHealth["m"] = h
	c.RecordStatus(s, Resources{}, now.Add(time.Second))
	if !strings.Contains(string(c.Analysis().Text()), "protocol-udp") {
		t.Fatal("lost protocol transition")
	}
}
