package debuglog

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

const connectionLimit = 256

var ansiPattern = regexp.MustCompile(`\[[0-9;]*m`)
var connectionPattern = regexp.MustCompile(`\[(\d+) [^\]]+\] (.*)`)
var tunPattern = regexp.MustCompile(`s2l-([^/\\]+)\.log`)

type Connection struct {
	ID        string    `json:"id"`
	TunName   string    `json:"tun_name"`
	Source    string    `json:"source,omitempty"`
	Target    string    `json:"target,omitempty"`
	Protocol  string    `json:"protocol,omitempty"`
	Stage     string    `json:"stage"`
	Error     string    `json:"error,omitempty"`
	ReplyCode string    `json:"reply_code,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}
type FailedTarget struct {
	Target   string `json:"target"`
	Protocol string `json:"protocol"`
	Count    int    `json:"count"`
}

var replyPattern = regexp.MustCompile(`code[= ](\d+)`)

// This is log evidence, not packet accounting. "outbound connection" records an
// attempted forward, not a completed handshake or a successful image transfer.
func (c *Collector) observeConnectionsLocked(source string, p []byte) {
	tun := tunPattern.FindStringSubmatch(source)
	if !strings.HasPrefix(source, "sing-box/") || len(tun) != 2 {
		return
	}
	if c.connectionFragments == nil {
		c.connectionFragments = map[string]string{}
	}
	if _, ok := c.connectionFragments[source]; !ok && len(c.connectionFragments) >= 256 {
		return
	}
	pending := c.connectionFragments[source] + string(p)
	for {
		end := strings.IndexByte(pending, '\n')
		if end < 0 {
			break
		}
		line := ansiPattern.ReplaceAllString(pending[:end], "")
		pending = pending[end+1:]
		match := connectionPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		message := match[2]
		key := tun[1] + "/" + match[1]
		if c.connections == nil {
			c.connections = map[string]Connection{}
		}
		v, ok := c.connections[key]
		if !ok {
			if len(c.connections) >= connectionLimit {
				oldest := ""
				var at time.Time
				for k, item := range c.connections {
					if oldest == "" || item.LastSeen.Before(at) {
						oldest = k
						at = item.LastSeen
					}
				}
				delete(c.connections, oldest)
				c.droppedConnections++
			}
			v = Connection{ID: match[1], TunName: tun[1], Stage: "observed", FirstSeen: time.Now()}
		}
		v.LastSeen = time.Now()
		switch {
		case strings.Contains(message, "inbound packet connection from "):
			v.Protocol = "udp"
			v.Source = tailAddress(message, "inbound packet connection from ")
		case strings.Contains(message, "inbound connection from "):
			v.Protocol = "tcp"
			v.Source = tailAddress(message, "inbound connection from ")
		case strings.Contains(message, "inbound packet connection to "):
			v.Protocol = "udp"
			v.Target = tailAddress(message, "inbound packet connection to ")
		case strings.Contains(message, "inbound connection to "):
			v.Protocol = "tcp"
			v.Target = tailAddress(message, "inbound connection to ")
		case strings.Contains(message, "outbound connection to ") || strings.Contains(message, "outbound packet connection to "):
			v.Stage = "forward-requested"
		}
		if strings.Contains(line, "ERROR") || strings.Contains(line, "WARN") {
			v.Stage = "connection-error"
			if strings.Contains(message, "socks5: request rejected") {
				v.Stage = "proxy-rejected"
			}
			if strings.Contains(message, "connect proxy:") {
				v.Stage = "proxy-connect"
			}
			if strings.Contains(message, "socks auth") || strings.Contains(message, "socks greeting") {
				v.Stage = "proxy-handshake"
			}
			if strings.Contains(message, "unpack request: dns:") {
				v.Stage = "dns-parse"
			}
			if len(message) > 512 {
				message = message[:512]
			}
			v.Error = message
			if reply := replyPattern.FindStringSubmatch(message); len(reply) == 2 {
				v.ReplyCode = reply[1]
			}
		}
		c.connections[key] = v
	}
	if len(pending) > 4096 {
		pending = ""
	}
	c.connectionFragments[source] = pending
}
func tailAddress(message, prefix string) string {
	_, value, _ := strings.Cut(message, prefix)
	if len(value) > 300 {
		return value[:300]
	}
	return value
}
func (c *Collector) connectionSummaryLocked() ([]Connection, []FailedTarget) {
	connections := make([]Connection, 0, len(c.connections))
	counts := map[string]FailedTarget{}
	for _, v := range c.connections {
		connections = append(connections, v)
		if v.Error != "" && v.Target != "" {
			k := v.Protocol + "/" + v.Target
			f := counts[k]
			f.Target = v.Target
			f.Protocol = v.Protocol
			f.Count++
			counts[k] = f
		}
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].LastSeen.After(connections[j].LastSeen) })
	targets := make([]FailedTarget, 0, len(counts))
	for _, v := range counts {
		targets = append(targets, v)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Count == targets[j].Count {
			return targets[i].Target < targets[j].Target
		}
		return targets[i].Count > targets[j].Count
	})
	if len(targets) > 20 {
		targets = targets[:20]
	}
	return connections, targets
}
