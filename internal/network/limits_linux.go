//go:build linux

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
)

func connectionLimitArgs(m model.TunnelMapping) []string {
	if m.MaxConnections == 0 {
		return nil
	}
	_, subnet, _ := net.ParseCIDR(m.Subnet)
	ones, _ := subnet.Mask.Size()
	return []string{"-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-p", "tcp", "--syn", "-m", "connlimit", "--connlimit-above", fmt.Sprint(m.MaxConnections), "--connlimit-mask", fmt.Sprint(ones), "--connlimit-saddr", "-j", "REJECT", "--reject-with", "tcp-reset", "-m", "comment", "--comment", mappingComment(m) + "-connections"}
}
func applyBandwidth(m model.TunnelMapping) error {
	return applyBandwidthContext(context.Background(), m)
}
func applyBandwidthContext(ctx context.Context, m model.TunnelMapping) error {
	run := func(name string, args ...string) error { return runContext(ctx, name, args...) }
	output := func(name string, args ...string) (string, error) { return outputContext(ctx, name, args...) }
	if m.UploadKbps > 0 {
		if err := run("tc", "qdisc", "replace", "dev", m.TunName, "root", "handle", "1:", "tbf", "rate", fmt.Sprintf("%dkbit", m.UploadKbps), "burst", fmt.Sprint(limitBurst(m.UploadKbps)), "latency", "100ms"); err != nil {
			return err
		}
	}
	if m.DownloadKbps > 0 {
		if err := run("tc", "qdisc", "replace", "dev", m.TunName, "handle", "ffff:", "ingress"); err != nil {
			return err
		}
		existing, err := output("tc", "filter", "show", "dev", m.TunName, "parent", "ffff:", "pref", "10")
		if err != nil {
			return err
		}
		// Older versions appended u32 nodes on every repair. This preference is
		// reserved for the managed limiter; replace its entire old selector set.
		if strings.TrimSpace(existing) != "" {
			if err = run("tc", "filter", "delete", "dev", m.TunName, "parent", "ffff:", "protocol", "ip", "pref", "10"); err != nil {
				return err
			}
		}
		if err := run("tc", "filter", "replace", "dev", m.TunName, "parent", "ffff:", "protocol", "ip", "pref", "10", "handle", "800::800", "u32", "match", "u32", "0", "0", "police", "rate", fmt.Sprintf("%dkbit", m.DownloadKbps), "burst", fmt.Sprint(limitBurst(m.DownloadKbps)), "drop", "flowid", ":1"); err != nil {
			return err
		}
	}
	return nil
}
func limitBurst(kbps int) int {
	v := kbps * 1000 / 8 / 100
	if v < 16384 {
		return 16384
	}
	return v
}
func verifyBandwidth(ctx context.Context, m model.TunnelMapping) error {
	if m.UploadKbps == 0 && m.DownloadKbps == 0 {
		return nil
	}
	q, e := outputContext(ctx, "tc", "-j", "qdisc", "show", "dev", m.TunName)
	if e != nil {
		return e
	}
	var queues []struct {
		Kind    string `json:"kind"`
		Handle  string `json:"handle"`
		Options struct {
			Rate uint64 `json:"rate"`
		} `json:"options"`
	}
	if e = json.Unmarshal([]byte(q), &queues); e != nil {
		return fmt.Errorf("invalid qdisc snapshot")
	}
	uploadOK, ingress := m.UploadKbps == 0, false
	for _, v := range queues {
		if v.Kind == "tbf" && v.Handle == "1:" && v.Options.Rate == uint64(m.UploadKbps)*125 {
			uploadOK = true
		}
		if v.Kind == "ingress" && v.Handle == "ffff:" {
			ingress = true
		}
	}
	if !uploadOK {
		return missingState("upload limiter missing or rate changed", "bandwidth-upload")
	}
	if m.DownloadKbps > 0 {
		if !ingress {
			return missingState("download limiter missing", "bandwidth-download")
		}
		f, e := outputContext(ctx, "tc", "filter", "show", "dev", m.TunName, "parent", "ffff:", "pref", "10")
		if e != nil {
			return e
		}
		if !validPolicer(f, m.DownloadKbps) {
			return missingState("download limiter missing or rate changed", "bandwidth-download")
		}
	}
	return nil
}

var policeRate = regexp.MustCompile(`(?i)police[^\n]*\brate ([0-9.]+)([KMG]?)bit\b`)

func validPolicer(text string, kbps int) bool {
	matches := policeRate.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return false
	}
	match := matches[0]
	if len(match) != 3 || !strings.Contains(text, "match 00000000/00000000 at 0") || !strings.Contains(text, "action drop") {
		return false
	}
	rate, e := strconv.ParseFloat(match[1], 64)
	if e != nil {
		return false
	}
	scale := 1.0
	switch strings.ToUpper(match[2]) {
	case "K":
		scale = 1e3
	case "M":
		scale = 1e6
	case "G":
		scale = 1e9
	}
	expected := float64(kbps) * 1000
	// tc's text formatter rounds human-readable rates. Allow only its display
	// precision; material configuration drift must trigger rule repair.
	return math.Abs(rate*scale-expected) <= math.Max(1, expected*0.001)
}
